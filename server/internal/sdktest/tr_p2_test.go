package sdktest_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/packs/tr/trtest"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// TR-2: the iyzico provider, the bank statement importers and the İleti
// Merkezi SMS provider of the Turkish pack, against the in-process fakes of
// packs/tr/trtest, through the conformance suites and the real core.

const (
	iyzKey    = "sandbox-api-key"
	iyzSecret = "sandbox-secret-key"
)

func iyzicoSettings() map[string]any {
	return map[string]any{"tr_payments_iyzico_enabled": true, "tr_payments_iyzico_api_key": iyzKey, "tr_payments_iyzico_secret_key": iyzSecret,
		"tr_payments_iyzico_sandbox": true}
}

func newIyzicoFake(t *testing.T) *trtest.Iyzico {
	t.Helper()
	f := trtest.NewIyzico(iyzKey, iyzSecret)
	t.Cleanup(f.Close)
	return f
}

func TestTRIyzicoConformsToThePaymentProviderSuite(t *testing.T) {
	fake := newIyzicoFake(t)
	sdktest.ConformPaymentProvider(t, func() sdk.Pack { return tr.New(tr.WithHTTPClient(fake.Client())) }, sdktest.PaymentFixture{
		Provider: tr.IyzicoID,
		Settings: iyzicoSettings(),
		Paid: func(e sdktest.PaidEvent) sdk.WebhookRequest {
			basket, err := tr.BasketID(sdk.PaymentTarget{SiteID: e.Site, UnitID: e.Unit, DebtorKind: e.DebtorKind})
			if err != nil {
				t.Fatal(err)
			}
			price := decimal(e.Gross)
			fake.Seed(trtest.IyzicoTxn{Token: e.Txn, PaymentID: "pay-" + e.Txn, ConversationID: e.Reference, BasketID: basket, Price: price})
			// the fee is split as iyzico reports it: a fixed part and a rate part
			fake.Pay(e.Txn, "2026-02-03", decimal(25), decimal(e.Fee-25))
			return fake.Webhook(e.Txn)
		},
		Tampered: func(r sdk.WebhookRequest) sdk.WebhookRequest {
			r.Header = r.Header.Clone()
			r.Header.Set(tr.SignatureHeader, strings.Repeat("a", 64))
			return r
		},
	})
}

func decimal(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

func importerSamples(importer string) []sdktest.ImporterSample {
	var out []sdktest.ImporterSample
	for _, s := range trtest.BankSamples(importer) {
		out = append(out, sdktest.ImporterSample{Name: s.Name, Filename: s.Filename, Data: s.Data, Text: s.Text, WantLines: s.WantLines, WantTotal: s.WantTotal, WantErrors: s.WantErrors})
	}
	return out
}

func TestTRZiraatConformsToTheStatementImporterSuite(t *testing.T) {
	sdktest.ConformStatementImporter(t, func() sdk.Pack { return tr.New() }, "tr.ziraat", importerSamples("tr.ziraat"), trtest.NotAStatement)
}

func TestTRIsbankConformsToTheStatementImporterSuite(t *testing.T) {
	sdktest.ConformStatementImporter(t, func() sdk.Pack { return tr.New() }, "tr.isbank", importerSamples("tr.isbank"), trtest.NotAStatement)
}

// Two synthetic statement files import through the core and the reference code
// scheme, the payer name and the amount produce the matching suggestions.
func TestTRStatementsImportAndMatchOnTheReferenceCode(t *testing.T) {
	h := newTR(t)
	a12, b3, d5 := h.Unit("A-12"), h.Unit("B-3"), h.Unit("D-5")
	for unit, owner := range map[sdk.ID]string{a12: "Ayşe Yılmaz", b3: "Mehmet Kaya", d5: "Şükrü Çelik"} {
		u := uuid.MustParse(string(unit))
		h.Env.Occupy(u, h.Env.Person(owner), "owner", "2025-01-01", "")
		h.PostCharge(unit, 125000)
	}
	for _, c := range []struct{ file, ref string }{{"ziraat_hesap_hareketleri.csv", "A-12-2026-02"}, {"isbank_ekstre_cp1254.csv", "D-5-2026-03"}} {
		var sample trtest.BankSample
		for _, s := range trtest.BankSamples("") {
			if s.Filename == c.file {
				sample = s
			}
		}
		if _, err := h.ImportStatement(sample.Importer, sample.Filename, sample.Data); err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		items, _, err := h.BankImport.ListTransactions(h.Ctx(), h.Env.OrgID, h.Env.SiteID, bankimport.TxFilter{}, pageParams())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, it := range items {
			if it.Reference == nil || *it.Reference != c.ref {
				continue
			}
			found = true
			door := strings.SplitN(c.ref, "-2026", 2)[0]
			if it.MatchStatus != "suggested" || len(it.Suggestions) == 0 || it.Suggestions[0].UnitDoorNumber != door || !strings.Contains(it.Suggestions[0].Reason, "reference") {
				t.Errorf("%s: line %s is %s with suggestions %+v, want door %s on the reference code", c.file, c.ref, it.MatchStatus, it.Suggestions, door)
			}
		}
		if !found {
			t.Errorf("%s: no line with the reference %s", c.file, c.ref)
		}
	}
	// a line without a reference code in its text keeps the bank's own dekont number as its reference
	items, _, _ := h.BankImport.ListTransactions(h.Ctx(), h.Env.OrgID, h.Env.SiteID, bankimport.TxFilter{}, pageParams())
	var named bool
	for _, it := range items {
		if it.Counterparty != nil && *it.Counterparty == "FATMA DEMİR" {
			named = true
			if it.Reference == nil || *it.Reference != "DK1004" {
				t.Errorf("reference of the line without a code: %v", it.Reference)
			}
		}
	}
	if !named {
		t.Error("the counterparty was not read from the description")
	}
}

func pageParams() ledger.PageParams { return ledger.PageParams{} }

func TestTRSMSThroughThePickerAndTheOrganizationSetting(t *testing.T) {
	fake := trtest.NewIletiMerkezi("im-key", "im-hash")
	defer fake.Close()
	settings := map[string]any{"tr_sms_iletimerkezi_enabled": true, "tr_sms_iletimerkezi_username": "im-key", "tr_sms_iletimerkezi_password": "im-hash", "tr_sms_iletimerkezi_sender_id": "SITEYONT"}
	h := sdktest.New(t, tr.New(tr.WithHTTPClient(fake.Client()), tr.WithSMSTestEndpoint("https://api.test.invalid")), sdktest.WithSettings(settings))
	org := uuid.MustParse(string(h.Org))
	picker := h.Host.SMSPicker()
	if _, ok := picker.Pick(h.Ctx(), org); ok {
		t.Fatal("the pack provider is chosen only when the organization names it")
	}
	h.Exec(`UPDATE organization SET settings = settings || '{"sms_provider": "tr.iletimerkezi"}' WHERE id = $1`, h.Env.OrgID)
	gw, ok := picker.Pick(h.Ctx(), org)
	if !ok {
		t.Fatal("the organization chose tr.iletimerkezi and it is enabled")
	}
	if _, err := (sms.Routed{Default: &sms.Recorder{}, Picker: picker}).Send(sms.WithOrg(h.Ctx(), org), "+905320000009", "Toplanti cagrisi"); err != nil {
		t.Fatal(err)
	}
	if _, err := gw.Send(h.Ctx(), "+905320000010", "x"); err != nil {
		t.Fatal(err)
	}
	sent := fake.Sent()
	if len(sent) != 2 || sent[0].Number != "905320000009" || sent[0].IYS != "0" || sent[0].Sender != "SITEYONT" {
		t.Fatalf("sent %+v", sent)
	}
	// wrong credentials are refused when the settings are saved (the test endpoint is configured here only)
	bad := map[string]any{"tr_sms_iletimerkezi_enabled": true, "tr_sms_iletimerkezi_username": "im-key", "tr_sms_iletimerkezi_password": "nope", "tr_sms_iletimerkezi_sender_id": "SITEYONT"}
	if _, err := h.TryEnable(bad); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("saving wrong İleti Merkezi credentials: %v", err)
	}
	// a provider outage reaches the caller as an error
	fake.Close()
	if _, err := gw.Send(h.Ctx(), "+905320000011", "x"); err == nil {
		t.Fatal("a send while the provider is down must fail")
	}
}

func TestTRPackRegistersTheTR2Contributions(t *testing.T) {
	h := newTR(t)
	pk, _ := h.Host.Lookup("tr")
	var got []string
	got = append(got, pk.C.Provides()...)
	want := []string{"payment_provider:tr.iyzico", "sms_provider:tr.iletimerkezi", "statement_importer:tr.ziraat", "statement_importer:tr.isbank"}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("the pack does not provide %s (provides %v)", w, got)
		}
	}
	// the iyzico webhook route is the core's, not a pack route
	rec := h.Webhook("tr.iyzico", []byte(`{}`), http.Header{})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unsigned iyzico webhook: status %d, want 401", rec.Code)
	}
}
