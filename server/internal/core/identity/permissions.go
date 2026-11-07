package identity

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Account permissions: held by every signed-in caller, in no organization. They
// are listed with ScopeInstance and every role, which makes the Resolver give
// them to every authenticated principal as instance-level grants
// (Resolver.InstanceGrants); the /v1/me and /v1/me/sessions operations check
// them with authz.Require(ctx, perm, authz.Scope{}). A caller without any
// membership holds them; an anonymous one does not (401).
const (
	PermMeRead            authz.Permission = "me.read"
	PermSessionsManageOwn authz.Permission = "sessions.manage_own"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	// the instance grant is what carries the permission; the membership scopes keep it listed per organization in /v1/me
	anywhere := []authz.ScopeType{authz.ScopeInstance, authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	return []authz.PermissionDef{
		{Name: PermMeRead, Description: "Read one's own account, organizations and permissions", Roles: authz.EveryRole(), Scopes: anywhere},
		{Name: PermSessionsManageOwn, Description: "List and sign out one's own sessions", Roles: authz.EveryRole(), Scopes: anywhere},
	}
}
