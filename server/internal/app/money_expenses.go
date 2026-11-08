package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/platform/db"
)

// RecurringExpenseArgs is the hourly job that posts the recurring expenses that fell due.
type RecurringExpenseArgs struct{}

// Kind is the River job kind.
func (RecurringExpenseArgs) Kind() string { return "expenses.recurring" }

type recurringExpenseWorker struct {
	river.WorkerDefaults[RecurringExpenseArgs]
	d   *db.DB
	log *slog.Logger
}

// Work posts every due recurring expense of every site that has one; a missed hour (or a stopped
// server) is caught up by the next run, one expense per due day.
func (w *recurringExpenseWorker) Work(ctx context.Context, _ *river.Job[RecurringExpenseArgs]) error {
	type ref struct{ org, site uuid.UUID }
	var sites []ref
	err := w.d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT org_id, site_id FROM recurring_expense WHERE active AND next_on <= current_date + 1 ORDER BY 1, 2`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r ref
			if err := rows.Scan(&r.org, &r.site); err != nil {
				return err
			}
			sites = append(sites, r)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	svc := treasury.NewService(w.d, nil, nil)
	failed := 0
	for _, s := range sites {
		n, err := svc.RunRecurring(ledger.SystemContext(ctx, s.org), s.org, s.site, w.log)
		if err != nil {
			w.log.Error("recurring expenses", "site_id", s.site, "error", err)
			failed++
			continue
		}
		if n > 0 {
			w.log.Info("recurring expenses posted", "site_id", s.site, "count", n)
		}
	}
	if failed > 0 {
		return errors.New("recurring expenses: some sites failed; they are retried with the next run")
	}
	return nil
}
