package payments_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Every site has the statutory late fee from its first day, the schedule only grows, and a new entry starts today or later.
func TestIntegrationLateFeePolicyIsAppendOnly(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	// the first use of the money module gives the site the statutory default
	if rows, err := w.bill.GetLateFeePolicy(ctx, e.OrgID, e.SiteID); err != nil || len(rows) != 0 {
		t.Fatalf("before any use: %v %v", rows, err)
	}
	if err := e.DB.InTenantTx(ctx, e.OrgID, func(tx pgx.Tx) error { _, err := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID); return err }); err != nil {
		t.Fatal(err)
	}
	rows, err := w.bill.GetLateFeePolicy(ctx, e.OrgID, e.SiteID)
	if err != nil || len(rows) != 1 || rows[0].RateBPSPerMonth != 500 || rows[0].GraceDays != 0 {
		t.Fatalf("default %v %v", rows, err)
	}
	v0, _ := w.bill.PolicyVersion(ctx, e.OrgID, e.SiteID)
	e.Clock.Set("2026-03-10")
	// a site that adds its own entry keeps the automatic default as the period before it
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-03-10", RateBPSPerMonth: 300, GraceDays: 5}); err != nil {
		t.Fatal(err)
	}
	if v1, _ := w.bill.PolicyVersion(ctx, e.OrgID, e.SiteID); v1 <= v0 {
		t.Errorf("version %d -> %d", v0, v1)
	}
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-03-10", RateBPSPerMonth: 100}); problem.From(err).Status != 409 {
		t.Errorf("rewriting an entry: %v", err)
	}
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-03-01", RateBPSPerMonth: 100}); problem.From(err).Status != 422 {
		t.Errorf("an entry in the past: %v", err)
	}
	// without a legal preset the policy answer reports the schema's bound as the maximum, not the Turkish 5 %
	if c, err := w.bill.RateCeilingBPS(ctx, e.OrgID, e.SiteID); err != nil || c != 10000 {
		t.Errorf("ceiling without a preset: %d %v", c, err)
	}
	e.Exec(`UPDATE site SET settings = settings || '{"late_fee_policy": {"monthly_rate_ceiling_bps": 500}}'::jsonb WHERE org_id = $1 AND id = $2`, e.OrgID, e.SiteID) // no ceiling without a legal preset; the Turkish pack sets 5 %
	if c, err := w.bill.RateCeilingBPS(ctx, e.OrgID, e.SiteID); err != nil || c != 500 {
		t.Errorf("ceiling of the preset: %d %v", c, err)
	}
	if _, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-04-01", RateBPSPerMonth: 501}); problem.From(err).Status != 422 {
		t.Errorf("above the ceiling of the preset: %v", err)
	}
	e.Clock.Set("2026-04-01")
	if rows, err := w.bill.SetLateFeePolicy(ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-04-01", RateBPSPerMonth: 200}); err != nil || len(rows) != 3 {
		t.Fatalf("a later entry %v %v", rows, err)
	}
	// the database refuses to rewrite or remove a row that is not the automatic default
	if err := e.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE late_fee_policy SET rate_bps_per_month = 1 WHERE org_id = $1`, e.OrgID)
		return err
	}); err == nil {
		t.Error("the database let a policy row change")
	}
}
