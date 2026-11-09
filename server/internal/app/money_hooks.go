package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/httpapi"
)

// govMoney implements governance.DecisionEffects with the money module: adopting
// a budget finalizes it under the decision (treasury.Service.FinalizeBudget), a
// dues-rate decision appends a rate period to the dues plan and a special
// assessment bills the units by its allocation key (billing.GovernanceEffects),
// all in the apply transaction.
type govMoney struct {
	billing.GovernanceEffects
	budgets money.BudgetFinalizer
}

// newDecisionEffects is the decision effects the server installs: the money module's.
func newDecisionEffects(mny httpapi.MoneyDeps) governance.DecisionEffects {
	return govMoney{GovernanceEffects: billing.GovernanceEffects{Billing: mny.Billing}, budgets: mny.Treasury}
}

// FinalizeBudget implements governance.DecisionEffects.
func (g govMoney) FinalizeBudget(ctx context.Context, tx pgx.Tx, d governance.DecisionRef, budgetID uuid.UUID) (governance.EffectRecord, error) {
	rec, err := g.budgets.FinalizeBudget(ctx, tx, money.DecisionRef{ID: d.DecisionID, SiteID: d.SiteID}, budgetID)
	if err != nil {
		return governance.EffectRecord{}, err
	}
	return governance.EffectRecord{RecordType: rec.Kind, RecordID: rec.ResourceID}, nil
}
