package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The next tenant of a unit must not see what the previous tenant filed: private
// tickets (reporter name and all) and the papers in the unit's private folder.
func TestIntegrationNextTenantCannotSeePreviousTenantsData(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	private := e.privateCategory(t, s)
	mgr := e.role(t, "site_manager")

	// the old tenant, while she lives there: a private ticket and her lease
	old := e.addResident(t, s.SiteFix, s.Unit, "tenant", "Old")
	rec := e.call(t, old.token, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Noise from the neighbours","description":"every night"}`, s.Site, private))
	mustStatus(t, rec, 201)
	tk := jsonOf[ticketJSON](t, rec)
	var ownFolder uuid.UUID
	rec = e.call(t, old.token, "GET", orgURL(org, "/folders")+"?site_id="+s.Site.String()+"&kind=unit", "")
	for _, f := range jsonOf[struct {
		Items []struct {
			ID uuid.UUID `json:"id"`
		} `json:"items"`
	}](t, rec).Items {
		ownFolder = f.ID
	}
	lease := e.upload(t, old.token, org, s.Site)
	rec = e.call(t, old.token, "POST", orgURL(org, "/documents"), fmt.Sprintf(`{"site_id":%q,"folder_id":%q,"title":"Old lease","file_id":%q}`, s.Site, ownFolder, lease))
	mustStatus(t, rec, 201)
	doc := jsonOf[struct {
		ID uuid.UUID `json:"id"`
	}](t, rec)

	// she moved out 10 days ago: her ticket and lease are older than that
	e.w.Exec(t, `UPDATE ticket SET created_at = now() - interval '100 days' WHERE org_id = $1 AND id = $2`, org.ID, tk.ID)
	e.w.Exec(t, `UPDATE document SET created_at = now() - interval '100 days' WHERE org_id = $1 AND id = $2`, org.ID, doc.ID)
	e.w.Exec(t, `UPDATE occupancy SET valid_from = current_date - 400, valid_to = current_date - 10 WHERE org_id = $1 AND person_id = $2`, org.ID, old.person)

	next := e.addResident(t, s.SiteFix, s.Unit, "tenant", "Next")
	e.w.Exec(t, `UPDATE occupancy SET valid_from = current_date - 10 WHERE org_id = $1 AND person_id = $2`, org.ID, next.person)

	tpath := orgURL(org, "/tickets/"+tk.ID.String())
	mustStatus(t, e.call(t, next.token, "GET", tpath, ""), 404)
	list := e.call(t, next.token, "GET", orgURL(org, "/tickets")+"?site_id="+s.Site.String(), "")
	mustStatus(t, list, 200)
	if strings.Contains(list.Body.String(), tk.ID.String()) || strings.Contains(list.Body.String(), "Old") {
		t.Fatalf("the next tenant lists the previous tenant's ticket: %s", list.Body.String())
	}
	mustStatus(t, e.call(t, next.token, "GET", orgURL(org, "/documents/"+doc.ID.String()), ""), 404)
	docs := e.call(t, next.token, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String(), "")
	mustStatus(t, docs, 200)
	if strings.Contains(docs.Body.String(), "Old lease") {
		t.Fatalf("the next tenant sees the previous tenant's lease: %s", docs.Body.String())
	}
	if rec := e.call(t, next.token, "GET", orgURL(org, "/files/"+lease.String()), ""); rec.Code == 200 {
		t.Fatalf("the next tenant downloaded the previous tenant's lease")
	}
	// management still sees the history
	mustStatus(t, e.call(t, mgr, "GET", tpath, ""), 200)
	mustStatus(t, e.call(t, mgr, "GET", orgURL(org, "/documents/"+doc.ID.String()), ""), 200)

	// the new tenant's own papers and tickets are theirs
	rec = e.call(t, next.token, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Heating","description":"cold"}`, s.Site, private))
	mustStatus(t, rec, 201)
	mine := jsonOf[ticketJSON](t, rec)
	mustStatus(t, e.call(t, next.token, "GET", orgURL(org, "/tickets/"+mine.ID.String()), ""), 200)
	lease2 := e.upload(t, next.token, org, s.Site)
	rec = e.call(t, next.token, "POST", orgURL(org, "/documents"), fmt.Sprintf(`{"site_id":%q,"folder_id":%q,"title":"New lease","file_id":%q}`, s.Site, ownFolder, lease2))
	mustStatus(t, rec, 201)
	docs = e.call(t, next.token, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String(), "")
	if !strings.Contains(docs.Body.String(), "New lease") || strings.Contains(docs.Body.String(), "Old lease") {
		t.Fatalf("the new tenant's documents: %s", docs.Body.String())
	}
}
