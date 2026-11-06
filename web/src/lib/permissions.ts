import type { Me, MeOrganization } from '@/api/schema'

export interface PermissionScope {
  siteId?: string
  blockId?: string
}

export function findOrg(me: Me | undefined, orgId: string | undefined): MeOrganization | undefined {
  return me?.organizations.find((o) => o.id === orgId)
}

/**
 * Pure permission check against the effective permissions in `GET /v1/me` (web plan §8).
 *  - A grant without site_ids/block_ids is organization-wide.
 *  - With a scope, a scoped grant matches only when it lists that site/block.
 *  - Without a scope, a scoped grant counts ("may do this somewhere") so navigation can show the area.
 * Exact match only: no wildcard, deny by default. The server enforces; this only decides what to offer.
 */
export function can(
  me: Me | undefined,
  orgId: string | undefined,
  permission: string,
  scope?: PermissionScope,
): boolean {
  const org = findOrg(me, orgId)
  if (!org) return false
  return org.permissions.some((g) => {
    if (g.permission !== permission) return false
    const scoped = (g.site_ids?.length ?? 0) > 0 || (g.block_ids?.length ?? 0) > 0
    if (!scoped || !scope) return true
    if (scope.siteId && g.site_ids?.includes(scope.siteId)) return true
    if (scope.blockId && g.block_ids?.includes(scope.blockId)) return true
    return false
  })
}
