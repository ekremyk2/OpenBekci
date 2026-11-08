import { useCallback, useRef, useState, useSyncExternalStore } from 'react'
import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryKey,
  type UseQueryOptions,
} from '@tanstack/react-query'
import { useParams } from '@tanstack/react-router'
import type { Client } from 'openapi-fetch'
import { api } from '@/api/client'
import type { components, paths } from '@/api/draft'
import { useSession } from '@/app/session'

/**
 * Governance (0.2), community and access (0.3) are built against the DRAFT contract
 * (api/openapi-draft.yaml). The shared client is typed with the served schema, so it is re-typed
 * here; the runtime object is the same one, with the same middleware (Idempotency-Key check,
 * problem handling).
 *
 * This kit is shared by the meetings, bookings, visitors, gate and access features.
 */
export const gApi = api as unknown as Client<paths>
export type S = components['schemas']

export const idemHeader = (key: string) => ({ 'Idempotency-Key': key })
export const ifMatch = (version: number) => ({ 'If-Match': `"${version}"` })
export const GUARD_HEADER = 'X-Guard-Session'

/** Where these screens are: organization, the site being worked on and its formatting facts. */
export interface SiteCtx {
  orgId: string
  siteId: string
  siteName: string
  currency: string
  timeZone: string
  sites: { id: string; name: string }[]
  setSiteId: (id: string) => void
}

const SITE_KEY = 'ob.site'
const siteListeners = new Set<() => void>()
function storedSite(orgId: string): string | null {
  try {
    return localStorage.getItem(`${SITE_KEY}.${orgId}`)
  } catch {
    return null
  }
}

/** Resolves the active site of the current organization (remembered per organization). */
export function useSiteCtx(): SiteCtx | null {
  const { org } = useSession()
  const params = useParams({ strict: false }) as { orgId?: string }
  const orgId = org?.id ?? params.orgId ?? ''
  const remembered = useSyncExternalStore(
    (cb) => {
      siteListeners.add(cb)
      return () => siteListeners.delete(cb)
    },
    () => storedSite(orgId),
    () => null,
  )
  const sites = org?.sites ?? []
  const site = sites.find((s) => s.id === remembered) ?? sites[0]
  const setSiteId = useCallback(
    (id: string) => {
      try {
        localStorage.setItem(`${SITE_KEY}.${orgId}`, id)
      } catch {
        /* ignore */
      }
      for (const l of siteListeners) l()
    },
    [orgId],
  )
  if (!org || !site) return null
  return {
    orgId,
    siteId: site.id,
    siteName: site.name,
    currency: site.currency,
    timeZone: site.timezone,
    sites: sites.map((s) => ({ id: s.id, name: s.name })),
    setSiteId,
  }
}

/** Query keys start with ['ob', orgId] so one invalidation refreshes everything in the module. */
export const okey = (orgId: string, ...rest: unknown[]): QueryKey => ['ob', orgId, ...rest]

export function useOrgQuery<T>(
  key: unknown[],
  fn: () => Promise<T>,
  options: Partial<Omit<UseQueryOptions<T>, 'queryKey' | 'queryFn'>> = {},
) {
  const { orgId } = useParams({ strict: false }) as { orgId?: string }
  return useQuery<T>({ queryKey: okey(orgId ?? '', ...key), queryFn: fn, staleTime: 0, ...options })
}

/** openapi-fetch returns `{ data }`; the middleware already threw on every non-2xx. */
export async function unwrap<T>(p: Promise<{ data?: T }>): Promise<Exclude<T, string>> {
  const r = await p
  if (r.data === undefined) throw new Error('Empty response')
  return r.data as Exclude<T, string>
}

/** Mutation that refreshes this module's queries when it succeeds. */
export function useOrgMutation<TVars, TData>(
  fn: (vars: TVars) => Promise<TData>,
  opts: { onSuccess?: (data: TData, vars: TVars) => void; invalidate?: boolean } = {},
) {
  const qc = useQueryClient()
  const { orgId } = useParams({ strict: false }) as { orgId?: string }
  return useMutation<TData, Error, TVars>({
    mutationFn: fn,
    onSuccess: async (data, vars) => {
      if (opts.invalidate !== false) await qc.invalidateQueries({ queryKey: ['ob', orgId ?? ''] })
      opts.onSuccess?.(data, vars)
    },
  })
}

/** One Idempotency-Key per row action, kept until it succeeds so a retry reuses it. */
export function useKeyStore() {
  const keys = useRef(new Map<string, string>())
  return {
    get(id: string) {
      let k = keys.current.get(id)
      if (!k) {
        k = crypto.randomUUID()
        keys.current.set(id, k)
      }
      return k
    },
    done(id: string) {
      keys.current.delete(id)
    },
  }
}

export function useToggle(initial = false) {
  const [on, setOn] = useState(initial)
  return { on, open: () => setOn(true), close: () => setOn(false), set: setOn }
}
