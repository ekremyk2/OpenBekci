// Package tickets is the ticket module (backend plan 4.9, decisions O1 to O3):
// a configurable workflow per site, categories, private and common tickets
// with "+1" votes and merging, public and internal comments, photos and an
// append-only status history.
package tickets

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module.
const (
	PermRead         authz.Permission = "tickets.read"
	PermReadAssigned authz.Permission = "tickets.read_assigned"
	PermManage       authz.Permission = "tickets.manage"
	PermWork         authz.Permission = "tickets.work"
	PermOpen         authz.Permission = "tickets.open"
)

// AnyView is the permission set of the read operations: any one suffices.
var AnyView = authz.AnyOf(PermRead, PermOpen, PermReadAssigned)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	mgmt := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember}
	return []authz.PermissionDef{
		{Name: PermRead, Description: "Read every ticket of the site, internal comments included",
			Roles: append(append([]authz.Role{}, mgmt...), authz.RoleAuditor), Scopes: orgSite},
		{Name: PermReadAssigned, Description: "Read the tickets assigned to the caller and the common tickets of the site",
			Roles: []authz.Role{authz.RoleStaff}, Scopes: orgSite},
		{Name: PermManage, Description: "Edit the workflow and categories; assign, merge, change visibility, write internal notes, open on behalf",
			Roles: mgmt, Scopes: orgSite},
		{Name: PermWork, Description: "Move assigned tickets through the workflow and add internal notes",
			Roles: []authz.Role{authz.RoleStaff}, Scopes: orgSite},
		{Name: PermOpen, Description: "Open tickets, comment, vote (+1); read own and common tickets",
			Roles: append(append([]authz.Role{}, mgmt...), authz.RoleStaff), Scopes: orgSite,
			Occupants: []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}},
	}
}
