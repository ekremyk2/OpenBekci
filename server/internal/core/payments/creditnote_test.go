package payments_test

import (
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/payments"
)

// A voided payment gets one credit note, numbered CN-<year>-<n> gap-free, filed in the unit's documents and
// linked on the payment; a second call files nothing new; a payment still confirmed gets none.
func TestIntegrationCreditNoteIsFiledOnceAndNumbered(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	docs := docsService(t, x)
	docs.SetFiler(documents.NewService(e.DB, docs.Files))
	docs.Now = e.Clock.Now
	e.Clock.Set("2026-02-10")
	a1 := billing.Debtor{UnitID: &x.a1, Kind: "owner"}
	rec := func() payments.Payment {
		p, err := x.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "cash", Amount: TRY(12345), ReceivedOn: "2026-02-10", AssetAccountID: &x.cash})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p1, p2 := rec(), rec()
	if _, err := docs.FileCreditNote(ctx, e.OrgID, p1.ID); err == nil {
		t.Fatal("a credit note for a confirmed payment")
	}
	for _, p := range []payments.Payment{p1, p2} {
		if _, err := x.pay.Void(ctx, e.OrgID, p.ID, "refunded in cash"); err != nil {
			t.Fatal(err)
		}
	}
	f1, err := docs.FileCreditNote(ctx, e.OrgID, p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f2, err := docs.FileCreditNote(ctx, e.OrgID, p1.ID); err != nil || f2.ID != f1.ID {
		t.Fatalf("a second call must return the filed credit note: %v %v", f2.ID, err)
	}
	if _, err := docs.FileCreditNote(ctx, e.OrgID, p2.ID); err != nil {
		t.Fatal(err)
	}
	for i, p := range []payments.Payment{p1, p2} {
		want := "CN-2026-00000" + string(rune('1'+i))
		if e.Int(`SELECT count(*) FROM payment WHERE org_id = $1 AND id = $2 AND credit_note_no = $3 AND credit_note_document_id IS NOT NULL`, e.OrgID, p.ID, want) != 1 {
			t.Errorf("payment %d: no linked credit note %s", i, want)
		}
	}
	if n := e.Int(`SELECT count(*) FROM document WHERE org_id = $1 AND generated_type = 'payment_credit_note'`, e.OrgID); n != 2 {
		t.Errorf("%d credit note documents", n)
	}
	if _, _, err := docs.ReceiptPDF(ctx, e.OrgID, p1.ID); err == nil {
		t.Error("a plain receipt of a voided payment")
	}
}
