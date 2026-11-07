package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"mime/multipart"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// The authz matrix (backend plan sections 7 and 20): every operation of the
// spec x every built-in role (plus a user with no membership and a platform
// administrator) x {own organization, another organization, anonymous}. The
// expected status is derived from the spec itself:
//
//   - under /v1/orgs/{org_id}: anonymous 401; another organization 404 (no
//     active membership there, so its existence does not leak); in the own
//     organization 403 unless the role holds every permission in the
//     operation's x-openbekci-roles (the role to permission matrix is the root
//     x-openbekci-roles of the spec); a caller without a membership (also a
//     platform administrator) gets 404 there;
//   - other operations: public ones (security: []) answer the same to
//     everybody; the rest need a signed-in caller (anonymous 401) and then
//     answer the same to every kind of user.
//
// Adding an operation without a row below, or an operation under /v1/orgs
// without x-openbekci-roles, fails this test.

// mreq is a request of the matrix.
type mreq struct {
	method, path, body string
	headers            map[string]string
}

// mrow is the harness row of one operation.
type mrow struct {
	// perms are the permissions the handler requires (its authz.Require
	// calls). They must equal the operation's x-openbekci-roles annotation.
	perms []authz.Permission
	// status is the status a permitted caller gets.
	status int
	// build makes the request for org (its id replaces {org_id} in path).
	build func(w *authztest.World, org authztest.Org, t *testing.T) mreq
	// override replaces the derived expectation where an operation has its
	// own rule (public and auth endpoints); ok=false keeps the derived one.
	override func(actor, target string) (status int, ok bool)
}

func mget(path string) func(*authztest.World, authztest.Org, *testing.T) mreq {
	return func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
		return mreq{method: "GET", path: strings.ReplaceAll(path, "{org_id}", org.ID.String())}
	}
}

func fixed(method, path, body string) func(*authztest.World, authztest.Org, *testing.T) mreq {
	return func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
		return mreq{method: method, path: strings.ReplaceAll(path, "{org_id}", org.ID.String()), body: body}
	}
}

// authzMatrixRows is keyed by operationId. Add one row per new operation.
var authzMatrixRows = map[string]mrow{
	// public: the same answer for everybody; an empty body is rejected by validation (422) without side effects
	"instance.getDiscovery": {status: 200, build: mget("/.well-known/openbekci.json")},
	"auth.createSession":    {status: 422, build: fixed("POST", "/v1/auth/session", `{}`)},
	"auth.createToken":      {status: 422, build: fixed("POST", "/v1/auth/token", `{}`)},
	"auth.refreshToken":     {status: 422, build: fixed("POST", "/v1/auth/token/refresh", `{}`)},
	"auth.requestOtp":       {status: 422, build: fixed("POST", "/v1/auth/otp", `{}`)},
	"auth.requestMagicLink": {status: 422, build: fixed("POST", "/v1/auth/magic-link", `{}`)},
	"auth.consumeMagicLink": {status: 422, build: fixed("POST", "/v1/auth/magic-link/consume", `{}`)},

	// signed-in callers, no organization involved: every kind of user gets the same answer
	"instance.getInstance":        {status: 200, build: mget("/v1/instance")},
	"me.get":                      {perms: []authz.Permission{identity.PermMeRead}, status: 200, build: mget("/v1/me")},
	"me.listSessions":             {perms: []authz.Permission{identity.PermSessionsManageOwn}, status: 200, build: mget("/v1/me/sessions")},
	"me.revokeSession":            {perms: []authz.Permission{identity.PermSessionsManageOwn}, status: 204, build: fixed("DELETE", "/v1/me/sessions/current", "")},
	"me.revokeAllSessions":        {perms: []authz.Permission{identity.PermSessionsManageOwn}, status: 204, build: fixed("DELETE", "/v1/me/sessions", "")},
	"auth.deleteSession":          {status: 204, build: fixed("DELETE", "/v1/auth/session", "")},
	"auth.reauth":                 {status: 422, build: fixed("POST", "/v1/auth/reauth", `{}`)},
	"auth.requestReauthEmailCode": {status: 503 /* no mailer in the matrix environment */, build: fixed("POST", "/v1/auth/reauth/email-code", `{}`)},
	"auth.changePassword":         {status: 422, build: fixed("POST", "/v1/auth/password", `{}`)},

	// inside an organization
	"org.get": {perms: []authz.Permission{tenancy.PermOrgRead}, status: 200, build: mget("/v1/orgs/{org_id}")},
	"org.update": {perms: []authz.Permission{tenancy.PermOrgManage}, status: 200,
		build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "PATCH", path: "/v1/orgs/" + org.ID.String(), body: `{"name":"Renamed by the matrix"}`,
				headers: map[string]string{"If-Match": `"` + strconv.Itoa(w.OrgVersion(t, org)) + `"`}}
		}},
	"audit.list": {perms: []authz.Permission{audit.PermRead}, status: 200, build: mget("/v1/orgs/{org_id}/audit")},

	// instance level: only platform administrators hold platform.manage
	"platform.listAudit": {perms: []authz.Permission{audit.PermPlatformRead}, status: 200, build: mget("/v1/platform/audit"),
		override: func(actor, _ string) (int, bool) {
			switch actor {
			case "anonymous":
				return 0, false // 401 as derived
			case "platform_admin":
				return 200, true
			}
			return 403, true
		}},

	"files.list": {perms: []authz.Permission{files.PermRead}, status: 200, build: mget("/v1/orgs/{org_id}/files")},
	"files.upload": {perms: []authz.Permission{files.PermUpload}, status: 201,
		build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			body, ctype := multipartBody(map[string]string{"scope_type": testScope, "scope_id": uuid.NewString()}, "matrix.txt", []byte("uploaded by the matrix"))
			return mreq{method: "POST", path: "/v1/orgs/" + org.ID.String() + "/files", body: body, headers: map[string]string{"Content-Type": ctype}}
		}},
	"files.get": {perms: []authz.Permission{files.PermRead}, status: 200,
		build: func(_ *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/orgs/" + org.ID.String() + "/files/" + matrixFile(t, org).ID.String()}
		}},
	"files.download": {perms: []authz.Permission{files.PermRead}, status: 200,
		build: func(_ *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/orgs/" + org.ID.String() + "/files/" + matrixFile(t, org).ID.String() + "/content"}
		}},
	"files.delete": {perms: []authz.Permission{files.PermManage}, status: 204,
		build: func(_ *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "DELETE", path: "/v1/orgs/" + org.ID.String() + "/files/" + matrixFile(t, org).ID.String()}
		}},
}

// matrixFiles is the files service of the environment the matrix runs against.
var matrixFiles *files.Service

// matrixFile stores a file in org that every member may read.
func matrixFile(t *testing.T, org authztest.Org) files.File {
	t.Helper()
	f, err := matrixFiles.UploadGenerated(t.Context(), files.UploadParams{
		OrgID: org.ID, Name: "matrix.txt", Scope: &files.ScopeRef{Type: testScope, ID: uuid.New()},
		Content: strings.NewReader("a file for the authorization matrix"),
	})
	if err != nil {
		t.Fatalf("store a file for the matrix: %v", err)
	}
	return f
}

// multipartBody builds an upload form: fields first, then the file part.
func multipartBody(fields map[string]string, filename string, content []byte) (body, contentType string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	part, _ := w.CreateFormFile("file", filename)
	_, _ = part.Write(content)
	_ = w.Close()
	return buf.String(), w.FormDataContentType()
}

func extJSON(t *testing.T, ext map[string]any, name string, into any) bool {
	t.Helper()
	raw, ok := ext[name]
	if !ok {
		return false
	}
	var b []byte
	switch v := raw.(type) {
	case json.RawMessage:
		b = v
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%s is not the expected shape: %v", name, err)
	}
	return true
}

func TestIntegrationAuthzMatrix(t *testing.T) {
	spec := loadSpec(t)
	e := newIDEnv(t, false, false)
	w := authztest.NewWorld(t, e.db, e.svc)
	matrixFiles = e.files

	var matrix map[string][]string // role -> permissions, from the spec
	if !extJSON(t, spec.Extensions, "x-openbekci-roles", &matrix) {
		t.Fatal("the spec has no root x-openbekci-roles (role to permission matrix); run task gen")
	}

	type opKey struct{ method, path string }
	ops := map[string]opKey{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			ops[op.OperationID] = opKey{method, path}
		}
	}
	ids := make([]string, 0, len(ops))
	for id := range ops {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for id := range authzMatrixRows {
		if _, ok := ops[id]; !ok {
			t.Errorf("authz matrix row %q matches no operation of the spec; remove it", id)
		}
	}

	var serial int
	do := func(r mreq, token string) *httptest.ResponseRecorder {
		serial++
		rd := strings.NewReader(r.body)
		hr := httptest.NewRequest(r.method, r.path, rd)
		hr.RemoteAddr = fmt.Sprintf("203.0.113.%d:4000", 1+serial%250) // spread over addresses: no abuse limiter trips
		if r.body != "" {
			hr.Header.Set("Content-Type", "application/json")
		}
		for k, v := range r.headers {
			hr.Header.Set(k, v)
		}
		if token != "" {
			hr.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, hr)
		return rec
	}

	for _, id := range ids {
		key := ops[id]
		op := spec.Paths.Find(key.path).GetOperation(key.method)
		row, ok := authzMatrixRows[id]
		if !ok {
			t.Errorf("operation %s (%s %s) has no authz matrix row: add one to authzMatrixRows in internal/httpapi/authzmatrix_test.go", id, key.method, key.path)
			continue
		}
		orgScoped := isOrgPath(key.path)
		var required []string
		hasRoles := extJSON(t, op.Extensions, "x-openbekci-roles", &required)
		if orgScoped && (!hasRoles || len(required) == 0) {
			t.Errorf("operation %s (%s %s) is under /v1/orgs/{org_id} but has no x-openbekci-roles: list the permission(s) it requires", id, key.method, key.path)
			continue
		}
		if hasRoles {
			got := make([]string, len(row.perms))
			for i, p := range row.perms {
				got[i] = string(p)
			}
			want := slices.Clone(required)
			sort.Strings(got)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Errorf("operation %s: x-openbekci-roles %v does not match the permissions its handler requires %v (row perms)", id, want, got)
			}
		} else if len(row.perms) > 0 {
			t.Errorf("operation %s: the row lists permissions %v but the operation has no x-openbekci-roles", id, row.perms)
		}
		public := op.Security != nil && len(*op.Security) == 0

		type target struct {
			name string
			org  authztest.Org
		}
		targets := []target{{"own", w.OrgA}}
		if orgScoped {
			targets = append(targets, target{"other", w.OrgB})
		}
		check := func(actor, tgt string, rec *httptest.ResponseRecorder, want int) {
			if rec.Code != want {
				t.Errorf("%s as %s on %s org: status %d, want %d: %s", id, actor, tgt, rec.Code, want, strings.TrimSpace(rec.Body.String()))
			}
		}
		// anonymous
		for _, tg := range targets {
			want := 401
			if public {
				want = row.status
			}
			if row.override != nil {
				if s, ok := row.override("anonymous", tg.name); ok {
					want = s
				}
			}
			check("anonymous", tg.name, do(row.build(w, tg.org, t), ""), want)
		}
		for _, a := range w.Actors {
			for _, tg := range targets {
				want := row.status
				if orgScoped {
					switch {
					case tg.name == "other" || a.Role == "":
						want = 404
					case !holdsAll(matrix[string(a.Role)], required):
						want = 403
					}
				}
				if row.override != nil {
					if s, ok := row.override(a.Name, tg.name); ok {
						want = s
					}
				}
				check(a.Name, tg.name, do(row.build(w, tg.org, t), w.Token(t, a)), want)
			}
		}
	}
}

func holdsAll(have, need []string) bool {
	for _, n := range need {
		if !slices.Contains(have, n) {
			return false
		}
	}
	return true
}

// TestAuthzMatrixRowsCoverSpec checks the table against the spec without a database: every operation has a row, every org operation is annotated, no row is stale.
func TestAuthzMatrixRowsCoverSpec(t *testing.T) {
	spec := loadSpec(t)
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			seen[op.OperationID] = true
			if _, ok := authzMatrixRows[op.OperationID]; !ok {
				t.Errorf("operation %s (%s %s) has no authz matrix row: add one to authzMatrixRows in internal/httpapi/authzmatrix_test.go", op.OperationID, method, path)
			}
			if isOrgPath(path) {
				var roles []string
				if !extJSON(t, op.Extensions, "x-openbekci-roles", &roles) || len(roles) == 0 {
					t.Errorf("operation %s (%s %s) is under /v1/orgs/{org_id} but has no x-openbekci-roles", op.OperationID, method, path)
				}
			}
		}
	}
	for id := range authzMatrixRows {
		if !seen[id] {
			t.Errorf("authz matrix row %q matches no operation of the spec", id)
		}
	}
}
