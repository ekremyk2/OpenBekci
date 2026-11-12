import type { QueryClient } from '@tanstack/react-query'
import { redirect } from '@tanstack/react-router'
import { defaultLanding, meQueryOptions } from '@/app/session'

const JOIN_KEY = 'ob.joinCode'

/** The site code a resident signed up with, kept so an emailed sign-in link can lead back to the claim. */
export function rememberJoin(code: string | null) {
  try {
    if (code) localStorage.setItem(JOIN_KEY, code)
    else localStorage.removeItem(JOIN_KEY)
  } catch {
    /* ignore */
  }
}
function pendingJoin(): string | null {
  try {
    return localStorage.getItem(JOIN_KEY)
  } catch {
    return null
  }
}

/** After a session cookie was set: load /v1/me, then go to the claim in progress or the default landing. */
export async function landAfterSignIn(queryClient: QueryClient) {
  const me = await queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 })
  const code = me.organizations.length === 0 ? pendingJoin() : null
  return code ? ({ to: '/join/$code', params: { code } } as const) : defaultLanding(me)
}

/** For `beforeLoad`: sign in done, leave the page. */
export async function redirectAfterSignIn(queryClient: QueryClient): Promise<never> {
  throw redirect({ ...(await landAfterSignIn(queryClient)), replace: true })
}
