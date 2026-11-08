package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// moneyNotifier enqueues money notifications as River jobs inside the business
// transaction. The River client exists only after the services, so it is set later.
type moneyNotifier struct{ client atomic.Pointer[jobs.Client] }

var notifier = &moneyNotifier{}

// MoneyNotifyArgs is the job a money event becomes: one job per event, consumed
// by the notifications module (Type is one of the ledger.Notify* constants and
// Payload is documented there).
type MoneyNotifyArgs struct {
	Type    string          `json:"type"`
	OrgID   uuid.UUID       `json:"org_id"`
	SiteID  uuid.UUID       `json:"site_id"`
	Payload json.RawMessage `json:"payload"`
}

// Kind is the River job kind.
func (MoneyNotifyArgs) Kind() string { return ledger.NotificationJobKind }

// Enqueue implements ledger.Notifier.
func (n *moneyNotifier) Enqueue(ctx context.Context, tx pgx.Tx, e ledger.Notification) error {
	c := n.client.Load()
	if c == nil {
		return errors.New("money notifier: job client not started")
	}
	_, err := c.InsertTx(ctx, tx, MoneyNotifyArgs{Type: e.Type, OrgID: e.OrgID, SiteID: e.SiteID, Payload: e.Payload}, nil)
	return err
}

// newMoney builds the money services (phase C) served by the HTTP API.
func newMoney(d *db.DB) httpapi.MoneyDeps {
	tn := tenancy.NewService(d)
	bill := billing.NewService(d, tn, nil)
	pay := payments.NewService(d, nil, notifier)
	return httpapi.MoneyDeps{
		Ledger:      ledger.NewService(d, nil),
		Billing:     bill,
		Payments:    pay,
		Reports:     reports.NewService(d, nil),
		BankImport:  newBankImport(d, pay),
		Collections: newCollections(d, bill),
	}
}

type logNotifyWorker struct {
	river.WorkerDefaults[MoneyNotifyArgs]
	log *slog.Logger
}

// Work drops the notification with a log line until the notifications module
// registers a consumer for the kind.
func (w *logNotifyWorker) Work(_ context.Context, job *river.Job[MoneyNotifyArgs]) error {
	w.log.Info("money notification (no notifications module installed)", "type", job.Args.Type, "site_id", job.Args.SiteID)
	return nil
}

// CatchUpArgs is the hourly money catch-up: charge generation and late fee crystallization of every active site.
type CatchUpArgs struct{}

// Kind is the River job kind.
func (CatchUpArgs) Kind() string { return "billing.catch_up" }

type catchUpWorker struct {
	river.WorkerDefaults[CatchUpArgs]
	d   *db.DB
	log *slog.Logger
}

type siteRef struct{ org, site uuid.UUID }

// Work generates the missing charges and reconciles the late fees of every
// active site; both steps are idempotent, so a missed hour is caught up by the next run.
func (w *catchUpWorker) Work(ctx context.Context, _ *river.Job[CatchUpArgs]) error {
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
	svc := billing.NewService(w.d, tenancy.NewService(w.d), nil)
	var failed int
	for _, s := range sites {
		sctx := ledger.SystemContext(ctx, s.org)
		if n, err := svc.GenerateCharges(sctx, s.org, s.site); err != nil {
			w.log.Error("generate charges", "site_id", s.site, "error", err)
			failed++
		} else if n > 0 {
			w.log.Info("charges generated", "site_id", s.site, "count", n)
		}
		if n, err := svc.CrystallizeLateFees(sctx, s.org, s.site); err != nil {
			w.log.Error("crystallize late fees", "site_id", s.site, "error", err)
			failed++
		} else if n > 0 {
			w.log.Info("late fees reconciled", "site_id", s.site, "postings", n)
		}
	}
	if failed > 0 {
		return errors.New("money catch-up: some sites failed; they are retried with the next run")
	}
	return nil
}

// moneyJobs returns the job options of the money modules.
func moneyJobs(d *db.DB, log *slog.Logger) func(o *jobs.Options) {
	return func(o *jobs.Options) {
		o.Workers = func(ws *river.Workers) {
			river.AddWorker(ws, &logNotifyWorker{log: log})
			river.AddWorker(ws, &catchUpWorker{d: d, log: log})
			collectJobs(ws, d, log)
		}
		o.Periodic = append(o.Periodic, river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return CatchUpArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}))
		o.Periodic = append(o.Periodic, collectPeriodic()...)
	}
}
