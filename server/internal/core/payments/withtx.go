package payments

import (
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
)

// WithTx returns a Service that runs inside the caller's transaction instead of opening its own.
func (s *Service) WithTx(tx pgx.Tx) *Service {
	return &Service{db: ledger.FixedTx{Tx: tx}, Now: s.Now, Notifier: s.Notifier}
}
