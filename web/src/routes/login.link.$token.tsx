import { createFileRoute, redirect } from '@tanstack/react-router'
import { z } from 'zod'
import { draftApi } from '@/api/draft'
import { orgNext } from '@/app/guards'
import { clearSession, defaultLanding, meQueryOptions } from '@/app/session'
import { WebLoginLinkFailed } from '@/features/auth/WebLoginLinkFailed'

/**
 * Web-login link (backend §8, mobile §7.2): the mobile app opens `/login/link/{token}?next=/orgs/{id}/…`.
 * The token is single use, so it is exchanged exactly once, in `beforeLoad` (a route-level effect a
 * React re-render or StrictMode cannot repeat), for the cookie session; then we go to `next` when it
 * is a path inside `/orgs/$orgId/…`, else to the default landing page.
 */
export const Route = createFileRoute('/login/link/$token')({
  validateSearch: z.object({ next: z.string().optional().catch(undefined) }),
  beforeLoad: async ({ context, params, search }) => {
    // whatever was cached belongs to a previous user
    clearSession(context.queryClient)
    try {
      await draftApi.POST('/v1/auth/web-login-link:exchange', { body: { token: params.token } })
    } catch {
      return { failed: true }
    }
    const me = await context.queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 })
    const target = orgNext(search.next)
    if (target) throw redirect({ href: target, replace: true })
    throw redirect({ ...defaultLanding(me), replace: true })
  },
  component: WebLoginLinkFailed,
})
