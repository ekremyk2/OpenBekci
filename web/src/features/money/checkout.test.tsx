import { describe, expect, it } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { server } from '@/test/setup'
import { isFinalCheckout, pollDelay, usePollCheckout } from './checkout'

const checkout = (status: string, extra: Record<string, unknown> = {}) => ({
  id: 'co-1',
  org_id: 'org-1',
  site_id: 'site-a',
  unit_id: 'u-1',
  debtor_kind: 'owner',
  charge_ids: [],
  provider: 'iyzico',
  merchant_of_record: 'site',
  amount: { amount: 10_000, currency: 'TRY' },
  status,
  checkout_url: 'https://pay.example.test/x',
  return_url: 'http://localhost/orgs/org-1/pay/return?checkout_id=co-1',
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  payment_id: null,
  failure_code: null,
  created_by: 'u-1',
  created_at: new Date().toISOString(),
  completed_at: null,
  ...extra,
})

function makeWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

const fast = { delay: () => 5 }

describe('pollDelay', () => {
  it('starts quick, backs off and settles at ten seconds', () => {
    const seq = [0, 1, 2, 3, 4, 5, 6, 50].map(pollDelay)
    expect(seq).toEqual([1000, 2000, 3000, 5000, 8000, 10_000, 10_000, 10_000])
    expect(seq.every((d, i) => i === 0 || d >= seq[i - 1]!)).toBe(true)
  })

  it('treats anything but pending as final', () => {
    expect(isFinalCheckout('pending')).toBe(false)
    for (const s of ['paid', 'failed', 'expired', 'refunded', 'something_new'])
      expect(isFinalCheckout(s)).toBe(true)
  })
})

describe('usePollCheckout', () => {
  it('polls until the status is final and then stops', async () => {
    let calls = 0
    server.use(
      http.get('*/v1/orgs/:orgId/checkouts/:id', () => {
        calls += 1
        return HttpResponse.json(
          calls < 4 ? checkout('pending') : checkout('paid', { payment_id: 'pay-1' }),
        )
      }),
    )
    const { result } = renderHook(() => usePollCheckout('org-1', 'co-1', fast), {
      wrapper: makeWrapper(),
    })
    await waitFor(() => expect(result.current.checkout?.status).toBe('paid'))
    expect(calls).toBe(4)
    expect(result.current.stalled).toBe(false)
    // nothing more is asked once the answer is final
    await new Promise((r) => setTimeout(r, 60))
    expect(calls).toBe(4)
  })

  it('stops on failed and expired as well', async () => {
    for (const status of ['failed', 'expired']) {
      let calls = 0
      server.use(
        http.get('*/v1/orgs/:orgId/checkouts/:id', () => {
          calls += 1
          return HttpResponse.json(checkout(calls < 2 ? 'pending' : status))
        }),
      )
      const { result, unmount } = renderHook(() => usePollCheckout('org-1', 'co-1', fast), {
        wrapper: makeWrapper(),
      })
      await waitFor(() => expect(result.current.checkout?.status).toBe(status))
      await new Promise((r) => setTimeout(r, 40))
      expect(calls).toBe(2)
      unmount()
    }
  })

  it('stalls once past expiry while the server still says pending, and rechecks on demand', async () => {
    let calls = 0
    server.use(
      http.get('*/v1/orgs/:orgId/checkouts/:id', () => {
        calls += 1
        return HttpResponse.json(
          checkout('pending', { expires_at: new Date(Date.now() - 60_000).toISOString() }),
        )
      }),
    )
    const { result } = renderHook(() => usePollCheckout('org-1', 'co-1', fast), {
      wrapper: makeWrapper(),
    })
    await waitFor(() => expect(result.current.stalled).toBe(true))
    await new Promise((r) => setTimeout(r, 60))
    expect(calls).toBe(1) // the poll stopped by itself
    result.current.recheck()
    await waitFor(() => expect(calls).toBe(2))
  })

  it('reports an unknown checkout as an error', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/checkouts/:id', () =>
        HttpResponse.json({ status: 404, code: 'not_found' }, { status: 404 }),
      ),
    )
    const { result } = renderHook(() => usePollCheckout('org-1', 'nope', fast), {
      wrapper: makeWrapper(),
    })
    await waitFor(() => expect(result.current.error).not.toBeNull())
    expect(result.current.checkout).toBeUndefined()
  })

  it('does not ask without a checkout id', async () => {
    let calls = 0
    server.use(
      http.get('*/v1/orgs/:orgId/checkouts/:id', () => {
        calls += 1
        return HttpResponse.json(checkout('paid'))
      }),
    )
    renderHook(() => usePollCheckout('org-1', '', fast), { wrapper: makeWrapper() })
    await new Promise((r) => setTimeout(r, 30))
    expect(calls).toBe(0)
  })
})
