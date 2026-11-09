package people

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module. The names are the draft contract's
// (api/PERMISSIONS.md); constants that share a value are the old
// finer-grained names kept so the service code reads the same.
const (
	PermPersonRead       authz.Permission = "people.read"
	PermPersonManage     authz.Permission = "people.manage"
	PermNationalID       authz.Permission = "people.national_id"
	PermPersonErase      authz.Permission = "people.erase"
	PermPersonExport     authz.Permission = "people.export"
	PermMoveOutManage    authz.Permission = "people.move_out"
	PermOccupancyRead    authz.Permission = "occupancies.read"
	PermOccupancyManage  authz.Permission = "occupancies.manage"
	PermMoneyAccessGrant authz.Permission = "occupancies.grant_money"
	PermHouseholdEnd     authz.Permission = "occupancies.household"
	PermMembershipRead   authz.Permission = "memberships.read"
	PermMembershipManage authz.Permission = "memberships.manage"
	PermInvitationManage authz.Permission = "invitations.manage"
	PermInvitationHouse  authz.Permission = "invitations.household"
	PermClaimManage      authz.Permission = "claims.decide"
	PermDirectoryRead    authz.Permission = "directory.read"
	PermVendorRead       authz.Permission = "vendors.read"
	PermVendorManage     authz.Permission = "vendors.manage"
	PermRolesRead        authz.Permission = "roles.read"
	PermMeManage         authz.Permission = "me.manage"
	PermCodesRead        authz.Permission = "codes.read"

	// Aliases: move-outs are read with the permission that records them; the
	// directory consent is part of managing oneself.
	PermMoveOutRead      = PermMoveOutManage
	PermDirectoryConsent = PermMeManage
)

// Permissions declares the module's permissions; the wiring registers them.
//
// Portal rights derive from occupancies (S3): a permission that lists
// Occupants is held, at the site of the occupancy's unit, by the people who
// currently own, rent or live in a unit there. Services narrow it to the
// caller's own unit (authz.CanActOnUnit / CanAccessUnit).
func Permissions() []authz.PermissionDef {
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}
	board := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember}
	overview := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor}
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	orgSiteBlock := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	owners := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant}
	allOcc := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}
	return []authz.PermissionDef{
		{Name: PermPersonRead, Description: "Read persons and their contact data", Roles: overview, Scopes: orgSite},
		{Name: PermPersonManage, Description: "Create and edit persons", Roles: board, Scopes: orgSite},
		{Name: PermNationalID, Description: "Read the masked national id and set or clear it", Roles: managers, Scopes: orgSite},
		{Name: PermPersonErase, Description: "Erase a person for a privacy request (re-authentication required)", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: []authz.ScopeType{authz.ScopeOrg}},
		{Name: PermPersonExport, Description: "Request a person's data export (the person requests their own with me.manage)", Roles: managers, Scopes: orgSite},
		{Name: PermMoveOutManage, Description: "Record and read move-outs and sales", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermOccupancyRead, Description: "Read occupancies (occupants read their own through me/occupancies until the service narrows reads to their unit)",
			Roles: overview, Scopes: orgSiteBlock},
		{Name: PermOccupancyManage, Description: "Create, change and end occupancies", Roles: board, Scopes: orgSiteBlock},
		{Name: PermHouseholdEnd, Description: "End a household member's occupancy on the caller's own unit",
			Roles: managers, Scopes: orgSite, Occupants: owners},
		{Name: PermMoneyAccessGrant, Description: "Let a household member see a unit's money (managers, owners and tenants of the unit)",
			Roles: managers, Scopes: orgSiteBlock, Occupants: owners},
		{Name: PermMembershipRead, Description: "Read role memberships", Roles: overview, Scopes: orgSite},
		{Name: PermMembershipManage, Description: "Create and end role memberships", Roles: managers, Scopes: orgSite},
		{Name: PermInvitationManage, Description: "Send, list, resend and revoke invitations", Roles: board, Scopes: orgSite},
		{Name: PermInvitationHouse, Description: "Invite household members to one's own unit and manage those invitations", Roles: managers, Scopes: orgSite, Occupants: owners},
		{Name: PermClaimManage, Description: "Approve or reject unit claims from the site QR path; read the site join code", Roles: managers, Scopes: orgSite},
		{Name: PermDirectoryRead, Description: "Read the resident directory (managers and board see everyone, residents only who consented)",
			Roles: overview, Scopes: orgSite, Occupants: allOcc},
		{Name: PermVendorRead, Description: "Read vendors", Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant, authz.RoleStaff}, Scopes: orgSite},
		{Name: PermVendorManage, Description: "Create and edit vendors", Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAccountant}, Scopes: orgSite},
		{Name: PermRolesRead, Description: "Read the role to permission matrix", Roles: overview, Scopes: orgSite},
		{Name: PermMeManage, Description: "Read and edit the caller's own person, directory consent and unit list in the organization",
			Roles: authz.BuiltinRoles(), Scopes: orgSiteBlock, Occupants: allOcc},
		{Name: PermCodesRead, Description: "Read enum and status labels", Roles: authz.BuiltinRoles(), Scopes: orgSiteBlock, Occupants: allOcc},
	}
}
