import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { draftApi } from '@/api/draft'
import { clearSession } from '@/app/session'
import { redirectAfterSignIn } from '@/features/auth/finish'
import { WebLoginLinkFailed } from '@/features/auth/WebLoginLinkFailed'

/**
 * Magic-link landing (backend §6). The token is single use, so like `/login/link/$token` it is
 * exchanged exactly once, in `beforeLoad`, for the cookie session.
 */
export const Route = createFileRoute('/login/magic')({
  validateSearch: z.object({ token: z.string().optional().catch(undefined) }),
  beforeLoad: async ({ context, search }) => {
    if (!search.token) return
    clearSession(context.queryClient)
    try {
      await draftApi.POST('/v1/auth/magic-link/consume', {
        body: { token: search.token, client: 'web' },
      })
    } catch {
      return
    }
    return redirectAfterSignIn(context.queryClient)
  },
  component: () => <WebLoginLinkFailed magic />,
})
