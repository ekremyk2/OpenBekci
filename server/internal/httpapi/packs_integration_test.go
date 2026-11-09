package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/packs"
)

// The pack endpoints end to end over HTTP with the example pack compiled in.
func TestIntegrationPackEndpoints(t *testing.T) {
	e := newIDEnv(t, false, false)
	w := authztest.NewWorld(t, e.db, e.svc)
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	host, err := packs.New(packs.Config{DB: e.db, Log: log, EncryptionKey: []byte("0123456789abcdef0123456789abcdef-enc")}, []sdk.Pack{example.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	e.tenSvc.WithSiteHook(host)
	e.deps.Packs = host
	e.deps.Instance.WithPacks(host)
	host.OnChange(e.deps.Instance.Invalidate)
	e.h = NewRouter(e.deps, Options{})

	org := w.OrgA.ID.String()
	admin := "Bearer " + w.Token(t, w.Actor("org_admin"))
	manager := "Bearer " + w.Token(t, w.Actor("site_manager"))
	base := "/v1/orgs/" + org

	call := func(auth, method, path, body string) (int, map[string]any, string) {
		t.Helper()
		var hdr []string
		if auth != "" {
			hdr = append(hdr, "Authorization", auth)
		}
		rec := e.do(method, path, body, false, hdr...)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec.Body.String()
	}

	// only org admins use the pack endpoints
	if code, _, _ := call(manager, "GET", base+"/packs", ""); code != 403 {
		t.Fatalf("site manager lists packs: %d, want 403", code)
	}
	if code, _, _ := call("", "GET", base+"/packs", ""); code != 401 {
		t.Fatalf("anonymous lists packs: %d, want 401", code)
	}

	// a site exists before the pack: enabling fills its unchanged settings
	if code, _, body := call(admin, "POST", base+"/sites", `{"name":"First","timezone":"Europe/Istanbul","currency":"TRY"}`); code != 201 {
		t.Fatalf("create site: %d %s", code, body)
	}
	code, list, body := call(admin, "GET", base+"/packs", "")
	items, _ := list["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["id"] != "example" || items[0].(map[string]any)["enabled"] != false {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, _, body := call(admin, "POST", base+"/packs/example/enable", `{"settings":{"example_sender":"X"}}`); code != 422 {
		t.Fatalf("enable without the required secrets: %d %s", code, body)
	}
	if code, _, body := call(admin, "POST", base+"/packs/nope/enable", `{}`); code != 404 {
		t.Fatalf("enable an unknown pack: %d %s", code, body)
	}
	const secret = "k_live_http_secret_1"
	code, pk, body := call(admin, "POST", base+"/packs/example/enable", `{"settings":{"example_api_key":"`+secret+`","example_webhook_secret":"whsec_http_12345","example_sender":"HTTPSEND"}}`)
	if code != 200 || pk["enabled"] != true || pk["enabled_at"] == nil {
		t.Fatalf("enable: %d %s", code, body)
	}
	if strings.Contains(body, secret) || strings.Contains(body, "whsec_http_12345") {
		t.Fatalf("a secret was returned by the enable response: %s", body)
	}
	if sec, _ := pk["secrets_set"].([]any); len(sec) != 2 {
		t.Fatalf("secrets_set = %v", pk["secrets_set"])
	}
	if _, body2 := func() (int, string) { c, _, b := call(admin, "GET", base+"/packs", ""); return c, b }(); strings.Contains(body2, secret) {
		t.Fatalf("a secret was returned by the list: %s", body2)
	}

	// the discovery document and /v1/instance show the pack and the SDK version
	code, disco, body := call("", "GET", "/.well-known/openbekci.json", "")
	if code != 200 || disco["sdk_version"] != sdk.Version || !anyIs(disco["packs"], "example") {
		t.Fatalf("discovery: %d %s", code, body)
	}
	code, inst, body := call(admin, "GET", "/v1/instance", "")
	details, _ := inst["pack_details"].([]any)
	if code != 200 || len(details) != 1 || details[0].(map[string]any)["enabled"] != true || inst["sdk_version"] != sdk.Version {
		t.Fatalf("instance: %d %s", code, body)
	}

	// site settings show the "pack default" marker, for the site that existed and the one created later
	code, sites, body := call(admin, "GET", base+"/sites", "")
	first := sites["items"].([]any)[0].(map[string]any)["id"].(string)
	if code != 200 {
		t.Fatalf("sites: %d %s", code, body)
	}
	code, st, body := call(admin, "GET", base+"/sites/"+first+"/settings", "")
	defs, _ := st["pack_defaults"].(map[string]any)
	if code != 200 || defs["late_fee_policy"] == nil || defs["late_fee_policy"].(map[string]any)["pack"] != "example" || st["legal_preset"] != "example" {
		t.Fatalf("site settings after enabling: %d %s", code, body)
	}
	if err := e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE organization SET country_code = 'XX' WHERE id = $1`, w.OrgA.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, created, body := call(admin, "POST", base+"/sites", `{"name":"Second","timezone":"Europe/Istanbul","currency":"TRY"}`)
	if code != 201 {
		t.Fatalf("create second site: %d %s", code, body)
	}
	code, st2, body := call(admin, "GET", base+"/sites/"+created["id"].(string)+"/settings", "")
	if code != 200 || st2["pack_defaults"] == nil || st2["legal_preset"] != "example" {
		t.Fatalf("settings of a site created after enabling: %d %s", code, body)
	}

	// pack settings: replace non-secret values; secrets keep theirs; site scope
	code, res, body := call(admin, "PUT", base+"/packs/example/settings", `{"scope":"org","values":{"example_sender":"CHANGED"}}`)
	if code != 200 || res["values"].(map[string]any)["example_sender"] != "CHANGED" || len(res["secrets_set"].([]any)) != 2 {
		t.Fatalf("update settings: %d %s", code, body)
	}
	code, res, body = call(admin, "PUT", base+"/packs/example/settings", `{"scope":"site","site_id":"`+first+`","values":{"example_account":"ACC"}}`)
	if code != 200 || res["site_id"] != first {
		t.Fatalf("site settings: %d %s", code, body)
	}
	if code, _, body := call(admin, "PUT", base+"/packs/example/settings", `{"scope":"site","values":{}}`); code != 422 {
		t.Fatalf("site scope without a site: %d %s", code, body)
	}
	if code, _, body := call(admin, "PUT", base+"/packs/example/settings", `{"scope":"org","values":{"example_sender":"WAYTOOLONGSENDER"}}`); code != 422 {
		t.Fatalf("a value that breaks the schema: %d %s", code, body)
	}

	// the webhook/callback route is public, tenant from the path, the pack verifies the caller
	callback := base + "/packs/example/callback"
	if code, _, _ := call("", "POST", callback, `{}`); code != 401 {
		t.Fatalf("callback without a signature: %d, want 401 from the pack", code)
	}
	payload := `{"x":1}`
	rec := e.do("POST", callback, payload, false, example.SignatureHeader, example.Sign("whsec_http_12345", []byte(payload)))
	if rec.Code != 200 {
		t.Fatalf("signed callback: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/v1/orgs/"+w.OrgB.ID.String()+"/packs/example/callback", payload, false, example.SignatureHeader, example.Sign("whsec_http_12345", []byte(payload))); rec.Code != 404 {
		t.Fatalf("callback of an organization without the pack: %d, want 404", rec.Code)
	}
	if rec := e.do("GET", base+"/packs/example/enable", "", false); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("GET on the enable operation: %d", rec.Code)
	}

	// disable: the pack stays listed, routes answer 404
	code, pk, body = call(admin, "POST", base+"/packs/example/disable", "")
	if code != 200 || pk["enabled"] != false {
		t.Fatalf("disable: %d %s", code, body)
	}
	if rec := e.do("POST", callback, payload, false, example.SignatureHeader, example.Sign("whsec_http_12345", []byte(payload))); rec.Code != 404 {
		t.Fatalf("callback after disable: %d, want 404", rec.Code)
	}
	var audited int
	if err := e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE org_id = $1 AND action LIKE 'pack.%'`, w.OrgA.ID).Scan(&audited)
	}); err != nil {
		t.Fatal(err)
	}
	if audited < 4 {
		t.Fatalf("%d pack audit events, want at least enable, update settings (twice), presets and disable", audited)
	}
}

func anyIs(v any, want string) bool {
	list, _ := v.([]any)
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}
