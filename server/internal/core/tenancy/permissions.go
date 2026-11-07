package tenancy

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the sites part of the module.
const (
	PermSiteRead         authz.Permission = "site.read"
	PermSiteCreate       authz.Permission = "site.create"
	PermSiteManage       authz.Permission = "site.manage"
	PermSiteExport       authz.Permission = "site.export"
	PermSiteImport       authz.Permission = "site.import"
	PermSiteOffboard     authz.Permission = "site.offboard"
	PermUnitRead         authz.Permission = "unit.read"
	PermUnitManage       authz.Permission = "unit.manage"
	PermAllocationRead   authz.Permission = "allocation.read"
	PermAllocationManage authz.Permission = "allocation.manage"
	PermVehicleRead      authz.Permission = "vehicle.read"
	PermVehicleManage    authz.Permission = "vehicle.manage"
	PermImportRun        authz.Permission = "import.run"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	all := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant, authz.RoleStaff}
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	orgSiteBlock := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	org := []authz.ScopeType{authz.ScopeOrg}
	return append(orgPermissions(), []authz.PermissionDef{
		{Name: PermSiteRead, Description: "Read sites and their blocks", Roles: all, Scopes: orgSite},
		{Name: PermSiteCreate, Description: "Create sites", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermSiteManage, Description: "Change a site's settings, bank details, lock date and blocks", Roles: managers, Scopes: orgSite},
		{Name: PermSiteExport, Description: "Export a whole site to an archive (site hand-over)", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermSiteImport, Description: "Import a site archive into the organization", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermSiteOffboard, Description: "Offboard a site after a hand-over (revokes the organization's access)", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermUnitRead, Description: "Read units, tags and appurtenances", Roles: all, Scopes: orgSiteBlock},
		{Name: PermUnitManage, Description: "Create and change units and tags", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermAllocationRead, Description: "Read allocation keys and their values",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant}, Scopes: orgSite},
		{Name: PermAllocationManage, Description: "Create and change allocation keys and their values",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleAccountant}, Scopes: orgSite},
		{Name: PermVehicleRead, Description: "Read vehicles and export plates",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleStaff, authz.RoleGuard}, Scopes: orgSiteBlock},
		{Name: PermVehicleManage, Description: "Add and remove vehicles", Roles: managers, Scopes: orgSiteBlock},
		{Name: PermImportRun, Description: "Import units and people from a spreadsheet", Roles: managers, Scopes: orgSite},
	}...)
}
