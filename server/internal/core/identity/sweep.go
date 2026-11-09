package identity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/platform/db"
)

const (
	// PendingMaxAge is how long a sign-up through a join code may wait for its
	// contact to be proven before the pending account is deleted. The code and
	// the link live minutes, so a day is generous.
	PendingMaxAge = 24 * time.Hour
	// PendingSweepInterval is how often abandoned sign-ups are swept.
	PendingSweepInterval = time.Hour
)

// SweepPendingArgs is the arguments of identity.sweep_pending_signups.
type SweepPendingArgs struct{}

// Kind implements river.JobArgs.
func (SweepPendingArgs) Kind() string { return "identity.sweep_pending_signups" }

// SweepPendingSignUps deletes the pending accounts created before now-maxAge
// that never proved their contact, and with them their codes and links (the
// otp_code rows cascade). An account that became active in the meantime, or
// that an operator already attached a person or a claim to, is left alone.
// The delete states the status itself, so it cannot race a verification that
// activates the account. It returns how many accounts went.
func SweepPendingSignUps(ctx context.Context, d *db.DB, now time.Time, maxAge time.Duration) (int64, error) {
	var n int64
	err := d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM "user" u
			WHERE u.status = 'pending' AND u.created_at < $1
			  AND NOT EXISTS (SELECT 1 FROM person p WHERE p.user_id = u.id)
			  AND NOT EXISTS (SELECT 1 FROM unit_claim c WHERE c.user_id = u.id)`, now.Add(-maxAge))
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sweep pending sign-ups: %w", err)
	}
	return n, nil
}

type sweepPendingWorker struct {
	river.WorkerDefaults[SweepPendingArgs]
	d   *db.DB
	log *slog.Logger
}

func (w *sweepPendingWorker) Work(ctx context.Context, _ *river.Job[SweepPendingArgs]) error {
	n, err := SweepPendingSignUps(ctx, w.d, time.Now(), PendingMaxAge)
	if err == nil && n > 0 {
		w.log.InfoContext(ctx, "swept abandoned sign-ups", "accounts", n)
	}
	return err
}

// RegisterWorkers adds the module's River workers (jobs.Options.Register).
func RegisterWorkers(ws *river.Workers, d *db.DB, log *slog.Logger) {
	river.AddWorker(ws, &sweepPendingWorker{d: d, log: log})
}

// PeriodicJobs are the module's schedules (jobs.Options.Periodic).
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(PendingSweepInterval),
			func() (river.JobArgs, *river.InsertOpts) { return SweepPendingArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
