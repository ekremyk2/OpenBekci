package access_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// A future-dated occupancy is projected on its start date: the activation job marks
// the unit's vehicles in the first local hour of that day, without a row change.
func TestIntegrationFutureOccupancyIsProjectedOnItsStartDate(t *testing.T) {
	e := newEnv(t)
	e.point("Gate", "vehicle_gate", nil, "")
	e.vehicle(e.site, e.unit, "34DAY123")
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	now := e.clk.Now().In(loc)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	person := id.New()
	e.exec(`INSERT INTO person (id, org_id, display_name, contact) VALUES ($1, $2, 'Future', '{}')`, person, e.org)
	e.exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from) VALUES ($1, $2, $3, $4, $5, 'owner', 'owner', $6)`,
		id.New(), e.org, e.site.SiteID, e.unit, person, tomorrow.Format("2006-01-02"))
	e.project(e.site)
	if c := e.creds()["34DAY123"]; c.Published {
		t.Fatalf("the plate is admitted a day before the occupancy starts: %+v", c)
	}
	// outside the first local hour of the start date nothing is queued
	if n, err := e.svc.MarkDueOccupancies(context.Background()); err != nil || n != 0 {
		t.Fatalf("marked %d today: %v", n, err)
	}
	e.clk.Advance(tomorrow.Add(10 * time.Minute).Sub(e.clk.Now()))
	if n, err := e.svc.MarkDueOccupancies(context.Background()); err != nil || n != 1 {
		t.Fatalf("marked %d on the start date: %v", n, err)
	}
	e.project(e.site)
	if c := e.creds()["34DAY123"]; !c.Published || c.Status != "active" {
		t.Fatalf("not admitted on the start date: %+v", c)
	}
}

// The nightly reconcile runs at a fixed site-local hour, not at server start.
func TestIntegrationNightlyReconcileIsAtSiteLocalTime(t *testing.T) {
	e := newEnv(t)
	e.point("Gate", "vehicle_gate", nil, "")
	loc, _ := time.LoadLocation("Europe/Istanbul")
	now := e.clk.Now().In(loc)
	at := func(hour int) []string {
		target := time.Date(now.Year(), now.Month(), now.Day()+1, hour, 30, 0, 0, loc)
		e.clk.Advance(target.Sub(e.clk.Now()))
		sites, err := e.svc.SitesAtLocalHour(context.Background(), 3)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range sites {
			out = append(out, s.SiteID.String())
		}
		return out
	}
	if got := at(2); len(got) != 0 {
		t.Fatalf("02:30 local: %v", got)
	}
	if got := at(3); len(got) != 1 || got[0] != e.site.SiteID.String() {
		t.Fatalf("03:30 local: %v", got)
	}
	if got := at(4); len(got) != 0 {
		t.Fatalf("04:30 local: %v", got)
	}
}
