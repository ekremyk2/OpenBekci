package httpapi

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// End to end through the real River client: the delivery job is unique per
// (event, endpoint), a failed attempt is retried by River with the module's
// backoff until the bound, and success stops it.
func TestIntegrationWebhookRiverOutbox(t *testing.T) {
	p := newPB(t)
	rcv := newReceiver(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := integrations.NewService(p.e.db, integrations.Config{
		Registry: p.e.deps.Authz.Registry(), EncryptionKey: []byte(testEncryptionKey), Log: log, Notifier: p.e.notes, MaxAttempts: 3,
		TLSConfig:    &tls.Config{RootCAs: testRoots, MinVersion: tls.VersionTLS12},
		AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, KeyRatePerMinute: -1,
		Backoff: func(int) time.Duration { return 100 * time.Millisecond },
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := jobs.New(p.e.db.WorkerPool(), log, jobs.Options{Work: true, Register: svc.Workers})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetInsert(integrations.Insert(client))
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(sctx)
	})
	p.e.deps.Integrations = svc // the HTTP handlers of this environment use the River-backed service
	p.e.h = NewRouter(p.e.deps, Options{})

	ep, _ := p.newEndpoint(rcv.url(), `["decision.applied"]`)
	epID := str(ep, "id")
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; log = %v", what, p.deliveries(epID))
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	riverJobs := func() (n int) {
		_ = p.e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'webhooks.deliver'`).Scan(&n)
		})
		return n
	}

	// 500, 500, then 200: three attempts, one delivery, River's retries
	rcv.status.Store(500)
	p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
	waitFor("the first two attempts", func() bool { return len(rcv.requests()) >= 2 })
	rcv.status.Store(200)
	waitFor("the delivery to succeed", func() bool {
		l := p.deliveries(epID)
		return len(l) == 1 && str(l[0], "status") == "succeeded"
	})
	if l := p.deliveries(epID); num(l[0], "attempts") != 3 || len(rcv.requests()) != 3 {
		t.Errorf("log = %v after %d requests", l, len(rcv.requests()))
	}

	// the same event emitted twice is one job
	evt := integrations.Event{ID: id.New(), Type: integrations.EventDecisionApplied, Version: 1, OrgID: p.org.ID, OccurredAt: time.Now(), IDs: map[string]uuid.UUID{"decision_id": id.New()}}
	before := riverJobs()
	for i := 0; i < 2; i++ {
		if err := p.e.db.InTenantTx(ctx, p.org.ID, func(tx pgx.Tx) error { return svc.Emit(ctx, tx, evt) }); err != nil {
			t.Fatal(err)
		}
	}
	if n := riverJobs() - before; n != 1 {
		t.Errorf("the same event inserted %d River jobs, want 1", n)
	}
	waitFor("the event to be delivered", func() bool {
		l := p.deliveries(epID)
		return len(l) == 2 && str(l[0], "status") == "succeeded"
	})

	// a receiver that never recovers: abandoned after the bound, endpoint failing
	rcv.status.Store(500)
	p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
	waitFor("the delivery to be abandoned", func() bool { return str(p.deliveries(epID)[0], "status") == "failed" })
	if l := p.deliveries(epID); num(l[0], "attempts") != 3 {
		t.Errorf("attempts = %v, want the bound of 3", l[0]["attempts"])
	}
	waitFor("the endpoint to be marked failing", func() bool {
		return p.must(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 200).Body["failing"] == true
	})
}
