package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
)

// WithTx returns a Service that runs inside the caller's transaction instead of opening its own.
func (s *Service) WithTx(tx pgx.Tx) *Service {
	return &Service{db: ledger.FixedTx{Tx: tx}, tn: s.tn, Now: s.Now}
}

// EndLateFeeFreeze ends an open freeze on a civil date (the caller has checked its permission).
func (s *Service) EndLateFeeFreeze(ctx context.Context, orgID, freezeID uuid.UUID, to string) error {
	return s.run(ctx, orgID, "freeze", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE late_fee_freeze SET to_on = GREATEST($3::date, from_on) WHERE org_id=$1 AND id=$2 AND to_on IS NULL`, orgID, freezeID, to)
		return err
	})
}
