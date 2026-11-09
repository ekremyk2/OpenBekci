package sdktest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// PaidEvent describes the paid transaction a fixture turns into a webhook.
type PaidEvent struct {
	Txn        string
	Site, Unit sdk.ID
	DebtorKind string
	Gross, Fee int64
	Currency   string
	Reference  string
}

// PaymentFixture teaches ConformPaymentProvider how to talk to one provider.
// Webhook formats are provider specific, so the pack's own tests provide the
// builders; everything else is checked by the suite.
type PaymentFixture struct {
	// Provider is the provider id (PaymentProvider.ID).
	Provider string
	// Settings are the organization settings the pack is enabled with
	// (credentials, webhook secret).
	Settings map[string]any
	// Paid builds a correctly signed webhook reporting a paid transaction.
	Paid func(PaidEvent) sdk.WebhookRequest
	// Tampered returns the request with a signature that does not verify.
	Tampered func(sdk.WebhookRequest) sdk.WebhookRequest
	// Refunded builds a correctly signed webhook reporting a refund started at
	// the provider's side. Optional: the refund-webhook case is skipped without it.
	Refunded func(txn, refundID string) sdk.WebhookRequest
	// Prepare runs before Paid builds a webhook, for providers that only honour transactions they created
	// (it stands in for the checkout that would have made the transaction). Optional.
	Prepare func(h *Harness, e PaidEvent)
}

func post(h *Harness, provider string, req sdk.WebhookRequest) int {
	h.T.Helper()
	return h.Webhook(provider, req.Body, req.Header).Code
}

// ConformPaymentProvider is the conformance suite of payment providers (packs
// plan 3.11): idempotent webhooks keyed by (organization, provider,
// provider_txn_id), gross, fee and net posting through a clearing account,
// refunds posted as reversals, settlement, and the mapping of provider errors.
// newPack must return a fresh pack for each call.
func ConformPaymentProvider(t *testing.T, newPack func() sdk.Pack, fx PaymentFixture) {
	t.Helper()
	if fx.Provider == "" || fx.Paid == nil || fx.Tampered == nil {
		t.Fatal("ConformPaymentProvider: the fixture needs Provider, Paid and Tampered")
	}
	boot := func(t *testing.T) (*Harness, sdk.ID) {
		h := New(t, newPack(), WithSettings(fx.Settings))
		unit := h.Unit("A-1")
		h.PostCharge(unit, 10000)
		// whatever the case did, the books must still be sound at its end
		t.Cleanup(h.AssertLedgerInvariants)
		return h, unit
	}
	paid := func(h *Harness, unit sdk.ID, txn string) sdk.WebhookRequest {
		ev := PaidEvent{Txn: txn, Site: h.Site, Unit: unit, DebtorKind: "owner", Gross: 10000, Fee: 250, Currency: "TRY", Reference: "ref-" + txn}
		if fx.Prepare != nil {
			fx.Prepare(h, ev)
		}
		return fx.Paid(ev)
	}
	count := func(h *Harness, where string, args ...any) int64 {
		return h.Int(`SELECT count(*) FROM payment WHERE org_id = '`+string(h.Org)+`' AND `+where, args...)
	}

	t.Run("webhook_is_idempotent", func(t *testing.T) {
		h, unit := boot(t)
		req := paid(h, unit, "tx-idem")
		for i := 1; i <= 3; i++ {
			if code := post(h, fx.Provider, req); code != http.StatusOK {
				t.Fatalf("delivery %d of the same webhook: status %d, want 200 (a replay is acknowledged)", i, code)
			}
		}
		if n := count(h, `provider = $1 AND provider_txn_id = 'tx-idem'`, fx.Provider); n != 1 {
			t.Fatalf("%d payments recorded for one provider transaction, want exactly 1", n)
		}
		d, c := h.Balance("1200")
		if d != 10000 || c != 10000 {
			t.Fatalf("owner receivable debit/credit = %d/%d after one payment of the charge, want 10000/10000 (replays must not post twice)", d, c)
		}
		// another organization's transaction with the same id is another transaction: the key includes the organization
		h2, unit2 := boot(t)
		if code := post(h2, fx.Provider, paid(h2, unit2, "tx-idem")); code != http.StatusOK {
			t.Fatalf("same txn id in another organization: status %d", code)
		}
		if n := count(h2, `provider_txn_id = 'tx-idem'`); n != 1 {
			t.Fatalf("the same provider txn id in a second organization recorded %d payments there, want 1", n)
		}
	})

	t.Run("gross_fee_net_through_clearing", func(t *testing.T) {
		h, unit := boot(t)
		if code := post(h, fx.Provider, paid(h, unit, "tx-gfn")); code != http.StatusOK {
			t.Fatalf("paid webhook: status %d", code)
		}
		var method string
		var gross, fee, net int64
		h.Env.Query([]any{&method, &gross, &fee, &net}, `SELECT method, gross, fee, net FROM payment WHERE org_id = $1 AND provider_txn_id = 'tx-gfn'`, h.Env.OrgID)
		if method != "card:"+fx.Provider || gross != 10000 || fee != 250 || net != 9750 {
			t.Fatalf("payment is %s gross %d fee %d net %d, want card:%s 10000/250/9750", method, gross, fee, net, fx.Provider)
		}
		d, c := h.Balance("1190." + fx.Provider)
		if d-c != 9750 {
			t.Fatalf("clearing account holds %d, want the net 9750", d-c)
		}
		if d, c := h.Balance("5050"); d-c != 250 {
			t.Fatalf("fee expense is %d, want 250", d-c)
		}
		if d, c := h.Balance("1200"); d != c {
			t.Fatalf("owner receivable is not settled: debit %d credit %d", d, c)
		}
		// the payout moves the net to the bank, once
		for i := 0; i < 2; i++ {
			out, err := h.Host.Services(h.PackID).Payments().RecordSettlement(h.PackCtx(), sdk.ProviderSettlement{Provider: fx.Provider, Site: h.Site, Ref: "st-1",
				SettledOn: h.Env.Clock.Now().Format("2006-01-02"), Net: sdk.Money{Amount: 9750, Currency: "TRY"}})
			if err != nil {
				t.Fatalf("settlement: %v", err)
			}
			if out.Duplicate != (i == 1) {
				t.Fatalf("settlement replay %d: Duplicate = %v", i, out.Duplicate)
			}
		}
		if d, c := h.Balance("1190." + fx.Provider); d != c {
			t.Fatalf("clearing account holds %d after the settlement, want 0", d-c)
		}
		if d, c := h.Balance("1100"); d-c != 9750 {
			t.Fatalf("bank holds %d after the settlement, want 9750", d-c)
		}
	})

	t.Run("refund_is_posted_as_a_reversal", func(t *testing.T) {
		h, unit := boot(t)
		if code := post(h, fx.Provider, paid(h, unit, "tx-ref")); code != http.StatusOK {
			t.Fatalf("paid webhook: status %d", code)
		}
		ctx := h.Ctx()
		req := sdk.RefundRequest{ProviderTxnID: "tx-ref", RefundRef: "refund-1", Amount: sdk.Money{Amount: 10000, Currency: "TRY"}, Reason: "conformance"}
		out, err := h.Host.RefundPayment(ctx, h.Env.OrgID, h.PackID, fx.Provider, req)
		if err != nil {
			t.Fatalf("refund: %v", err)
		}
		if out.Duplicate {
			t.Fatal("the first refund reports Duplicate")
		}
		if n := count(h, `provider_txn_id = 'tx-ref' AND status = 'refunded'`); n != 1 {
			t.Fatalf("the payment is not marked refunded")
		}
		if d, c := h.Balance("1190." + fx.Provider); d != c {
			t.Fatalf("clearing account holds %d after the refund, want 0 (the refund is a reversal)", d-c)
		}
		if d, c := h.Balance("5050"); d != c {
			t.Fatalf("fee expense holds %d after the refund, want 0", d-c)
		}
		if d, c := h.Balance("1200"); d-c != 10000 {
			t.Fatalf("owner receivable is %d after the refund, want the charge open again (10000)", d-c)
		}
		again, err := h.Host.RefundPayment(ctx, h.Env.OrgID, h.PackID, fx.Provider, req)
		if err != nil {
			t.Fatalf("replayed refund: %v", err)
		}
		if !again.Duplicate {
			t.Fatal("a replayed refund is not reported as Duplicate")
		}
		if d, c := h.Balance("1190." + fx.Provider); d != c {
			t.Fatalf("a replayed refund posted again: clearing holds %d", d-c)
		}
		// a partial refund must not reach the ledger as a full one
		half := sdk.RefundRequest{ProviderTxnID: "tx-ref", RefundRef: "refund-2", Amount: sdk.Money{Amount: 5000, Currency: "TRY"}}
		if _, err := h.Host.RefundPayment(ctx, h.Env.OrgID, h.PackID, fx.Provider, half); statusOf(err) != 422 {
			t.Fatalf("a partial refund answered %v, want a 422 problem (partial refunds are unsupported)", err)
		}
	})

	if fx.Refunded != nil {
		t.Run("refund_webhook_is_idempotent", func(t *testing.T) {
			h, unit := boot(t)
			post(h, fx.Provider, paid(h, unit, "tx-rw"))
			for i := 0; i < 2; i++ {
				if code := post(h, fx.Provider, fx.Refunded("tx-rw", "rf-1")); code != http.StatusOK {
					t.Fatalf("refund webhook delivery %d: status %d", i+1, code)
				}
			}
			if n := count(h, `provider_txn_id = 'tx-rw' AND status = 'refunded'`); n != 1 {
				t.Fatal("the refund webhook did not mark the payment refunded")
			}
			if d, c := h.Balance("1190." + fx.Provider); d != c {
				t.Fatalf("clearing holds %d after the refund webhook, want 0", d-c)
			}
		})
	}

	t.Run("error_mapping", func(t *testing.T) {
		h, unit := boot(t)
		// a webhook whose signature does not verify is refused and records nothing
		rec := h.Webhook(fx.Provider, fx.Tampered(paid(h, unit, "tx-bad")).Body, fx.Tampered(paid(h, unit, "tx-bad")).Header)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("tampered webhook: status %d, want 401 (ErrInvalidSignature)\n%s", rec.Code, rec.Body)
		}
		if n := count(h, `provider_txn_id = 'tx-bad'`); n != 0 {
			t.Fatalf("a webhook with a bad signature recorded %d payments", n)
		}
		// a transaction the provider does not know maps to not_found
		_, err := h.Host.PaymentStatus(h.Ctx(), h.Env.OrgID, h.PackID, fx.Provider, "no-such-transaction")
		if statusOf(err) != 404 {
			t.Fatalf("status of an unknown transaction: %v, want a 404 problem (ErrNotFound)", err)
		}
		// a refund of a transaction nobody recorded is not found
		_, err = h.Host.RefundPayment(h.Ctx(), h.Env.OrgID, h.PackID, fx.Provider, sdk.RefundRequest{ProviderTxnID: "never-recorded", RefundRef: "x"})
		if statusOf(err) != 404 {
			t.Fatalf("refund of an unknown transaction: %v, want a 404 problem", err)
		}
		// the mapping of the SDK's sentinel errors is part of the contract
		for sentinel, want := range map[error]int{sdk.ErrInvalidSignature: 401, sdk.ErrNotFound: 404, sdk.ErrRejected: 422, sdk.ErrUnsupported: 422,
			sdk.ErrCredentials: 422, sdk.ErrUnavailable: 503, errors.New("anything else"): 500} {
			if got := statusOf(packs.MapError(h.PackID, sentinel)); got != want {
				t.Errorf("MapError(%v) = %d, want %d", sentinel, got, want)
			}
		}
		var pe *problem.Error
		if !errors.As(packs.MapError(h.PackID, errors.New("boom")), &pe) || pe.Code != problem.CodePackError {
			t.Errorf("an unknown provider error must map to pack_error")
		}
		// a disabled pack answers 404 and records nothing
		h.Disable()
		if rec := h.Webhook(fx.Provider, paid(h, unit, "tx-off").Body, paid(h, unit, "tx-off").Header); rec.Code != http.StatusNotFound {
			t.Fatalf("webhook for a disabled pack: status %d, want 404", rec.Code)
		}
		if n := count(h, `provider_txn_id = 'tx-off'`); n != 0 {
			t.Fatalf("a disabled pack recorded %d payments", n)
		}
	})

	t.Run("unknown_organization_and_provider_are_404_without_side_effects", func(t *testing.T) {
		h, unit := boot(t)
		req := paid(h, unit, "tx-ghost")
		other := NewID()
		// an organization nobody created
		post404 := func(path string) {
			t.Helper()
			r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(req.Body))
			for k, vs := range req.Header {
				for _, v := range vs {
					r.Header.Add(k, v)
				}
			}
			rec := httptest.NewRecorder()
			h.Router.ServeHTTP(rec, r)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("POST %s: status %d, want 404\n%s", path, rec.Code, rec.Body)
			}
		}
		post404("/v1/orgs/" + string(other) + "/packs/" + h.PackID + "/payments/" + fx.Provider + "/webhook")
		post404("/v1/orgs/" + string(h.Org) + "/packs/" + h.PackID + "/payments/" + h.PackID + ".nosuchprovider/webhook")
		post404("/v1/orgs/" + string(h.Org) + "/packs/nosuchpack/payments/" + fx.Provider + "/webhook")
		post404("/v1/orgs/not-a-uuid/packs/" + h.PackID + "/payments/" + fx.Provider + "/webhook")
		if n := h.Int(`SELECT count(*) FROM payment WHERE provider_txn_id = 'tx-ghost'`); n != 0 {
			t.Fatalf("a webhook for an unknown organization or provider recorded %d payments", n)
		}
	})

	t.Run("lock_date_applies_to_provider_payments", func(t *testing.T) {
		h, unit := boot(t)
		// the fixture clock is the payment's day; lock it: a locked period takes no new postings, cash or card
		h.Exec(`UPDATE site SET lock_date = $3 WHERE org_id = $1 AND id = $2`, h.Env.OrgID, h.Env.SiteID, h.Env.Clock.Now().Format("2006-01-02"))
		if code := post(h, fx.Provider, paid(h, unit, "tx-locked")); code < 400 || code == http.StatusUnauthorized || code >= 500 {
			t.Fatalf("a provider payment into a locked period: status %d, want a 4xx refusal", code)
		}
		if n := count(h, `provider_txn_id = 'tx-locked'`); n != 0 {
			t.Fatalf("a locked period took %d provider payments", n)
		}
		if _, err := h.Payments.Record(h.Ctx(), h.Env.OrgID, payments.Input{SiteID: h.Env.SiteID, Debtor: billing.Debtor{UnitID: ptr(uuid.MustParse(string(unit))), Kind: "owner"}, Method: "bank_transfer",
			Amount: money.New(100, "TRY"), ReceivedOn: h.Env.Clock.Now().Format("2006-01-02")}); err == nil {
			t.Fatal("a cash-like payment into the locked period was accepted: the control of this case is broken")
		}
	})

	t.Run("receipts_follow_the_cash_numbering", func(t *testing.T) {
		h, unit := boot(t)
		h.RecordPayment(unit, 100)
		post(h, fx.Provider, paid(h, unit, "tx-r1"))
		post(h, fx.Provider, paid(h, unit, "tx-r1"))
		var n1, n2 string
		h.Env.Query([]any{&n1}, `SELECT receipt_no FROM payment WHERE org_id = $1 AND provider_txn_id IS NULL`, h.Env.OrgID)
		h.Env.Query([]any{&n2}, `SELECT receipt_no FROM payment WHERE org_id = $1 AND provider_txn_id = 'tx-r1'`, h.Env.OrgID)
		if n1 == "" || n2 == "" || n1[:4] != n2[:4] || n2 <= n1 {
			t.Fatalf("receipt numbers %q (cash) and %q (provider) must come from one gap-free sequence", n1, n2)
		}
	})

	t.Run("checkout_and_status", func(t *testing.T) {
		h, unit := boot(t)
		co, err := h.Host.CreateCheckout(h.Ctx(), h.Env.OrgID, h.PackID, fx.Provider, sdk.CheckoutRequest{
			Reference: "chk-" + strconv.FormatInt(time.Now().UnixNano(), 36), Gross: sdk.Money{Amount: 10000, Currency: "TRY"}, Locale: "en",
			Target: sdk.PaymentTarget{SiteID: h.Site, UnitID: unit, DebtorKind: "owner"}, ReturnURL: "https://example.test/return",
			// the fixture settings are organization settings: the organization is the merchant
			MerchantOfRecord: sdk.MerchantOrganization})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		if co.ProviderTxnID == "" || !strings.HasPrefix(co.CheckoutURL, "http") {
			t.Fatalf("checkout %+v needs a provider transaction id and a checkout_url", co)
		}
		st, err := h.Host.PaymentStatus(h.Ctx(), h.Env.OrgID, h.PackID, fx.Provider, co.ProviderTxnID)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if st.State != sdk.PaymentPending {
			t.Fatalf("a new checkout is %q, want pending", st.State)
		}
	})
}

func statusOf(err error) int {
	var pe *problem.Error
	if errors.As(err, &pe) {
		return pe.Status
	}
	return 0
}

// ImporterSample is one anonymized statement file and what a correct parse of
// it looks like.
type ImporterSample struct {
	Name     string
	Filename string
	Data     []byte
	// Text is the content of the file as text, for binary or non-UTF-8 files
	// (a workbook, windows-1254): the raw rows of a parse are checked against
	// it instead of the bytes. Empty means the bytes are the text.
	Text string
	// WantLines is the number of parsed lines and WantTotal their sum in minor units.
	WantLines int
	WantTotal int64
	// WantErrors is the number of rows reported as errors (default 0).
	WantErrors int
}

// ConformStatementImporter is the conformance suite of statement importers:
// format detection (the samples are detected, a junk file is not), totals,
// row numbering, and raw-payload stripping (everything the core needs is in
// the typed fields, the raw rows only repeat the file, and an import survives
// the core's raw pruning with the same totals). notAStatement is any file that
// is not in the importer's format. newPack must return a fresh pack per call.
func ConformStatementImporter(t *testing.T, newPack func() sdk.Pack, importerID string, samples []ImporterSample, notAStatement []byte, opts ...Option) {
	t.Helper()
	if len(samples) == 0 {
		t.Fatal("ConformStatementImporter: no samples")
	}
	h := New(t, newPack(), append([]Option{WithoutEnable()}, opts...)...)
	var im sdk.StatementImporter
	pk, _ := h.Host.Lookup(h.PackID)
	for _, c := range pk.C.Importers {
		if c.ID() == importerID {
			im = c
		}
	}
	if im == nil {
		t.Fatalf("pack %s registered no statement importer %q", h.PackID, importerID)
	}
	if len(im.Formats()) == 0 {
		t.Error("Formats() is empty")
	}
	if im.Detect("junk.csv", notAStatement) {
		t.Errorf("Detect accepted a file that is not in the importer's format")
	}
	ctx := context.Background()
	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			head := s.Data
			if len(head) > 1024 {
				head = head[:1024]
			}
			if !im.Detect(s.Filename, head) {
				t.Fatalf("Detect does not recognize the sample (detection must work on the first KiB)")
			}
			st, err := im.Parse(ctx, s.Filename, s.Data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(st.Lines) != s.WantLines || len(st.Errors) != s.WantErrors {
				t.Fatalf("parsed %d lines and %d row errors, want %d and %d (errors: %v)", len(st.Lines), len(st.Errors), s.WantLines, s.WantErrors, st.Errors)
			}
			if st.Total() != s.WantTotal {
				t.Fatalf("total of the lines = %d, want %d", st.Total(), s.WantTotal)
			}
			if st.OpeningBalance != nil && st.ClosingBalance != nil && *st.OpeningBalance+st.Total() != *st.ClosingBalance {
				t.Errorf("opening %d + total %d != closing %d", *st.OpeningBalance, st.Total(), *st.ClosingBalance)
			}
			seen := map[int]bool{}
			for _, l := range st.Lines {
				if _, err := time.Parse("2006-01-02", l.BookedOn); err != nil {
					t.Errorf("line %d: booked_on %q is not YYYY-MM-DD", l.LineNo, l.BookedOn)
				}
				if l.LineNo <= 0 || seen[l.LineNo] {
					t.Errorf("line numbers must be positive and unique, got %d", l.LineNo)
				}
				seen[l.LineNo] = true
				if l.Amount == 0 {
					t.Errorf("line %d has a zero amount", l.LineNo)
				}
			}
			haystack := s.Text
			if haystack == "" {
				haystack = string(s.Data)
			}
			// raw payload: nothing the core needs may live only in Raw, and Raw only repeats the file
			stripped := st.StripRaw()
			for i, l := range st.Lines {
				x := stripped.Lines[i]
				if x.Raw != nil || x.Amount != l.Amount || x.BookedOn != l.BookedOn || x.Reference != l.Reference || x.Counterparty != l.Counterparty || x.Description != l.Description {
					t.Fatalf("line %d changes when Raw is stripped", l.LineNo)
				}
				for k, v := range l.Raw {
					if v != "" && !strings.Contains(haystack, v) {
						t.Errorf("line %d raw[%q] = %q does not appear in the file: Raw must be the source row, not derived or invented data", l.LineNo, k, v)
					}
				}
			}
		})
	}

	// end to end through the core: import, then the core's raw pruning keeps what matters
	t.Run("core_import_and_prune", func(t *testing.T) {
		h.Enable(h.settings)
		s := samples[0]
		lines, err := h.ImportStatement(importerID, s.Filename, s.Data)
		if err != nil {
			t.Fatalf("core import: %v", err)
		}
		if lines != int64(s.WantLines) {
			t.Fatalf("the core stored %d lines, want %d", lines, s.WantLines)
		}
		raws := h.Int(`SELECT count(*) FROM bank_transaction WHERE org_id = $1 AND raw IS NOT NULL`, h.Env.OrgID)
		if raws == 0 {
			t.Fatal("the core kept no raw rows at all")
		}
		h.Exec(`UPDATE bank_transaction SET created_at = now() - interval '100 days' WHERE org_id = $1`, h.Env.OrgID)
		if _, err := bankimport.PruneRaw(context.Background(), h.Env.DB, time.Now()); err != nil {
			t.Fatal(err)
		}
		if n := h.Int(`SELECT count(*) FROM bank_transaction WHERE org_id = $1 AND raw IS NOT NULL`, h.Env.OrgID); n != 0 {
			t.Fatalf("%d raw rows survive the pruning", n)
		}
		if total := h.Int(`SELECT COALESCE(sum(amount), 0) FROM bank_transaction WHERE org_id = $1`, h.Env.OrgID); total != s.WantTotal {
			t.Fatalf("after pruning the stored lines sum to %d, want %d", total, s.WantTotal)
		}
	})
}

func ptr(u uuid.UUID) *uuid.UUID { return &u }
