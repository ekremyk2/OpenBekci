package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/permissions"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const ticketScope = "ticket"

type clock struct {
	mu sync.Mutex
	d  time.Duration
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return time.Now().Add(c.d) }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.d += d
	c.mu.Unlock()
}

type env struct {
	t        *testing.T
	db       *db.DB
	world    *authztest.World
	resolver *authz.Resolver
	blob     *storage.Local
	root     string
	svc      *files.Service
	clock    *clock
	// tickets is the fake ticket module: only these users may read the ticket's files
	mu      sync.Mutex
	readers map[uuid.UUID][]uuid.UUID // ticket id -> user ids
}

type scanFunc func(context.Context, io.Reader) (files.Verdict, error)

func (f scanFunc) Scan(ctx context.Context, r io.Reader) (files.Verdict, error) { return f(ctx, r) }

func newEnv(t *testing.T, mutate func(*files.Config)) *env {
	t.Helper()
	d := dbtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ident := identity.NewService(d, identity.Config{InstanceName: "Test", BaseURL: "https://bekci.example.org",
		SecretKey: []byte("0123456789abcdef0123456789abcdef"), Log: log})
	reg, err := permissions.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: d, world: authztest.NewWorld(t, d, ident), resolver: authz.NewResolver(d, reg, nil),
		root: t.TempDir(), clock: &clock{}, readers: map[uuid.UUID][]uuid.UUID{}}
	signer := storage.NewSigner([]byte("test secret"))
	e.blob = storage.NewLocal(e.root, signer)
	cfg := files.Config{Signer: signer, Log: log, Now: e.clock.now}
	if mutate != nil {
		mutate(&cfg)
	}
	e.svc = files.NewService(d, e.blob, cfg)
	e.svc.RegisterResolver(ticketScope, files.ResolverFuncs{
		Read: func(ctx context.Context, _ pgx.Tx, ref files.ScopeRef) (bool, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			uid := authz.PrincipalFromContext(ctx).UserID
			for _, u := range e.readers[ref.ID] {
				if u == uid {
					return true, nil
				}
			}
			return false, nil
		},
		Attach: func(ctx context.Context, _ pgx.Tx, ref files.ScopeRef) (bool, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			uid := authz.PrincipalFromContext(ctx).UserID
			for _, u := range e.readers[ref.ID] {
				if u == uid {
					return true, nil
				}
			}
			return false, nil
		},
	}, files.WithMaxFiles(2))
	return e
}

// ctx is the request context of actor in org, built like the HTTP pipeline does.
func (e *env) ctx(a authztest.Actor, org authztest.Org) context.Context {
	e.t.Helper()
	pr := &authz.Principal{ID: a.UserID, UserID: a.UserID}
	ctx := authz.WithPrincipal(context.Background(), pr)
	ctx = authz.WithGrants(ctx, e.resolver.InstanceGrants(pr))
	g, member, err := e.resolver.ResolveOrg(ctx, pr, org.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if !member {
		return ctx // no grants: every call is refused
	}
	return authz.WithGrants(ctx, g)
}

func (e *env) ticket(users ...authztest.Actor) uuid.UUID {
	id := uuid.New()
	for _, u := range users {
		e.readers[id] = append(e.readers[id], u.UserID)
	}
	return id
}

func (e *env) upload(ctx context.Context, org authztest.Org, name string, content []byte, scope *files.ScopeRef) (files.File, error) {
	return e.svc.Upload(ctx, files.UploadParams{OrgID: org.ID, Name: name, Content: bytes.NewReader(content), Scope: scope})
}

func (e *env) mustUpload(ctx context.Context, org authztest.Org, name string, content []byte, scope *files.ScopeRef) files.File {
	e.t.Helper()
	f, err := e.upload(ctx, org, name, content, scope)
	if err != nil {
		e.t.Fatalf("upload %s: %v", name, err)
	}
	return f
}

func read(t *testing.T, d *files.Download) []byte {
	t.Helper()
	defer d.Body.Close()
	b, err := io.ReadAll(d.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func status(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	pe := problem.From(err)
	return pe.Status, string(pe.Code)
}

func wantStatus(t *testing.T, err error, want int, what string) {
	t.Helper()
	if got, code := status(err); got != want {
		t.Errorf("%s: status %d (%s), want %d (err %v)", what, got, code, want, err)
	}
}

func (e *env) blobExists(key string) bool {
	_, err := e.blob.Stat(context.Background(), key)
	return err == nil
}

func (e *env) fileRows() int {
	e.t.Helper()
	var n int
	err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM file`).Scan(&n)
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func photo(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIntegrationUploadStoresHashesAndServes(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.world.Actor("org_admin")
	ctx := e.ctx(admin, e.world.OrgA)

	content := []byte("Meeting minutes, 12 June.\nAll in favour.\n")
	f := e.mustUpload(ctx, e.world.OrgA, "minutes.txt", content, nil)
	sum := sha256.Sum256(content)
	if f.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %s, want %x", f.SHA256, sum)
	}
	if f.Size != int64(len(content)) || f.Mime != "text/plain" || f.Status != files.StatusReady || f.Source != files.SourceUploaded {
		t.Errorf("metadata = %+v", f)
	}
	if f.UploadedBy == nil || *f.UploadedBy != admin.UserID {
		t.Errorf("uploaded_by = %v", f.UploadedBy)
	}
	if f.RetentionUntil == nil || time.Until(*f.RetentionUntil) < 6*24*time.Hour {
		t.Errorf("a draft must get the unattached lifetime, got %v", f.RetentionUntil)
	}
	if !strings.HasPrefix(f.Key, f.OrgID.String()+"/") || !strings.HasSuffix(f.Key, f.ID.String()) {
		t.Errorf("key = %s", f.Key)
	}

	d, err := e.svc.Open(ctx, e.world.OrgA.ID, f.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Inline || !strings.HasPrefix(d.ContentDisposition(), "attachment") || !strings.Contains(d.ContentDisposition(), "minutes.txt") {
		t.Errorf("a text file must be an attachment, got %q", d.ContentDisposition())
	}
	got := read(t, d)
	gsum := sha256.Sum256(got)
	if !bytes.Equal(got, content) || hex.EncodeToString(gsum[:]) != f.SHA256 {
		t.Error("downloaded bytes differ from the upload")
	}

	// metadata round trip and listing
	g, err := e.svc.Get(ctx, e.world.OrgA.ID, f.ID)
	if err != nil || g.SHA256 != f.SHA256 || g.Name != "minutes.txt" {
		t.Fatalf("Get = %+v, %v", g, err)
	}
	list, next, err := e.svc.List(ctx, e.world.OrgA.ID, files.ListParams{})
	if err != nil || len(list) != 1 || next != "" || list[0].ID != f.ID {
		t.Fatalf("List = %d files, next %q, %v", len(list), next, err)
	}
	// usage
	u, err := e.svc.Usage(ctx, e.world.OrgA.ID)
	if err != nil || u.Files != 1 || u.Bytes != int64(len(content)) {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	// the audit trail records the upload without the file name in clear
	var diff string
	err = e.db.InTenantTx(context.Background(), e.world.OrgA.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT diff::text FROM audit_event WHERE action = 'files.upload' AND target_id = $1`, f.ID).Scan(&diff)
	})
	if err != nil {
		t.Fatalf("audit event: %v", err)
	}
	if strings.Contains(diff, "minutes.txt") || !strings.Contains(diff, "hash_after") {
		t.Errorf("the file name must appear only as a hash in the audit diff: %s", diff)
	}
}

func TestIntegrationUploadRejectsWrongContent(t *testing.T) {
	e := newEnv(t, nil)
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	exe := append([]byte("MZ\x90\x00\x03\x00\x00\x00"), bytes.Repeat([]byte{0}, 600)...)
	_, err := e.upload(ctx, e.world.OrgA, "holiday.jpg", exe, nil)
	wantStatus(t, err, 415, "an executable renamed .jpg")
	_, err = e.upload(ctx, e.world.OrgA, "setup.exe", exe, nil)
	wantStatus(t, err, 415, "an .exe")
	_, err = e.upload(ctx, e.world.OrgA, "page.jpg", []byte("<html><script>alert(1)</script></html>"), nil)
	wantStatus(t, err, 415, "html renamed .jpg")
	_, err = e.upload(ctx, e.world.OrgA, "report.png", []byte("%PDF-1.7\n..."), nil)
	wantStatus(t, err, 415, "a pdf renamed .png")
	_, err = e.upload(ctx, e.world.OrgA, "empty.txt", nil, nil)
	wantStatus(t, err, 422, "an empty file")
	_, err = e.upload(ctx, e.world.OrgA, "broken.jpg", append(photo(t, 50, 50)[:300], bytes.Repeat([]byte{1}, 10)...), nil)
	wantStatus(t, err, 422, "a truncated jpeg")
	_, err = e.upload(ctx, e.world.OrgA, "", []byte("x"), nil)
	wantStatus(t, err, 422, "no name")
	if n := e.fileRows(); n != 0 {
		t.Errorf("rejected uploads left %d file rows", n)
	}
	if entries, _ := storageFiles(e.root); len(entries) != 0 {
		t.Errorf("rejected uploads left blobs behind: %v", entries)
	}
}

func TestIntegrationUploadSizeLimit(t *testing.T) {
	e := newEnv(t, func(c *files.Config) { c.MaxUploadBytes = 2048 })
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	_, err := e.upload(ctx, e.world.OrgA, "big.txt", bytes.Repeat([]byte("a"), 2049), nil)
	wantStatus(t, err, 413, "a file over the limit")
	if code := problem.From(err).Code; code != problem.CodePayloadTooLarge {
		t.Errorf("code = %s", code)
	}
	_, err = e.upload(ctx, e.world.OrgA, "big.jpg", append(photo(t, 200, 200), bytes.Repeat([]byte{0}, 2048)...), nil)
	wantStatus(t, err, 413, "an image over the limit")
	if n := e.fileRows(); n != 0 {
		t.Errorf("%d file rows left after refused uploads", n)
	}
	if entries, _ := storageFiles(e.root); len(entries) != 0 {
		t.Errorf("blobs left after refused uploads: %v", entries)
	}
	f := e.mustUpload(ctx, e.world.OrgA, "ok.txt", bytes.Repeat([]byte("a"), 2048), nil)
	if f.Size != 2048 {
		t.Errorf("a file exactly at the limit: size %d", f.Size)
	}
	// the refused uploads did not count towards usage
	u, _ := e.svc.Usage(ctx, e.world.OrgA.ID)
	if u.Files != 1 || u.Bytes != 2048 {
		t.Errorf("usage = %+v", u)
	}
}

func TestIntegrationImagesAreStrippedAndGetVariants(t *testing.T) {
	e := newEnv(t, nil)
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	in := jpegWithGPSFile(t, 2400, 1800)
	f := e.mustUpload(ctx, e.world.OrgA, "kitchen.JPG", in, nil)
	if f.Mime != "image/jpeg" {
		t.Errorf("mime = %s", f.Mime)
	}
	thumb, medium := f.Variants[files.VariantThumb], f.Variants[files.VariantMedium]
	if thumb.Width != 256 || thumb.Height != 192 || medium.Width != 1600 || medium.Height != 1200 {
		t.Errorf("variants: thumb %+v medium %+v", thumb, medium)
	}
	orig := read(t, mustOpen(t, e, ctx, e.world.OrgA, f.ID, ""))
	if bytes.Contains(orig, []byte("Exif")) || bytes.Contains(orig, []byte("SECRET-GPS")) {
		t.Error("the stored original still has EXIF")
	}
	sum := sha256.Sum256(orig)
	if hex.EncodeToString(sum[:]) != f.SHA256 || int64(len(orig)) != f.Size {
		t.Error("hash and size describe the stored original, not the upload")
	}
	for _, v := range []string{files.VariantThumb, files.VariantMedium} {
		d := mustOpen(t, e, ctx, e.world.OrgA, f.ID, v)
		if !d.Inline || !strings.HasPrefix(d.ContentDisposition(), "inline") {
			t.Errorf("%s must be inline, got %q", v, d.ContentDisposition())
		}
		b := read(t, d)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width != f.Variants[v].Width || cfg.Height != f.Variants[v].Height {
			t.Errorf("%s decodes as %+v (%v), metadata says %+v", v, cfg, err, f.Variants[v])
		}
		if bytes.Contains(b, []byte("Exif")) || bytes.Contains(b, []byte("SECRET-GPS")) {
			t.Errorf("%s has EXIF", v)
		}
		if d.Size != int64(len(b)) {
			t.Errorf("%s Content-Length %d, body %d", v, d.Size, len(b))
		}
	}
	// a document has no variants
	doc := e.mustUpload(ctx, e.world.OrgA, "a.txt", []byte("hello"), nil)
	_, err := e.svc.Open(ctx, e.world.OrgA.ID, doc.ID, files.VariantThumb)
	wantStatus(t, err, 404, "a variant of a text file")
	// usage counts the original and the variants
	u, _ := e.svc.Usage(ctx, e.world.OrgA.ID)
	want := f.Size + thumb.Size + medium.Size + doc.Size
	if u.Bytes != want || u.Files != 2 {
		t.Errorf("usage = %+v, want %d bytes in 2 files", u, want)
	}
}

func mustOpen(t *testing.T, e *env, ctx context.Context, org authztest.Org, id uuid.UUID, variant string) *files.Download {
	t.Helper()
	d, err := e.svc.Open(ctx, org.ID, id, variant)
	if err != nil {
		t.Fatalf("open %s: %v", variant, err)
	}
	return d
}

// jpegWithGPSFile is a photo with EXIF GPS data injected after SOI.
func jpegWithGPSFile(t *testing.T, w, h int) []byte {
	t.Helper()
	raw := photo(t, w, h)
	payload := append([]byte("Exif\x00\x00II*\x00\x08\x00\x00\x00"), []byte("SECRET-GPS-41N-29E")...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out := append([]byte{}, raw[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, raw[2:]...)
}

func storageFiles(root string) ([]string, error) {
	var out []string
	err := walk(root, func(p string) { out = append(out, p) })
	return out, err
}

func TestIntegrationQuarantine(t *testing.T) {
	var scanned []string
	e := newEnv(t, func(c *files.Config) {
		c.Scanner = scanFunc(func(_ context.Context, r io.Reader) (files.Verdict, error) {
			b, _ := io.ReadAll(r)
			scanned = append(scanned, strconv.Itoa(len(b)))
			if bytes.Contains(b, []byte("EICAR")) {
				return files.Verdict{Infected: true, Signature: "Eicar-Test-Signature"}, nil
			}
			return files.Verdict{}, nil
		})
	})
	admin := e.world.Actor("org_admin")
	ctx := e.ctx(admin, e.world.OrgA)

	clean := e.mustUpload(ctx, e.world.OrgA, "clean.txt", []byte("all good"), nil)
	if clean.Status != files.StatusReady {
		t.Fatalf("clean file status = %s", clean.Status)
	}
	bad := e.mustUpload(ctx, e.world.OrgA, "virus.txt", []byte("X5O!P%@AP EICAR-STANDARD-ANTIVIRUS-TEST-FILE"), nil)
	if bad.Status != files.StatusQuarantined {
		t.Fatalf("infected file status = %s, want quarantined", bad.Status)
	}
	badImage := e.mustUpload(ctx, e.world.OrgA, "virus.jpg", append(photo(t, 40, 40), []byte("EICAR")...), nil)
	if badImage.Status != files.StatusQuarantined || len(badImage.Variants) != 0 {
		t.Errorf("infected image: status %s, %d variants", badImage.Status, len(badImage.Variants))
	}
	if len(scanned) != 3 {
		t.Errorf("scanner ran %d times, want 3", len(scanned))
	}
	// nobody can download it, not even the administrator who uploaded it; the metadata stays visible
	for _, f := range []files.File{bad, badImage} {
		_, err := e.svc.Open(ctx, e.world.OrgA.ID, f.ID, "")
		wantStatus(t, err, 404, "download of a quarantined file")
		g, err := e.svc.Get(ctx, e.world.OrgA.ID, f.ID)
		if err != nil || g.Status != files.StatusQuarantined {
			t.Errorf("Get of a quarantined file = %+v, %v", g, err)
		}
	}
	// other members never see it
	guard := e.ctx(e.world.Actor("staff"), e.world.OrgA)
	_, err := e.svc.Get(guard, e.world.OrgA.ID, bad.ID)
	wantStatus(t, err, 404, "a quarantined file seen by another member")
	// the audit trail has a quarantine event
	var n int
	_ = e.db.InTenantTx(context.Background(), e.world.OrgA.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE action = 'files.quarantine'`).Scan(&n)
	})
	if n != 2 {
		t.Errorf("%d quarantine events, want 2", n)
	}
}

func TestIntegrationScannerDownRefusesUpload(t *testing.T) {
	e := newEnv(t, func(c *files.Config) {
		c.Scanner = scanFunc(func(context.Context, io.Reader) (files.Verdict, error) {
			return files.Verdict{}, errors.New("connection refused")
		})
	})
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	for _, name := range []string{"a.txt", "a.jpg"} {
		content := []byte("hello")
		if strings.HasSuffix(name, "jpg") {
			content = photo(t, 20, 20)
		}
		_, err := e.upload(ctx, e.world.OrgA, name, content, nil)
		wantStatus(t, err, 503, "upload with the scanner down")
		if problem.From(err).Code != problem.CodeScanUnavailable {
			t.Errorf("code = %s", problem.From(err).Code)
		}
	}
	if n := e.fileRows(); n != 0 {
		t.Errorf("%d rows left", n)
	}
}

func TestIntegrationVisibilityResolvers(t *testing.T) {
	e := newEnv(t, nil)
	staff, guard, admin := e.world.Actor("staff"), e.world.Actor("guard"), e.world.Actor("org_admin")
	ticket := e.ticket(staff) // only staff may read and attach (the "resident" of this ticket)
	ref := &files.ScopeRef{Type: ticketScope, ID: ticket}

	// the guard may not attach to a ticket that is not theirs; an unregistered scope type is invalid
	_, err := e.upload(e.ctx(guard, e.world.OrgA), e.world.OrgA, "x.txt", []byte("x"), ref)
	wantStatus(t, err, 403, "attach refused by the resolver")
	_, err = e.upload(e.ctx(staff, e.world.OrgA), e.world.OrgA, "x.txt", []byte("x"), &files.ScopeRef{Type: "nope", ID: ticket})
	wantStatus(t, err, 422, "unregistered scope type")

	f := e.mustUpload(e.ctx(staff, e.world.OrgA), e.world.OrgA, "leak.pdf", []byte("%PDF-1.4 leak"), ref)
	if f.Scope == nil || *f.Scope != *ref || f.RetentionUntil != nil {
		t.Fatalf("attached file = %+v", f)
	}
	// who can read: the uploader and the resolver's readers, and files.manage; nobody else
	read := func(a authztest.Actor, org authztest.Org) error {
		_, err := e.svc.Get(e.ctx(a, org), e.world.OrgA.ID, f.ID)
		return err
	}
	if err := read(staff, e.world.OrgA); err != nil {
		t.Errorf("uploader: %v", err)
	}
	if err := read(admin, e.world.OrgA); err != nil {
		t.Errorf("files.manage holder: %v", err)
	}
	wantStatus(t, read(guard, e.world.OrgA), 404, "an unrelated member")
	wantStatus(t, read(e.world.Actor("no_membership"), e.world.OrgA), 403, "a user with no membership")
	// another organization's administrator cannot reach it by id, either
	_, err = e.svc.Get(e.ctx(e.world.BAdmin, e.world.OrgB), e.world.OrgA.ID, f.ID)
	wantStatus(t, err, 403, "an administrator of another organization")
	_, err = e.svc.Get(e.ctx(e.world.BAdmin, e.world.OrgB), e.world.OrgB.ID, f.ID)
	wantStatus(t, err, 404, "own organization, other organization's file id")
	// a second reader is granted by the owning module and can then read and download
	e.mu.Lock()
	e.readers[ticket] = append(e.readers[ticket], guard.UserID)
	e.mu.Unlock()
	if err := read(guard, e.world.OrgA); err != nil {
		t.Errorf("reader added by the resolver: %v", err)
	}
	if d, err := e.svc.Open(e.ctx(guard, e.world.OrgA), e.world.OrgA.ID, f.ID, ""); err != nil {
		t.Errorf("download by that reader: %v", err)
	} else {
		_ = d.Body.Close()
	}
	// listing by scope: needs read access to the scope
	lst, _, err := e.svc.List(e.ctx(guard, e.world.OrgA), e.world.OrgA.ID, files.ListParams{Scope: ref})
	if err != nil || len(lst) != 1 {
		t.Errorf("list by scope = %d files, %v", len(lst), err)
	}
	other := &files.ScopeRef{Type: ticketScope, ID: e.ticket(staff)}
	_, _, err = e.svc.List(e.ctx(guard, e.world.OrgA), e.world.OrgA.ID, files.ListParams{Scope: other})
	wantStatus(t, err, 404, "list by a scope the caller cannot read")
	// listing without a scope shows an ordinary member only their own uploads
	lst, _, err = e.svc.List(e.ctx(guard, e.world.OrgA), e.world.OrgA.ID, files.ListParams{})
	if err != nil || len(lst) != 0 {
		t.Errorf("unscoped list for a non-uploader = %d, %v", len(lst), err)
	}
	lst, _, _ = e.svc.List(e.ctx(admin, e.world.OrgA), e.world.OrgA.ID, files.ListParams{})
	if len(lst) != 1 {
		t.Errorf("unscoped list for an administrator = %d", len(lst))
	}
	// the per-scope file limit (2 for tickets)
	e.mustUpload(e.ctx(staff, e.world.OrgA), e.world.OrgA, "b.txt", []byte("b"), ref)
	_, err = e.upload(e.ctx(staff, e.world.OrgA), e.world.OrgA, "c.txt", []byte("c"), ref)
	wantStatus(t, err, 409, "third file on a ticket limited to 2")
}

func TestIntegrationAttachDetachAndDrafts(t *testing.T) {
	e := newEnv(t, nil)
	staff, guard := e.world.Actor("staff"), e.world.Actor("guard")
	ctx := e.ctx(staff, e.world.OrgA)
	ticket := e.ticket(staff)
	ref := files.ScopeRef{Type: ticketScope, ID: ticket}

	draft := e.mustUpload(ctx, e.world.OrgA, "draft.txt", []byte("draft"), nil)
	if draft.Scope != nil || draft.RetentionUntil == nil {
		t.Fatalf("a draft has no scope and an expiry: %+v", draft)
	}
	attach := func(c context.Context, id uuid.UUID, r files.ScopeRef) (f files.File, err error) {
		err = e.db.InTenantTx(c, e.world.OrgA.ID, func(tx pgx.Tx) (err error) {
			f, err = e.svc.Attach(c, tx, e.world.OrgA.ID, id, r)
			return err
		})
		return f, err
	}
	// somebody else's draft cannot be attached
	_, err := attach(e.ctx(guard, e.world.OrgA), draft.ID, ref)
	wantStatus(t, err, 404, "attaching someone else's file")
	f, err := attach(ctx, draft.ID, ref)
	if err != nil || f.Scope == nil || f.RetentionUntil != nil {
		t.Fatalf("attach = %+v, %v", f, err)
	}
	_, err = attach(ctx, draft.ID, files.ScopeRef{Type: ticketScope, ID: uuid.New()})
	wantStatus(t, err, 409, "attaching an attached file")
	if _, err := attach(ctx, uuid.New(), ref); err == nil {
		t.Error("attaching a file that does not exist succeeded")
	}
	// detach turns it back into an expiring draft
	err = e.db.InTenantTx(ctx, e.world.OrgA.ID, func(tx pgx.Tx) error { return e.svc.Detach(ctx, tx, e.world.OrgA.ID, draft.ID) })
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.svc.Get(ctx, e.world.OrgA.ID, draft.ID)
	if err != nil || g.Scope != nil || g.RetentionUntil == nil {
		t.Fatalf("after detach = %+v, %v", g, err)
	}
	// a rolled-back transaction leaves the file unattached
	_ = e.db.InTenantTx(ctx, e.world.OrgA.ID, func(tx pgx.Tx) error {
		if _, err := e.svc.Attach(ctx, tx, e.world.OrgA.ID, draft.ID, ref); err != nil {
			t.Fatal(err)
		}
		return errors.New("the ticket insert failed")
	})
	g, _ = e.svc.Get(ctx, e.world.OrgA.ID, draft.ID)
	if g.Scope != nil {
		t.Error("attach survived a rolled-back transaction")
	}
}

func TestIntegrationDeleteAndPrune(t *testing.T) {
	e := newEnv(t, nil)
	admin, staff := e.world.Actor("org_admin"), e.world.Actor("staff")
	actx, sctx := e.ctx(admin, e.world.OrgA), e.ctx(staff, e.world.OrgA)

	img := e.mustUpload(actx, e.world.OrgA, "a.jpg", photo(t, 600, 400), nil)
	doc := e.mustUpload(actx, e.world.OrgA, "b.txt", []byte("bbb"), nil)
	keep := e.mustUpload(actx, e.world.OrgA, "keep.txt", []byte("keep"), &files.ScopeRef{Type: ticketScope, ID: e.ticket(admin)})
	expired := e.mustUpload(actx, e.world.OrgA, "old.txt", []byte("old"), nil)

	// only files.manage deletes
	wantStatus(t, e.svc.Delete(sctx, e.world.OrgA.ID, doc.ID), 403, "delete by a member without files.manage")
	if err := e.svc.Delete(actx, e.world.OrgA.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(actx, e.world.OrgA.ID, doc.ID); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.svc.Delete(actx, e.world.OrgA.ID, doc.ID), 404, "deleting twice")
	_, err := e.svc.Get(actx, e.world.OrgA.ID, doc.ID)
	wantStatus(t, err, 404, "get after delete")
	_, err = e.svc.Open(actx, e.world.OrgA.ID, doc.ID, "")
	wantStatus(t, err, 404, "download after delete")
	// the blobs are still there until the job runs
	if !e.blobExists(img.Key) || !e.blobExists(img.Key+".thumb") {
		t.Fatal("blobs must stay until maintenance.prune_files")
	}
	// a draft left for longer than its lifetime expires
	e.clock.advance(8 * 24 * time.Hour)

	res, err := e.svc.Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Expired != 1 || res.Purged != 3 {
		t.Errorf("prune = %+v, want 1 expired and 3 purged (a.jpg, b.txt, old.txt)", res)
	}
	for _, f := range []files.File{img, doc, expired} {
		for _, k := range []string{f.Key, f.Key + ".thumb", f.Key + ".medium"} {
			if e.blobExists(k) {
				t.Errorf("blob %s survived the purge", k)
			}
		}
	}
	if !e.blobExists(keep.Key) {
		t.Error("an attached file with no retention date was purged")
	}
	if n := e.fileRows(); n != 1 {
		t.Errorf("%d file rows left, want 1", n)
	}
	u, _ := e.svc.Usage(actx, e.world.OrgA.ID)
	if u.Files != 1 || u.Bytes != keep.Size {
		t.Errorf("usage after prune = %+v, want only the attached file", u)
	}
	// a second pass has nothing to do
	if res, err = e.svc.Prune(context.Background()); err != nil || res.Purged != 0 || res.Expired != 0 {
		t.Errorf("second prune = %+v, %v", res, err)
	}
	// stale pending uploads (the request died) are swept
	var stale uuid.UUID
	err = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		stale = uuid.New()
		_, err := tx.Exec(context.Background(), `
			INSERT INTO file (id, org_id, key, name, mime, status, created_at)
			VALUES ($1, $2, $3, 'half.txt', 'application/octet-stream', 'pending', now() - interval '2 days')`,
			stale, e.world.OrgA.ID, storage.FileKey(e.world.OrgA.ID, stale, time.Now(), ""))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, err = e.svc.Prune(context.Background()); err != nil || res.Expired != 1 || res.Purged != 1 {
		t.Errorf("stale pending prune = %+v, %v", res, err)
	}
	if n := e.fileRows(); n != 1 {
		t.Errorf("%d file rows after the stale sweep, want 1", n)
	}
	// the maintenance events are in the audit trail, by the job
	var jobEvents int
	_ = e.db.InTenantTx(context.Background(), e.world.OrgA.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE action = 'files.expire' AND actor_kind = 'job'`).Scan(&jobEvents)
	})
	if jobEvents != 2 {
		t.Errorf("%d files.expire job events, want 2", jobEvents)
	}
}

func TestIntegrationPruneKeepsRowsStillReferenced(t *testing.T) {
	e := newEnv(t, nil)
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	f := e.mustUpload(ctx, e.world.OrgA, "doc.txt", []byte("referenced"), nil)
	// another module's table points at the file with the composite foreign key
	err := e.db.Migrator().QueryRow(context.Background(), `SELECT 1`).Scan(new(int))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.Migrator().Exec(context.Background(), `
		CREATE TABLE pin (org_id uuid NOT NULL, file_id uuid NOT NULL, FOREIGN KEY (org_id, file_id) REFERENCES file (org_id, id))`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Migrator().Exec(context.Background(), `GRANT ALL ON pin TO openbekci_platform`); err != nil {
		t.Fatal(err)
	}
	if err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO pin VALUES ($1, $2)`, f.OrgID, f.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, e.world.OrgA.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Prune(context.Background())
	if err != nil || res.Purged != 1 {
		t.Fatalf("prune = %+v, %v", res, err)
	}
	if e.blobExists(f.Key) {
		t.Error("the blob must be gone even though the row is referenced")
	}
	if n := e.fileRows(); n != 1 {
		t.Errorf("the referenced row must stay, %d rows", n)
	}
	// and it is not purged again
	if res, err = e.svc.Prune(context.Background()); err != nil || res.Purged != 0 {
		t.Errorf("second prune = %+v, %v", res, err)
	}
}

func TestIntegrationTenantIsolationOfFileRows(t *testing.T) {
	e := newEnv(t, nil)
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	e.mustUpload(ctx, e.world.OrgA, "a.txt", []byte("a"), nil)
	var n int
	err := e.db.InTenantTx(context.Background(), e.world.OrgB.ID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM file`).Scan(&n); err != nil {
			return err
		}
		// writing a row for another organization is refused by the policy
		_, err := tx.Exec(context.Background(), `INSERT INTO file (id, org_id, key, name, mime) VALUES ($1, $2, 'x', 'x', 'x')`, uuid.New(), e.world.OrgA.ID)
		if err == nil {
			t.Error("a tenant transaction inserted a file for another organization")
		}
		return nil
	})
	_ = err
	if n != 0 {
		t.Errorf("organization B sees %d of A's files", n)
	}
}

func TestIntegrationSignedLinks(t *testing.T) {
	e := newEnv(t, nil)
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	f := e.mustUpload(ctx, e.world.OrgA, "kitchen.jpg", photo(t, 800, 600), nil)
	links := e.svc.Links(ctx, f)
	if links.Original == "" || links.Variants[files.VariantThumb] == "" {
		t.Fatalf("links = %+v", links)
	}
	parse := func(raw string) (variant string, exp int64, sig string) {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		exp, _ = strconv.ParseInt(q.Get("exp"), 10, 64)
		return q.Get("variant"), exp, q.Get("sig")
	}
	variant, exp, sig := parse(links.Variants[files.VariantThumb])
	d, err := e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, variant, exp, sig)
	if err != nil {
		t.Fatalf("a fresh signed link: %v", err)
	}
	if b := read(t, d); len(b) == 0 || d.Size != int64(len(b)) {
		t.Error("signed download returned nothing")
	}
	// tampering: other file, other variant, other org, mangled signature, no signature
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, uuid.New(), variant, exp, sig)
	wantStatus(t, err, 401, "signature of another file")
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, files.VariantMedium, exp, sig)
	wantStatus(t, err, 401, "signature reused for another variant")
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgB.ID, f.ID, variant, exp, sig)
	wantStatus(t, err, 401, "signature reused in another organization")
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, variant, exp+60, sig)
	wantStatus(t, err, 401, "extended expiry")
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, variant, exp, sig[:len(sig)-2]+"00")
	wantStatus(t, err, 401, "mangled signature")
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, variant, exp, "")
	wantStatus(t, err, 401, "empty signature")
	// expiry
	e.clock.advance(10 * time.Minute)
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, variant, exp, sig)
	wantStatus(t, err, 401, "expired link")
	// a deleted file stops being served even with a valid link
	e.clock.advance(-10 * time.Minute)
	if err := e.svc.Delete(ctx, e.world.OrgA.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.OpenSigned(context.Background(), e.world.OrgA.ID, f.ID, variant, exp, sig)
	wantStatus(t, err, 404, "link to a deleted file")
}

func TestIntegrationPermissionsGate(t *testing.T) {
	e := newEnv(t, nil)
	nobody := e.ctx(e.world.Actor("no_membership"), e.world.OrgA)
	_, err := e.upload(nobody, e.world.OrgA, "a.txt", []byte("a"), nil)
	wantStatus(t, err, 403, "upload without a membership")
	_, err = e.svc.Usage(e.ctx(e.world.Actor("staff"), e.world.OrgA), e.world.OrgA.ID)
	wantStatus(t, err, 403, "usage without files.manage")
	_, err = e.svc.Usage(context.Background(), e.world.OrgA.ID)
	wantStatus(t, err, 401, "usage without a caller")
	// generated documents (server side) need no principal and are recorded as system events
	f, err := e.svc.UploadGenerated(context.Background(), files.UploadParams{
		OrgID: e.world.OrgA.ID, Name: "statement.pdf", Content: strings.NewReader("%PDF-1.7 statement"),
		Scope: &files.ScopeRef{Type: "document", ID: uuid.New()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != files.SourceGenerated || f.UploadedBy != nil || f.RetentionUntil != nil || f.Status != files.StatusReady {
		t.Errorf("generated file = %+v", f)
	}
	var kind string
	_ = e.db.InTenantTx(context.Background(), e.world.OrgA.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT actor_kind FROM audit_event WHERE action = 'files.generate' AND target_id = $1`, f.ID).Scan(&kind)
	})
	if kind != "system" {
		t.Errorf("generated file event actor = %q, want system", kind)
	}
}
