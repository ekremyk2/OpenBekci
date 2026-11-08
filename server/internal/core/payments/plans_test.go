package payments_test

import (
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
)

func TestIntegrationListPlans(t *testing.T) {
	w := newWorld(t)
	e := w.e
	amount := int64(1000)
	if _, err := w.bill.CreatePlan(e.Ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", Start: "2026-01-01", Amount: &amount}); err != nil {
		t.Fatal(err)
	}
	items, _, err := w.bill.ListPlans(e.Ctx, e.OrgID, e.SiteID, ledger.PageParams{})
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, err)
	}
	if _, err := w.bill.GetPlan(e.Ctx, e.OrgID, items[0].ID); err != nil {
		t.Fatal(err)
	}
}
