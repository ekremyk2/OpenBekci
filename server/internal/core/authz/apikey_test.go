package authz

import (
	"testing"

	"github.com/google/uuid"
)

// An API key's grants are the intersection of its scopes and the creator's
// role grants: never occupancy rights, never another site, never a scope
// the creator does not hold.
func TestKeyGrantsIntersection(t *testing.T) {
	reg := NewRegistry()
	admin := []Role{RoleOrgAdmin}
	if err := reg.Register("t",
		PermissionDef{Name: "a.read", Description: "a", Roles: admin, Scopes: []ScopeType{ScopeOrg, ScopeSite}},
		PermissionDef{Name: "a.manage", Description: "a", Roles: admin, Scopes: []ScopeType{ScopeOrg}},
		PermissionDef{Name: "b.read", Description: "b", Roles: admin, Scopes: []ScopeType{ScopeOrg, ScopeSite}},
		PermissionDef{Name: "me.read", Description: "m", Roles: EveryRole(), Scopes: []ScopeType{ScopeInstance}},
	); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(nil, reg, nil)
	org, s1, s2 := uuid.New(), uuid.New(), uuid.New()
	creator := Grants{OrgID: org, Items: []Grant{
		{Permission: "a.read", ScopeType: ScopeOrg},
		{Permission: "a.manage", ScopeType: ScopeOrg},
		{Permission: "b.read", ScopeType: ScopeOrg},
		{Permission: "b.read", ScopeType: ScopeSite, ScopeID: &s2},
		{Permission: "a.read", ScopeType: ScopeSite, ScopeID: &s1, FromOccupancy: true},
	}, Occupancies: []OccupancyAccess{{UnitID: uuid.New()}}}

	has := func(g Grants, perm Permission, site *uuid.UUID) bool {
		return g.Has(perm, Scope{OrgID: org, SiteID: site})
	}

	// unrestricted: the named permissions, org-wide
	g := r.keyGrants(creator, &APIKeyInfo{KeyID: uuid.New(), OrgID: org, Scopes: []Permission{"a.read", "a.manage"}})
	if !has(g, "a.read", nil) || !has(g, "a.manage", nil) || has(g, "b.read", nil) || has(g, "b.read", &s2) {
		t.Errorf("unrestricted = %+v", g.Items)
	}
	if len(g.Occupancies) != 0 {
		t.Error("a key carries occupancies")
	}
	// a scope the creator does not hold gives nothing
	g = r.keyGrants(Grants{OrgID: org, Items: []Grant{{Permission: "a.read", ScopeType: ScopeOrg}}}, &APIKeyInfo{OrgID: org, Scopes: []Permission{"a.read", "a.manage"}})
	if !has(g, "a.read", nil) || has(g, "a.manage", nil) {
		t.Errorf("not held = %+v", g.Items)
	}
	// an occupancy right is never a key right, even when the scope names it
	g = r.keyGrants(Grants{OrgID: org, Items: []Grant{{Permission: "a.read", ScopeType: ScopeSite, ScopeID: &s1, FromOccupancy: true}}}, &APIKeyInfo{OrgID: org, Scopes: []Permission{"a.read"}})
	if len(g.Items) != 0 {
		t.Errorf("occupancy grant leaked into a key: %+v", g.Items)
	}

	// restricted to s1: org grants become s1 grants where the permission can be granted at a site, are dropped otherwise
	g = r.keyGrants(creator, &APIKeyInfo{OrgID: org, SiteID: &s1, Scopes: []Permission{"a.read", "a.manage", "b.read"}})
	if !has(g, "a.read", &s1) || has(g, "a.read", &s2) || has(g, "a.read", nil) || has(g, "a.manage", &s1) || has(g, "a.manage", nil) || !has(g, "b.read", &s1) || has(g, "b.read", &s2) {
		t.Errorf("restricted to s1 = %+v", g.Items)
	}
	// restricted to s2: the creator's own s2 grant is kept
	g = r.keyGrants(Grants{OrgID: org, Items: []Grant{{Permission: "b.read", ScopeType: ScopeSite, ScopeID: &s2}}}, &APIKeyInfo{OrgID: org, SiteID: &s2, Scopes: []Permission{"b.read"}})
	if !has(g, "b.read", &s2) || has(g, "b.read", &s1) {
		t.Errorf("restricted to s2 = %+v", g.Items)
	}
	// a site grant of the creator for another site never reaches a key restricted elsewhere
	g = r.keyGrants(Grants{OrgID: org, Items: []Grant{{Permission: "b.read", ScopeType: ScopeSite, ScopeID: &s2}}}, &APIKeyInfo{OrgID: org, SiteID: &s1, Scopes: []Permission{"b.read"}})
	if len(g.Items) != 0 {
		t.Errorf("restricted to s1, creator only at s2 = %+v", g.Items)
	}

	// no account or instance permissions for a key
	if got := r.InstanceGrants(&Principal{UserID: uuid.New(), APIKey: &APIKeyInfo{OrgID: org}}); len(got.Items) != 0 {
		t.Errorf("a key holds instance grants: %+v", got.Items)
	}
	if got := r.InstanceGrants(&Principal{UserID: uuid.New()}); len(got.Items) != 1 {
		t.Errorf("a person holds %d account grants, want 1", len(got.Items))
	}
	// a key of another organization is not a member there
	if _, member, err := r.ResolveOrg(t.Context(), &Principal{UserID: uuid.New(), APIKey: &APIKeyInfo{OrgID: uuid.New()}}, org); err != nil || member {
		t.Errorf("another organization: member=%v err=%v", member, err)
	}
}
