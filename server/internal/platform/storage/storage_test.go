package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/uuid"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

// blobContract is what every driver must do.
func blobContract(t *testing.T, b storage.Blob) {
	t.Helper()
	ctx := context.Background()
	org, id := uuid.New(), uuid.New()
	key := storage.FileKey(org, id, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), "")

	if _, err := b.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat of a missing key: %v, want ErrNotFound", err)
	}
	if _, err := b.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get of a missing key: %v, want ErrNotFound", err)
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete of a missing key must succeed: %v", err)
	}

	data := bytes.Repeat([]byte("openbekci "), 1000)
	if err := b.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	st, err := b.Stat(ctx, key)
	if err != nil || st.Size != int64(len(data)) {
		t.Fatalf("Stat = %+v, %v; want size %d", st, err, len(data))
	}
	rc, err := b.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("Get returned different bytes")
	}

	// unknown size, replaces the blob
	if err := b.Put(ctx, key, io.MultiReader(strings.NewReader("new "), strings.NewReader("content")), -1, "text/plain"); err != nil {
		t.Fatalf("Put with unknown size: %v", err)
	}
	rc, err = b.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "new content" {
		t.Fatalf("after overwrite got %q", got)
	}

	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat after delete: %v", err)
	}

	for _, bad := range []string{"", "../x", "a/../b", "/abs", "a//b", "a\\b", "a/b/"} {
		if err := b.Put(ctx, bad, strings.NewReader("x"), 1, ""); err == nil {
			t.Errorf("Put accepted the invalid key %q", bad)
		}
	}
}

func TestLocalContract(t *testing.T) {
	blobContract(t, storage.NewLocal(t.TempDir(), storage.NewSigner([]byte("k"))))
}

func TestLocalPutIsAtomic(t *testing.T) {
	root := t.TempDir()
	l := storage.NewLocal(root, nil)
	ctx := context.Background()
	key := "a/b/c"
	if err := l.Put(ctx, key, strings.NewReader("old"), 3, ""); err != nil {
		t.Fatal(err)
	}
	// a write that fails halfway must leave the old blob and no stray files
	boom := errors.New("connection reset")
	r := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(boom))
	if err := l.Put(ctx, key, r, -1, ""); !errors.Is(err, boom) {
		t.Fatalf("Put = %v, want the reader's error", err)
	}
	rc, err := l.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "old" {
		t.Fatalf("blob after a failed write = %q, want the old content", got)
	}
	// size mismatch stores nothing
	if err := l.Put(ctx, "a/b/d", strings.NewReader("12345"), 9, ""); err == nil {
		t.Fatal("Put accepted a size mismatch")
	}
	if _, err := l.Stat(ctx, "a/b/d"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("blob exists after a size mismatch: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "a", "b"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	// a cancelled context aborts the copy
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.Put(cctx, "a/b/e", strings.NewReader("x"), -1, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put with a cancelled context = %v", err)
	}
}

func TestSignedURLs(t *testing.T) {
	signer := storage.NewSigner([]byte("instance secret"))
	l := storage.NewLocal(t.TempDir(), signer)
	org, id := uuid.New(), uuid.New()
	key := storage.FileKey(org, id, time.Now(), "thumb")

	raw, err := l.SignedURL(context.Background(), key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := "/v1/orgs/" + org.String() + "/files/" + id.String()
	if u.Path != wantPath {
		t.Fatalf("path = %q, want %q", u.Path, wantPath)
	}
	q := u.Query()
	if q.Get("variant") != "thumb" {
		t.Fatalf("variant = %q", q.Get("variant"))
	}
	exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
	msg := storage.DownloadMessage(org.String(), id.String(), "thumb")
	now := time.Now()
	if err := signer.Verify(msg, exp, q.Get("sig"), now); err != nil {
		t.Fatalf("fresh URL does not verify: %v", err)
	}
	// expiry
	if err := signer.Verify(msg, exp, q.Get("sig"), time.Unix(exp+1, 0)); !errors.Is(err, storage.ErrSignatureExpired) {
		t.Fatalf("expired URL: %v", err)
	}
	// tampering: another file, another variant, a later expiry, a mangled signature, another instance
	other := storage.DownloadMessage(org.String(), uuid.NewString(), "thumb")
	if err := signer.Verify(other, exp, q.Get("sig"), now); !errors.Is(err, storage.ErrSignatureInvalid) {
		t.Errorf("signature accepted for another file: %v", err)
	}
	if err := signer.Verify(storage.DownloadMessage(org.String(), id.String(), ""), exp, q.Get("sig"), now); !errors.Is(err, storage.ErrSignatureInvalid) {
		t.Errorf("signature accepted for another variant: %v", err)
	}
	if err := signer.Verify(msg, exp+3600, q.Get("sig"), now); !errors.Is(err, storage.ErrSignatureInvalid) {
		t.Errorf("signature accepted with an extended expiry: %v", err)
	}
	for _, sig := range []string{"", "zz", q.Get("sig")[:10], strings.Repeat("0", 64)} {
		if err := signer.Verify(msg, exp, sig, now); !errors.Is(err, storage.ErrSignatureInvalid) {
			t.Errorf("bad signature %q accepted: %v", sig, err)
		}
	}
	if err := storage.NewSigner([]byte("other instance")).Verify(msg, exp, q.Get("sig"), now); !errors.Is(err, storage.ErrSignatureInvalid) {
		t.Errorf("signature accepted by another instance: %v", err)
	}
	// keys outside the file layout cannot be signed
	if _, err := l.SignedURL(context.Background(), "a/b/c", time.Minute); !errors.Is(err, storage.ErrUnsupported) {
		t.Errorf("SignedURL for a foreign key: %v", err)
	}
}

func TestS3AgainstFake(t *testing.T) {
	backend := s3mem.New()
	if err := backend.CreateBucket("files"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	b, err := storage.NewS3(context.Background(), storage.S3Config{
		Bucket: "files", Endpoint: srv.URL, AccessKey: "test", SecretKey: "test", PathStyle: true, Prefix: "obk/",
	})
	if err != nil {
		t.Fatal(err)
	}
	blobContract(t, b)

	// presigned GET: downloads from the store without credentials
	ctx := context.Background()
	org, id := uuid.New(), uuid.New()
	key := storage.FileKey(org, id, time.Now(), "")
	if err := b.Put(ctx, key, strings.NewReader("hello s3"), 8, "text/plain"); err != nil {
		t.Fatal(err)
	}
	signed, err := b.SignedURL(ctx, key, time.Minute, storage.WithDisposition(`attachment; filename="a.txt"`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(signed, "X-Amz-Signature=") {
		t.Fatalf("not a presigned URL: %s", signed)
	}
	resp, err := http.Get(signed) //nolint:noctx // test server
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "hello s3" {
		t.Fatalf("presigned GET = %d %q", resp.StatusCode, body)
	}
}
