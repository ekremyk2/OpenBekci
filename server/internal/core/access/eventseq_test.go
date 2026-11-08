package access_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// sync_seq is assigned under a per-site row lock: two concurrent inserts commit in
// the order of their numbers, so a poller never skips a lower number that commits
// after a higher one.
func TestIntegrationEventSeqIsCommitOrdered(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	insert := func(tx pgx.Tx) (int64, error) {
		var seq int64
		err := tx.QueryRow(context.Background(), `INSERT INTO access_event (id, org_id, site_id, point_id, agent_id, decision, reason, occurred_at)
			VALUES ($1, $2, $3, $4, $5, 'allow', 'match', now()) RETURNING sync_seq`, id.New(), e.org, e.site.SiteID, gate, a.ID).Scan(&seq)
		return seq, err
	}
	firstSeq := make(chan int64, 1)
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
			s, err := insert(tx)
			firstSeq <- s
			<-release // hold the transaction open: the second insert must wait for it
			return err
		})
	}()
	s1 := <-firstSeq

	secondSeq := make(chan int64, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
			s, err := insert(tx)
			secondSeq <- s
			return err
		})
	}()
	select {
	case s := <-secondSeq:
		t.Fatalf("the second insert (seq %d) did not wait for the first transaction to commit", s)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	s2 := <-secondSeq
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if s2 != s1+1 {
		t.Fatalf("seq %d then %d: not consecutive in commit order", s1, s2)
	}

	// a rolled-back insert leaves no hole
	_ = e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		if _, err := insert(tx); err != nil {
			t.Fatal(err)
		}
		return context.Canceled
	})
	var s3 int64
	if err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		var err error
		s3, err = insert(tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if s3 != s2+1 {
		t.Fatalf("a rollback left a gap: %d then %d", s2, s3)
	}
}
