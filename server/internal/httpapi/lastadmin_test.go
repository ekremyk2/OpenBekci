package httpapi

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// M5: concurrent removal of every administrator leaves one.
func TestIntegrationLastOrgAdminSurvivesConcurrentRemoval(t *testing.T) {
	p := newPB(t)
	p.w.NewMembership(t, p.org, p.w.NewPerson(t, p.org), "org_admin")
	p.w.NewMembership(t, p.org, p.w.NewPerson(t, p.org), "org_admin")
	var ids []uuid.UUID
	err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT id FROM membership WHERE org_id = $1 AND role = 'org_admin' AND scope_type = 'org'
			AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())`, p.org.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil || len(ids) < 3 {
		t.Fatalf("admins: %v %v", ids, err)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.call("DELETE", p.base()+"/memberships/"+id.String(), "", p.adm, "If-Match", `"1"`)
		}()
	}
	wg.Wait()
	if n := p.qInt(`SELECT count(*) FROM membership WHERE org_id = $1 AND role = 'org_admin' AND scope_type = 'org' AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())`, p.org.ID); n < 1 {
		t.Fatalf("the organization was left without an administrator (%d)", n)
	}
}
