package platformadmin

import "github.com/openbekci/openbekci/server/internal/core/authz"

// PermManage is the instance operator permission (the /v1/platform endpoints).
const PermManage authz.Permission = "platform.manage"

// Permissions declares the module's permissions; the wiring registers them.
// platform_admin is a pseudo-role (the is_platform_admin flag of the user): it
// holds platform.manage and nothing inside organizations; an operator who
// needs to act in an organization is given a membership there.
func Permissions() []authz.PermissionDef {
	return []authz.PermissionDef{
		{Name: PermManage, Description: "Operate the instance: organizations, settings, jobs, health",
			Roles: []authz.Role{authz.RolePlatformAdmin}, Scopes: []authz.ScopeType{authz.ScopeInstance}},
	}
}
