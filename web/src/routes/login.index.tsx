import { createFileRoute, redirect } from '@tanstack/react-router'
import { z } from 'zod'
import { safeNext } from '@/app/guards'
import { defaultLanding, loadSession } from '@/app/session'
import { LoginPage } from '@/features/auth/LoginPage'

export const Route = createFileRoute('/login/')({
  validateSearch: z.object({
    next: z.string().optional().catch(undefined),
    method: z.enum(['password', 'sms_otp', 'magic_link']).optional().catch(undefined),
  }),
  beforeLoad: async ({ context, search }) => {
    const me = await loadSession(context.queryClient)
    if (!me) return
    const next = safeNext(search.next)
    if (next) throw redirect({ href: next, replace: true })
    throw redirect({ ...defaultLanding(me), replace: true })
  },
  component: function Login() {
    const { next, method } = Route.useSearch()
    return <LoginPage next={next} method={method} />
  },
})
