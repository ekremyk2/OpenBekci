package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

type filesEnv struct {
	*idEnv
	w      *authztest.World
	router http.Handler
}

func newFilesEnv(t *testing.T, fileLimit int64) *filesEnv {
	t.Helper()
	e := newIDEnv(t, false, false)
	fe := &filesEnv{idEnv: e, w: authztest.NewWorld(t, e.db, e.svc), router: e.h}
	if fileLimit > 0 {
		// a second router with a small upload limit, over the same database
		signer := storage.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
		small := files.NewService(e.db, storage.NewLocal(t.TempDir(), signer), files.Config{MaxUploadBytes: fileLimit, Signer: signer})
		allow := func(context.Context, pgx.Tx, files.ScopeRef) (bool, error) { return true, nil }
		small.RegisterResolver(testScope, files.ResolverFuncs{Read: allow, Attach: allow}, files.WithMaxFiles(3))
		deps := e.deps
		deps.Files = small
		fe.router = NewRouter(deps, Options{MaxUploadBytes: UploadBodyLimit(fileLimit)})
	}
	return fe
}

func (fe *filesEnv) do(t *testing.T, actor, method, path string, body []byte, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.20:4000"
	if actor != "" {
		r.Header.Set("Authorization", "Bearer "+fe.w.Token(t, fe.w.Actor(actor)))
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	fe.router.ServeHTTP(rec, r)
	return rec
}

func (fe *filesEnv) upload(t *testing.T, actor, filename string, content []byte, fields map[string]string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	body, ctype := multipartBody(fields, filename, content)
	return fe.do(t, actor, "POST", "/v1/orgs/"+fe.w.OrgA.ID.String()+"/files", []byte(body), append([]string{"Content-Type", ctype}, headers...)...)
}

func decodeFile(t *testing.T, rec *httptest.ResponseRecorder) (f struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Mime      string    `json:"mime"`
	Sha256    string    `json:"sha256"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	ScopeType *string   `json:"scope_type"`
	Variants  map[string]struct {
		Width, Height int
	} `json:"variants"`
	Links struct {
		Original *string           `json:"original"`
		Variants map[string]string `json:"variants"`
	} `json:"links"`
}) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return f
}

func gpsPhoto(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	payload := append([]byte("Exif\x00\x00II*\x00\x08\x00\x00\x00"), []byte("GPS-LAT-41.0082-LON-28.9784")...)
	out := append([]byte{}, raw[:2]...)
	out = append(out, 0xFF, 0xE1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	out = append(out, payload...)
	return append(out, raw[2:]...)
}

func TestIntegrationFilesUploadAndDownloadOverHTTP(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	in := gpsPhoto(t, 2000, 1000)

	rec := fe.upload(t, "staff", "balcony.jpg", in, nil)
	if rec.Code != 201 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	f := decodeFile(t, rec)
	if loc := rec.Header().Get("Location"); loc != "/v1/orgs/"+org+"/files/"+f.ID.String() {
		t.Errorf("Location = %q", loc)
	}
	if f.Status != "ready" || f.Source != "uploaded" || f.Mime != "image/jpeg" || f.Name != "balcony.jpg" || f.ScopeType != nil {
		t.Errorf("file = %+v", f)
	}
	if f.Variants["thumb"].Width != 256 || f.Variants["thumb"].Height != 128 || f.Variants["medium"].Width != 1600 {
		t.Errorf("variants = %+v", f.Variants)
	}

	// the thumbnail through the API: image, inline, no EXIF, never cached
	thumb := fe.do(t, "staff", "GET", "/v1/orgs/"+org+"/files/"+f.ID.String()+"/content?variant=thumb", nil)
	if thumb.Code != 200 {
		t.Fatalf("thumb: %d %s", thumb.Code, thumb.Body.String())
	}
	h := thumb.Header()
	if h.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "private, no-store" {
		t.Errorf("thumb headers = %v", h)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumb.Body.Bytes()))
	if err != nil || format != "jpeg" || cfg.Width != 256 {
		t.Errorf("thumb decodes as %+v %s %v", cfg, format, err)
	}
	for _, name := range []string{"thumb", "medium", ""} {
		path := "/v1/orgs/" + org + "/files/" + f.ID.String() + "/content"
		if name != "" {
			path += "?variant=" + name
		}
		b := fe.do(t, "staff", "GET", path, nil).Body.Bytes()
		if bytes.Contains(b, []byte("Exif")) || bytes.Contains(b, []byte("GPS-LAT")) {
			t.Errorf("variant %q still carries the EXIF GPS block", name)
		}
	}
	if fe.do(t, "staff", "GET", "/v1/orgs/"+org+"/files/"+f.ID.String()+"/content?variant=huge", nil).Code != 422 {
		t.Error("an unknown variant must be a validation error")
	}

	// a document is an attachment
	doc := fe.upload(t, "staff", "rules.txt", []byte("No noise after ten."), nil)
	df := decodeFile(t, doc)
	d := fe.do(t, "staff", "GET", "/v1/orgs/"+org+"/files/"+df.ID.String()+"/content", nil)
	if d.Code != 200 || !strings.HasPrefix(d.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(d.Header().Get("Content-Disposition"), "rules.txt") ||
		d.Body.String() != "No noise after ten." || d.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("document: %d %v %q", d.Code, d.Header(), d.Body.String())
	}

	// the uploader and administrators read it back; an unrelated member does not see a draft
	if rec := fe.do(t, "staff", "GET", "/v1/orgs/"+org+"/files/"+df.ID.String(), nil); rec.Code != 200 {
		t.Errorf("get by the uploader: %d", rec.Code)
	}
	if rec := fe.do(t, "org_admin", "GET", "/v1/orgs/"+org+"/files/"+df.ID.String(), nil); rec.Code != 200 {
		t.Errorf("get by an administrator: %d", rec.Code)
	}
	if rec := fe.do(t, "guard", "GET", "/v1/orgs/"+org+"/files/"+df.ID.String(), nil); rec.Code != 404 {
		t.Errorf("get by an unrelated member: %d, want 404", rec.Code)
	}
	if rec := fe.do(t, "guard", "GET", "/v1/orgs/"+org+"/files/"+df.ID.String()+"/content", nil); rec.Code != 404 {
		t.Errorf("download by an unrelated member: %d, want 404", rec.Code)
	}
	// delete: administrators only
	if rec := fe.do(t, "staff", "DELETE", "/v1/orgs/"+org+"/files/"+df.ID.String(), nil); rec.Code != 403 {
		t.Errorf("delete by staff: %d", rec.Code)
	}
	if rec := fe.do(t, "org_admin", "DELETE", "/v1/orgs/"+org+"/files/"+df.ID.String(), nil); rec.Code != 204 {
		t.Errorf("delete by an administrator: %d", rec.Code)
	}
	if rec := fe.do(t, "org_admin", "GET", "/v1/orgs/"+org+"/files/"+df.ID.String(), nil); rec.Code != 404 {
		t.Errorf("get after delete: %d", rec.Code)
	}

	// scoped upload and listing through the scope's resolver
	scope := uuid.NewString()
	rec = fe.upload(t, "staff", "att.txt", []byte("attached"), map[string]string{"scope_type": testScope, "scope_id": scope})
	if rec.Code != 201 || decodeFile(t, rec).ScopeType == nil {
		t.Fatalf("scoped upload: %d %s", rec.Code, rec.Body.String())
	}
	list := fe.do(t, "guard", "GET", "/v1/orgs/"+org+"/files?scope_type="+testScope+"&scope_id="+scope, nil)
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || list.Code != 200 || len(page.Items) != 1 || page.NextCursor != nil {
		t.Errorf("list by scope: %d %s", list.Code, list.Body.String())
	}
	if rec := fe.do(t, "guard", "GET", "/v1/orgs/"+org+"/files?scope_type="+testScope, nil); rec.Code != 422 {
		t.Errorf("scope_type without scope_id: %d", rec.Code)
	}
}

func TestIntegrationFilesUploadRejections(t *testing.T) {
	fe := newFilesEnv(t, 8192)
	org := fe.w.OrgA.ID.String()
	problemCode := func(rec *httptest.ResponseRecorder) string {
		var p struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return p.Code
	}

	// size limit: the file part over the per-file limit, through the streaming path
	rec := fe.upload(t, "staff", "big.txt", bytes.Repeat([]byte("a"), 8193), nil)
	if rec.Code != 413 || problemCode(rec) != "payload_too_large" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Errorf("file over the limit: %d %s", rec.Code, rec.Body.String())
	}
	// ... and the request body over the request cap is refused before anything is read
	rec = fe.upload(t, "staff", "huge.txt", bytes.Repeat([]byte("a"), 3<<20), nil)
	if rec.Code != 413 {
		t.Errorf("request over the body cap: %d", rec.Code)
	}
	// ... but the JSON cap is not what uploads are held to: a 60 KiB body is far over 1 KiB-ish JSON limits only if it were JSON
	fe2 := newFilesEnv(t, 0)
	if rec := fe2.upload(t, "staff", "mid.txt", bytes.Repeat([]byte("a"), 2<<20), nil); rec.Code != 201 {
		t.Errorf("a 2 MiB upload must pass the multipart route although JSON bodies are capped at 1 MiB: %d %s", rec.Code, rec.Body.String())
	}

	exe := append([]byte("MZ\x90\x00"), bytes.Repeat([]byte{0}, 600)...)
	if rec = fe.upload(t, "staff", "holiday.jpg", exe, nil); rec.Code != 415 || problemCode(rec) != "unsupported_media_type" {
		t.Errorf("exe as jpg: %d %s", rec.Code, rec.Body.String())
	}
	// not multipart at all
	rec = fe.do(t, "staff", "POST", "/v1/orgs/"+org+"/files", []byte(`{"name":"x"}`), "Content-Type", "application/json")
	if rec.Code != 415 {
		t.Errorf("JSON body: %d %s", rec.Code, rec.Body.String())
	}
	// unknown form field, missing file, scope id without type, bad site id
	body, ctype := multipartBody(map[string]string{"colour": "red"}, "a.txt", []byte("a"))
	if rec = fe.do(t, "staff", "POST", "/v1/orgs/"+org+"/files", []byte(body), "Content-Type", ctype); rec.Code != 422 {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body.String())
	}
	var nofile bytes.Buffer
	nofile.WriteString("--b\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nx\r\n--b--\r\n")
	if rec = fe.do(t, "staff", "POST", "/v1/orgs/"+org+"/files", nofile.Bytes(), "Content-Type", "multipart/form-data; boundary=b"); rec.Code != 422 {
		t.Errorf("no file part: %d %s", rec.Code, rec.Body.String())
	}
	if rec = fe.upload(t, "staff", "a.txt", []byte("a"), map[string]string{"scope_id": uuid.NewString()}); rec.Code != 422 {
		t.Errorf("scope_id without scope_type: %d", rec.Code)
	}
	if rec = fe.upload(t, "staff", "a.txt", []byte("a"), map[string]string{"site_id": "nope"}); rec.Code != 422 {
		t.Errorf("bad site id: %d", rec.Code)
	}
	if rec = fe.upload(t, "staff", "a.txt", []byte("a"), map[string]string{"scope_type": "nonesuch", "scope_id": uuid.NewString()}); rec.Code != 422 {
		t.Errorf("unregistered scope type: %d %s", rec.Code, rec.Body.String())
	}
	// ticket-style limit: the test scope allows 3 files per resource
	scope := map[string]string{"scope_type": testScope, "scope_id": uuid.NewString()}
	for i := 0; i < 3; i++ {
		if rec = fe.upload(t, "staff", "p.txt", []byte("p"), scope); rec.Code != 201 {
			t.Fatalf("file %d on the resource: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec = fe.upload(t, "staff", "p.txt", []byte("p"), scope); rec.Code != 409 {
		t.Errorf("fourth file on the resource: %d %s", rec.Code, rec.Body.String())
	}
	// unauthenticated uploads never get as far as reading the body
	if rec = fe.upload(t, "", "a.txt", []byte("a"), nil); rec.Code != 401 {
		t.Errorf("anonymous upload: %d", rec.Code)
	}
}

func TestIntegrationFilesIdempotentUpload(t *testing.T) {
	fe := newFilesEnv(t, 0)
	key := "upload-" + uuid.NewString()
	first := fe.upload(t, "staff", "once.txt", []byte("same bytes"), nil, "Idempotency-Key", key)
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	// a retry builds a fresh multipart boundary but is the same upload
	second := fe.upload(t, "staff", "once.txt", []byte("same bytes"), nil, "Idempotency-Key", key)
	if second.Code != 201 || second.Header().Get("Idempotent-Replayed") != "true" || second.Body.String() != first.Body.String() {
		t.Errorf("retry: %d replayed=%q same=%v", second.Code, second.Header().Get("Idempotent-Replayed"), second.Body.String() == first.Body.String())
	}
	// only one file exists
	rec := fe.do(t, "org_admin", "GET", "/v1/orgs/"+fe.w.OrgA.ID.String()+"/files", nil)
	var page struct{ Items []json.RawMessage }
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 {
		t.Errorf("%d files after an idempotent retry, want 1", len(page.Items))
	}
}

func TestIntegrationFilesSignedLinksOverHTTP(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	f := decodeFile(t, fe.upload(t, "staff", "plan.jpg", gpsPhoto(t, 600, 400), nil))
	if f.Links.Original == nil || f.Links.Variants["thumb"] == "" {
		t.Fatalf("links = %+v", f.Links)
	}
	// without any credentials, the signed link works
	link := f.Links.Variants["thumb"]
	rec := fe.do(t, "", "GET", link, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("signed link: %d %v", rec.Code, rec.Header())
	}
	u, _ := url.Parse(link)
	q := u.Query()
	base := "/v1/orgs/" + org + "/files/" + f.ID.String() + "/content"
	// no signature, a broken one, the signature on the original instead of the thumbnail, an extended expiry
	for name, path := range map[string]string{
		"no signature":         base,
		"mangled signature":    base + "?variant=thumb&exp=" + q.Get("exp") + "&sig=" + q.Get("sig")[:60] + "0000",
		"signature of variant": base + "?exp=" + q.Get("exp") + "&sig=" + q.Get("sig"),
		"extended expiry":      base + "?variant=thumb&exp=99999999999&sig=" + q.Get("sig"),
		"expired":              base + "?variant=thumb&exp=1000&sig=" + q.Get("sig"),
	} {
		if rec := fe.do(t, "", "GET", path, nil); rec.Code != 401 {
			t.Errorf("%s: %d, want 401", name, rec.Code)
		}
	}
	// a link for one organization does not open a file id of another
	if rec := fe.do(t, "", "GET", "/v1/orgs/"+fe.w.OrgB.ID.String()+"/files/"+f.ID.String()+"/content?"+u.RawQuery, nil); rec.Code == 200 {
		t.Error("a signed link opened another organization's path")
	}
}

func TestIntegrationFilesMatrixWiring(t *testing.T) {
	// the spec marks the upload and download as streamed and the role matrix grants files.upload to every member
	spec := loadSpec(t)
	for _, c := range []struct{ path, method string }{
		{"/v1/orgs/{org_id}/files", "POST"}, {"/v1/orgs/{org_id}/files/{file_id}/content", "GET"},
	} {
		op := spec.Paths.Find(c.path).GetOperation(c.method)
		if !extBool(op.Extensions, "x-openbekci-stream") {
			t.Errorf("%s %s lacks x-openbekci-stream", c.method, c.path)
		}
	}
	var matrix map[string][]string
	if !extJSON(t, spec.Extensions, "x-openbekci-roles", &matrix) {
		t.Fatal("no role matrix in the spec")
	}
	for role, perms := range matrix {
		if role == "platform_admin" {
			continue
		}
		if !holdsAll(perms, []string{"files.upload", "files.read"}) {
			t.Errorf("role %s lacks files.upload or files.read", role)
		}
		if got := holdsAll(perms, []string{"files.manage"}); got != (role == "org_admin" || role == "site_manager") {
			t.Errorf("role %s files.manage = %v", role, got)
		}
	}
}
