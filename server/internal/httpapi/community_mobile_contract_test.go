package httpapi

import (
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// What a booking preview says is what create does: the same fee, the same
// field-error codes, the same 409s and refusals; and it writes nothing.
func TestIntegrationCommunityBookingPreview(t *testing.T) {
	c := newCommEnv(t)
	fid := c.facility(hallRules)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	household, _ := c.resident("household1", c.f.Unit, "household")
	other := c.w.NewUnit(t, c.f)
	stranger, _ := c.resident("owner2", other, "owner")
	pv := func(token string, from, to time.Time, extra string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"facility_id":%q,"unit_id":%q,"start":%q,"end":%q%s}`, fid, c.f.Unit, rfc(from), rfc(to), extra)
		return c.req(token, "POST", c.path("/facilities/"+fid+"/bookings:preview"), body)
	}
	bookings := func() int {
		return len(c.want(c.req(c.manager, "GET", c.path("/bookings?site_id="+c.f.Site.String()), ""), 200)["items"].([]any))
	}

	d := 5
	from, to := slot(t, d, 10), slot(t, d, 12)
	got := c.want(pv(owner, from, to, ""), 200)
	fee := got["fee"].(map[string]any)
	rule := got["refund_rule"].(map[string]any)
	quota := got["quota"].(map[string]any)
	if got["bookable"] != true || got["conflict"] != nil || len(got["violations"].([]any)) != 0 ||
		fee["amount"].(float64) != 20000 || fee["currency"] != "TRY" || fee["debtor_kind"] != "owner" ||
		rule["cancel_refund_cutoff_hours"].(float64) != 24 || rule["refundable_until"] != rfc(from.Add(-24*time.Hour)) ||
		quota["limit"].(float64) != 2 || quota["period"] != "month" || quota["used"].(float64) != 0 || quota["remaining"].(float64) != 2 {
		t.Fatalf("preview = %v", got)
	}
	// nothing was written: no booking, no charge, no audit event
	if n := bookings(); n != 0 || len(c.charge.charges) != 0 {
		t.Fatalf("preview wrote: %d bookings, %d charges", n, len(c.charge.charges))
	}
	// a preview carries no Idempotency-Key and is not replayed
	c.want(pv(owner, from, to, ""), 200)

	// the manager previews for the unit and chooses the side charged
	mg := c.want(pv(c.manager, from, to, `,"debtor_kind":"tenant"`), 200)
	if mg["fee"].(map[string]any)["debtor_kind"] != "tenant" {
		t.Fatalf("manager preview = %v", mg)
	}

	// after one booking: quota used, the slot is taken
	c.want(c.book(owner, fid, c.f.Unit, from, to), 201)
	got = c.want(pv(owner, slot(t, d, 14), slot(t, d, 15), ""), 200)
	if q := got["quota"].(map[string]any); q["used"].(float64) != 1 || q["remaining"].(float64) != 1 || got["bookable"] != true {
		t.Fatalf("quota after one booking = %v", got)
	}
	got = c.want(pv(owner, slot(t, d, 11), slot(t, d, 13), ""), 200)
	if got["conflict"] != "slot_taken" || got["bookable"] != false {
		t.Fatalf("taken slot = %v", got)
	}
	c.problemCode(c.book(owner, fid, c.f.Unit, slot(t, d, 11), slot(t, d, 13)), 409, "conflict")

	// a blocked date
	s := slot(t, d+2, 10)
	c.want(c.req(c.manager, "POST", c.path("/facilities/"+fid+"/blocks"), fmt.Sprintf(`{"start":%q,"end":%q,"reason":"Painting"}`, rfc(s), rfc(s.Add(3*time.Hour)))), 201)
	if got = c.want(pv(owner, slot(t, d+2, 11), slot(t, d+2, 12), ""), 200); got["conflict"] != "facility_blocked" || got["bookable"] != false {
		t.Fatalf("blocked = %v", got)
	}

	// the rule violations carry the codes create answers 422 with
	cases := []struct {
		name     string
		from, to time.Time
		code     string
	}{
		{"before opening", slot(t, d+3, 6), slot(t, d+3, 7), "outside_opening_hours"},
		{"too long", slot(t, d+4, 9), slot(t, d+4, 14), "too_long"},
		{"too soon", time.Now().Add(10 * time.Minute), time.Now().Add(70 * time.Minute), "too_soon"},
		{"too far ahead", slot(t, 60, 10), slot(t, 60, 11), "too_far_ahead"},
	}
	for _, tc := range cases {
		prev := c.want(pv(owner, tc.from, tc.to, ""), 200)
		var codes []string
		for _, v := range prev["violations"].([]any) {
			codes = append(codes, v.(map[string]any)["code"].(string))
		}
		if !contains(codes, tc.code) || prev["bookable"] != false {
			t.Errorf("%s: violations %v", tc.name, prev["violations"])
		}
		if created := c.problemCode(c.book(owner, fid, c.f.Unit, tc.from, tc.to), 422, "validation_failed"); !reflect.DeepEqual(created, codes) {
			t.Errorf("%s: preview codes %v, create codes %v", tc.name, codes, created)
		}
	}
	// the quota used up: preview says so
	c.want(c.book(owner, fid, c.f.Unit, slot(t, d, 14), slot(t, d, 15)), 201)
	prev := c.want(pv(owner, slot(t, d, 16), slot(t, d, 17), ""), 200)
	if q := prev["quota"].(map[string]any); q["remaining"].(float64) != 0 || prev["bookable"] != false {
		t.Fatalf("used-up quota = %v", prev)
	}

	// refusals depend on who asks, as for create
	c.problemCode(pv(household, slot(t, d+1, 10), slot(t, d+1, 11), ""), 422, "validation_failed")
	c.problemCode(pv(stranger, slot(t, d+1, 10), slot(t, d+1, 11), ""), 403, "insufficient_permission")
	c.problemCode(pv(owner, to, from, ""), 422, "validation_failed") // end before start
	c.want(c.req(owner, "POST", c.path("/facilities/"+fid+"/bookings:preview"), `{}`), 422)
	// the body's facility must be the path's
	c.problemCode(c.req(owner, "POST", c.path("/facilities/"+c.facility(hallRules)+"/bookings:preview"),
		fmt.Sprintf(`{"facility_id":%q,"unit_id":%q,"start":%q,"end":%q}`, fid, c.f.Unit, rfc(slot(t, d+1, 10)), rfc(slot(t, d+1, 11)))), 422, "validation_failed")
	c.want(c.req("", "POST", c.path("/facilities/"+fid+"/bookings:preview"), `{}`), 401)
	if n := bookings(); n != 2 {
		t.Fatalf("%d bookings after the previews, want only the 2 created", n)
	}
}

// Generated clients send null for an omitted optional body.
func TestIntegrationCommunityCancelBookingNullBody(t *testing.T) {
	c := newCommEnv(t)
	fid := c.facility(hallRules)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	b := c.want(c.book(owner, fid, c.f.Unit, slot(t, 6, 10), slot(t, 6, 11)), 201)
	got := c.want(c.req(owner, "POST", c.path("/bookings/"+b["id"].(string)+":cancel"), `null`, "Idempotency-Key", c.idem()), 200)
	if got["status"] != "cancelled" || got["refund"] == nil {
		t.Fatalf("cancel with a null body = %v", got)
	}
	// a required body is still required: null is not a decision
	owner2, _ := c.resident("owner2", c.f.Unit, "owner")
	c.want(c.req(owner2, "POST", c.path("/visitor-approval-requests/"+b["id"].(string)+":decide"), `null`), 422)
}

// One approval request can be read by the resident it is addressed to and by
// the site's managers; the shape is the list item's.
func TestIntegrationCommunityGetApprovalRequest(t *testing.T) {
	c := newCommEnv(t)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	member, _ := c.resident("member1", c.f.Unit, "household")
	other := c.w.NewUnit(t, c.f)
	stranger, _ := c.resident("owner2", other, "owner")

	c.want(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Courier"}`, c.f.Site, c.f.Unit)), 201)
	item := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=pending"), ""), 200)["items"].([]any)[0].(map[string]any)
	rid := item["id"].(string)
	// the notification names the request: the id in its payload opens it
	notes := c.notes.byTemplate("visitor.approval_requested")
	if len(notes) != 1 || fmt.Sprint(notes[0].Payload["request_id"]) != rid {
		t.Fatalf("notification payload = %+v", notes)
	}

	for name, token := range map[string]string{"addressee": owner, "household": member, "manager": c.manager, "board": c.board, "auditor": c.auditor} {
		got := c.want(c.req(token, "GET", c.path("/visitor-approval-requests/"+rid), ""), 200)
		if !reflect.DeepEqual(got, item) {
			t.Errorf("%s reads %v, want the list item %v", name, got, item)
		}
	}
	// not addressed to them: 404, as if it did not exist
	c.problemCode(c.req(stranger, "GET", c.path("/visitor-approval-requests/"+rid), ""), 404, "not_found")
	c.problemCode(c.req(owner, "GET", c.path("/visitor-approval-requests/"+c.org.ID.String()), ""), 404, "not_found")
	c.want(c.req("", "GET", c.path("/visitor-approval-requests/"+rid), ""), 401)
	// the answer shows in the single read
	c.want(c.req(owner, "POST", c.path("/visitor-approval-requests/"+rid+":decide"), `{"decision":"deny"}`), 200)
	if got := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests/"+rid), ""), 200); got["status"] != "denied" || got["decided_by"] == nil {
		t.Fatalf("after the answer = %v", got)
	}
	// an expired pending request reads as expired
	c.want(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Late"}`, c.f.Site, c.f.Unit)), 201)
	c.w.Exec(t, `UPDATE visitor_approval_request SET expires_at = now() - interval '1 minute' WHERE org_id = $1 AND status = 'pending'`, c.org.ID)
	late := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=expired"), ""), 200)["items"].([]any)
	if len(late) != 1 {
		t.Fatalf("expired requests = %v", late)
	}
	if got := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests/"+late[0].(map[string]any)["id"].(string)), ""), 200); got["status"] != "expired" {
		t.Fatalf("expired single read = %v", got)
	}
}
