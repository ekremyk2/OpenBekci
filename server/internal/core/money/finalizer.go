package money

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DecisionRef names the assembly decision whose effect is being applied.
type DecisionRef struct {
	ID uuid.UUID
	// SiteID is the site the decision belongs to.
	SiteID uuid.UUID
}

// EffectRecord is what an applied effect reports back to the governance module: the kind
// of resource it changed and its id, kept on the decision.
type EffectRecord struct {
	Kind       string
	ResourceID uuid.UUID
	Summary    string
}

// BudgetFinalizer is the money side of the governance decision effect "adopt the budget":
// it makes a draft budget final under the decision, inside the transaction that records
// the decision's effects. The governance module calls it through its DecisionEffects
// interface (FinalizeBudget); the treasury service implements it.
type BudgetFinalizer interface {
	FinalizeBudget(ctx context.Context, tx pgx.Tx, d DecisionRef, budgetID uuid.UUID) (EffectRecord, error)
}
