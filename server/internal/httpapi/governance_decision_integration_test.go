package httpapi

import (
	"fmt"
	"testing"
)

// An accepted decision needs recorded, non-empty counts whose threshold
// evaluation says accepted (or an explicit, reasoned override); counts are
// final once the item has a decision; decisions are final after the meeting
// closes, except for dissents added within the window.
func TestIntegrationGovernanceDecisionGating(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	h := seedHeld(t, w, org)
	base := func(s string) string { return orgPath(org, s) }
	mpath := "/meetings/" + h.meeting.String()
	for i := 0; i < 4; i++ {
		c.must(201, mgr, "POST", base(mpath+"/attendance"), fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, h.owners[i], h.units[i]))
	}
	counts := base("/agenda-items/" + h.item.String() + "/vote-counts")
	dec := func(extra string) string {
		return fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"agenda_item_id":%q,"text":"Budget approved","outcome":"accepted"%s}`, h.site, h.meeting, h.item, extra)
	}
	// nothing recorded
	c.must(422, mgr, "POST", base("/decisions"), dec(""))
	// recorded but empty: the review's probe
	c.must(200, mgr, "PUT", counts, `{"yes_count":0,"no_count":0,"abstain_count":0}`)
	c.must(422, mgr, "POST", base("/decisions"), dec(""))
	// shares omitted: never estimated, so the hint is undetermined and cannot gate accepted
	vc := c.must(200, mgr, "PUT", counts, `{"yes_count":3,"no_count":1,"abstain_count":0}`)
	if gstr(vc, "outcome_hint") != "undetermined" || vc["yes_share"] != nil {
		t.Fatalf("counts without shares = %v", vc)
	}
	c.must(422, mgr, "POST", base("/decisions"), dec(""))
	// an override needs a reason
	c.must(422, mgr, "POST", base("/decisions"), dec(`,"outcome_override":{"reason":"  "}`))
	// with shares the threshold says accepted (3 of 4 owners and of the share)
	vc = c.must(200, mgr, "PUT", counts, `{"yes_count":3,"no_count":1,"abstain_count":0,"yes_share":3,"no_share":1,"abstain_share":0}`)
	if gstr(vc, "outcome_hint") != "accepted" {
		t.Fatalf("counts = %v", vc)
	}
	d := c.must(201, mgr, "POST", base("/decisions"), dec(""))
	if d["outcome_override_reason"] != nil {
		t.Fatalf("decision = %v", d)
	}
	// counts are immutable once a decision exists
	c.must(409, mgr, "PUT", counts, `{"yes_count":0,"no_count":4,"abstain_count":0,"yes_share":0,"no_share":4}`)

	// an explicit override is recorded and audited
	dec2 := fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"text":"Acclaimed","outcome":"accepted","outcome_override":{"reason":"adopted by acclamation, counted by the chair"}}`, h.site, h.meeting)
	d2 := c.must(201, mgr, "POST", base("/decisions"), dec2)
	if gstr(d2, "outcome_override_reason") != "adopted by acclamation, counted by the chair" {
		t.Fatalf("override decision = %v", d2)
	}

	// close the meeting: decisions become final, dissents can be added in the window
	c.must(200, mgr, "POST", base(mpath+":close"), ``)
	c.must(409, mgr, "PATCH", base("/decisions/"+gstr(d, "id")), `{"text":"Budget approved, amended"}`, "If-Match", `"1"`)
	c.must(409, mgr, "PATCH", base("/decisions/"+gstr(d, "id")), `{"outcome":"rejected"}`, "If-Match", `"1"`)
	dis := fmt.Sprintf(`{"dissents":[{"person_id":%q,"text":"I object"}]}`, h.owners[3])
	d = c.must(200, mgr, "PATCH", base("/decisions/"+gstr(d, "id")), dis, "If-Match", `"1"`)
	if len(d["dissents"].([]any)) != 1 {
		t.Fatalf("dissents = %v", d["dissents"])
	}
	// removing or changing one is refused
	c.must(409, mgr, "PATCH", base("/decisions/"+gstr(d, "id")), `{"dissents":[]}`, "If-Match", fmt.Sprintf(`"%d"`, gnum(d, "version")))
	// after the window nothing can be added
	w.Exec(t, `UPDATE meeting SET closed_at = now() - interval '40 days' WHERE org_id = $1 AND id = $2`, org.ID, h.meeting)
	more := fmt.Sprintf(`{"dissents":[{"person_id":%q,"text":"I object"},{"person_id":%q,"text":"Me too"}]}`, h.owners[3], h.owners[2])
	c.must(409, mgr, "PATCH", base("/decisions/"+gstr(d, "id")), more, "If-Match", fmt.Sprintf(`"%d"`, gnum(d, "version")))
}
