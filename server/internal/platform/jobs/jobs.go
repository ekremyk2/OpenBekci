// Package jobs wires River, the job queue that doubles as the transactional
// outbox (backend plan, section 11).
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Client is the River client type used throughout the server.
type Client = river.Client[pgx.Tx]

// Options configures New.
type Options struct {
	// Work makes the client fetch and run jobs. Without it the client can only
	// insert jobs (for example from a process started with --no-worker).
	Work bool
	// PollOnly disables LISTEN/NOTIFY; required behind a transaction pooler.
	PollOnly bool
	// Idempotency is pruned by the hourly maintenance.prune_idempotency job.
	// Nil registers neither the worker nor the schedule.
	Idempotency Pruner
	// ExpireArchives clears stored site archives past their retention (daily).
	// Nil registers neither the worker nor the schedule.
	ExpireArchives Pruner
}

// Pruner deletes expired idempotency keys (*idempotency.PG).
type Pruner interface {
	Purge(ctx context.Context) (int64, error)
}

// PruneIdempotencyInterval is how often expired idempotency keys are pruned.
const PruneIdempotencyInterval = time.Hour

// PruneIdempotencyArgs is the arguments of maintenance.prune_idempotency.
type PruneIdempotencyArgs struct{}

// Kind implements river.JobArgs.
func (PruneIdempotencyArgs) Kind() string { return "maintenance.prune_idempotency" }

type pruneIdempotencyWorker struct {
	river.WorkerDefaults[PruneIdempotencyArgs]
	log    *slog.Logger
	pruner Pruner
}

func (w *pruneIdempotencyWorker) Work(ctx context.Context, _ *river.Job[PruneIdempotencyArgs]) error {
	n, err := w.pruner.Purge(ctx)
	if err != nil {
		return fmt.Errorf("prune idempotency keys: %w", err)
	}
	if n > 0 {
		w.log.InfoContext(ctx, "pruned idempotency keys", "count", n)
	}
	return nil
}

// ExpireArchivesInterval is how often stored site archives are checked for expiry.
const ExpireArchivesInterval = 24 * time.Hour

// ExpireArchivesArgs is the arguments of maintenance.expire_site_archives.
type ExpireArchivesArgs struct{}

// Kind implements river.JobArgs.
func (ExpireArchivesArgs) Kind() string { return "maintenance.expire_site_archives" }

type expireArchivesWorker struct {
	river.WorkerDefaults[ExpireArchivesArgs]
	log    *slog.Logger
	pruner Pruner
}

func (w *expireArchivesWorker) Work(ctx context.Context, _ *river.Job[ExpireArchivesArgs]) error {
	n, err := w.pruner.Purge(ctx)
	if err != nil {
		return fmt.Errorf("expire site archives: %w", err)
	}
	if n > 0 {
		w.log.InfoContext(ctx, "expired site archives", "count", n)
	}
	return nil
}

// PingArgs is the arguments of the trivial platform.ping job.
type PingArgs struct{}

// Kind implements river.JobArgs.
func (PingArgs) Kind() string { return "platform.ping" }

type pingWorker struct {
	river.WorkerDefaults[PingArgs]
	log *slog.Logger
}

func (w *pingWorker) Work(ctx context.Context, job *river.Job[PingArgs]) error {
	w.log.InfoContext(ctx, "platform.ping", "job_id", job.ID)
	return nil
}

// New creates a River client on pool. Call Start to begin working jobs.
func New(pool *pgxpool.Pool, log *slog.Logger, o Options) (*Client, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &pingWorker{log: log})
	if o.Idempotency != nil {
		river.AddWorker(workers, &pruneIdempotencyWorker{log: log, pruner: o.Idempotency})
	}
	if o.ExpireArchives != nil {
		river.AddWorker(workers, &expireArchivesWorker{log: log, pruner: o.ExpireArchives})
	}

	cfg := &river.Config{
		Logger:   log,
		Workers:  workers,
		PollOnly: o.PollOnly,
	}
	if o.Work {
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		}
		if o.ExpireArchives != nil {
			cfg.PeriodicJobs = append(cfg.PeriodicJobs, river.NewPeriodicJob(
				river.PeriodicInterval(ExpireArchivesInterval),
				func() (river.JobArgs, *river.InsertOpts) { return ExpireArchivesArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true},
			))
		}
		if o.Idempotency != nil {
			// Hourly, and once when the worker starts. River enqueues it from
			// the elected leader only, so replicas do not prune in parallel.
			cfg.PeriodicJobs = append(cfg.PeriodicJobs,
				river.NewPeriodicJob(
					river.PeriodicInterval(PruneIdempotencyInterval),
					func() (river.JobArgs, *river.InsertOpts) { return PruneIdempotencyArgs{}, nil },
					&river.PeriodicJobOpts{RunOnStart: true},
				),
			)
		}
	}
	c, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}
	return c, nil
}
