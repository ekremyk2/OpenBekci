package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/collections"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// newBankImport and newCollections build the phase C3 services on top of the payments and billing services.
func newBankImport(d *db.DB, pay *payments.Service) *bankimport.Service {
	return bankimport.NewService(d, pay, nil)
}

func newCollections(d *db.DB, bill *billing.Service, notify jobs.Notifier) *collections.Service {
	return collections.NewService(d, bill, nil, moneyNotifier{out: notify})
}

// CollectionsRunArgs is the hourly dunning stage catch-up of every active site.
type CollectionsRunArgs struct{}

// Kind is the River job kind.
func (CollectionsRunArgs) Kind() string { return "collections.run_stages" }

type collectionsWorker struct {
	river.WorkerDefaults[CollectionsRunArgs]
	d      *db.DB
	log    *slog.Logger
	notify jobs.Notifier
}

// Work fires the due dunning stages of every active site; the events are unique per
// debtor, stage, period and channel, so a repeated or missed run is harmless.
func (w *collectionsWorker) Work(ctx context.Context, _ *river.Job[CollectionsRunArgs]) error {
	var sites []siteRef
	err := w.d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT org_id, id FROM site WHERE status = 'active' ORDER BY org_id, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s siteRef
			if err := rows.Scan(&s.org, &s.site); err != nil {
				return err
			}
			sites = append(sites, s)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	svc := newCollections(w.d, billing.NewService(w.d, tenancy.NewService(w.d), nil), w.notify)
	failed := 0
	for _, s := range sites {
		if n, err := svc.RunStages(ledger.SystemContext(ctx, s.org), s.org, s.site); err != nil {
			w.log.Error("dunning stages", "site_id", s.site, "error", err)
			failed++
		} else if n > 0 {
			w.log.Info("dunning events created", "site_id", s.site, "count", n)
		}
	}
	if failed > 0 {
		return errors.New("dunning stages: some sites failed; they are retried with the next run")
	}
	return nil
}

// PruneBankRawArgs strips the raw payload of old bank statement lines.
type PruneBankRawArgs struct{}

// Kind is the River job kind.
func (PruneBankRawArgs) Kind() string { return "bank.prune_raw" }

type pruneBankRawWorker struct {
	river.WorkerDefaults[PruneBankRawArgs]
	d   *db.DB
	log *slog.Logger
}

// Work removes the parsed source row of statement lines older than 90 days.
func (w *pruneBankRawWorker) Work(ctx context.Context, _ *river.Job[PruneBankRawArgs]) error {
	n, err := bankimport.PruneRaw(ctx, w.d, time.Now())
	if err == nil && n > 0 {
		w.log.Info("bank statement raw payloads stripped", "lines", n)
	}
	return err
}

// collectJobs registers the phase C3 workers and periodic jobs.
func collectJobs(ws *river.Workers, d *db.DB, log *slog.Logger, notify jobs.Notifier) {
	river.AddWorker(ws, &collectionsWorker{d: d, log: log, notify: notify})
	river.AddWorker(ws, &pruneBankRawWorker{d: d, log: log})
}

func collectPeriodic() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return CollectionsRunArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return PruneBankRawArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
