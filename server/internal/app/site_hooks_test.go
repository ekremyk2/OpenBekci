package app

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type recHook struct {
	name string
	log  *[]string
	err  error
}

func (h recHook) SiteCreated(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	*h.log = append(*h.log, h.name)
	return h.err
}

// The hooks run in order and the first error stops the chain (the creating transaction then rolls back).
func TestSiteHooksOrderAndFailure(t *testing.T) {
	var log []string
	ok := siteHooks{recHook{"packs", &log, nil}, recHook{"tickets", &log, nil}}
	if err := ok.SiteCreated(context.Background(), nil, uuid.New(), uuid.New()); err != nil || len(log) != 2 || log[0] != "packs" || log[1] != "tickets" {
		t.Fatalf("order %v, err %v", log, err)
	}
	log = nil
	boom := errors.New("boom")
	bad := siteHooks{recHook{"packs", &log, boom}, recHook{"tickets", &log, nil}}
	if err := bad.SiteCreated(context.Background(), nil, uuid.New(), uuid.New()); !errors.Is(err, boom) || len(log) != 1 {
		t.Fatalf("a failing hook did not stop the chain: %v, err %v", log, err)
	}
}
