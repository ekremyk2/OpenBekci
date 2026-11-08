package access_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// The River jobs: the reconcile runs when the worker starts, access.project
// drains the queued sources, and the lookups the jobs use (which sites have
// work) see across organizations without exposing anything else.
func TestIntegrationJobs(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34JOB111")
	e.point("Gate", "vehicle_gate", nil, "")

	dirty, err := e.svc.DirtySites(context.Background())
	if err != nil || len(dirty) != 1 || dirty[0] != e.site {
		t.Fatalf("dirty sites: %v %v", dirty, err)
	}
	active, err := e.svc.ActiveSites(context.Background())
	if err != nil || len(active) != 1 || active[0] != e.site {
		t.Fatalf("active sites: %v %v", active, err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := jobs.New(e.d.WorkerPool(), log, jobs.Options{Work: true,
		Register: e.svc.Workers, Periodic: access.PeriodicJobs()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) })

	// the nightly reconcile no longer runs at start-up (it runs at a site-local hour); a targeted one does
	if _, err := client.Insert(ctx, access.ReconcileArgs{OrgID: e.site.OrgID, SiteID: e.site.SiteID}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor10(t, "the reconcile", func() bool { return e.creds()["34JOB111"].Published })

	e.vehicle(e.site, e.unit, "34JOB222")
	if _, err := client.Insert(ctx, access.ProjectArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor10(t, "access.project", func() bool { return e.creds()["34JOB222"].Published })
	mustEq(t, "the queue is empty afterwards", e.count("access_dirty"), 0)

	// a targeted reconcile and the prune job run clean
	if _, err := client.Insert(ctx, access.ReconcileArgs{OrgID: e.site.OrgID, SiteID: e.site.SiteID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Insert(ctx, access.PruneArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	mustEq(t, "credentials", len(e.creds()), 2)
}

func waitFor10(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Retention: the opening log is pruned by a SECURITY DEFINER function, and the
// observed plates of a credential can be scrubbed the same way; nothing else
// can delete or change a row of it.
func TestIntegrationOpeningLogRetention(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	cred := id.New()
	e.exec(`INSERT INTO credential (id, org_id, site_id, kind, value, source_type, point_scope, valid_from) VALUES ($1, $2, $3, 'plate', '34RET999', 'manual', 'all', now())`, cred, e.org, e.site.SiteID)
	ins := func(age string, plate string) {
		e.exec(`INSERT INTO access_event (id, org_id, site_id, point_id, agent_id, credential_id, decision, reason, observed_plate, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'allow', 'match', $7, now() - $8::interval)`, id.New(), e.org, e.site.SiteID, gate, a.ID, cred, plate, age)
	}
	ins("100 days", "34OLD111")
	ins("10 days", "34NEW111")
	ins("1 hour", "34NEW222")
	n, err := e.svc.PruneEvents(context.Background(), e.org)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	mustEq(t, "events kept", e.count("access_event"), 2)

	err = e.d.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		var scrubbed int64
		if err := tx.QueryRow(context.Background(), `SELECT access_scrub_plate($1, $2)`, e.org, cred).Scan(&scrubbed); err != nil {
			return err
		}
		mustEq(t, "scrubbed", scrubbed, int64(2))
		var left int
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM access_event WHERE observed_plate IS NOT NULL`).Scan(&left); err != nil {
			return err
		}
		mustEq(t, "plates left", left, 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, "scrubbing keeps the rows", e.count("access_event"), 2)
}
