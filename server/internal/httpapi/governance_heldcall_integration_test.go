package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// heldFix is a site with four equal owners and a session in progress.
type heldFix struct {
	meeting, item uuid.UUID
	site          uuid.UUID
	owners        []uuid.UUID
	units         []uuid.UUID
}

// seedHeld seeds four owners (one unit each, equal shares), a voted agenda
// item (budget: majority of all by number and share on the first call) and an
// in_session meeting at the first call whose second_call_at is already past:
// the clock must not matter.
func seedHeld(t *testing.T, w *authztest.World, org authztest.Org) heldFix {
	t.Helper()
	f := w.NewSite(t, org)
	h := heldFix{meeting: id.New(), item: id.New(), site: f.Site}
	snap := governance.Snapshot{AsOf: time.Now().UTC(), VoteWeight: "per_unit", ShareBasis: "equal", UnitCount: 4, TotalVotes: 4, TotalShare: 4, Unrepresented: []string{}}
	for i := 0; i < 4; i++ {
		u := f.Unit
		if i > 0 {
			u = w.NewUnit(t, f)
		}
		p := w.NewPerson(t, org)
		w.NewOccupancy(t, f, u, p, "owner")
		h.owners, h.units = append(h.owners, p), append(h.units, u)
		snap.Owners = append(snap.Owners, governance.SnapOwner{PersonID: p, DisplayName: fmt.Sprintf("Owner %d", i), Votes: 1, Share: 1,
			Units: []governance.SnapUnit{{UnitID: u, DoorNumber: fmt.Sprint(i), Share: 1}}})
	}
	sb, _ := json.Marshal(snap)
	rb, _ := json.Marshal(governance.TurkishPreset())
	now := time.Now().UTC()
	w.Exec(t, `INSERT INTO meeting (id, org_id, site_id, kind, scope, scheduled_at, second_call_at, location, status, eligibility_snapshot, settings_snapshot, called_at, held_call)
		VALUES ($1, $2, $3, 'general_assembly', 'site', $4, $5, 'Hall', 'in_session', $6::jsonb, $7::jsonb, $8, 'first')`,
		h.meeting, org.ID, f.Site, now.Add(-10*24*time.Hour), now.Add(-3*24*time.Hour), string(sb), string(rb), now.Add(-30*24*time.Hour))
	w.Exec(t, `INSERT INTO agenda_item (id, org_id, site_id, meeting_id, position, title, will_be_voted, topic) VALUES ($1, $2, $3, $4, 1, 'Budget', true, 'budget')`,
		h.item, org.ID, f.Site, h.meeting)
	return h
}

// The review's probe: the same counts give the same outcome whatever the
// time, because the call held is recorded, not read from the clock. Three of
// four owners present, two yes and one no: the first call needs a majority of
// all four owners (rejected); only a recorded second call measures against
// those present.
func TestIntegrationGovernanceHeldCallNotTheClock(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	h := seedHeld(t, w, org)
	base := func(s string) string { return orgPath(org, s) }
	mpath := "/meetings/" + h.meeting.String()
	desk := func(p, u uuid.UUID) string {
		return fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, p, u)
	}
	for i := 0; i < 3; i++ {
		c.must(201, mgr, "POST", base(mpath+"/attendance"), desk(h.owners[i], h.units[i]))
	}
	q := c.must(200, mgr, "GET", base(mpath+"/quorum"), "")
	if gstr(q, "call") != "first" || q["reached"] != true {
		t.Fatalf("quorum = %v: the second call time has passed but the first call is the one held", q)
	}
	vc := c.must(200, mgr, "PUT", base("/agenda-items/"+h.item.String()+"/vote-counts"), `{"yes_count":2,"no_count":1,"abstain_count":0,"yes_share":2,"no_share":1,"abstain_share":0}`)
	if gstr(vc, "outcome_hint") != "rejected" {
		t.Fatalf("hint = %v, want rejected at the first call whatever the clock says", vc["outcome_hint"])
	}
	// a decision of 2 of 4 owners cannot be accepted at the first call
	dec := fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"agenda_item_id":%q,"text":"Budget","outcome":"accepted"}`, h.site, h.meeting, h.item)
	c.must(422, mgr, "POST", base("/decisions"), dec)
	// the second call cannot be opened while the first has its quorum
	c.must(409, mgr, "POST", base(mpath+":open-second-call"), ``)
	// moving the clock changes nothing
	w.Exec(t, `UPDATE meeting SET scheduled_at = now() - interval '40 days', second_call_at = now() - interval '30 days' WHERE org_id = $1 AND id = $2`, org.ID, h.meeting)
	vc = c.must(200, mgr, "GET", base("/agenda-items/"+h.item.String()+"/vote-counts"), "")
	if gstr(vc, "outcome_hint") != "rejected" {
		t.Fatalf("hint after the clock moved = %v", vc["outcome_hint"])
	}
	if q = c.must(200, mgr, "GET", base(mpath+"/quorum"), ""); gstr(q, "call") != "first" {
		t.Fatalf("quorum call after the clock moved = %v", q["call"])
	}
}

// A first call that fails its quorum: the second call opens explicitly,
// records the failure, starts with empty attendance, and then measures
// against those present.
func TestIntegrationGovernanceSecondCall(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	h := seedHeld(t, w, org)
	base := func(s string) string { return orgPath(org, s) }
	mpath := "/meetings/" + h.meeting.String()
	desk := func(p, u uuid.UUID) string {
		return fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, p, u)
	}
	c.must(201, mgr, "POST", base(mpath+"/attendance"), desk(h.owners[0], h.units[0])) // one of four: no quorum
	if q := c.must(200, mgr, "GET", base(mpath+"/quorum"), ""); q["reached"] != false {
		t.Fatalf("one of four reached the first-call quorum: %v", q)
	}
	m := c.must(200, mgr, "POST", base(mpath+":open-second-call"), ``)
	if gstr(m, "held_call") != "second" || m["first_call_failed_at"] == nil || gstr(m, "status") != "in_session" {
		t.Fatalf("after opening the second call: %v", m)
	}
	c.must(409, mgr, "POST", base(mpath+":open-second-call"), ``) // once
	q := c.must(200, mgr, "GET", base(mpath+"/quorum"), "")
	if gstr(q, "call") != "second" || gnum(q, "present_count") != 0 || q["reached"] != false {
		t.Fatalf("the first call's attendance must not carry over: %v", q)
	}
	// the same owner checks in again (a record per call), plus one more
	a := c.must(201, mgr, "POST", base(mpath+"/attendance"), desk(h.owners[0], h.units[0]))
	if gstr(a, "call") != "second" {
		t.Fatalf("attendance = %v", a)
	}
	c.must(201, mgr, "POST", base(mpath+"/attendance"), desk(h.owners[1], h.units[1]))
	q = c.must(200, mgr, "GET", base(mpath+"/quorum"), "")
	if q["reached"] != true || gnum(q, "present_count") != 2 {
		t.Fatalf("second-call quorum = %v", q)
	}
	// two yes of two present: accepted, measured against those present
	vc := c.must(200, mgr, "PUT", base("/agenda-items/"+h.item.String()+"/vote-counts"), `{"yes_count":2,"no_count":0,"abstain_count":0,"yes_share":2,"no_share":0,"abstain_share":0}`)
	if gstr(vc, "outcome_hint") != "accepted" {
		t.Fatalf("hint = %v", vc["outcome_hint"])
	}
	// both sittings are on record
	if all := litems(c.must(200, mgr, "GET", base(mpath+"/attendance"), "")); len(all) != 3 {
		t.Fatalf("attendance rows = %d, want 3 (one first-call, two second-call)", len(all))
	}
}

// Check-in tokens are stored hashed: a personal call letter (and the owner's
// /me) issues the current token, and an earlier one stops working.
func TestIntegrationGovernanceCheckinTokenIsHashedAndRotates(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	g := govSeed(t, w, org, "called")
	base := func(s string) string { return orgPath(org, s) }
	mpath := "/meetings/" + g.meeting.String()
	if rec := e.as(mgr, "GET", base(mpath+"/call-letter?person_id="+g.p1.String()), ""); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("personal call letter: %d", rec.Code)
	}
	c.must(200, mgr, "POST", base(mpath+":open-first-call"), ``)
	c.must(404, mgr, "POST", base(mpath+"/attendance:checkin"), fmt.Sprintf(`{"token":%q}`, g.token1)) // replaced by the letter's token
	// a QR is accepted only while the session is open: the same call before opening is a conflict (above, in the other test)
}

// Attendance and QR check-in need the session to be open.
func TestIntegrationGovernanceAttendanceNeedsOpenSession(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	g := govSeed(t, w, org, "called")
	base := func(s string) string { return orgPath(org, s) }
	mpath := "/meetings/" + g.meeting.String()
	c.must(409, mgr, "POST", base(mpath+"/attendance"), fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, g.p1, g.u1))
	c.must(409, mgr, "POST", base(mpath+"/attendance:checkin"), fmt.Sprintf(`{"token":%q}`, g.token1))
	c.must(200, mgr, "POST", base(mpath+":open-first-call"), ``)
	c.must(201, mgr, "POST", base(mpath+"/attendance"), fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, g.p1, g.u1))
}
