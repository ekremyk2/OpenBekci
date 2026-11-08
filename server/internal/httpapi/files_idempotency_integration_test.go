package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// chunkedUpload sends the upload with no Content-Length, as a client streaming
// with Transfer-Encoding: chunked does.
func (fe *filesEnv) chunkedUpload(t *testing.T, actor, filename string, content []byte, key string) *httptest.ResponseRecorder {
	t.Helper()
	body, ctype := multipartBody(nil, filename, content)
	r := httptest.NewRequest("POST", "/v1/orgs/"+fe.w.OrgA.ID.String()+"/files", io.NopCloser(strings.NewReader(body)))
	if r.ContentLength != -1 {
		t.Fatalf("the request must be chunked, ContentLength = %d", r.ContentLength)
	}
	r.RemoteAddr = "203.0.113.20:4000"
	r.Header.Set("Authorization", "Bearer "+fe.w.Token(t, fe.w.Actor(actor)))
	r.Header.Set("Content-Type", ctype)
	r.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	fe.router.ServeHTTP(rec, r)
	return rec
}

func (fe *filesEnv) fileCount(t *testing.T) int {
	t.Helper()
	rec := fe.do(t, "org_admin", "GET", "/v1/orgs/"+fe.w.OrgA.ID.String()+"/files", nil)
	var page struct{ Items []json.RawMessage }
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("list: %s", rec.Body.String())
	}
	return len(page.Items)
}

func problemCode(rec *httptest.ResponseRecorder) string {
	var p struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p.Code
}

// Two files of the same length under one key used to replay the first: the
// second upload was reported stored and was lost.
func TestIntegrationFilesIdempotencyComparesContentOfEqualLengthFiles(t *testing.T) {
	fe := newFilesEnv(t, 0)
	key := "equal-" + uuid.NewString()
	first := fe.upload(t, "staff", "a.txt", []byte("AAAAAAAAAA"), nil, "Idempotency-Key", key)
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	second := fe.upload(t, "staff", "a.txt", []byte("BBBBBBBBBB"), nil, "Idempotency-Key", key)
	if second.Code != 422 || problemCode(second) != "idempotency_mismatch" {
		t.Errorf("a different file of the same length: %d %s, want 422 idempotency_mismatch", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotent-Replayed") != "" {
		t.Error("a mismatch must not be marked as a replay")
	}
	// the same file again still replays, whatever the multipart boundary
	again := fe.upload(t, "staff", "a.txt", []byte("AAAAAAAAAA"), nil, "Idempotency-Key", key)
	if again.Code != 201 || again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Errorf("same file: %d replayed=%q", again.Code, again.Header().Get("Idempotent-Replayed"))
	}
	// a different length is a mismatch as before
	if third := fe.upload(t, "staff", "a.txt", []byte("CCC"), nil, "Idempotency-Key", key); third.Code != 422 {
		t.Errorf("different length: %d", third.Code)
	}
	if n := fe.fileCount(t); n != 1 {
		t.Errorf("%d files stored, want 1 (nothing for the mismatches)", n)
	}
}

func TestIntegrationFilesIdempotencyChunkedUploads(t *testing.T) {
	fe := newFilesEnv(t, 0)
	key := "chunked-" + uuid.NewString()
	first := fe.chunkedUpload(t, "staff", "c.txt", []byte("chunked bytes"), key)
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	// the same chunked upload again replays
	again := fe.chunkedUpload(t, "staff", "c.txt", []byte("chunked bytes"), key)
	if again.Code != 201 || again.Header().Get("Idempotent-Replayed") != "true" || !bytes.Equal(again.Body.Bytes(), first.Body.Bytes()) {
		t.Errorf("chunked retry: %d replayed=%q %s", again.Code, again.Header().Get("Idempotent-Replayed"), again.Body.String())
	}
	// other content under the key is a mismatch, also of equal length
	other := fe.chunkedUpload(t, "staff", "c.txt", []byte("CHUNKED BYTES"), key)
	if other.Code != 422 || problemCode(other) != "idempotency_mismatch" {
		t.Errorf("chunked, other content: %d %s", other.Code, other.Body.String())
	}
	if n := fe.fileCount(t); n != 1 {
		t.Errorf("%d files stored, want 1", n)
	}
}
