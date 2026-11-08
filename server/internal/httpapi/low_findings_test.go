package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// The appurtenance rules hold in the database even when the application check is bypassed.
func TestIntegrationAppurtenanceChainsAreRefusedByTheDatabase(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	a, b, c := p.w.NewUnit(t, f), p.w.NewUnit(t, f), p.w.NewUnit(t, f)
	exec := func(sql string, args ...any) error {
		return p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), sql, args...)
			return err
		})
	}
	if err := exec(`UPDATE unit SET parent_unit_id = $2 WHERE id = $1`, a, b); err != nil {
		t.Fatal(err)
	}
	if err := exec(`UPDATE unit SET parent_unit_id = $2 WHERE id = $1`, b, c); err == nil || !strings.Contains(err.Error(), "appurtenance") {
		t.Fatalf("a parent that has children got a parent of its own: %v", err)
	}
	if err := exec(`UPDATE unit SET parent_unit_id = $2 WHERE id = $1`, c, a); err == nil || !strings.Contains(err.Error(), "appurtenance") {
		t.Fatalf("a chain was built: %v", err)
	}
	if err := exec(`UPDATE unit SET parent_unit_id = $2 WHERE id = $1`, b, b); err == nil {
		t.Fatal("a self-parent was accepted")
	}
	_ = id.New
}

// GetPerson answers 404 for a missing person and for one outside the caller's scope alike.
func TestIntegrationGetPersonDoesNotRevealOutOfScopePeople(t *testing.T) {
	p := newPB(t)
	f1 := p.w.NewSite(t, p.org)
	f2 := p.w.NewSite(t, p.org)
	other := p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, f2, f2.Unit, other, "owner")
	mgr := p.w.Person(t, p.org, p.w.Actor("no_membership"))
	p.w.AddMembership(t, p.org, mgr, authz.RoleSiteManager, authz.ScopeSite, &f1.Site, time.Now().Add(-time.Hour), time.Time{})
	tok := p.w.Token(t, mgr)
	missing := p.call("GET", p.base()+"/persons/"+id.New().String(), "", tok)
	out := p.call("GET", p.base()+"/persons/"+other.String(), "", tok)
	if missing.Code != 404 || out.Code != 404 {
		t.Fatalf("missing=%d out-of-scope=%d, both want 404", missing.Code, out.Code)
	}
	p.must(p.call("GET", p.base()+"/persons/"+other.String(), "", p.adm), 200)
}
