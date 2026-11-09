package privacy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Retention windows (the settings table in the server README). Expired OTP
// codes, sessions, login attempts and the sign-in delivery log are pruned by
// the identity module's hourly prune, visitor logs per site by the community
// module's job (site setting visitor_retention_days), uploaded files and
// bank raw payloads by their modules; this job covers the delivery
// bookkeeping of messages and webhooks, which would otherwise grow forever.
const (
	// DeliveryRetention is how long finished notification and webhook delivery rows are kept.
	DeliveryRetention = 90 * 24 * time.Hour
	// RetentionInterval is how often the job runs.
	RetentionInterval = 24 * time.Hour
)

// RetentionArgs is the arguments of privacy.retention.
type RetentionArgs struct{}

// Kind implements river.JobArgs.
func (RetentionArgs) Kind() string { return "privacy.retention" }

// Prune deletes finished delivery rows older than DeliveryRetention in every
// organization and returns how many went. A pending or sending row is never
// deleted.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	cutoff := s.now().Add(-DeliveryRetention)
	var total int64
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM notification_delivery WHERE status IN ('sent', 'failed', 'skipped') AND updated_at < $1`,
			`DELETE FROM webhook_delivery WHERE status IN ('succeeded', 'failed') AND updated_at < $1`,
		} {
			tag, err := tx.Exec(ctx, q, cutoff)
			if err != nil {
				return err
			}
			total += tag.RowsAffected()
		}
		return nil
	})
	return total, err
}

type retentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	s *Service
}

func (w *retentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	n, err := w.s.Prune(ctx)
	if err != nil {
		return fmt.Errorf("prune deliveries: %w", err)
	}
	if n > 0 {
		w.s.log.InfoContext(ctx, "pruned delivery rows", "rows", n)
	}
	return nil
}

// RegisterWorkers adds the module's River workers (jobs.Options.Register).
func (s *Service) RegisterWorkers(w *river.Workers) {
	river.AddWorker(w, &exportWorker{s: s})
	river.AddWorker(w, &retentionWorker{s: s})
}

// PeriodicJobs are the module's schedules (jobs.Options.Periodic).
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(RetentionInterval),
			func() (river.JobArgs, *river.InsertOpts) { return RetentionArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
