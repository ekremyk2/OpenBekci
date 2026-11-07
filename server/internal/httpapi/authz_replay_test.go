package httpapi

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

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
