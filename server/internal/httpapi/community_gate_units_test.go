package httpapi

import (
	"strings"
	"testing"
)

// The booth's unit picker: door number or block only, nothing about people.
func TestIntegrationGateSearchUnits(t *testing.T) {
	c := newCommEnv(t)
	c.resident("owner1", c.f.Unit, "owner")
	path := func(q string) string { return c.path("/gate/units?site_id=" + c.f.Site.String() + "&q=" + q) }
	got := c.want(c.req(c.guard, "GET", path(c.f.DoorNum), ""), 200)["items"].([]any)
	found := false
	for _, it := range got {
		u := it.(map[string]any)
		if len(u) != 3 {
			t.Fatalf("unit keys = %v, want id, door_number, block_name only", u)
		}
		found = found || u["id"] == c.f.Unit.String() && u["door_number"] == c.f.DoorNum
	}
	if !found {
		t.Fatalf("unit %s not found by its door number %q: %v", c.f.Unit, c.f.DoorNum, got)
	}
	if items := c.want(c.req(c.guard, "GET", path("zzz-no-such-door"), ""), 200)["items"].([]any); len(items) != 0 {
		t.Fatalf("unexpected matches: %v", items)
	}
	c.problemCode(c.rawReq(c.guard, "GET", path(strings.ToLower(c.f.DoorNum)), ""), 401, "guard_session_required")
	c.problemCode(c.req(c.board, "GET", path("1"), ""), 403, "insufficient_permission")
}
