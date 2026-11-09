package sdktest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/problem"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

const (
	apiKey  = "k_live_example_1"
	whSec   = "whsec_0123456789"
	sampleA = "example-bank-v1\ndate;amount;counterparty;description;reference;balance\n" +
		"03.02.2026;1.250,00;Ayse Yilmaz;Dues February;A-12-202602;10.250,00\n" +
		"05.02.2026;-30,50;Bank;Fee;;10.219,50\n" +
		"07.02.2026;800,00;Mehmet Kaya;Dues;B-3-202602;11.019,50\n"
)

var settings = map[string]any{"example_api_key": apiKey, "example_webhook_secret": whSec, "example_sender": "OPENBEKCI"}

func newExample() sdk.Pack { return example.New() }

func signed(body []byte, secret string) sdk.WebhookRequest {
	return sdk.WebhookRequest{Method: "POST", Body: body, Header: sdktest.Header(example.SignatureHeader, example.Sign(secret, body))}
}

// TestExamplePackConformsPaymentProvider runs the payment provider conformance suite on the example pack.
func TestExamplePackConformsPaymentProvider(t *testing.T) {
	sdktest.ConformPaymentProvider(t, newExample, sdktest.PaymentFixture{
		Provider: "example",
		Settings: settings,
		Paid: func(e sdktest.PaidEvent) sdk.WebhookRequest {
			body, _ := json.Marshal(map[string]any{"event": "paid", "txn": e.Txn, "site": e.Site, "unit": e.Unit, "debtor_kind": e.DebtorKind,
				"gross": e.Gross, "fee": e.Fee, "currency": e.Currency, "reference": e.Reference})
			return signed(body, whSec)
		},
		Tampered: func(r sdk.WebhookRequest) sdk.WebhookRequest { return signed(r.Body, "wrong-secret") },
		Refunded: func(txn, refund string) sdk.WebhookRequest {
			body, _ := json.Marshal(map[string]any{"event": "refunded", "txn": txn, "refund_id": refund})
			return signed(body, whSec)
		},
	})
}

// TestExamplePackConformsStatementImporter runs the statement importer conformance suite on the example pack.
func TestExamplePackConformsStatementImporter(t *testing.T) {
	sdktest.ConformStatementImporter(t, newExample, "example", []sdktest.ImporterSample{
		{Name: "three lines", Filename: "february.csv", Data: []byte(sampleA), WantLines: 3, WantTotal: 125000 - 3050 + 80000},
		{Name: "one bad row", Filename: "bad.csv", Data: []byte("example-bank-v1\ndate;amount\n01.03.2026;10,00\nnot a date;5,00\n"), WantLines: 1, WantTotal: 1000, WantErrors: 1},
	}, []byte("date,amount\n2026-01-01,10\n"), sdktest.WithSettings(settings))
}

func TestExampleLifecycleAndSettings(t *testing.T) {
	pk := example.New()
	h := sdktest.New(t, pk, sdktest.WithoutEnable())
	ctx := h.Ctx()

	// before enabling: nothing is selected, routes answer 404
	if rec := h.CallRoute("POST", "/callback", []byte("{}")); rec.Code != http.StatusNotFound {
		t.Fatalf("route of a disabled pack: %d, want 404", rec.Code)
	}
	list, err := h.Host.List(ctx, h.Env.OrgID)
	if err != nil || len(list) != 1 || list[0].Enabled {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if list[0].Schemas["org"] == nil || list[0].Schemas["site"] == nil || list[0].Countries[0] != "XX" {
		t.Errorf("the list lacks the manifest or the schemas: %+v", list[0])
	}

	// settings are validated against the schema (required secrets) and ValidateSettings
	if _, err := h.TryEnable(map[string]any{"example_sender": "x"}); !isValidation(err) {
		t.Fatalf("enable without the required secrets: %v, want a validation problem", err)
	}
	if _, err := h.TryEnable(map[string]any{"example_api_key": "bad", "example_webhook_secret": whSec}); !isValidation(err) {
		t.Fatalf("enable with refused credentials: %v, want a validation problem", err)
	}
	if _, err := h.TryEnable(map[string]any{"example_api_key": apiKey, "example_webhook_secret": whSec, "example_sender": "forbidden"}); !isValidation(err) {
		t.Fatalf("enable with a ValidateSettings failure: %v, want a validation problem", err)
	}
	if _, err := h.TryEnable(map[string]any{"example_api_key": apiKey, "example_webhook_secret": whSec, "other_key": 1}); !isValidation(err) {
		t.Fatalf("enable with an unknown key: %v, want a validation problem", err)
	}
	if on, _ := h.Host.Enabled(ctx, h.Env.OrgID, "example"); on {
		t.Fatal("a failed enablement left the pack enabled")
	}

	st := h.Enable(settings)
	if !st.Enabled || st.EnabledAt == nil {
		t.Fatalf("state after enable: %+v", st)
	}
	// secrets are never returned: only their names
	if _, leaked := st.Settings["example_api_key"]; leaked || strings.Contains(mustJSON(st), apiKey) || strings.Contains(mustJSON(st), whSec) {
		t.Fatalf("a secret leaked into the pack state: %s", mustJSON(st))
	}
	if strings.Join(st.SecretsSet, ",") != "example_api_key,example_webhook_secret" || st.Settings["example_sender"] != "OPENBEKCI" {
		t.Fatalf("settings %v, secrets set %v", st.Settings, st.SecretsSet)
	}
	// secrets are stored encrypted, the pack reads them in clear text
	if n := h.Int(`SELECT count(*) FROM pack_enablement WHERE org_id = $1 AND (settings::text LIKE '%' || $2 || '%' OR secrets::text LIKE '%' || $2 || '%')`, h.Env.OrgID, apiKey); n != 0 {
		t.Fatal("a secret is stored in clear text")
	}
	got, err := h.Host.Services("example").Settings().All(h.PackCtx(), sdk.OrgScope())
	if err != nil || got["example_api_key"] != apiKey || got["example_webhook_secret"] != whSec {
		t.Fatalf("the pack reads %v, %v", got, err)
	}
	// OnEnable ran
	if n := h.Int(`SELECT count(*) FROM pack_example_event WHERE org_id = $1 AND kind = 'lifecycle' AND ref LIKE 'enabled:%'`, h.Env.OrgID); n != 1 {
		t.Fatalf("OnEnable ran %d times", n)
	}
	// audited without values
	if n := h.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'pack.enable'`, h.Env.OrgID); n != 1 {
		t.Fatalf("%d pack.enable audit events", n)
	}
	if n := h.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND (diff::text LIKE '%' || $2 || '%' OR diff::text LIKE '%' || $3 || '%')`, h.Env.OrgID, apiKey, whSec); n != 0 {
		t.Fatal("a secret value reached the audit trail")
	}

	// updating settings: secrets keep their value unless named; null clears
	res, _, err := h.Host.UpdateSettings(ctx, h.Env.OrgID, "example", sdk.OrgScope(), map[string]any{"example_sender": "NEWSENDER"})
	if err != nil || res.Values["example_sender"] != "NEWSENDER" || len(res.SecretsSet) != 2 {
		t.Fatalf("update settings: %+v, %v", res, err)
	}
	if got, _ := h.Host.Services("example").Settings().All(h.PackCtx(), sdk.OrgScope()); got["example_api_key"] != apiKey {
		t.Fatal("an unnamed secret lost its value on update")
	}
	if _, _, err := h.Host.UpdateSettings(ctx, h.Env.OrgID, "example", sdk.OrgScope(), map[string]any{"example_api_key": nil}); !isValidation(err) {
		t.Fatalf("clearing a required secret: %v, want a validation problem", err)
	}
	// site scope
	site, isSite, err := h.Host.UpdateSettings(ctx, h.Env.OrgID, "example", sdk.SiteScope(h.Site), map[string]any{"example_account": "ACC-1"})
	if err != nil || !isSite || site.Values["example_account"] != "ACC-1" {
		t.Fatalf("site settings: %+v, %v", site, err)
	}
	if v, ok, _ := h.Host.Services("example").Settings().Get(h.PackCtx(), sdk.SiteScope(h.Site), "example_account"); !ok || v != "ACC-1" {
		t.Fatalf("pack reads the site setting as %v, %v", v, ok)
	}
	if _, _, err := h.Host.UpdateSettings(ctx, h.Env.OrgID, "nope", sdk.OrgScope(), nil); statusOf(err) != 404 {
		t.Fatalf("unknown pack: %v", err)
	}

	// disable: data and settings stay, selection stops, routes 404, settings still readable
	h.Disable()
	if on, _ := h.Host.Enabled(ctx, h.Env.OrgID, "example"); on {
		t.Fatal("still enabled")
	}
	if rec := h.CallRoute("POST", "/callback", []byte("{}")); rec.Code != http.StatusNotFound {
		t.Fatalf("route after disable: %d", rec.Code)
	}
	if ims, _ := h.Host.Importers(ctx, h.Env.OrgID); len(ims) != 0 {
		t.Fatalf("a disabled pack still offers importers: %v", ims)
	}
	if _, _, err := h.Host.UpdateSettings(ctx, h.Env.OrgID, "example", sdk.OrgScope(), map[string]any{}); statusOf(err) != 409 {
		t.Fatalf("settings of a disabled pack: %v, want 409", err)
	}
	if n := h.Int(`SELECT count(*) FROM pack_example_event WHERE org_id = $1 AND kind = 'lifecycle'`, h.Env.OrgID); n != 2 {
		t.Fatalf("lifecycle rows after disable = %d, want 2 (enabled, disabled)", n)
	}

	// purge deletes the pack's rows for the organization, never while enabled
	h.Enable(nil) // re-enable keeps the stored settings
	if _, err := h.Host.Purge(context.Background(), h.Env.OrgID, "example"); statusOf(err) != 409 {
		t.Fatalf("purge of an enabled pack: %v, want 409", err)
	}
	h.Disable()
	res2, err := h.Host.Purge(context.Background(), h.Env.OrgID, "example")
	if err != nil || !res2.Settings || res2.Tables["pack_example_event"] == 0 {
		t.Fatalf("purge: %+v, %v", res2, err)
	}
	if n := h.Int(`SELECT count(*) FROM pack_example_event WHERE org_id = $1`, h.Env.OrgID) + h.Int(`SELECT count(*) FROM pack_enablement WHERE org_id = $1`, h.Env.OrgID); n != 0 {
		t.Fatalf("%d rows survive the purge", n)
	}
}

func TestExamplePackRoutesJobsEventsAndTenancy(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))

	// a route: public, the pack verifies the caller
	body := []byte(`{"hello":"world"}`)
	if rec := h.CallRoute("POST", "/callback", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned callback: %d", rec.Code)
	}
	rec := h.CallRoute("POST", "/callback", body, example.SignatureHeader, example.Sign(whSec, body))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"org":"`+string(h.Org)+`"`) {
		t.Fatalf("signed callback: %d %s", rec.Code, rec.Body)
	}
	if rec := h.CallRoute("GET", "/callback", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a route with another method: %d, want 404", rec.Code)
	}
	if rec := h.CallRoute("POST", "/enable", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a reserved path: %d, want 404", rec.Code)
	}

	// a job
	h.Exec(`INSERT INTO pack_example_txn (org_id, txn, state, created_at) VALUES ($1, 'old', 'failed', now() - interval '3 days')`, h.Env.OrgID)
	if err := h.RunJob("example.cleanup", nil); err != nil {
		t.Fatal(err)
	}
	if n := h.Int(`SELECT count(*) FROM pack_example_txn WHERE org_id = $1 AND txn = 'old'`, h.Env.OrgID); n != 0 {
		t.Fatal("the job did not run")
	}
	if err := h.RunJob("example.cleanup", []byte(`"x"`)); err == nil {
		t.Fatal("a bad payload must fail the job")
	}
	if err := h.RunJob("example.nope", nil); err == nil {
		t.Fatal("an unknown job kind must fail")
	}

	// events: delivered as jobs, deduplicated, handlers idempotent
	unit := h.Unit("A-9")
	h.PostCharge(unit, 5000)
	pid := h.RecordPayment(unit, 5000) // the payments module emits payment.recorded
	h.RunPendingEvents()
	h.RunPendingEvents()
	if n := h.Int(`SELECT count(*) FROM pack_example_event WHERE org_id = $1 AND kind = 'payment.recorded' AND ref = $2`, h.Env.OrgID, string(pid)); n != 1 {
		t.Fatalf("the subscriber saw payment %s %d times, want 1", pid, n)
	}
	ev := sdk.Event{Type: sdk.EventPaymentRecorded, IDs: map[string]sdk.ID{"payment": "11111111-1111-4111-8111-111111111111", "site": h.Site}}
	h.Emit(ev)
	h.Emit(ev) // the same event twice
	if n := h.Int(`SELECT count(*) FROM pack_example_event WHERE org_id = $1 AND kind = 'payment.recorded' AND ref = '11111111-1111-4111-8111-111111111111'`, h.Env.OrgID); n != 1 {
		t.Fatalf("an event emitted twice ran the handler %d times", n)
	}

	// row-level security: the pack's rows are invisible to another organization
	other := uuid.New()
	h.Exec(`INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, 'other', 'Other')`, other)
	var seen int
	err := h.Env.DB.InTenantTx(context.Background(), other, func(tx pgxTx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM pack_example_event`).Scan(&seen)
	})
	if err != nil || seen != 0 {
		t.Fatalf("another organization sees %d of the pack's rows (%v)", seen, err)
	}
	if n := h.Int(`SELECT count(*) FROM pack_example_event WHERE org_id = $1`, h.Env.OrgID); n == 0 {
		t.Fatal("the fixture organization has no rows to isolate")
	}
	// a pack cannot write another tenant's rows through its own tenant transaction
	werr := h.Host.Services("example").DB().InTx(h.PackCtx(), func(tx sdk.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO pack_example_event (org_id, kind, ref) VALUES ($1::uuid, 'x', 'y')`, other.String())
		return err
	})
	if werr == nil {
		t.Fatal("a tenant transaction wrote a row of another organization")
	}
}

func TestExamplePackPresets(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithoutEnable())
	site := h.Env.SiteID
	// the site overrides one of the keys before the pack arrives
	h.Exec(`UPDATE site SET settings = jsonb_build_object('quorum', jsonb_build_object('first_call', 'mine')) WHERE org_id = $1 AND id = $2`, h.Env.OrgID, site)

	h.Enable(settings)
	defs, err := h.Host.SiteDefaults(context.Background(), h.Env.OrgID, site)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := defs["quorum"]; ok {
		t.Fatal("the pack overwrote a setting the site had changed")
	}
	for _, k := range []string{"thresholds", "vote_weight", "schedule", "transfer_rule", "late_fee_policy", "legal_preset"} {
		if d, ok := defs[k]; !ok || d.Pack != "example" {
			t.Errorf("setting %q is not marked as a pack default of example: %+v", k, defs)
		}
	}
	var sched map[string]any
	var raw []byte
	h.Env.Query([]any{&raw}, `SELECT settings->'schedule' FROM site WHERE org_id = $1 AND id = $2`, h.Env.OrgID, site)
	_ = json.Unmarshal(raw, &sched)
	if sched["min_notice_days"] != float64(14) {
		t.Fatalf("schedule preset = %v", sched)
	}

	// the site edits a default: it is the site's own from then on
	h.Exec(`UPDATE site SET settings = jsonb_set(settings, '{schedule,min_notice_days}', '30') WHERE org_id = $1 AND id = $2`, h.Env.OrgID, site)
	defs, _ = h.Host.SiteDefaults(context.Background(), h.Env.OrgID, site)
	if _, ok := defs["schedule"]; ok {
		t.Fatal("an edited setting is still shown as a pack default")
	}
	h.Enable(nil) // enabling again leaves edited settings alone
	h.Env.Query([]any{&raw}, `SELECT settings->'schedule' FROM site WHERE org_id = $1 AND id = $2`, h.Env.OrgID, site)
	_ = json.Unmarshal(raw, &sched)
	if sched["min_notice_days"] != float64(30) {
		t.Fatalf("re-enabling overwrote the site's edit: %v", sched)
	}

	// a site created later gets the presets of the enabled pack of its organization's country,
	// and the chart is localized
	created, err := h.Tenancy.CreateSite(h.Ctx(), h.Env.OrgID, tenancy.Doc{"name": json.RawMessage(`"Second site"`)})
	if err != nil {
		t.Fatal(err)
	}
	if created.Settings["legal_preset"] != "example" || created.Settings["late_fee_policy"] == nil {
		t.Fatalf("the new site did not get the presets: %v", created.Settings)
	}
	defs, _ = h.Host.SiteDefaults(context.Background(), h.Env.OrgID, created.ID)
	if defs["late_fee_policy"].Pack != "example" {
		t.Fatalf("the new site's late fee policy is not marked as a pack default: %v", defs)
	}
}

func TestExamplePackSMSAndImporters(t *testing.T) {
	pk := example.New()
	h := sdktest.New(t, pk, sdktest.WithSettings(settings))
	ctx := context.Background()

	// the organization picks the pack's SMS provider; the core gateway routes to it
	if _, ok := h.Host.SMSPicker().Pick(ctx, h.Env.OrgID); ok {
		t.Fatal("the pack's provider is picked before the organization chose it")
	}
	h.Exec(`UPDATE organization SET settings = jsonb_build_object('sms_provider', 'example') WHERE id = $1`, h.Env.OrgID)
	gw, ok := h.Host.SMSPicker().Pick(ctx, h.Env.OrgID)
	if !ok {
		t.Fatal("the organization's chosen pack provider is not picked")
	}
	if _, err := gw.Send(ctx, "+905550000000", "hello"); err != nil {
		t.Fatal(err)
	}
	if sent := pk.Sent(); len(sent) != 1 || sent[0].Text != "hello" {
		t.Fatalf("sent = %v", sent)
	}
	if _, err := gw.Send(ctx, "05550000000", "x"); err == nil {
		t.Fatal("a provider error must come back")
	}
	h.Disable()
	if _, ok := h.Host.SMSPicker().Pick(ctx, h.Env.OrgID); ok {
		t.Fatal("a disabled pack's SMS provider is still picked")
	}
	h.Enable(nil)

	// statement importers of the enabled pack are listed next to the core's and used by the import
	ims := h.BankImport.Importers(ctx, h.Env.OrgID)
	if len(ims) != 2 || ims[1].ID != "example" {
		t.Fatalf("importers = %+v", ims)
	}
	if id, ok, _ := h.Host.DetectImporter(ctx, h.Env.OrgID, "x.csv", []byte(sampleA)); !ok || id != "example" {
		t.Fatalf("DetectImporter = %q, %v", id, ok)
	}
	if n, err := h.ImportStatement("example", "feb.csv", []byte(sampleA)); err != nil || n != 3 {
		t.Fatalf("import = %d, %v", n, err)
	}
	h.Disable()
	if _, err := h.ImportStatement("example", "feb2.csv", []byte(sampleA)); err == nil {
		t.Fatal("a disabled pack's importer was accepted")
	}
}

func TestExamplePackContributions(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	pk, _ := h.Host.Lookup("example")
	got := strings.Join(pk.C.Provides(), " ")
	for _, want := range []string{"payment_provider:example", "sms_provider:example", "chat_provider:example", "statement_importer:example", "quorum_preset:example", "threshold_preset:example",
		"vote_weight_preset:example", "schedule_preset:example", "transfer_rule:example", "late_fee_preset:example", "allocation_policy:example.even",
		"payment_allocation_order:example.newest_first", "document_template:example.receipt", "chart_of_accounts:example", "address_format:example.xx",
		"validator:example.tax_id", "reference_data:example.areas", "consent_texts:example.privacy", "catalog:example.labels", "settings_schema:settings",
		"route:POST /callback", "job:example.cleanup", "subscription:payment.recorded example.on_payment"} {
		if !strings.Contains(got, want) {
			t.Errorf("the example pack does not provide %q (provides: %s)", want, got)
		}
	}
	ctx := context.Background()
	if cps, err := h.Host.ChatProviders(ctx, h.Env.OrgID); err != nil || len(cps) != 1 || cps[0].ID() != "example" || cps[0].DeepLink("BEKCI-X") != "https://chat.example.test/start?code=BEKCI-X" {
		t.Errorf("chat providers: %v %v", cps, err)
	} else if err := cps[0].Send(ctx, "gone", chat.Message{Text: "t"}); !errors.Is(err, chat.ErrDead) {
		t.Errorf("an unreachable chat is %v, want chat.ErrDead", err)
	}
	if labels, _ := h.Host.Labels(ctx, h.Env.OrgID, "tr"); labels["example.status.paid"] != "Odendi" {
		t.Errorf("catalog labels: %v", labels)
	}
	if tpl, ok, _ := h.Host.Template(ctx, h.Env.OrgID, "example.receipt"); !ok {
		t.Error("template not found")
	} else if doc, err := tpl.Render(ctx, sdk.RenderRequest{Locale: "tr", Data: map[string]any{"receipt_no": "1", "amount": "10"}}); err != nil || !strings.Contains(string(doc.Body), "Makbuz") {
		t.Errorf("template render: %v %v", doc, err)
	}
	if vs, _ := h.Host.Validators(ctx, h.Env.OrgID, "tax_id"); len(vs) != 1 || vs[0].Validate("12345678") != nil || vs[0].Validate("123") == nil {
		t.Errorf("validators: %v", vs)
	}
	if hooks := h.Host.ReadyChecks(); len(hooks) != 1 || hooks[0].Name != "pack:example" || hooks[0].Fn(ctx) != nil {
		t.Errorf("ready checks: %v", hooks)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func statusOf(err error) int {
	var pe *problem.Error
	if asProblem(err, &pe) {
		return pe.Status
	}
	return 0
}

func isValidation(err error) bool { return statusOf(err) == 422 }

var _ = packs.ErrNamespace
