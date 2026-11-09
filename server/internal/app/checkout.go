package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/moneydocs"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/platform/db"
)

// receiptFiler adapts the money documents service to checkout.ReceiptFiler.
type receiptFiler struct{ docs *moneydocs.Service }

func (r receiptFiler) FileReceipt(ctx context.Context, org, payment uuid.UUID) error {
	if r.docs == nil {
		return nil
	}
	_, err := r.docs.FileReceipt(ctx, org, payment)
	return err
}

// newCheckout builds the card checkout module on top of the packs host.
func newCheckout(cfg Config, log *slog.Logger, d *db.DB, providers checkout.Providers, pay *payments.Service, docs *moneydocs.Service) *checkout.Service {
	return checkout.NewService(checkout.Config{
		DB: d, Platform: d, Payments: pay, Providers: providers, Receipts: receiptFiler{docs: docs}, BaseURL: cfg.BaseURL, Log: log,
	})
}

const (
	// ExpireCheckoutsInterval is how often pending checkouts past their expiry are expired.
	ExpireCheckoutsInterval = 5 * time.Minute
	// SyncSettlementsInterval is how often the payment providers are asked for their payouts.
	SyncSettlementsInterval = 6 * time.Hour
	// settlementWindowDays is how far back each sync asks: payouts are idempotent per provider ref, so overlap is safe.
	settlementWindowDays = 14
)

// ExpireCheckoutsArgs is the arguments of checkout.expire.
type ExpireCheckoutsArgs struct{}

// Kind is the River job kind.
func (ExpireCheckoutsArgs) Kind() string { return "checkout.expire" }

type expireCheckoutsWorker struct {
	river.WorkerDefaults[ExpireCheckoutsArgs]
	svc *checkout.Service
	log *slog.Logger
}

func (w *expireCheckoutsWorker) Work(ctx context.Context, _ *river.Job[ExpireCheckoutsArgs]) error {
	n, err := w.svc.ExpireStale(ctx)
	if err != nil {
		return fmt.Errorf("expire checkouts: %w", err)
	}
	if n > 0 {
		w.log.InfoContext(ctx, "expired checkouts", "count", n)
	}
	return nil
}

// SyncSettlementsArgs is the arguments of checkout.sync_settlements.
type SyncSettlementsArgs struct{}

// Kind is the River job kind.
func (SyncSettlementsArgs) Kind() string { return "checkout.sync_settlements" }

type syncSettlementsWorker struct {
	river.WorkerDefaults[SyncSettlementsArgs]
	svc *checkout.Service
	log *slog.Logger
	now func() time.Time
}

// Work asks every enabled payment provider for the payouts of the last weeks and posts them.
func (w *syncSettlementsWorker) Work(ctx context.Context, _ *river.Job[SyncSettlementsArgs]) error {
	to := w.now().UTC()
	from := to.AddDate(0, 0, -settlementWindowDays)
	posted, failed, err := w.svc.SyncSettlements(ctx, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("sync settlements: %w", err)
	}
	if posted > 0 {
		w.log.InfoContext(ctx, "provider settlements posted", "count", posted)
	}
	if failed > 0 {
		return fmt.Errorf("sync settlements: %d provider calls failed; they are retried with the next run", failed)
	}
	return nil
}

// checkoutJobs returns the checkout workers (jobs.Options.Register) and schedules (jobs.Options.Periodic).
func checkoutJobs(svc *checkout.Service, log *slog.Logger) (func(*river.Workers), []*river.PeriodicJob) {
	register := func(ws *river.Workers) {
		river.AddWorker(ws, &expireCheckoutsWorker{svc: svc, log: log})
		river.AddWorker(ws, &syncSettlementsWorker{svc: svc, log: log, now: time.Now})
	}
	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(ExpireCheckoutsInterval),
			func() (river.JobArgs, *river.InsertOpts) { return ExpireCheckoutsArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(SyncSettlementsInterval),
			func() (river.JobArgs, *river.InsertOpts) { return SyncSettlementsArgs{}, nil }, &river.PeriodicJobOpts{}),
	}
	return register, periodic
}
