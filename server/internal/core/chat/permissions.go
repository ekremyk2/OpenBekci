package chat

import "github.com/openbekci/openbekci/server/internal/core/authz"

// PermManage edits the organization's chat bot credentials.
const PermManage authz.Permission = "chat.manage"

// PermGroupManage links a site's Telegram group and maintains its chat invite links.
const PermGroupManage authz.Permission = "chatgroups.manage"

// Permissions declares the module's permissions. Linking a chat account needs
// notifications.read (every member); only the credentials need chat.manage.
func Permissions() []authz.PermissionDef {
	return []authz.PermissionDef{
		{Name: PermManage, Description: "Edit the organization's chat bot credentials (Telegram, WhatsApp)",
			Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: []authz.ScopeType{authz.ScopeOrg}},
		{Name: PermGroupManage, Description: "Link the site's Telegram group and edit its WhatsApp invite link",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}, Scopes: []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}},
	}
}
