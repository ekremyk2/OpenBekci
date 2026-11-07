package people

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module.
const (
	PermPersonRead       authz.Permission = "person.read"
	PermPersonManage     authz.Permission = "person.manage"
	PermOccupancyRead    authz.Permission = "occupancy.read"
	PermOccupancyManage  authz.Permission = "occupancy.manage"
	PermMoneyAccessGrant authz.Permission = "occupancy.grant_money"
	PermMembershipRead   authz.Permission = "membership.read"
	PermMembershipManage authz.Permission = "membership.manage"
	PermInvitationManage authz.Permission = "invitation.manage"
	PermInvitationHouse  authz.Permission = "invitation.household"
	PermClaimManage      authz.Permission = "claim.manage"
	PermDirectoryRead    authz.Permission = "directory.read"
	PermDirectoryConsent authz.Permission = "directory.consent"
	PermVendorRead       authz.Permission = "vendor.read"
	PermVendorManage     authz.Permission = "vendor.manage"
	PermMoveOutManage    authz.Permission = "moveout.manage"
	PermMoveOutRead      authz.Permission = "moveout.read"
)

// Permissions declares the module's permissions; the wiring registers them.
//
// Portal rights derive from occupancies (S3): a permission that lists
// Occupants is held, at the site of the occupancy's unit, by the people who
// currently own, rent or live in a unit there. Services narrow it to the
// caller's own unit (authz.CanActOnUnit / CanAccessUnit).
func Permissions() []authz.PermissionDef {
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}
	overview := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor}
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	orgSiteBlock := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	owners := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant}
	allOcc := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}
	return []authz.PermissionDef{
		{Name: PermPersonRead, Description: "Read people, their contacts and masked identity numbers", Roles: overview, Scopes: orgSite},
		{Name: PermPersonManage, Description: "Create and change people", Roles: managers, Scopes: orgSite},
		{Name: PermOccupancyRead, Description: "Read occupancies (who owns, rents or lives in a unit)",
			Roles: append(append([]authz.Role{}, overview...), authz.RoleAccountant), Scopes: orgSiteBlock},
		{Name: PermOccupancyManage, Description: "Add, change and end occupancies", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermMoneyAccessGrant, Description: "Let a household member see a unit's money (managers, owners and tenants of the unit)",
			Roles: managers, Scopes: orgSiteBlock, Occupants: owners},
		{Name: PermMoveOutManage, Description: "Record a move-out or transfer", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermMoveOutRead, Description: "Read move-outs and their closing documents",
			Roles: append(append([]authz.Role{}, overview...), authz.RoleAccountant), Scopes: orgSiteBlock},
		{Name: PermMembershipRead, Description: "Read role memberships", Roles: overview, Scopes: orgSite},
		{Name: PermMembershipManage, Description: "Grant and end role memberships", Roles: managers, Scopes: orgSite},
		{Name: PermInvitationManage, Description: "Invite people, manage the site join code and unit claims' invitations", Roles: managers, Scopes: orgSite},
		{Name: PermInvitationHouse, Description: "Invite household members to one's own unit", Roles: managers, Scopes: orgSite, Occupants: owners},
		{Name: PermClaimManage, Description: "Approve or reject unit claims from the site join code", Roles: managers, Scopes: orgSite},
		{Name: PermDirectoryRead, Description: "Read the site directory (managers and board see everyone, residents only who consented)",
			Roles: overview, Scopes: orgSite, Occupants: allOcc},
		{Name: PermDirectoryConsent, Description: "Choose what the site directory shows about oneself",
			Roles: authz.BuiltinRoles(), Scopes: orgSiteBlock, Occupants: allOcc},
		{Name: PermVendorRead, Description: "Read vendors", Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant, authz.RoleStaff}, Scopes: orgSite},
		{Name: PermVendorManage, Description: "Create and change vendors", Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleAccountant}, Scopes: orgSite},
	}
}
