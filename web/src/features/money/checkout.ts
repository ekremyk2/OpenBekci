import { useQuery } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'
import { moneyApi, unwrap, type MoneyCtx, type S } from './api'

/** A checkout is final once it is no longer `pending` (unknown future statuses count as final). */
export const isFinalCheckout = (status: string) => status !== 'pending'

/**
 * Delay before poll number `attempt` (0-based), in milliseconds: the provider usually confirms
 * within seconds, so the first looks are quick and later ones back off to one every ten seconds.
 */
export function pollDelay(attempt: number): number {
  const steps = [1_000, 2_000, 3_000, 5_000, 8_000]
  return steps[attempt] ?? 10_000
}

export interface CheckoutPoll {
  checkout: S['Checkout'] | undefined
  isPending: boolean
  error: Error | null
  /** The checkout is past `expires_at` and the server still says pending: polling has stopped. */
  stalled: boolean
  /** Ask once more (the "Check again" button). */
  recheck: () => void
}

/**
 * Polls `money.getCheckout` with backoff until the status is final. A pending checkout past its
 * `expires_at` is asked once more after that moment (the server expires it on the spot); if it
 * still answers pending the poll stops and `stalled` is set, so the screen offers "Check again".
 */
export function usePollCheckout(
  orgId: string,
  checkoutId: string,
  opts: { delay?: (attempt: number) => number } = {},
): CheckoutPoll {
  const delay = opts.delay ?? pollDelay
  const q = useQuery({
    queryKey: ['money', orgId, 'checkout', checkoutId],
    enabled: checkoutId !== '',
    staleTime: 0,
    gcTime: 0,
    retry: 1,
    queryFn: () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/checkouts/{checkout_id}', {
          params: { path: { org_id: orgId, checkout_id: checkoutId } },
        }),
      ),
    refetchInterval: (query) => {
      const c = query.state.data
      if (!c || isFinalCheckout(c.status)) return false
      if (Date.parse(c.expires_at) < query.state.dataUpdatedAt) return false
      return delay(Math.max(0, query.state.dataUpdateCount - 1))
    },
  })
  const c = q.data
  return {
    checkout: c,
    isPending: q.isPending,
    error: q.error,
    stalled: !!c && !isFinalCheckout(c.status) && Date.parse(c.expires_at) < q.dataUpdatedAt,
    recheck: () => void q.refetch(),
  }
}

/** The card-payment facts of a site, from the site settings every member can read. */
export function useSiteCard(ctx: MoneyCtx) {
  return useQuery({
    queryKey: ['tenancy.getSiteSettings', ctx.orgId, ctx.siteId, 'payments'],
    staleTime: 30_000,
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/settings', {
        params: { path: { org_id: ctx.orgId, site_id: ctx.siteId } },
      })
      return data?.payments ?? null
    },
  })
}
