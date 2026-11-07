package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/identity"
)

// Tests of the review fixes of the community module (PR 17): who may see a
// credential, what ends it, and the limits around the guard console.

// residentOcc is resident plus the id of the occupancy row it made.
func (c *commEnv) residentOcc(name string, unit uuid.UUID, kind string) (token string, person, occ uuid.UUID) {
	c.t.Helper()
	u, err := c.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: name + "@resident.example.test"})
	if err != nil {
		c.t.Fatal(err)
	}
	a := c.w.Person(c.t, c.org, authztest.Actor{Name: name, UserID: u.ID})
	occ = c.w.NewOccupancy(c.t, c.f, unit, a.PersonID, kind)
	return c.w.Token(c.t, a), a.PersonID, occ
}

func (c *commEnv) scanReason(qr string) (bool, string) {
	c.t.Helper()
	res := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr)), 200)
	return res["valid"] == true, res["reason"].(string)
}

func (c *commEnv) today() string { return time.Now().In(istanbul(c.t)).Format("2006-01-02") }

func TestIntegrationCommunityOccupancyEndRevokesCredentials(t *testing.T) {
	c := newCommEnv(t)
	ctx := context.Background()
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	tenant, _, tenantOcc := c.residentOcc("tenant1", c.f.Unit, "tenant")

	from, to := time.Now().Add(-10*time.Minute), time.Now().Add(2*time.Hour)
	entry := func(tok, name string) (string, string) {
		e := c.want(c.req(tok, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":%q,"plate":"34 %s 1","window_from":%q,"window_to":%q}`,
			c.f.Site, c.f.Unit, name, name[:2], rfc(from), rfc(to))), 201)
		return e["id"].(string), e["qr_token"].(string)
	}
	tenantEntry, tenantQR := entry(tenant, "Tenant guest")
	_, ownerQR := entry(owner, "Owner guest")
	rv := c.want(c.req(tenant, "POST", c.path("/recurring-visitors"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Tenant cleaner","days":[1,2,3,4,5,6,7],"start":"00:00","end":"23:59","valid_from":"2026-01-01"}`, c.f.Site, c.f.Unit)), 201)
	rvQR := rv["qr_token"].(string)
	for _, qr := range []string{tenantQR, ownerQR, rvQR} {
		if ok, why := c.scanReason(qr); !ok {
			t.Fatalf("before the move-out %q is %s", qr[:8], why)
		}
	}
	mark := time.Now().Add(-time.Second)

	// the tenant moves out (the formal use case: the people module's hook ends their credentials)
	c.want(c.req(c.manager, "POST", c.path("/occupancies/"+tenantOcc.String()+"/move-out"), fmt.Sprintf(`{"as_of":%q}`, c.today())), 201)
	if ok, why := c.scanReason(tenantQR); ok || why != "revoked" {
		t.Fatalf("the moved-out tenant's QR: valid=%v reason=%s", ok, why)
	}
	if ok, why := c.scanReason(rvQR); ok || why != "revoked" {
		t.Fatalf("the moved-out tenant's recurring QR: valid=%v reason=%s", ok, why)
	}
	if ok, why := c.scanReason(ownerQR); !ok {
		t.Fatalf("the owner's guest must stay valid: %s", why)
	}
	if got := c.want(c.req(owner, "GET", c.path("/visitor-entries/"+tenantEntry), ""), 200); got["cancelled"] != true {
		t.Fatalf("the open pre-registration should be cancelled: %v", got)
	}
	if got := c.want(c.req(owner, "GET", c.path("/recurring-visitors/"+rv["id"].(string)), ""), 200); got["status"] != "revoked" {
		t.Fatalf("the recurring visitor should be revoked: %v", got["status"])
	}
	// the projection marks them inactive and keeps only the owner's guest active
	delta, err := c.comm().VisitorCredentials(ctx, c.org.ID, c.f.Site, mark)
	if err != nil {
		t.Fatal(err)
	}
	inactive := map[string]bool{}
	for _, cr := range delta {
		if !cr.Active {
			inactive[cr.SourceID.String()] = true
		}
	}
	if !inactive[tenantEntry] || !inactive[rv["id"].(string)] {
		t.Fatalf("the delta should carry the tenant's credentials as inactive: %v", inactive)
	}
	snap, _ := c.comm().VisitorCredentials(ctx, c.org.ID, c.f.Site, time.Time{})
	for _, cr := range snap {
		if cr.SourceID.String() == tenantEntry || cr.SourceID.String() == rv["id"].(string) {
			t.Fatalf("a moved-out tenant's credential is still in the snapshot: %+v", cr)
		}
	}

	// backstop: nobody lives in a unit any more (end date reached without the hook, e.g. a scheduled end)
	other := c.w.NewUnit(t, c.f)
	o2, _, o2occ := c.residentOcc("owner3", other, "owner")
	e := c.want(c.req(o2, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Left behind","plate":"34 LB 1","window_from":%q,"window_to":%q}`, c.f.Site, other, rfc(from), rfc(to))), 201)
	qr := e["qr_token"].(string)
	if ok, why := c.scanReason(qr); !ok {
		t.Fatalf("before the end: %s", why)
	}
	c.w.Exec(t, `UPDATE occupancy SET valid_to = (now() AT TIME ZONE 'Europe/Istanbul')::date WHERE id = $1`, o2occ)
	if ok, why := c.scanReason(qr); ok || why != "revoked" {
		t.Fatalf("a unit without occupancy: valid=%v reason=%s", ok, why)
	}
	creds, _ := c.comm().VisitorCredentials(ctx, c.org.ID, c.f.Site, time.Time{})
	for _, cr := range creds {
		if cr.SourceID.String() == e["id"].(string) {
			t.Fatalf("credential of an unoccupied unit is active: %+v", cr)
		}
	}
	// the expiry job finishes the cleanup for the person who left
	if n, err := c.comm().ExpireVisitorsOrg(ctx, c.org.ID); err != nil || n == 0 {
		t.Fatalf("expiry job: n=%d err=%v", n, err)
	}
	if got := c.want(c.req(c.manager, "GET", c.path("/visitor-entries/"+e["id"].(string)), ""), 200); got["cancelled"] != true {
		t.Fatalf("the sweep should cancel the leaver's pre-registration: %v", got)
	}
}
