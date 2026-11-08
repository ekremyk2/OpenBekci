package reports

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// PersonStatement is the statement of a person's carried debt (statements.read): the site is
// the one where the person has charges, the latest first. A person without carried debt is 404.
func (s *Service) PersonStatement(ctx context.Context, orgID, personID uuid.UUID, from, to string) (Statement, error) {
	if err := authz.RequireAnywhere(ctx, "statements.read"); err != nil {
		return Statement{}, problem.NotFound("no such person")
	}
	var site uuid.UUID
	err := s.run(ctx, orgID, "person", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT site_id FROM charge WHERE org_id=$1 AND debtor_kind='person' AND debtor_person_id=$2 ORDER BY created_at DESC, id DESC LIMIT 1`, orgID, personID).Scan(&site)
	})
	if err != nil {
		return Statement{}, err
	}
	return s.DebtorStatement(ctx, orgID, site, billing.Debtor{PersonID: &personID}, from, to)
}
