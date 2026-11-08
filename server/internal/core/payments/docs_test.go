package payments_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/moneydocs"
	"github.com/openbekci/openbekci/server/internal/core/moneyhooks"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/pdf"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

func TestPDFDrawsTheLiraSign(t *testing.T) {
	d := pdf.New("x")
	d.Line("Toplam: ₺1.234,50 şğı", 10, true)
	b := d.Bytes()
	if bytes.Contains(b, []byte("(?)")) || bytes.Contains(b, []byte("?1")) {
		t.Errorf("the lira sign came out as a question mark: %s", b)
	}
	if !bytes.Contains(b, []byte(" l S\n")) {
		t.Error("no strokes were drawn for the lira sign")
	}
	if !bytes.Contains(b, []byte("(1.234,50 \\201\\203\\205)")) {
		t.Errorf("the text after the sign is missing: %s", b)
	}
}

func docsService(t *testing.T, x *tw) *moneydocs.Service {
	signer := storage.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	fs := files.NewService(x.e.DB, storage.NewLocal(t.TempDir(), signer), files.Config{Signer: signer})
	fs.RegisterResolver(moneydocs.ScopePayment, moneydocs.PaymentFileResolver())
	fs.RegisterResolver(moneydocs.ScopeMoveOut, moneydocs.MoveOutFileResolver())
	return moneydocs.NewService(x.e.DB, fs, x.pay, reports.NewService(x.e.DB, x.e.Clock.Now), nil, nil)
}

func TestIntegrationReceiptIsFiledOnce(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	docs := docsService(t, x)
	e.Clock.Set("2026-02-10")
	a1 := billing.Debtor{UnitID: &x.a1, Kind: "owner"}
	p, err := x.pay.Record(ctx, e.OrgID, payments.Input{SiteID: e.SiteID, Debtor: a1, Method: "cash", Amount: TRY(12345), ReceivedOn: "2026-02-10", AssetAccountID: &x.cash})
	if err != nil {
		t.Fatal(err)
	}
	b, no, err := docs.ReceiptPDF(ctx, e.OrgID, p.ID)
	if err != nil || no != *p.ReceiptNo || !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.Contains(b, []byte(no)) {
		t.Fatalf("receipt %q %v", no, err)
	}
	f1, err := docs.FileReceipt(ctx, e.OrgID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := docs.FileReceipt(ctx, e.OrgID, p.ID)
	if err != nil || f2.ID != f1.ID {
		t.Errorf("a second call must return the filed receipt: %v %v", f1.ID, f2.ID)
	}
	if n := e.Int(`SELECT count(*) FROM file WHERE org_id = $1 AND scope_type = 'payment' AND scope_id = $2 AND source = 'generated'`, e.OrgID, p.ID); n != 1 {
		t.Errorf("%d receipt files", n)
	}
	// a declared payment has no receipt
	dp, err := x.pay.Declare(ctx, e.OrgID, payments.DeclareInput{SiteID: e.SiteID, Debtor: a1, Amount: TRY(100), PaidOn: "2026-02-09"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := docs.ReceiptPDF(ctx, e.OrgID, dp.ID); err == nil {
		t.Error("a receipt for an unconfirmed payment")
	}
}

func TestIntegrationMoveOutDocuments(t *testing.T) {
	x := newTW(t)
	e, ctx := x.e, x.e.Ctx
	docs := docsService(t, x)
	x.plan(t, 100000)
	e.Clock.Set("2026-03-12")
	if _, err := x.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil {
		t.Fatal(err)
	}
	occ := e.Occupy(x.a1, x.t2, "tenant", "2026-01-01", "")
	_ = occ
	mo := id.New()
	// the move-out as the people module records it, carrying the debt to the person
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error {
		res, err := moneyhooks.MoveOut{}.OnMoveOut(ctx, tx, people.MoveOutEvent{OrgID: e.OrgID, SiteID: e.SiteID, MoveOutID: mo, UnitID: x.a1, PersonID: x.o1, Side: "owner", AsOf: "2026-03-12", OpenCharges: "carry_to_person"})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO move_out (id, org_id, site_id, occupancy_id, unit_id, person_id, as_of, open_charges, carried_amount_minor, carried_currency)
			SELECT $1, $2, $3, o.id, $4, $5, '2026-03-12', 'carry_to_person', $6, $7 FROM occupancy o WHERE o.org_id = $2 AND o.unit_id = $4 AND o.person_id = $5 LIMIT 1`, mo, e.OrgID, e.SiteID, x.a1, x.o1, res.CarriedMinor, res.Currency)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := docs.FillMoveOutDocuments(ctx, e.OrgID); err != nil || n != 1 {
		t.Fatalf("filled %d, %v", n, err)
	}
	var st, cert *uuid.UUID
	var s1, s2 string
	e.Query([]any{&st, &cert, &s1, &s2}, `SELECT closing_statement_file_id, certificate_file_id, closing_statement_status, certificate_status FROM move_out WHERE org_id=$1 AND id=$2`, e.OrgID, mo)
	if st == nil || cert == nil || s1 != "ready" || s2 != "ready" {
		t.Fatalf("move-out documents %v %v %s %s", st, cert, s1, s2)
	}
	if n, _ := docs.FillMoveOutDocuments(ctx, e.OrgID); n != 0 {
		t.Errorf("a second run filled %d", n)
	}
	x.invariants(t)
	_ = tenancy.NewService
}
