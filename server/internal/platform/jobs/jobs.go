// Package jobs wires River, the job queue that doubles as the transactional
// outbox (backend plan, section 11).
package jobs

import (
	"context"
	"fmt"
	"log/slog"

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

	cfg := &river.Config{
		Logger:   log,
		Workers:  workers,
		PollOnly: o.PollOnly,
	}
	if o.Work {
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		}
	}
	c, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}
	return c, nil
}
