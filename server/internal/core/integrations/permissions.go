// Package integrations holds organization API keys and signed outbound
// webhooks (backend plan sections 6, 7, 11 and 16, release 0.4).
//
// An API key authenticates as a principal whose rights are the intersection of
// the key's scopes and the grants of the administrator who created it, at call
// time. Webhooks ride the River outbox: a domain action that is audited emits a
// core event in the same transaction, which fans out into one delivery job per
// (event, endpoint); a delivery is an HMAC-signed JSON POST that carries ids
// only. See docs/integrations.md and docs/adr/0002-webhook-signing.md.
package integrations

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module. Only organization administrators hold them, at
// organization scope: a key or an endpoint belongs to the organization, not to
// a site.
const (
	// PermAPIKeysRead lists API keys (prefix and metadata, never a secret).
	PermAPIKeysRead authz.Permission = "apikeys.read"
	// PermAPIKeysManage creates, rotates and revokes API keys (re-authentication required).
	PermAPIKeysManage authz.Permission = "apikeys.manage"
	// PermWebhooksRead reads webhook endpoints and their delivery log.
	PermWebhooksRead authz.Permission = "webhooks.read"
	// PermWebhooksManage creates, changes, deletes and tests webhook endpoints.
	PermWebhooksManage authz.Permission = "webhooks.manage"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	admin := []authz.Role{authz.RoleOrgAdmin}
	org := []authz.ScopeType{authz.ScopeOrg}
	return []authz.PermissionDef{
		{Name: PermAPIKeysRead, Description: "List the organization's API keys", Roles: admin, Scopes: org},
		{Name: PermAPIKeysManage, Description: "Create, rotate and revoke API keys (re-authentication required)", Roles: admin, Scopes: org},
		{Name: PermWebhooksRead, Description: "Read webhook endpoints and their delivery log", Roles: admin, Scopes: org},
		{Name: PermWebhooksManage, Description: "Create, change, delete, test and rotate the secret of webhook endpoints (never through an API key)", Roles: admin, Scopes: org},
	}
}
