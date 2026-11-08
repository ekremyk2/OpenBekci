package payments_test

import (
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// moneyDecision is a decision of the site, as the governance module would hand it to the finalizer.
func moneyDecision(site uuid.UUID) money.DecisionRef {
	return money.DecisionRef{ID: id.New(), SiteID: site}
}
