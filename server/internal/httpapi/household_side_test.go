package httpapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// H2: each side manages its own household only.
func TestIntegrationHouseholdOperationsAreSideScoped(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	mkUser := func(person uuid.UUID) string {
		u, err := p.e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: id.New().String() + "@example.org"})
		if err != nil {
			t.Fatal(err)
		}
		p.w.Exec(t, `UPDATE person SET user_id = $2 WHERE id = $1`, person, u.ID)
		return tokenOf(t, p.e, u.ID)
	}
	owner, tenant := p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, f, f.Unit, owner, "owner")
	p.w.NewOccupancy(t, f, f.Unit, tenant, "tenant")
	ownerTok, tenantTok := mkUser(owner), mkUser(tenant)

	hOwner, hTenant := p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org)
	oOcc := p.w.NewOccupancy(t, f, f.Unit, hOwner, "household") // owner side
	tOcc := id.New()
	p.w.Exec(t, `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from) VALUES ($1, $2, $3, $4, $5, 'household', 'tenant', '2020-01-01')`,
		tOcc, p.org.ID, f.Site, f.Unit, hTenant)
	ownerInv := p.w.NewInvitation(t, f, "household", []byte("owner-side-token-hash-0000000000"))

	// the tenant cannot end the owner side's household member, nor see the owner side's invitations
	if r := p.call("DELETE", p.base()+"/me/household-occupancies/"+oOcc.String(), "", tenantTok); r.Code != 404 {
		t.Fatalf("tenant ended the owner's household member: %d %s", r.Code, r.Raw)
	}
	if n := p.qInt(`SELECT count(*) FROM occupancy WHERE id = $1 AND valid_to IS NULL`, oOcc); n != 1 {
		t.Fatal("the owner side's household member was ended")
	}
	for _, i := range items(p.must(p.call("GET", p.base()+"/me/household-invitations", "", tenantTok), 200)) {
		if str(i, "id") == ownerInv.String() {
			t.Fatal("the tenant lists the owner side's household invitations")
		}
	}
	if r := p.call("DELETE", p.base()+"/me/household-invitations/"+ownerInv.String(), "", tenantTok); r.Code != 404 {
		t.Fatalf("tenant revoked the owner's invitation: %d", r.Code)
	}
	if got := len(items(p.must(p.call("GET", p.base()+"/me/household-invitations", "", ownerTok), 200))); got != 1 {
		t.Fatalf("the owner sees %d of their own invitations", got)
	}

	// money access: owner and tenant each on their own side only
	p.must(p.call("PUT", p.base()+"/occupancies/"+tOcc.String()+"/money-access", `{"may_see_money":true}`, ownerTok), 403)
	p.must(p.call("PUT", p.base()+"/occupancies/"+oOcc.String()+"/money-access", `{"may_see_money":true}`, tenantTok), 403)
	p.must(p.call("PUT", p.base()+"/occupancies/"+tOcc.String()+"/money-access", `{"may_see_money":true}`, tenantTok), 200)
	p.must(p.call("PUT", p.base()+"/occupancies/"+oOcc.String()+"/money-access", `{"may_see_money":true}`, ownerTok), 200)

	// each side ends its own
	p.must(p.call("DELETE", p.base()+"/me/household-occupancies/"+oOcc.String(), "", ownerTok), 200)
}
