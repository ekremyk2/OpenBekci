import { createFileRoute, redirect } from '@tanstack/react-router'
import { defaultLanding, loadSession } from '@/app/session'

// `/` decides where to go: login, the remembered/only organization, or the picker.
export const Route = createFileRoute('/')({
  beforeLoad: async ({ context }) => {
    const me = await loadSession(context.queryClient)
    if (!me) throw redirect({ to: '/login', search: {}, replace: true })
    throw redirect({ ...defaultLanding(me), replace: true })
  },
})
