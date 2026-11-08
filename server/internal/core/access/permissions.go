package access

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the access-control module (design 07-access-control, release 0.3).
const (
	// PermRead reads agents, access points and credentials.
	PermRead authz.Permission = "access.read"
	// PermManage creates agents and access points and revokes credentials.
	PermManage authz.Permission = "access.manage"
	// PermOpen opens an access point by hand (the guard's button).
	PermOpen authz.Permission = "access.open"
	// PermViewEvents reads the opening log.
	PermViewEvents authz.Permission = "access.view_events"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}
	readers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor}
	return []authz.PermissionDef{
		{Name: PermRead, Description: "Read agents, access points and credentials", Roles: readers, Scopes: orgSite},
		{Name: PermManage, Description: "Create and revoke agents, manage access points and credentials", Roles: managers, Scopes: orgSite},
		{Name: PermOpen, Description: "Open an access point manually",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleGuard}, Scopes: orgSite},
		{Name: PermViewEvents, Description: "Read the opening log (access events)", Roles: readers, Scopes: orgSite},
	}
}
