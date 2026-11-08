package httpapi

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A poll is created without If-Match the first time, later changes need it,
// and a closed poll is final: it cannot be reopened.
func TestIntegrationGovernancePollLifecycle(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	g := govSeed(t, w, org, "draft")
	item := map[string]any{"id": g.item.String()}
	path := orgPath(org, "/agenda-items/"+gstr(item, "id")+"/poll")
	now := time.Now().UTC()
	body := func(opens, closes time.Time) string {
		return fmt.Sprintf(`{"question":"Agree?","opens_at":%q,"closes_at":%q}`, opens.Format(time.RFC3339), closes.Format(time.RFC3339))
	}
	// creation without If-Match
	p := c.must(200, mgr, "PUT", path, body(now.Add(-time.Hour), now.Add(48*time.Hour)))
	if gnum(p, "version") != 1 || gstr(p, "status") != "open" {
		t.Fatalf("created poll = %v", p)
	}
	// a later change needs it
	c.must(422, mgr, "PUT", path, body(now.Add(-time.Hour), now.Add(72*time.Hour)))
	c.must(409, mgr, "PUT", path, body(now.Add(-time.Hour), now.Add(72*time.Hour)), "If-Match", `"7"`)
	p = c.must(200, mgr, "PUT", path, body(now.Add(-time.Hour), now.Add(72*time.Hour)), "If-Match", `"1"`)
	if gnum(p, "version") != 2 {
		t.Fatalf("changed poll = %v", p)
	}
	// once it has closed, closing is final
	w.Exec(t, `UPDATE poll SET opens_at = now() - interval '3 days', closes_at = now() - interval '1 day' WHERE org_id = $1 AND agenda_item_id = $2`, org.ID, uuid.MustParse(gstr(item, "id")))
	closed := c.must(200, mgr, "GET", path, "")
	if gstr(closed, "status") != "closed" {
		t.Fatalf("poll = %v", closed)
	}
	c.must(409, mgr, "PUT", path, body(now.Add(-time.Hour), now.Add(48*time.Hour)), "If-Match", fmt.Sprintf(`"%d"`, gnum(closed, "version")))
	if again := c.must(200, mgr, "GET", path, ""); gstr(again, "status") != "closed" {
		t.Fatalf("a closed poll was reopened: %v", again)
	}
}
