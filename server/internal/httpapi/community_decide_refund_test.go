package httpapi

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// A retried approval tap gets the stored result (200); a different answer is 409.
func TestIntegrationCommunityApprovalReplayIsIdempotent(t *testing.T) {
	c := newCommEnv(t)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	member, _ := c.resident("member1", c.f.Unit, "household")
	c.want(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Courier"}`, c.f.Site, c.f.Unit)), 201)
	req := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=pending"), ""), 200)["items"].([]any)[0].(map[string]any)["id"].(string)
	first := c.want(c.req(owner, "POST", c.path("/visitor-approval-requests/"+req+":decide"), `{"decision":"approve"}`), 200)
	again := c.want(c.req(owner, "POST", c.path("/visitor-approval-requests/"+req+":decide"), `{"decision":"approve"}`), 200)
	other := c.want(c.req(member, "POST", c.path("/visitor-approval-requests/"+req+":decide"), `{"decision":"approve"}`), 200)
	for _, got := range []map[string]any{again, other} {
		if got["status"] != "approved" || got["decided_by"] != first["decided_by"] || got["decided_at"] != first["decided_at"] {
			t.Fatalf("replay = %v, want the stored result %v", got, first)
		}
	}
	c.problemCode(c.req(owner, "POST", c.path("/visitor-approval-requests/"+req+":decide"), `{"decision":"deny"}`), 409, "conflict")
	if len(c.hooks.decided) != 1 {
		t.Fatalf("the replay must not decide twice: %d decisions", len(c.hooks.decided))
	}
}

// Managers always refund the fee when they cancel; only a resident cancelling
// their own booking honours the cutoff.
func TestIntegrationCommunityManagerCancelAlwaysRefunds(t *testing.T) {
	c := newCommEnv(t)
	fid := c.facility(hallRules)
	seed := func() uuid.UUID {
		b := id.New()
		c.w.Exec(t, `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, start_at, end_at, debtor_kind, charge_id, fee_amount, fee_currency)
			VALUES ($1, $2, $3, $4, $5, now() + interval '3 hours', now() + interval '4 hours', 'owner', $6, 20000, 'TRY')`, b, c.org.ID, c.f.Site, uuid.MustParse(fid), c.f.Unit, id.New())
		return b
	}
	b := seed()
	got := c.want(c.req(c.manager, "POST", c.path("/bookings/"+b.String()+":cancel"), `{"reason":"maintenance"}`, "Idempotency-Key", c.idem()), 200)
	if got["refund"] == nil || len(c.charge.refunds) != 1 {
		t.Fatalf("manager cancel inside the cutoff: %v refunds=%d", got, len(c.charge.refunds))
	}
	b = seed()
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	kept := c.want(c.req(owner, "POST", c.path("/bookings/"+b.String()+":cancel"), ``, "Idempotency-Key", c.idem()), 200)
	if kept["refund"] != nil || len(c.charge.refunds) != 1 {
		t.Fatalf("resident cancel inside the cutoff must not refund: %v", kept)
	}
}
