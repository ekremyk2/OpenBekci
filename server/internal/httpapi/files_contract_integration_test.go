package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// formWithFileFirst builds an upload form with the file part first and then
// the given fields.
func formWithFileFirst(filename string, content []byte, after map[string]string) (body []byte, contentType string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", filename)
	_, _ = part.Write(content)
	for k, v := range after {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func fieldCodes(rec *httptest.ResponseRecorder) map[string]string {
	var p struct {
		Errors []struct{ Field, Code string } `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	out := map[string]string{}
	for _, f := range p.Errors {
		out[f.Field] = f.Code
	}
	return out
}

func TestIntegrationFilesFieldAfterFileIsRefusedAndUndone(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	for name, after := range map[string]map[string]string{
		"scope after the file":   {"scope": testScope + ":" + uuid.NewString()},
		"unknown after the file": {"colour": "red"},
		"name after the file":    {"name": "other.txt"},
	} {
		body, ctype := formWithFileFirst("late.txt", []byte("late fields"), after)
		rec := fe.do(t, "staff", "POST", "/v1/orgs/"+org+"/files", body, "Content-Type", ctype)
		if rec.Code != 422 || problemCode(rec) != "validation_failed" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
			continue
		}
		var field string
		for k := range after {
			field = k
		}
		if got := fieldCodes(rec)[field]; got != "multipart_order" {
			t.Errorf("%s: field code %q, want multipart_order (%s)", name, got, rec.Body.String())
		}
	}
	// the files that came with a refused form are not left behind
	if n := fe.fileCount(t); n != 0 {
		t.Errorf("%d files visible after refused uploads, want 0", n)
	}
	// an unknown field before the file is still unknown_field
	body, ctype := multipartBody(map[string]string{"colour": "red"}, "a.txt", []byte("a"))
	rec := fe.do(t, "staff", "POST", "/v1/orgs/"+org+"/files", []byte(body), "Content-Type", ctype)
	if rec.Code != 422 || fieldCodes(rec)["colour"] != "unknown_field" {
		t.Errorf("unknown field before the file: %d %s", rec.Code, rec.Body.String())
	}
}

// The site of a file is derived from the stored site row: an unknown site_id is a 404.
func TestIntegrationFilesSiteIDIsRefusedWithoutSites(t *testing.T) {
	fe := newFilesEnv(t, 0)
	rec := fe.upload(t, "org_admin", "a.txt", []byte("a"), map[string]string{"site_id": uuid.NewString()})
	if rec.Code != 404 {
		t.Errorf("site_id: %d %s", rec.Code, rec.Body.String())
	}
	if n := fe.fileCount(t); n != 0 {
		t.Errorf("%d files stored", n)
	}
}

func TestIntegrationFilesDeleteByUploaderOnlyWhileUnattached(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	draft := decodeFile(t, fe.upload(t, "staff", "draft.txt", []byte("a draft"), nil))
	attached := decodeFile(t, fe.upload(t, "staff", "att.txt", []byte("attached"), map[string]string{"scope": testScope + ":" + uuid.NewString()}))

	// the uploader discards a draft with files.upload alone
	if rec := fe.do(t, "staff", "DELETE", "/v1/orgs/"+org+"/files/"+draft.ID.String(), nil); rec.Code != 204 {
		t.Errorf("uploader deleting a draft: %d %s", rec.Code, rec.Body.String())
	}
	if rec := fe.do(t, "staff", "GET", "/v1/orgs/"+org+"/files/"+draft.ID.String()+"/meta", nil); rec.Code != 404 {
		t.Errorf("deleted draft is still there: %d", rec.Code)
	}
	// ... but not once it is attached to a resource
	rec := fe.do(t, "staff", "DELETE", "/v1/orgs/"+org+"/files/"+attached.ID.String(), nil)
	if rec.Code != 409 || problemCode(rec) != "conflict" {
		t.Errorf("uploader deleting an attached file: %d %s", rec.Code, rec.Body.String())
	}
	// a member who can read it may not delete it
	if rec := fe.do(t, "guard", "DELETE", "/v1/orgs/"+org+"/files/"+attached.ID.String(), nil); rec.Code != 403 {
		t.Errorf("reader deleting an attached file: %d", rec.Code)
	}
	// an administrator may
	if rec := fe.do(t, "org_admin", "DELETE", "/v1/orgs/"+org+"/files/"+attached.ID.String(), nil); rec.Code != 204 {
		t.Errorf("administrator deleting an attached file: %d", rec.Code)
	}
}

func TestIntegrationFilesUploadedByIsForUploaderAndManagers(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	f := decodeFile(t, fe.upload(t, "staff", "att.txt", []byte("attached"), map[string]string{"scope": testScope + ":" + uuid.NewString()}))
	staff := fe.w.Actor("staff").UserID.String()
	for actor, want := range map[string]string{"staff": staff, "org_admin": staff, "guard": ""} {
		rec := fe.do(t, actor, "GET", "/v1/orgs/"+org+"/files/"+f.ID.String()+"/meta", nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", actor, rec.Code, rec.Body.String())
		}
		got := decodeFile(t, rec)
		switch {
		case want == "" && got.UploadedBy != nil:
			t.Errorf("%s sees uploaded_by %s", actor, *got.UploadedBy)
		case want != "" && (got.UploadedBy == nil || *got.UploadedBy != want):
			t.Errorf("%s: uploaded_by = %v, want %s", actor, got.UploadedBy, want)
		}
	}
}

func TestIntegrationFilesDownloadSendsSandboxHeaders(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	f := decodeFile(t, fe.upload(t, "staff", "n.txt", []byte("notes"), nil))
	rec := fe.do(t, "staff", "GET", "/v1/orgs/"+org+"/files/"+f.ID.String(), nil)
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Security-Policy") != "sandbox" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Errorf("download: %d %v", rec.Code, h)
	}
}

// A link for an <img> tag works whatever session the browser happens to send.
func TestIntegrationFilesSignedLinkIgnoresTheSession(t *testing.T) {
	fe := newFilesEnv(t, 0)
	org := fe.w.OrgA.ID.String()
	f := decodeFile(t, fe.upload(t, "staff", "plan.jpg", gpsPhoto(t, 600, 400), map[string]string{"scope": testScope + ":" + uuid.NewString()}))
	link := f.Links.Variants["thumb"]
	if _, err := url.Parse(link); err != nil || link == "" {
		t.Fatalf("link %q: %v", link, err)
	}
	// the administrator of another organization is signed in; so is a user without any membership
	for _, actor := range []string{"b_admin", "no_membership", "staff"} {
		if rec := fe.do(t, actor, "GET", link, nil); rec.Code != 200 {
			t.Errorf("signed link with the session of %s: %d %s", actor, rec.Code, rec.Body.String())
		}
	}
	// an invalid signature is 401 whoever asks, even someone who could read the file
	bad := strings.Replace(link, "sig=", "sig=00", 1)
	for _, actor := range []string{"org_admin", "staff", ""} {
		if rec := fe.do(t, actor, "GET", bad, nil); rec.Code != 401 {
			t.Errorf("bad signature as %q: %d, want 401", actor, rec.Code)
		}
	}
	_ = org
}
