package files

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module.
//
// files.upload and files.read are held by every member: they are the gate to
// the endpoints. What a caller may actually read is decided per file (the
// uploader, a files.manage holder, or the visibility resolver the module that
// owns the attachment registered). files.manage covers every file of the
// organization or site, deleting included.
const (
	PermUpload authz.Permission = "files.upload"
	PermRead   authz.Permission = "files.read"
	PermManage authz.Permission = "files.manage"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	every := authz.BuiltinRoles()
	grantable := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	return []authz.PermissionDef{
		{Name: PermUpload, Description: "Upload files (attached to a resource the caller may attach to)", Roles: every, Scopes: grantable},
		{Name: PermRead, Description: "Open the files endpoints; each file is further limited by its attachment's visibility", Roles: every, Scopes: grantable},
		{Name: PermManage, Description: "Read and delete every file of the organization or site, and see storage usage",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}, Scopes: []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}},
	}
}
