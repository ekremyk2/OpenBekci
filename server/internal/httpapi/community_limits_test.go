package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Guard date browsing reaches yesterday, today and tomorrow only.
func TestIntegrationCommunityGuardDateBrowsingIsLimited(t *testing.T) {
	c := newCommEnv(t)
	day := func(offset int) string {
		return time.Now().In(istanbul(t)).AddDate(0, 0, offset).Format("2006-01-02")
	}
	for off, want := range map[int]int{-2: 422, -1: 200, 0: 200, 1: 200, 2: 422, -40: 422} {
		rec := c.req(c.guard, "GET", c.path("/gate/visitors?site_id="+c.f.Site.String()+"&date="+day(off)), "")
		if rec.Code != want {
			t.Errorf("date today%+d: status %d, want %d: %s", off, rec.Code, want, rec.Body)
		}
	}
}

// The guard sees a collected parcel for 7 days; an uncollected one is pruned with the
// visitor retention and reminded weekly.
func TestIntegrationCommunityParcelWindowPruneAndReminder(t *testing.T) {
	c := newCommEnv(t)
	ctx := context.Background()
	c.resident("owner1", c.f.Unit, "owner")
	parcel := func(received, collected string) uuid.UUID {
		d := id.New()
		if collected == "" {
			c.w.Exec(t, `INSERT INTO delivery (id, org_id, site_id, unit_id, received_at) VALUES ($1, $2, $3, $4, now() - $5::interval)`, d, c.org.ID, c.f.Site, c.f.Unit, received)
		} else {
			c.w.Exec(t, `INSERT INTO delivery (id, org_id, site_id, unit_id, received_at, collected_at, collected_by_name) VALUES ($1, $2, $3, $4, now() - $5::interval, now() - $6::interval, 'X')`,
				d, c.org.ID, c.f.Site, c.f.Unit, received, collected)
		}
		return d
	}
	recent, stale := parcel("4 days", "3 days"), parcel("12 days", "9 days")
	list := c.want(c.req(c.guard, "GET", c.path("/gate/deliveries?site_id="+c.f.Site.String()+"&status=collected"), ""), 200)["items"].([]any)
	seen := map[string]bool{}
	for _, it := range list {
		seen[it.(map[string]any)["id"].(string)] = true
	}
	if !seen[recent.String()] || seen[stale.String()] {
		t.Fatalf("collected parcels visible to the guard: %v (recent %s, stale %s)", seen, recent, stale)
	}

	waiting, old := parcel("15 days", ""), parcel("100 days", "")
	if n, err := c.comm().RemindParcelsOrg(ctx, c.org.ID); err != nil || n != 2 {
		t.Fatalf("reminders: n=%d err=%v, want both waiting parcels over a week old", n, err)
	}
	_ = waiting
	if got := len(c.notes.byTemplate("delivery.waiting_reminder")); got < 1 {
		t.Fatalf("no reminder notification")
	}
	if _, err := c.comm().PruneVisitorLogsOrg(ctx, c.org.ID); err != nil {
		t.Fatal(err)
	}
	exists := func(d uuid.UUID) bool {
		var n int
		_ = c.w.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM delivery WHERE id = $1`, d).Scan(&n)
		})
		return n == 1
	}
	if exists(old) || !exists(waiting) {
		t.Fatalf("uncollected parcels after the prune: 100 days old kept=%v, 15 days old kept=%v", exists(old), exists(waiting))
	}
}

// 20 walk-ins and call-contact reads an hour per guard session, audited when refused.
func TestIntegrationCommunityDeskLimit(t *testing.T) {
	c := newCommEnv(t)
	c.resident("owner1", c.f.Unit, "owner")
	body := fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Courier"}`, c.f.Site, c.f.Unit)
	var first string
	for i := range 20 {
		v := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), body), 201)
		if i == 0 {
			first = v["id"].(string)
		}
	}
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), body), 429, "rate_limited")
	c.problemCode(c.req(c.guard, "GET", c.path("/gate/visitors/"+first+"/call-contact"), ""), 429, "rate_limited")
	var throttled int
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'gate.throttled'`, c.org.ID).Scan(&throttled)
	})
	if throttled != 2 {
		t.Fatalf("throttled attempts audited = %d, want 2", throttled)
	}
}
