package httpapi

import (
	"fmt"
	"strings"
	"testing"
)

// A manager opening a private ticket for a resident files it under the
// resident's unit, not under the manager's own.
func TestIntegrationManagerOnBehalfTakesTheResidentsUnit(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	private := e.privateCategory(t, s)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	mgr := e.role(t, "site_manager")
	rec := e.call(t, mgr, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Door","description":"x","reporter_person_id":%q}`, s.Site, private, ann.person))
	mustStatus(t, rec, 201)
	if !strings.Contains(rec.Body.String(), `"unit_id":"`+s.Unit.String()+`"`) {
		t.Fatalf("ticket opened on behalf: %s", rec.Body.String())
	}
	// the resident has two units: the manager must say which
	e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Other")
	second := e.w.NewUnit(t, s.SiteFix)
	e.w.NewOccupancy(t, s.SiteFix, second, ann.person, "owner")
	mustStatus(t, e.call(t, mgr, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Door 2","description":"x","reporter_person_id":%q}`, s.Site, private, ann.person)), 422)
}

// Authorization comes before validation: somebody without notifications.manage
// is refused (403) even when the body is invalid.
func TestIntegrationSetDefaultsAuthorizesBeforeValidating(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	res := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	url := orgURL(org, "/notification-defaults") + "?site_id=" + s.Site.String()
	bad := `{"defaults":[{"type":"no.such.type","channels":[]}]}`
	mustStatus(t, e.call(t, res.token, "PUT", url, bad, "If-Match", `"1"`), 403)
	mustStatus(t, e.call(t, e.role(t, "site_manager"), "PUT", url, bad, "If-Match", `"1"`), 422)
}
