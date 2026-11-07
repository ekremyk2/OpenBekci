package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// The audit tables refuse UPDATE, DELETE and TRUNCATE even from their owner
// (the migrator), so a later GRANT cannot reopen them.
func TestIntegrationAuditTriggersBlockTheOwner(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	if err := e.db.InTenantTx(ctx, e.w.OrgA.ID, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{OrgID: e.w.OrgA.ID, Action: "test.created", TargetType: "thing", TargetID: id.New()})
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "test.created", TargetType: "thing", TargetID: id.New()})
	}); err != nil {
		t.Fatal(err)
	}
	pool := e.db.Migrator()
	if pool == nil {
		t.Skip("no migrator pool")
	}
	for _, table := range []string{"audit_event", "platform_audit_event"} {
		for _, stmt := range []string{
			`UPDATE ` + table + ` SET action = 'tampered'`,
			`DELETE FROM ` + table,
			`TRUNCATE ` + table,
		} {
			_, err := pool.Exec(ctx, stmt)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "23000" {
				t.Errorf("migrator %q: err = %v, want the append-only trigger (SQLSTATE 23000)", stmt, err)
			}
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n == 0 {
			t.Errorf("%s: %d rows, err %v; rows must survive", table, n, err)
		}
	}
	// a grant cannot reopen the table either
	if _, err := pool.Exec(ctx, `GRANT UPDATE, DELETE, TRUNCATE ON audit_event TO openbekci_app`); err != nil {
		t.Fatal(err)
	}
	err := e.db.InTenantTx(ctx, e.w.OrgA.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM audit_event`)
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23000" {
		t.Errorf("app role after a GRANT: err = %v, want the trigger", err)
	}
}

// Events of one transaction get increasing timestamps (clock_timestamp, not
// the transaction start), so the (at, id) cursor follows insertion order.
func TestIntegrationAuditEventsAreStampedAtInsert(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	org := e.w.OrgA
	var ats []time.Time
	err := e.db.InTenantTx(ctx, org.ID, func(tx pgx.Tx) error {
		for i := 0; i < 3; i++ {
			if err := audit.Record(ctx, tx, audit.Event{OrgID: org.ID, Action: "test.created", TargetType: "thing", TargetID: id.New()}); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT at FROM audit_event WHERE action = 'test.created' ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var at time.Time
			if err := rows.Scan(&at); err != nil {
				return err
			}
			ats = append(ats, at)
		}
		return rows.Err()
	})
	if err != nil || len(ats) != 3 {
		t.Fatalf("read back: %v (%d rows)", err, len(ats))
	}
	if !ats[0].Before(ats[1]) || !ats[1].Before(ats[2]) {
		t.Errorf("timestamps within one transaction must increase: %v", ats)
	}
}

// An idempotent replay must not bypass authorization: the permissions of the
// operation (x-openbekci-roles) are checked against the current grants before
// the idempotency store is consulted.
func TestIntegrationIdempotentReplayRechecksPermission(t *testing.T) {
	e := newAuthzEnv(t)
	admin := e.w.Actor("org_admin")
	org := e.w.OrgA
	tok := e.w.Token(t, admin)
	patch := func() int {
		rec := e.do("PATCH", e.orgPath(org, ""), `{"name":"Replay Test"}`, false,
			"Authorization", "Bearer "+tok, "If-Match", ifMatch(1), "Idempotency-Key", "replay-key-1")
		return rec.Code
	}
	e.mustJSON(e.do("PATCH", e.orgPath(org, ""), `{"name":"Replay Test"}`, false,
		"Authorization", "Bearer "+tok, "If-Match", ifMatch(1), "Idempotency-Key", "replay-key-1"), 200, nil)
	// still an administrator: the replay is served
	rec := e.do("PATCH", e.orgPath(org, ""), `{"name":"Replay Test"}`, false,
		"Authorization", "Bearer "+tok, "If-Match", ifMatch(1), "Idempotency-Key", "replay-key-1")
	if rec.Code != 200 || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay for a current admin: %d replayed=%q", rec.Code, rec.Header().Get("Idempotent-Replayed"))
	}

	// demote to guard (no org.read, no org.manage)
	err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE membership SET role = 'guard' WHERE org_id = $1 AND person_id = $2`, org.ID, admin.PersonID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do("PATCH", e.orgPath(org, ""), `{"name":"Replay Test"}`, false,
		"Authorization", "Bearer "+tok, "If-Match", ifMatch(1), "Idempotency-Key", "replay-key-1")
	e.problem(rec, 403, "insufficient_permission")
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Error("a demoted caller was served a stored response")
	}
	if got := patch(); got != 403 {
		t.Errorf("replay after demotion: %d, want 403", got)
	}
}
