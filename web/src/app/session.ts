import { queryOptions, useQuery, type QueryClient } from '@tanstack/react-query'
import { useParams } from '@tanstack/react-router'
import { api } from '@/api/client'
import { AppError } from '@/api/errors'
import type { Me, MeOrganization } from '@/api/schema'
import { can as canPure, findOrg, type PermissionScope } from '@/lib/permissions'

export const meQueryOptions = queryOptions({
  queryKey: ['me'],
  queryFn: async (): Promise<Me> => {
    const { data } = await api.GET('/v1/me')
    if (!data) throw new Error('Empty /v1/me response')
    return data
  },
  retry: false,
  staleTime: 30_000,
})

/** The signed-in user's /v1/me document, or null when there is no session (401). */
export async function loadSession(queryClient: QueryClient): Promise<Me | null> {
  try {
    return await queryClient.ensureQueryData(meQueryOptions)
  } catch (e) {
    if (e instanceof AppError && e.status === 401) return null
    throw e
  }
}

export function clearSession(queryClient: QueryClient) {
  queryClient.removeQueries({ queryKey: meQueryOptions.queryKey })
}

const LAST_ORG_KEY = 'ob.lastOrg'
export function rememberOrg(orgId: string) {
  try {
    localStorage.setItem(LAST_ORG_KEY, orgId)
  } catch {
    /* ignore */
  }
}
export function lastOrgId(): string | null {
  try {
    return localStorage.getItem(LAST_ORG_KEY)
  } catch {
    return null
  }
}

/** Where a freshly signed-in user should land. */
export function defaultLanding(
  me: Me,
): { to: '/orgs/$orgId'; params: { orgId: string } } | { to: '/select-org' } | { to: '/platform' } {
  const orgs = me.organizations
  const remembered = orgs.find((o) => o.id === lastOrgId())
  const only = orgs.length === 1 ? orgs[0] : undefined
  const pick = remembered ?? only
  if (pick) return { to: '/orgs/$orgId', params: { orgId: pick.id } }
  if (orgs.length === 0 && me.user.platform_admin) return { to: '/platform' }
  return { to: '/select-org' }
}

export interface Session {
  me: Me | undefined
  isLoading: boolean
  organizations: MeOrganization[]
  /** Organization of the current route (`/orgs/$orgId/...`), if any. */
  org: MeOrganization | undefined
  /** `can(permission, scope?)` bound to the current route's organization. */
  can: (permission: string, scope?: PermissionScope) => boolean
}

export function useSession(): Session {
  const { data: me, isLoading } = useQuery(meQueryOptions)
  const params = useParams({ strict: false }) as { orgId?: string }
  const org = findOrg(me, params.orgId)
  return {
    me,
    isLoading,
    organizations: me?.organizations ?? [],
    org,
    can: (permission, scope) => canPure(me, params.orgId, permission, scope),
  }
}
