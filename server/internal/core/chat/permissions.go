package chat

import "github.com/openbekci/openbekci/server/internal/core/authz"

// PermManage edits the organization's chat bot credentials.
const PermManage authz.Permission = "chat.manage"

// Permissions declares the module's permissions. Linking a chat account needs
// notifications.read (every member); only the credentials need chat.manage.
func Permissions() []authz.PermissionDef {
	return []authz.PermissionDef{
		{Name: PermManage, Description: "Edit the organization's chat bot credentials (Telegram, WhatsApp)",
			Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: []authz.ScopeType{authz.ScopeOrg}},
	}
}
