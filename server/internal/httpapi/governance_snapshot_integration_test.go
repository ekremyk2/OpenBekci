package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// The eligibility snapshot is a preview at the call and is retaken as of the
// day the session opens: an owner who sold after the call loses the vote and
// the buyer of record can check in.
func TestIntegrationGovernanceSnapshotRetakenAtOpening(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	f := w.NewSite(t, org)
	base := func(s string) string { return orgPath(org, s) }
	u1, u2, u3 := f.Unit, w.NewUnit(t, f), w.NewUnit(t, f)
	seller, p1, p3, buyer := w.NewPerson(t, org), w.NewPerson(t, org), w.NewPerson(t, org), w.NewPerson(t, org)
	w.NewOccupancy(t, f, u1, p1, "owner")
	sold := w.NewOccupancy(t, f, u2, seller, "owner")
	w.NewOccupancy(t, f, u3, p3, "owner")

	at := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Hour)
	mt := c.must(201, mgr, "POST", base("/meetings"), fmt.Sprintf(`{"site_id":%q,"kind":"general_assembly","scheduled_at":%q,"second_call_at":%q,"location":"Hall"}`,
		f.Site, at.Format(time.RFC3339), at.Add(10*24*time.Hour).Format(time.RFC3339)))
	mpath := "/meetings/" + gstr(mt, "id")
	c.must(201, mgr, "POST", base(mpath+"/agenda-items"), `{"title":"Budget","will_be_voted":true}`)
	called := c.must(200, mgr, "POST", base(mpath+":call"), `{}`)
	prev := gmap(called, "eligibility_snapshot")
	if gnum(prev, "total_votes") != 3 || prev["taken_at"] == nil {
		t.Fatalf("preview at the call = %v", prev)
	}

	// the sale: the seller's occupancy ends today, the buyer's starts today
	w.Exec(t, `UPDATE occupancy SET valid_to = (now() AT TIME ZONE 'utc')::date WHERE org_id = $1 AND id = $2`, org.ID, sold)
	bo := w.NewOccupancy(t, f, u2, buyer, "owner")
	w.Exec(t, `UPDATE occupancy SET valid_from = (now() AT TIME ZONE 'utc')::date WHERE org_id = $1 AND id = $2`, org.ID, bo)
	if again := c.must(200, mgr, "GET", base(mpath), ""); !snapshotHas(again, seller.String()) || snapshotHas(again, buyer.String()) {
		t.Fatalf("the preview must not change before the session opens: %v", again["eligibility_snapshot"])
	}

	opened := c.must(200, mgr, "POST", base(mpath+":open-first-call"), ``)
	if snapshotHas(opened, seller.String()) || !snapshotHas(opened, buyer.String()) || gnum(gmap(opened, "eligibility_snapshot"), "total_votes") != 3 {
		t.Fatalf("snapshot at opening = %v", opened["eligibility_snapshot"])
	}
	desk := func(p, u string) string {
		return fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, p, u)
	}
	c.must(201, mgr, "POST", base(mpath+"/attendance"), desk(buyer.String(), u2.String()))
	c.must(422, mgr, "POST", base(mpath+"/attendance"), desk(seller.String(), u2.String())) // no longer an owner of record
	// the buyer was never noticed: a pending call notice says so
	nl := litems(c.must(200, mgr, "GET", base(mpath+"/notices?status=pending"), ""))
	found := false
	for _, n := range nl {
		found = found || gstr(n, "person_id") == buyer.String()
	}
	if !found {
		t.Fatalf("no pending notice for the buyer: %v", nl)
	}
}

func snapshotHas(meeting map[string]any, person string) bool {
	snap := gmap(meeting, "eligibility_snapshot")
	owners, _ := snap["owners"].([]any)
	for _, o := range owners {
		if gstr(o.(map[string]any), "person_id") == person {
			return true
		}
	}
	return false
}
