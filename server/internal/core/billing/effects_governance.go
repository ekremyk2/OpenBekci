package billing

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// GovernanceEffects implements the dues-rate and special-assessment effects of
// governance.DecisionEffects (G5) and their previews with the billing service,
// in the governance transaction. FinalizeBudget and the budget preview stay
// unavailable here; the wiring composes this with the treasury's finalizer.
type GovernanceEffects struct {
	governance.DecisionEffectsUnavailable
	Billing *Service
}

var _ governance.DecisionEffects = GovernanceEffects{}

func duesRateOf(d governance.DecisionRef, in map[string]any) (DuesRateEffect, error) {
	raw, _ := json.Marshal(in)
	var b struct {
		PlanID uuid.UUID   `json:"plan_id"`
		From   string      `json:"from"`
		Amount money.Money `json:"amount"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return DuesRateEffect{}, problem.Validation(problem.Field{Field: "plan_id", Code: "invalid_value"})
	}
	return DuesRateEffect{DecisionID: d.DecisionID, SiteID: d.SiteID, PlanID: b.PlanID, From: b.From, Amount: b.Amount}, nil
}

// ApplyDuesRate implements governance.DecisionEffects.
func (g GovernanceEffects) ApplyDuesRate(ctx context.Context, tx pgx.Tx, d governance.DecisionRef, in map[string]any) (governance.EffectRecord, error) {
	e, err := duesRateOf(d, in)
	if err != nil {
		return governance.EffectRecord{}, err
	}
	r, err := g.Billing.WithTx(tx).ApplyDuesRate(ctx, d.OrgID, e, false)
	return governance.EffectRecord{RecordType: r.RecordType, RecordID: r.RecordID}, err
}

// ApplySpecialAssessment implements governance.DecisionEffects.
func (g GovernanceEffects) ApplySpecialAssessment(ctx context.Context, tx pgx.Tx, d governance.DecisionRef, plan map[string]any) (governance.EffectRecord, error) {
	r, err := g.Billing.WithTx(tx).ApplySpecialAssessment(ctx, d.OrgID, SpecialAssessmentEffect{DecisionID: d.DecisionID, SiteID: d.SiteID, DecisionText: d.Text, Plan: plan}, false)
	return governance.EffectRecord{RecordType: r.RecordType, RecordID: r.RecordID}, err
}

// Preview implements governance.DecisionEffects for dues_rate and special_assessment
// (for a special assessment `in` is the plan).
func (g GovernanceEffects) Preview(ctx context.Context, tx pgx.Tx, d governance.DecisionRef, kind string, in map[string]any) (map[string]any, error) {
	switch kind {
	case governance.EffectDuesRate:
		e, err := duesRateOf(d, in)
		if err != nil {
			return nil, err
		}
		r, err := g.Billing.WithTx(tx).ApplyDuesRate(ctx, d.OrgID, e, true)
		return r.Preview, err
	case governance.EffectSpecialAssessment:
		r, err := g.Billing.WithTx(tx).ApplySpecialAssessment(ctx, d.OrgID, SpecialAssessmentEffect{DecisionID: d.DecisionID, SiteID: d.SiteID, DecisionText: d.Text, Plan: in}, true)
		return r.Preview, err
	}
	return g.DecisionEffectsUnavailable.Preview(ctx, tx, d, kind, in)
}
