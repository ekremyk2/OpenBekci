package tenancy

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the sites part of the module. The names are the draft
// contract's (api/PERMISSIONS.md); constants that share a value are the old
// finer-grained names kept so the service code reads the same.
const (
	PermSiteRead        authz.Permission = "sites.read"
	PermSiteManage      authz.Permission = "sites.manage"
	PermSiteBankDetails authz.Permission = "sites.bank_details"
	PermSiteExport      authz.Permission = "sites.export"
	PermSiteOffboard    authz.Permission = "sites.offboard"
	PermUnitRead        authz.Permission = "units.read"
	PermUnitManage      authz.Permission = "units.manage"
	PermUnitImport      authz.Permission = "units.import"
	PermVehicleRead     authz.Permission = "vehicles.read"
	PermVehicleManage   authz.Permission = "vehicles.manage"
	PermJobsRead        authz.Permission = "jobs.read"
	PermExportsCreate   authz.Permission = "exports.create"

	// Aliases: creating a site is `sites.manage` held organization-wide;
	// importing a site archive needs `sites.export`; allocation keys follow
	// `units.read` / `sites.manage`; the spreadsheet import is `units.import`.
	PermSiteCreate       = PermSiteManage
	PermSiteImport       = PermSiteExport
	PermAllocationRead   = PermUnitRead
	PermAllocationManage = PermSiteManage
	PermImportRun        = PermUnitImport
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	staff := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant, authz.RoleStaff}
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember}
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	orgSiteBlock := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	org := []authz.ScopeType{authz.ScopeOrg}
	allOcc := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}
	owners := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant}
	return append(orgPermissions(), []authz.PermissionDef{
		{Name: PermSiteRead, Description: "Read a site, its blocks and basic settings", Roles: authz.BuiltinRoles(), Scopes: orgSite, Occupants: allOcc},
		{Name: PermSiteManage, Description: "Create and edit sites, settings, blocks, allocation keys and unit tags", Roles: managers, Scopes: orgSite},
		{Name: PermSiteBankDetails, Description: "Read the site bank details shown to payers",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant}, Scopes: orgSite, Occupants: owners},
		{Name: PermSiteExport, Description: "Export a site archive and import one (re-authentication required)", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermSiteOffboard, Description: "Offboard a site from the organization (re-authentication required)", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermUnitRead, Description: "Read units, appurtenances, allocation keys and tags (occupants follow once the service narrows reads to their own unit)", Roles: staff, Scopes: orgSiteBlock},
		{Name: PermUnitManage, Description: "Create and edit units", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermUnitImport, Description: "Import units and people from CSV or XLSX", Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}, Scopes: orgSite},
		{Name: PermVehicleRead, Description: "Read the plate registry and export it",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor}, Scopes: orgSiteBlock},
		{Name: PermVehicleManage, Description: "Register and remove vehicles (occupants: their own unit)", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermJobsRead, Description: "Read background jobs started by the caller (managers: all jobs of the organization)", Roles: authz.BuiltinRoles(), Scopes: orgSiteBlock, Occupants: allOcc},
		{Name: PermExportsCreate, Description: "Start an asynchronous export",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant}, Scopes: orgSite},
	}...)
}
