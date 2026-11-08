package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// A recorded payment tells the debtor its amount as money, not as a number:
// payments.Service sends {amount, currency}, the money notifier formats it for
// the organization's locale, and the payment.confirmed SMS and email render
// "₺2.000,00" text (not "200000" or an empty amount).
func TestPaymentConfirmedRendersTheAmount(t *testing.T) {
	w := seedNotifyWorld(t)
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	t.Cleanup(func() { authz.SetScopeLoader(nil) })
	rec := &recordingNotifier{}
	pay := payments.NewService(w.d, nil, moneyNotifier{out: rec})
	unit := w.unit
	ctx := ledger.SystemContext(context.Background(), w.org)
	p, err := pay.Record(ctx, w.org, payments.Input{
		SiteID: w.site, Debtor: billing.Debtor{UnitID: &unit, Kind: "owner"}, Method: "bank_transfer",
		Amount: money.New(200000, "TRY"), ReceivedOn: time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || rec.got[0].Type != "payment.confirmed" {
		t.Fatalf("notifications = %+v", rec.got)
	}
	payload := rec.got[0].Payload
	amount, ok := payload["amount"].(string)
	if !ok || amount != "₺2.000,00" {
		t.Fatalf("payload amount = %#v, want the formatted string ₺2.000,00", payload["amount"])
	}
	if payload["receipt_no"] == nil || p.ReceiptNo == nil {
		t.Fatalf("payload = %v", payload)
	}
	// an event that still carries a bare number of minor units is read in the site's currency
	bare := w.enqueue(t, ledger.NotifyPaymentConfirmed, map[string]any{"payment_id": p.ID.String(), "unit_id": w.unit.String(), "debtor_kind": "owner", "amount": 200000, "receipt_no": "R-9"})
	if len(bare) != 1 || bare[0].Payload["amount"] != amount {
		t.Fatalf("bare amount payload = %+v, want %q", bare, amount)
	}
	// a fractional number is not minor units: it is left as it came, never truncated into an amount
	frac := w.enqueue(t, ledger.NotifyPaymentConfirmed, map[string]any{"payment_id": p.ID.String(), "unit_id": w.unit.String(), "debtor_kind": "owner", "amount": 2000.5, "receipt_no": "R-10"})
	if len(frac) != 1 || frac[0].Payload["amount"] != 2000.5 {
		t.Fatalf("fractional amount payload = %+v, want it untouched", frac)
	}
	// an amount already formatted as text is not read again
	text := w.enqueue(t, ledger.NotifyPaymentConfirmed, map[string]any{"payment_id": p.ID.String(), "unit_id": w.unit.String(), "debtor_kind": "owner", "amount": "₺1,00", "receipt_no": "R-11"})
	if len(text) != 1 || text[0].Payload["amount"] != "₺1,00" {
		t.Fatalf("text amount payload = %+v, want it untouched", text)
	}
	payload["site"] = "Site"
	r := notifications.Render(notifications.TypePaymentConfirmed, "tr", payload)
	want := "A-1 için " + amount + " ödemeniz onaylandı."
	if r.Body != want || r.SMS != "Site: "+want || !strings.Contains(r.SMS, "₺2.000,00") {
		t.Fatalf("rendered = %+v\nwant body %q", r, want)
	}
}
