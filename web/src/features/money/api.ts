import { useCallback, useState, useSyncExternalStore } from 'react'
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
 * The money module is built against the DRAFT contract (api/openapi-draft.yaml). The shared client
 * is typed with the served schema, so it is re-typed here; the runtime object is the same one, with
 * the same middleware (Idempotency-Key check, If-Match, problem handling).
 */
export const moneyApi = api as unknown as Client<paths>
export type S = components['schemas']
export type MoneyAmount = S['Money']
export type Side = 'owner' | 'tenant'

export const idemHeader = (key: string) => ({ 'Idempotency-Key': key })
export const ifMatch = (version: number) => ({ 'If-Match': `"${version}"` })

export const money = (amount: number | bigint, currency: string): MoneyAmount => ({
  amount: Number(amount),
  currency,
})

/** Where the money screens are: organization, the site being worked on, and its formatting facts. */
export interface MoneyCtx {
  orgId: string
  siteId: string
  siteName: string
  currency: string
  timeZone: string
  sites: { id: string; name: string }[]
  setSiteId: (id: string) => void
}

const SITE_KEY = 'ob.money.site'
const siteListeners = new Set<() => void>()
function storedSite(orgId: string): string | null {
  try {
    return localStorage.getItem(`${SITE_KEY}.${orgId}`)
  } catch {
    return null
  }
}

/** Resolves the active site of the current organization (remembered per organization). */
export function useMoneyCtx(): MoneyCtx | null {
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

/** Calendar date (YYYY-MM-DD) of "today" in the site's timezone. */
export function todayIn(timeZone: string, now = new Date()): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now)
}

export const monthOf = (isoDate: string) => isoDate.slice(0, 7)

/** First and last day of the month containing `isoDate`. */
export function monthRange(isoDate: string): { from: string; to: string } {
  const [y, m] = isoDate.split('-').map(Number) as [number, number]
  const last = new Date(Date.UTC(y, m, 0)).getUTCDate()
  const mm = String(m).padStart(2, '0')
  return { from: `${y}-${mm}-01`, to: `${y}-${mm}-${String(last).padStart(2, '0')}` }
}

export function startOfYear(isoDate: string) {
  return `${isoDate.slice(0, 4)}-01-01`
}

/** Query keys always start with ['money', orgId] so one invalidation refreshes everything money. */
export const moneyKey = (orgId: string, ...rest: unknown[]): QueryKey => ['money', orgId, ...rest]

export function useMoneyQuery<T>(
  key: unknown[],
  fn: () => Promise<T>,
  options: Partial<Omit<UseQueryOptions<T>, 'queryKey' | 'queryFn'>> = {},
) {
  const { orgId } = useParams({ strict: false }) as { orgId?: string }
  return useQuery<T>({
    queryKey: moneyKey(orgId ?? '', ...key),
    queryFn: fn,
    staleTime: 0, // money screens always show the ledger as it is now
    ...options,
  })
}

/** Reference data (accounts, funds, units, keys) changes rarely: five minutes. */
export const REFERENCE_STALE = 5 * 60_000

/** openapi-fetch returns `{ data }`; the middleware already threw on every non-2xx. */
export async function unwrap<T>(p: Promise<{ data?: T }>): Promise<Exclude<T, string>> {
  const r = await p
  if (r.data === undefined) throw new Error('Empty response')
  // report endpoints also serve csv/xlsx/pdf (typed as string); this client only asks for JSON
  return r.data as Exclude<T, string>
}

/** Mutation that refreshes everything money-related when it succeeds. */
export function useMoneyMutation<TVars, TData>(
  fn: (vars: TVars) => Promise<TData>,
  opts: { onSuccess?: (data: TData, vars: TVars) => void } = {},
) {
  const qc = useQueryClient()
  const { orgId } = useParams({ strict: false }) as { orgId?: string }
  return useMutation<TData, Error, TVars>({
    mutationFn: fn,
    onSuccess: async (data, vars) => {
      await qc.invalidateQueries({ queryKey: ['money', orgId ?? ''] })
      opts.onSuccess?.(data, vars)
    },
  })
}

export { downloadFile, exportUrl } from '@/api/download'

export type ExportFormat = 'csv' | 'xlsx' | 'pdf'

/** Small local state helper for dialogs. */
export function useToggle(initial = false) {
  const [on, setOn] = useState(initial)
  return { on, open: () => setOn(true), close: () => setOn(false), set: setOn }
}

export const minorMoney = (v: bigint | number, currency: string): MoneyAmount => ({
  amount: Number(v),
  currency,
})
