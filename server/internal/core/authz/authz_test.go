package authz

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/problem"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	org := []ScopeType{ScopeOrg, ScopeSite, ScopeBlock}
	if err := r.Register("test",
		PermissionDef{Name: "org.read", Description: "read", Roles: []Role{RoleOrgAdmin, RoleAuditor}, Scopes: org},
		PermissionDef{Name: "org.manage", Description: "manage", Roles: []Role{RoleOrgAdmin}, Scopes: []ScopeType{ScopeOrg}},
		PermissionDef{Name: "ticket.close", Description: "close", Roles: []Role{RoleSiteManager, RoleStaff}, Scopes: org},
		PermissionDef{Name: "platform.manage", Description: "operate", Roles: []Role{RolePlatformAdmin}, Scopes: []ScopeType{ScopeInstance}},
	); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegisterValidation(t *testing.T) {
	ok := PermissionDef{Name: "a.b", Description: "d", Roles: []Role{RoleStaff}, Scopes: []ScopeType{ScopeOrg}}
	for name, c := range map[string]struct {
		defs []PermissionDef
		want string
	}{
		"bad name":      {[]PermissionDef{{Name: "NoDot", Description: "d", Roles: ok.Roles, Scopes: ok.Scopes}}, "area.action"},
		"no desc":       {[]PermissionDef{{Name: "a.c", Roles: ok.Roles, Scopes: ok.Scopes}}, "description"},
		"no roles":      {[]PermissionDef{{Name: "a.c", Description: "d", Scopes: ok.Scopes}}, "no roles"},
		"no scopes":     {[]PermissionDef{{Name: "a.c", Description: "d", Roles: ok.Roles}}, "no scopes"},
		"unknown role":  {[]PermissionDef{{Name: "a.c", Description: "d", Roles: []Role{"king"}, Scopes: ok.Scopes}}, "unknown role"},
		"unknown scope": {[]PermissionDef{{Name: "a.c", Description: "d", Roles: ok.Roles, Scopes: []ScopeType{"galaxy"}}}, "unknown scope"},
		"twice":         {[]PermissionDef{ok, ok}, "twice"},
	} {
		t.Run(name, func(t *testing.T) {
			r := NewRegistry()
			err := r.Register("m", c.defs...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
			if len(r.Permissions()) != 0 {
				t.Fatal("a failed Register must not register anything")
			}
		})
	}
	r := NewRegistry()
	if err := r.Register("m", ok); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("other", ok); err == nil || !strings.Contains(err.Error(), "already registered by m") {
		t.Fatalf("duplicate across modules: %v", err)
	}
}

func TestRegistryQueries(t *testing.T) {
	r := testRegistry(t)
	if got := r.Permissions(); !slices.Equal(got, []Permission{"org.manage", "org.read", "platform.manage", "ticket.close"}) {
		t.Errorf("Permissions = %v", got)
	}
	if got := r.RolePermissions(RoleOrgAdmin); !slices.Equal(got, []Permission{"org.manage", "org.read"}) {
		t.Errorf("org_admin = %v", got)
	}
	if !r.RoleHas(RoleAuditor, "org.read") || r.RoleHas(RoleAuditor, "org.manage") || r.RoleHas(RoleGuard, "org.read") {
		t.Error("RoleHas is wrong")
	}
	if r.Module("org.read") != "test" {
		t.Error("Module")
	}
	y := string(r.SpecYAML())
	for _, want := range []string{"      - org.manage\n", "  guard: []\n", "  platform_admin:\n    - platform.manage\n"} {
		if !strings.Contains(y, want) {
			t.Errorf("SpecYAML lacks %q:\n%s", want, y)
		}
	}
}

func TestGrantsFromMembershipsRespectsScopes(t *testing.T) {
	r := testRegistry(t)
	site := uuid.New()
	got := GrantsFromMemberships(r, []Membership{
		{Role: RoleOrgAdmin, ScopeType: ScopeOrg},
		{Role: RoleOrgAdmin, ScopeType: ScopeOrg}, // duplicate: one grant
		{Role: RoleOrgAdmin, ScopeType: ScopeSite, ScopeID: &site},
	})
	has := func(p Permission, st ScopeType) bool {
		for _, g := range got {
			if g.Permission == p && g.ScopeType == st {
				return true
			}
		}
		return false
	}
	if !has("org.manage", ScopeOrg) || !has("org.read", ScopeOrg) || !has("org.read", ScopeSite) {
		t.Errorf("missing grants: %+v", got)
	}
	if has("org.manage", ScopeSite) {
		t.Error("org.manage can only be granted org-wide; an org_admin membership limited to a site must not carry it")
	}
	if len(got) != 3 {
		t.Errorf("got %d grants, want 3 (deduplicated): %+v", len(got), got)
	}
}

func TestScopeContainment(t *testing.T) {
	org, other := uuid.New(), uuid.New()
	siteA, siteB, blockA1 := uuid.New(), uuid.New(), uuid.New()
	grants := func(items ...Grant) Grants { return Grants{OrgID: org, Items: items} }
	orgWide := Grant{Permission: "p.x", ScopeType: ScopeOrg}
	siteGrant := Grant{Permission: "p.x", ScopeType: ScopeSite, ScopeID: &siteA}
	blockGrant := Grant{Permission: "p.x", ScopeType: ScopeBlock, ScopeID: &blockA1}

	for _, c := range []struct {
		name  string
		g     Grants
		scope Scope
		want  bool
	}{
		{"org grant covers org", grants(orgWide), Scope{OrgID: org}, true},
		{"org grant covers site", grants(orgWide), Scope{OrgID: org, SiteID: &siteB}, true},
		{"org grant covers block", grants(orgWide), Scope{OrgID: org, SiteID: &siteA, BlockID: &blockA1}, true},
		{"org grant does not cover another org", grants(orgWide), Scope{OrgID: other}, false},
		{"site grant covers its site", grants(siteGrant), Scope{OrgID: org, SiteID: &siteA}, true},
		{"site grant covers a block of its site", grants(siteGrant), Scope{OrgID: org, SiteID: &siteA, BlockID: &blockA1}, true},
		{"site grant does not cover another site", grants(siteGrant), Scope{OrgID: org, SiteID: &siteB}, false},
		{"site grant does not cover the whole org", grants(siteGrant), Scope{OrgID: org}, false},
		{"block grant covers its block", grants(blockGrant), Scope{OrgID: org, SiteID: &siteA, BlockID: &blockA1}, true},
		{"block grant does not cover the site", grants(blockGrant), Scope{OrgID: org, SiteID: &siteA}, false},
		{"block grant does not cover another block", grants(blockGrant), Scope{OrgID: org, BlockID: &siteB}, false},
		{"wrong permission", Grants{OrgID: org, Items: []Grant{{Permission: "p.y", ScopeType: ScopeOrg}}}, Scope{OrgID: org}, false},
		{"instance grant covers only the instance", grants(Grant{Permission: "p.x", ScopeType: ScopeInstance}), Scope{}, true},
		{"instance grant gives nothing inside an org", grants(Grant{Permission: "p.x", ScopeType: ScopeInstance}), Scope{OrgID: org}, false},
		{"org grant is not instance-wide", grants(orgWide), Scope{}, false},
		{"no grants", Grants{}, Scope{OrgID: org}, false},
	} {
		if got := c.g.Has("p.x", c.scope); got != c.want {
			t.Errorf("%s: Has = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRequire(t *testing.T) {
	org := uuid.New()
	anon := context.Background()
	err := Require(anon, "org.read", Scope{OrgID: org})
	var pe *problem.Error
	if !errors.As(err, &pe) || pe.Status != http.StatusUnauthorized {
		t.Fatalf("anonymous: %v, want 401", err)
	}
	ctx := WithPrincipal(anon, &Principal{UserID: uuid.New()})
	err = Require(ctx, "org.read", Scope{OrgID: org})
	if !errors.As(err, &pe) || pe.Status != http.StatusForbidden || pe.Code != problem.CodeInsufficientPermission {
		t.Fatalf("no grants: %v, want 403 insufficient_permission", err)
	}
	ctx = WithGrants(ctx, Grants{OrgID: org, Items: []Grant{{Permission: "org.read", ScopeType: ScopeOrg}}})
	if err := Require(ctx, "org.read", Scope{OrgID: org}); err != nil {
		t.Fatalf("granted: %v", err)
	}
	if !Has(ctx, "org.read", Scope{OrgID: org}) || Has(ctx, "org.manage", Scope{OrgID: org}) || Has(anon, "org.read", Scope{OrgID: org}) {
		t.Error("Has is wrong")
	}
	// instance grants set first survive the org grants being added
	ctx = WithGrants(WithPrincipal(anon, &Principal{}), Grants{Items: []Grant{{Permission: "platform.manage", ScopeType: ScopeInstance}}})
	ctx = WithGrants(ctx, Grants{OrgID: org, Items: []Grant{{Permission: "org.read", ScopeType: ScopeOrg}}})
	if !Has(ctx, "platform.manage", Scope{}) || !Has(ctx, "org.read", Scope{OrgID: org}) {
		t.Error("WithGrants must merge instance and org grants")
	}
}

func TestCanAccessUnitIsAStub(t *testing.T) {
	ctx := WithPrincipal(context.Background(), &Principal{UserID: uuid.New()})
	var pe *problem.Error
	if err := CanAccessUnit(ctx, uuid.New(), SideOwner); !errors.As(err, &pe) || pe.Status != 403 {
		t.Fatalf("CanAccessUnit = %v, want 403 until occupancies exist", err)
	}
}

func TestSummarize(t *testing.T) {
	s1, s2, b1 := uuid.New(), uuid.New(), uuid.New()
	got := Summarize([]Grant{
		{Permission: "a.read", ScopeType: ScopeOrg},
		{Permission: "a.read", ScopeType: ScopeSite, ScopeID: &s1}, // swallowed by the org-wide grant
		{Permission: "b.read", ScopeType: ScopeSite, ScopeID: &s1},
		{Permission: "b.read", ScopeType: ScopeSite, ScopeID: &s2},
		{Permission: "b.read", ScopeType: ScopeBlock, ScopeID: &b1},
		{Permission: "platform.manage", ScopeType: ScopeInstance}, // not an organization permission
	})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Permission != "a.read" || got[0].SiteIDs != nil || got[0].BlockIDs != nil {
		t.Errorf("a.read must be org-wide: %+v", got[0])
	}
	if got[1].Permission != "b.read" || len(got[1].SiteIDs) != 2 || len(got[1].BlockIDs) != 1 {
		t.Errorf("b.read: %+v", got[1])
	}
}

func TestInstanceGrants(t *testing.T) {
	r := NewResolver(nil, testRegistry(t), nil)
	if g := r.InstanceGrants(&Principal{}); len(g.Items) != 0 {
		t.Errorf("a regular user has no instance grants: %+v", g)
	}
	if g := r.InstanceGrants(nil); len(g.Items) != 0 {
		t.Errorf("anonymous has no instance grants: %+v", g)
	}
	g := r.InstanceGrants(&Principal{IsPlatformAdmin: true})
	if len(g.Items) != 1 || g.Items[0].Permission != "platform.manage" {
		t.Errorf("platform admin gets platform.manage only: %+v", g)
	}
	if g.Has("org.read", Scope{OrgID: uuid.New()}) {
		t.Error("a platform admin has no organization permissions without a membership")
	}
}
