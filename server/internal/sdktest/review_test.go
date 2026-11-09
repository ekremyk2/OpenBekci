package sdktest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/moneytest"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// failingEnable is the example pack whose OnEnable fails while fail is set.
type failingEnable struct {
	*example.Pack
	fail *bool
}

func (f failingEnable) OnEnable(ctx context.Context, org sdk.ID) error {
	if *f.fail {
		return errors.New("seeding failed")
	}
	return f.Pack.OnEnable(ctx, org)
}

// leakyValidate is the example pack whose ValidateSettings echoes the api key
// in its error, as a careless pack quoting a failed request would.
type leakyValidate struct {
	*example.Pack
	sentinel error
}

func (l leakyValidate) ValidateSettings(_ context.Context, _ sdk.Scope, values map[string]any) error {
	err := fmt.Errorf("GET https://provider.test/ping?key=%v and secret %v failed", values["example_api_key"], values["example_webhook_secret"])
	if l.sentinel != nil {
		return fmt.Errorf("%w: %w", err, l.sentinel)
	}
	return err
}

// ensureChart makes the fixture site's chart of accounts exist (it is created on first use).
func ensureChart(h *sdktest.Harness) {
	if _, err := h.Host.Services("example").Ledger().Accounts(h.PackCtx(), h.Site); err != nil {
		h.T.Fatal(err)
	}
}

func siteState(h *sdktest.Harness) (settings, account string) {
	var s, a string
	h.Env.Query([]any{&s}, `SELECT settings::text FROM site WHERE org_id = $1 AND id = $2`, h.Env.OrgID, h.Env.SiteID)
	h.Env.Query([]any{&a}, `SELECT name FROM account WHERE org_id = $1 AND site_id = $2 AND code = '1100'`, h.Env.OrgID, h.Env.SiteID)
	return s, a
}

// An OnEnable that fails aborts the enablement completely: the presets and
// the localized chart names the enablement wrote are taken back, not just the
// enabled flag.
func TestFailedOnEnableWithdrawsPresetsAndChartNames(t *testing.T) {
	fail := false
	h := sdktest.New(t, failingEnable{example.New(), &fail}, sdktest.WithoutEnable())
	h.Exec(`UPDATE organization SET default_locale = 'tr' WHERE id = $1`, h.Env.OrgID)
	h.Exec(`UPDATE site SET settings = jsonb_build_object('quorum', jsonb_build_object('first_call', 'mine')) WHERE org_id = $1 AND id = $2`, h.Env.OrgID, h.Env.SiteID)
	ensureChart(h)
	settingsBefore, nameBefore := siteState(h)

	fail = true
	if _, err := h.TryEnable(settings); err == nil {
		t.Fatal("enable succeeded although OnEnable failed")
	}
	after, nameAfter := siteState(h)
	if after != settingsBefore {
		t.Fatalf("a failed enablement left site settings changed:\n before %s\n after  %s", settingsBefore, after)
	}
	if nameAfter != nameBefore {
		t.Fatalf("a failed enablement left the chart renamed: %q, was %q", nameAfter, nameBefore)
	}
	if n := h.Int(`SELECT count(*) FROM site_pack_default WHERE org_id = $1`, h.Env.OrgID); n != 0 {
		t.Fatalf("%d pack default markers survive a failed enablement", n)
	}
	if n := h.Int(`SELECT count(*) FROM pack_enablement WHERE org_id = $1`, h.Env.OrgID); n != 0 {
		t.Fatalf("the enablement row of a pack that never enabled survives (%d)", n)
	}
	if on, _ := h.Host.Enabled(h.Ctx(), h.Env.OrgID, "example"); on {
		t.Fatal("still enabled")
	}

	// the same after an earlier successful enablement and disable: the stored settings come back
	fail = false
	h.Enable(settings)
	h.Disable()
	okSettings, okName := siteState(h)
	if okName == nameBefore {
		t.Fatal("the control of this case is broken: the successful enablement did not localize the chart")
	}
	h.Exec(`UPDATE site SET settings = settings - 'schedule' WHERE org_id = $1 AND id = $2`, h.Env.OrgID, h.Env.SiteID)
	h.Exec(`DELETE FROM site_pack_default WHERE org_id = $1 AND key = 'schedule'`, h.Env.OrgID)
	h.Exec(`UPDATE account SET name = 'Hand named' WHERE org_id = $1 AND site_id = $2 AND code = '1100'`, h.Env.OrgID, h.Env.SiteID)
	prepared, _ := siteState(h)
	fail = true
	changed := map[string]any{"example_api_key": apiKey, "example_webhook_secret": whSec, "example_sender": "CHANGED"}
	if _, err := h.TryEnable(changed); err == nil {
		t.Fatal("re-enable succeeded although OnEnable failed")
	}
	got, name := siteState(h)
	if got != prepared || name != "Hand named" {
		t.Fatalf("the failed re-enablement changed the sites:\n before %s\n after  %s\n (name %q)", prepared, got, name)
	}
	_ = okSettings
	var sender string
	h.Env.Query([]any{&sender}, `SELECT settings->>'example_sender' FROM pack_enablement WHERE org_id = $1 AND pack_id = 'example'`, h.Env.OrgID)
	if sender != "OPENBEKCI" {
		t.Fatalf("the settings of the failed enablement (%q) replaced the stored ones", sender)
	}
	if on, _ := h.Host.Enabled(h.Ctx(), h.Env.OrgID, "example"); on {
		t.Fatal("enabled after the failed re-enablement")
	}
}

// ValidateSettings may quote what it was given; no secret reaches the admin.
func TestValidateSettingsErrorsDoNotLeakSecrets(t *testing.T) {
	for name, sentinel := range map[string]error{"plain": nil, "credentials": sdk.ErrCredentials, "rejected": sdk.ErrRejected} {
		t.Run(name, func(t *testing.T) {
			h := sdktest.New(t, leakyValidate{example.New(), sentinel}, sdktest.WithoutEnable())
			_, err := h.TryEnable(settings)
			if err == nil {
				t.Fatal("enable succeeded")
			}
			if statusOf(err) != 422 {
				t.Fatalf("status %d (%v), want 422", statusOf(err), err)
			}
			if strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), whSec) {
				t.Fatalf("a secret reached the error text: %v", err)
			}
			if !strings.Contains(err.Error(), "provider.test") {
				t.Fatalf("the redaction ate the useful part of the message: %v", err)
			}
		})
	}
}

// Purging a reserved id must not match the core's pack_enablement_* tables by prefix.
func TestPurgeRefusesReservedIDs(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	if _, _, err := h.Host.UpdateSettings(h.Ctx(), h.Env.OrgID, "example", sdk.SiteScope(h.Site), map[string]any{"example_account": "ACC-1"}); err != nil {
		t.Fatal(err)
	}
	before := h.Int(`SELECT count(*) FROM pack_enablement_site WHERE org_id = $1`, h.Env.OrgID)
	if before == 0 {
		t.Fatal("the control of this case is broken: no site settings")
	}
	for _, id := range []string{"enablement", "site", "goose", "core", "default"} {
		if _, err := h.Host.Purge(context.Background(), h.Env.OrgID, id); err == nil {
			t.Fatalf("purge of the reserved id %q was accepted", id)
		}
	}
	if n := h.Int(`SELECT count(*) FROM pack_enablement_site WHERE org_id = $1`, h.Env.OrgID); n != before {
		t.Fatalf("a purge of a reserved id deleted site settings: %d rows left of %d", n, before)
	}
}

// The transaction a pack gets cannot rewrite its tenant context, and the rows
// it returns hide the connection.
func TestPackTransactionStaysInsideItsTenant(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	other := sdktest.NewID()
	db := h.Host.Services("example").DB()
	ctx := h.PackCtx()
	for _, stmt := range []string{
		"SELECT set_config('app.org_id', '" + string(other) + "', true)",
		"SET LOCAL app.org_id = '" + string(other) + "'",
		"RESET ALL",
		"COMMIT",
	} {
		err := db.InTx(ctx, func(tx sdk.Tx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		if err == nil {
			t.Errorf("%q was executed in a pack transaction", stmt)
		}
	}
	err := db.InTx(ctx, func(tx sdk.Tx) error {
		return tx.QueryRow(ctx, `SELECT set_config('app.org_id', $1, true)`, string(other)).Scan(new(string))
	})
	if err == nil {
		t.Error("QueryRow executed set_config")
	}
	err = db.InTx(ctx, func(tx sdk.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM organization`)
		if err != nil {
			return err
		}
		defer rows.Close()
		if _, leaks := rows.(interface{ Conn() *pgx.Conn }); leaks {
			t.Error("the rows of a pack transaction expose the connection")
		}
		n := 0
		for rows.Next() {
			n++
		}
		if n != 1 {
			t.Errorf("a pack sees %d organizations, want only its own", n)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A pack whose migration alters a table that is not its own is refused, also
// when the change keeps the column count.
func TestPackMigrationsCannotTouchCoreTables(t *testing.T) {
	env := moneytest.New(t)
	for id, alter := range map[string]string{
		"evilone":   `ALTER TABLE unit ADD CONSTRAINT evil_chk CHECK (door_number <> 'x');`,
		"eviltwo":   `ALTER TABLE unit NO FORCE ROW LEVEL SECURITY;`,
		"evilthree": `DROP POLICY IF EXISTS tenant_isolation ON unit; CREATE POLICY evil ON unit USING (true);`,
		"evilfour":  `GRANT SELECT ON unit TO PUBLIC;`,
		"evilfive":  `ALTER TABLE unit ALTER COLUMN door_number SET DEFAULT 'x';`,
	} {
		mig := fstest.MapFS{"00001_evil.sql": {Data: []byte("-- +goose Up\nCREATE TABLE pack_" + id + "_t (org_id uuid NOT NULL, id uuid NOT NULL, PRIMARY KEY (org_id, id));\nSELECT openbekci_pack_table('pack_" + id + "_t');\n" +
			alter + "\n-- +goose Down\nDROP TABLE pack_" + id + "_t;\n")}}
		host, err := packs.New(packs.Config{DB: env.DB}, []sdk.Pack{&migrationPack{id: id, fs: mig}})
		if err != nil {
			t.Fatal(err)
		}
		if err := host.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		err = host.MigrateUp(context.Background())
		if !errors.Is(err, packs.ErrMigrations) || !strings.Contains(err.Error(), "unit") {
			t.Errorf("%s: MigrateUp = %v, want a migrations error naming the changed core table", id, err)
		}
	}
}

type migrationPack struct {
	id string
	fs fstest.MapFS
}

func (m *migrationPack) Manifest() sdk.Manifest {
	return sdk.Manifest{ID: m.id, Version: "0.1.0", MinSDK: sdk.Version}
}

func (m *migrationPack) Register(_ context.Context, r sdk.Registry) error {
	r.RegisterMigrations(m.fs)
	return nil
}

// A user account that already holds the provider fee code must not silently
// receive the fees: the posting is refused and nothing is recorded.
func TestProviderPostingRefusesAHijackedFeeAccount(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	unit := h.Unit("A-1")
	h.PostCharge(unit, 10000)
	h.Exec(`INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id)
		SELECT gen_random_uuid(), org_id, site_id, '5050', 'My own category', 'expense', 'expense_category', fund_id FROM account WHERE org_id = $1 AND site_id = $2 AND code = '5000'`, h.Env.OrgID, h.Env.SiteID)
	body := `{"event":"paid","txn":"tx-hijack","site":"` + string(h.Site) + `","unit":"` + string(unit) + `","debtor_kind":"owner","gross":10000,"fee":250,"currency":"TRY"}`
	req := signed([]byte(body), whSec)
	rec := h.Webhook("example", req.Body, req.Header)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409\n%s", rec.Code, rec.Body)
	}
	if n := h.Int(`SELECT count(*) FROM payment WHERE org_id = $1 AND provider_txn_id = 'tx-hijack'`, h.Env.OrgID); n != 0 {
		t.Fatalf("%d payments recorded into the wrong account", n)
	}
	if d, c := h.Balance("5050"); d != 0 || c != 0 {
		t.Fatalf("the user's account 5050 received %d/%d", d, c)
	}
}

// A settlement lands in a bank account.
func TestSettlementBankAccountMustBeABankAccount(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	ensureChart(h)
	rec := h.Host.Services("example").Payments()
	for _, code := range []string{"5000", "1200", "no-such"} {
		_, err := rec.RecordSettlement(h.PackCtx(), sdk.ProviderSettlement{Provider: "example", Site: h.Site, Ref: "st-" + code, BankAccountCode: code,
			SettledOn: h.Env.Clock.Now().Format("2006-01-02"), Net: sdk.Money{Amount: 100, Currency: "TRY"}})
		if statusOf(err) != 422 {
			t.Errorf("settlement into account %q: %v, want a 422 problem", code, err)
		}
	}
	if n := h.Int(`SELECT count(*) FROM provider_settlement WHERE org_id = $1`, h.Env.OrgID); n != 0 {
		t.Fatalf("%d settlements recorded into non-bank accounts", n)
	}
	if _, err := rec.RecordSettlement(h.PackCtx(), sdk.ProviderSettlement{Provider: "example", Site: h.Site, Ref: "st-ok", BankAccountCode: "1100",
		SettledOn: h.Env.Clock.Now().Format("2006-01-02"), Net: sdk.Money{Amount: 100, Currency: "TRY"}}); err != nil {
		t.Fatalf("settlement into the bank account: %v", err)
	}
	h.AssertLedgerInvariants()
}

// A panicking pack handler inside a database transaction must not strand the
// connection: the server keeps serving afterwards.
func TestPanicInPackTransactionReleasesTheConnection(t *testing.T) {
	h := sdktest.New(t, example.New(), sdktest.WithSettings(settings))
	db := h.Host.Services("example").DB()
	for i := 0; i < 30; i++ { // more panics than the test pool has connections
		func() {
			defer func() { _ = recover() }()
			_ = db.InTx(h.PackCtx(), func(sdk.Tx) error { panic("pack bug") })
		}()
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+h.Env.OrgID.String()+"/packs/example/callback", bytes.NewReader([]byte("{}")))
	h.Router.ServeHTTP(rec, r)
	if rec.Code == http.StatusInternalServerError || rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("the host is wedged after pack panics: %d %s", rec.Code, rec.Body)
	}
}
