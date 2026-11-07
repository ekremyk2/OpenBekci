package notifications

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module.
const (
	PermRead   authz.Permission = "notifications.read"
	PermManage authz.Permission = "notifications.manage"
)

// Permissions declares the module's permissions; the wiring registers them.
// notifications.read is the "member" pseudo role of the plan: everybody with a
// role or a current occupancy in the organization.
func Permissions() []authz.PermissionDef {
	all := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	return []authz.PermissionDef{
		{Name: PermRead, Description: "Read the caller's own notifications, mutes and preferences",
			Roles: authz.BuiltinRoles(), Scopes: all, Occupants: []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}},
		{Name: PermManage, Description: "Edit the site defaults for automatic messages; read the SMS counter",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}, Scopes: []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}},
	}
}
