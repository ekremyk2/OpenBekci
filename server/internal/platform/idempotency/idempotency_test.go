package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
)

func TestIntegrationStaleTakeoverRejectsLateComplete(t *testing.T) {
	d := dbtest.New(t)
	store := idempotency.NewPG(d)
	ctx := context.Background()
	sc := idempotency.Scope{PrincipalID: uuid.New()}

	first, err := store.Begin(ctx, sc, "stale-key-01", "hash")
	if err != nil || first.State != idempotency.Acquired {
		t.Fatalf("first begin: %+v %v", first, err)
	}

	// The first request hangs past StaleAfter.
	err = d.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE idempotency_key SET created_at = now() - interval '10 minutes'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	second, err := store.Begin(ctx, sc, "stale-key-01", "hash")
	if err != nil || second.State != idempotency.Acquired {
		t.Fatalf("takeover: %+v %v", second, err)
	}
	if second.Owner == first.Owner {
		t.Fatal("takeover reused the owner token")
	}

	// The original handler finishes late: its response must not land.
	late := idempotency.Response{Status: 201, ContentType: "application/json", Body: []byte(`{"who":"first"}`)}
	if err := store.Complete(ctx, sc, "stale-key-01", first.Owner, late); !errors.Is(err, idempotency.ErrNotOwner) {
		t.Fatalf("late Complete: %v, want ErrNotOwner", err)
	}
	// Nor can it release the new owner's key.
	if err := store.Abort(ctx, sc, "stale-key-01", first.Owner); err != nil {
		t.Fatal(err)
	}
	if mid, err := store.Begin(ctx, sc, "stale-key-01", "hash"); err != nil || mid.State != idempotency.InProgress {
		t.Fatalf("after late Abort: %+v %v, want the new owner still in progress", mid, err)
	}

	won := idempotency.Response{Status: 201, ContentType: "application/json", Body: []byte(`{"who":"second"}`)}
	if err := store.Complete(ctx, sc, "stale-key-01", second.Owner, won); err != nil {
		t.Fatalf("owner Complete: %v", err)
	}
	got, err := store.Begin(ctx, sc, "stale-key-01", "hash")
	if err != nil || got.State != idempotency.Replay || string(got.Response.Body) != `{"who":"second"}` {
		t.Fatalf("replay: %+v %v", got, err)
	}
}

func TestIntegrationScopeAndHeaders(t *testing.T) {
	store := idempotency.NewPG(dbtest.New(t))
	ctx := context.Background()
	alice := idempotency.Scope{PrincipalID: uuid.New()}
	bob := idempotency.Scope{PrincipalID: uuid.New()}
	org := uuid.New()
	aliceInOrg := idempotency.Scope{OrgID: &org, PrincipalID: alice.PrincipalID}

	r, err := store.Begin(ctx, alice, "shared-key", "h1")
	if err != nil || r.State != idempotency.Acquired {
		t.Fatalf("alice: %+v %v", r, err)
	}
	resp := idempotency.Response{
		Status: 201, ContentType: "application/json",
		Headers: map[string][]string{"Location": {"/v1/things/1"}}, Body: []byte(`{}`),
	}
	if err := store.Complete(ctx, alice, "shared-key", r.Owner, resp); err != nil {
		t.Fatal(err)
	}

	// The same key is free for another principal and for the same principal in an org.
	for name, sc := range map[string]idempotency.Scope{"bob": bob, "alice in org": aliceInOrg} {
		if got, err := store.Begin(ctx, sc, "shared-key", "other"); err != nil || got.State != idempotency.Acquired {
			t.Errorf("%s: %+v %v, want an independent key", name, got, err)
		}
	}

	got, err := store.Begin(ctx, alice, "shared-key", "h1")
	if err != nil || got.State != idempotency.Replay {
		t.Fatalf("replay: %+v %v", got, err)
	}
	if got.Response.Headers["Location"][0] != "/v1/things/1" || got.Response.ContentType != "application/json" {
		t.Errorf("stored headers lost: %+v", got.Response)
	}
}
