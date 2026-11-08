package authz

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// An occupancy that has not started yet makes nobody a former occupant; one that
// has ended does, once, per site.
func TestFormerGrantsIgnoreOccupanciesThatHaveNotStarted(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register("test", PermissionDef{Name: "documents.read_addressed", Description: "d", Former: true, Scopes: []ScopeType{ScopeSite}}); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{reg: reg}
	today := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	past := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	siteA, siteB := uuid.New(), uuid.New()
	all := []OccupancyAccess{
		{SiteID: siteA, UnitID: uuid.New(), Kind: OccTenant, ValidFrom: future},                // starts next month: not a former occupant
		{SiteID: siteB, UnitID: uuid.New(), Kind: OccTenant, ValidFrom: past, ValidTo: &ended}, // moved out
	}
	got := r.formerGrants(all, nil, today)
	if len(got) != 1 || got[0].ScopeID == nil || *got[0].ScopeID != siteB {
		t.Fatalf("former grants: %+v", got)
	}
}
