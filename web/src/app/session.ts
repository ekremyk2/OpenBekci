import { useSyncExternalStore } from 'react'
import { queryOptions, useQuery, type QueryClient } from '@tanstack/react-query'
import { useParams } from '@tanstack/react-router'
import { api } from '@/api/client'
import { AppError } from '@/api/errors'
import { emitApiEvent } from '@/api/events'
import type { Me, MeOrganization } from '@/api/schema'
import { can as canPure, findOrg, type PermissionScope } from '@/lib/permissions'

export const meQueryOptions = queryOptions({
  queryKey: ['me'],
  queryFn: async ({ client }): Promise<Me> => {
    try {
      const { data } = await api.GET('/v1/me')
      if (!data) throw new Error('Empty /v1/me response')
      return data
    } catch (e) {
      // /v1/me is exempt from the client's automatic 401 handling (loadSession treats it as "no
      // session"). A 401 on a *refetch* (window focus) means the session ended mid-use, so sign the
      // shell out instead of leaving stale data on screen.
      if (e instanceof AppError && e.status === 401 && client.getQueryData(['me']) !== undefined) {
        emitApiEvent({ type: 'unauthorized', error: e })
      }
      throw e
    }
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

/** Forgets everything cached for the signed-in user (sign-out and 401): nothing may leak to the next user. */
export function clearSession(queryClient: QueryClient) {
  queryClient.clear()
}

const LAST_ORG_KEY = 'ob.lastOrg'
const orgListeners = new Set<() => void>()
export function rememberOrg(orgId: string) {
  try {
    localStorage.setItem(LAST_ORG_KEY, orgId)
  } catch {
    /* ignore */
  }
  for (const l of orgListeners) l()
}
/** The organization the user last opened (the "selected" one outside of an /orgs/$orgId route). */
export function useLastOrgId(): string | null {
  return useSyncExternalStore(
    (cb) => {
      orgListeners.add(cb)
      return () => orgListeners.delete(cb)
    },
    lastOrgId,
    () => null,
  )
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
  if (orgs.length === 0 && me.user.is_platform_admin) return { to: '/platform' }
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
