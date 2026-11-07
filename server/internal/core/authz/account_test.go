package authz

import (
	"testing"

	"github.com/google/uuid"
)

func TestInstanceGrantsAccountPermissionsForEveryPrincipal(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register("test",
		PermissionDef{Name: "me.read", Description: "d", Roles: EveryRole(), Scopes: []ScopeType{ScopeInstance, ScopeOrg}},
		PermissionDef{Name: "platform.manage", Description: "d", Roles: []Role{RolePlatformAdmin}, Scopes: []ScopeType{ScopeInstance}},
		PermissionDef{Name: "org.read", Description: "d", Roles: []Role{RoleOrgAdmin}, Scopes: []ScopeType{ScopeOrg}},
	)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(nil, reg, nil)
	user := &Principal{ID: uuid.New()}
	g := r.InstanceGrants(user)
	if !g.Has("me.read", Scope{}) || g.Has("platform.manage", Scope{}) || g.Has("org.read", Scope{}) {
		t.Errorf("a signed-in user holds exactly the account permissions: %+v", g)
	}
	admin := &Principal{ID: uuid.New(), IsPlatformAdmin: true}
	g = r.InstanceGrants(admin)
	if !g.Has("me.read", Scope{}) || !g.Has("platform.manage", Scope{}) || g.Has("org.read", Scope{OrgID: uuid.New()}) {
		t.Errorf("platform admin grants: %+v", g)
	}
	if len(r.InstanceGrants(nil).Items) != 0 {
		t.Error("anonymous callers hold nothing")
	}
}
