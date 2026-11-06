package identity

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions held by every signed-in caller; no organization is involved, so
// the handlers only need an authenticated principal.
const (
	PermMeRead            authz.Permission = "me.read"
	PermSessionsManageOwn authz.Permission = "sessions.manage_own"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	anywhere := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock} // any membership, whatever its scope
	return []authz.PermissionDef{
		{Name: PermMeRead, Description: "Read one's own account, organizations and permissions", Roles: authz.EveryRole(), Scopes: anywhere},
		{Name: PermSessionsManageOwn, Description: "List and sign out one's own sessions", Roles: authz.EveryRole(), Scopes: anywhere},
	}
}
