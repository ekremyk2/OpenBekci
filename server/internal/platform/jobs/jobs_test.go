package jobs_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// The periodic maintenance.prune_idempotency job runs once at startup and
// removes expired keys, and only those.
func TestIntegrationPruneIdempotencyRunsOnStart(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	for key, expires := range map[string]string{"expired-key-1": "-1 hour", "live-key-0001": "+1 hour"} {
		err := d.InAppTx(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO idempotency_key (principal_id, key, request_hash, status, owner, response_status, expires_at)
				VALUES ($1, $2, 'h', 'done', $3, 200, now() + $4::interval)`,
				uuid.New(), key, uuid.New(), expires)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := jobs.New(d.WorkerPool(), log, jobs.Options{Work: true, Idempotency: idempotency.NewPG(d)})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(sctx)
	})

	count := func() (n int) {
		_ = d.InAppTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM idempotency_key`).Scan(&n)
		})
		return n
	}
	deadline := time.Now().Add(20 * time.Second)
	for count() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("expired key not pruned: %d rows left", count())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
