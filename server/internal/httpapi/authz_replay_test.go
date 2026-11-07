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

// GET /v1/platform/audit: platform administrators only, newest first, cursor
// pagination, IP truncated to the /24.
func TestIntegrationPlatformAuditIsReadable(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	err := e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		for i := 0; i < 3; i++ {
			c := audit.WithRequestMeta(ctx, audit.RequestMeta{IP: "203.0.113.77", RequestID: "req-1"})
			if err := audit.Record(c, tx, audit.Event{Action: "session.revoke", TargetType: "session", TargetID: id.New()}); err != nil {
				return err
			}
		}
		return audit.Record(audit.WithRequestMeta(ctx, audit.RequestMeta{IP: "2001:db8:1:2:3:4:5:6"}), tx,
			audit.Event{Action: "session.revoke_all", TargetType: "user", TargetID: id.New()})
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := e.w.Actor("platform_admin")
	e.problem(e.as(e.w.Actor("org_admin"), "GET", "/v1/platform/audit", ""), 403, "insufficient_permission")
	e.problem(e.do("GET", "/v1/platform/audit", "", false), 401, "unauthenticated")

	type page struct {
		Items []struct {
			Action string  `json:"action"`
			IP     *string `json:"ip"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	var p1, p2 page
	e.mustJSON(e.as(admin, "GET", "/v1/platform/audit?limit=2&action=session.revoke", ""), 200, &p1)
	if len(p1.Items) != 2 || p1.NextCursor == nil {
		t.Fatalf("first page: %+v", p1)
	}
	e.mustJSON(e.as(admin, "GET", "/v1/platform/audit?limit=2&action=session.revoke&cursor="+*p1.NextCursor, ""), 200, &p2)
	if len(p2.Items) != 1 || p2.NextCursor != nil {
		t.Fatalf("second page: %+v", p2)
	}
	if ip := p1.Items[0].IP; ip == nil || *ip != "203.0.113.0" {
		t.Errorf("ip = %v, want the /24 network 203.0.113.0", ip)
	}
	var v6 page
	e.mustJSON(e.as(admin, "GET", "/v1/platform/audit?action=session.revoke_all", ""), 200, &v6)
	if len(v6.Items) != 1 || v6.Items[0].IP == nil || *v6.Items[0].IP != "2001:db8:1::" {
		t.Errorf("ipv6 event: %+v", v6.Items)
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
