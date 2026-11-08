package app

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// The effects the server installs into governance (start calls newDecisionEffects)
// are the money module's: a dues-rate decision appends a rate to the plan
// instead of being refused as unavailable, and the audit row names the person.
func TestServerDecisionEffectsAreTheMoneyModules(t *testing.T) {
	w := seedNotifyWorld(t)
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	t.Cleanup(func() { authz.SetScopeLoader(nil) })
	mny := newMoney(w.d, nil, nil, nil)
	effects := newDecisionEffects(mny)
	if _, ok := effects.(govMoney); !ok {
		t.Fatalf("the server installs %T, want the money module's effects", effects)
	}
	sys := ledger.SystemContext(context.Background(), w.org)
	plan, err := mny.Billing.CreatePlan(sys, w.org, billing.PlanInput{SiteID: w.site, Name: "Aidat", Kind: "recurring", ChargeType: "dues", Frequency: "monthly", DueDay: 1,
		Start: "2026-01-01", AmountMode: "per_unit", Scope: "site", Amount: billing.Ptr(int64(100000))})
	if err != nil {
		t.Fatal(err)
	}
	user := id.New()
	ctx := authz.WithPrincipal(context.Background(), &authz.Principal{UserID: user})
	ref := governance.DecisionRef{OrgID: w.org, SiteID: w.site, DecisionID: id.New(), Text: "Dues rise"}
	in := map[string]any{"plan_id": plan.ID.String(), "from": "2027-03-01", "amount": map[string]any{"amount": 200000, "currency": "TRY"}}
	var rec governance.EffectRecord
	err = w.d.InTenantTx(ctx, w.org, func(tx pgx.Tx) error {
		var err error
		rec, err = effects.ApplyDuesRate(ctx, tx, ref, in)
		return err
	})
	if err != nil {
		t.Fatalf("apply dues rate: %v", err)
	}
	if rec.RecordType != billing.EffectRate || rec.RecordID == uuid.Nil {
		t.Fatalf("record = %+v", rec)
	}
	var rates int
	var actor *uuid.UUID
	err = w.d.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM charge_plan_rate WHERE org_id = $1 AND plan_id = $2 AND decision_id = $3`, w.org, plan.ID, ref.DecisionID).Scan(&rates); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(), `SELECT actor_user_id FROM audit_event WHERE org_id = $1 AND action = 'charge_plan_rate.create' AND target_id = $2`, w.org, rec.RecordID).Scan(&actor)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rates != 1 || actor == nil || *actor != user {
		t.Fatalf("rates under the decision = %d, audit actor = %v, want 1 and %s", rates, actor, user)
	}
}
