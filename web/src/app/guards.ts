import { redirect } from '@tanstack/react-router'
import type { QueryClient } from '@tanstack/react-query'
import type { Me, MeOrganization } from '@/api/schema'
import { findOrg } from '@/lib/permissions'
import { loadSession, rememberOrg } from './session'

export interface RouterContext {
  queryClient: QueryClient
}

/** Thrown from `beforeLoad`/loaders when a signed-in user may not see a route (renders the 403 page). */
export class ForbiddenError extends Error {
  constructor(message = 'forbidden') {
    super(message)
    this.name = 'ForbiddenError'
  }
}

/**
 * Only same-origin absolute paths may be used as a post-login destination (open-redirect guard).
 * Rejects absolute URLs, protocol-relative `//host`, backslashes, and control characters (a tab or
 * newline inside `/<TAB>/host` is stripped by browsers and turns it into `//host`).
 */
export function safeNext(next: string | undefined | null): string | undefined {
  if (!next || !next.startsWith('/') || next.startsWith('//')) return undefined
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\\]/.test(next)) return undefined
  if (next === '/login' || next.startsWith('/login/') || next.startsWith('/login?'))
    return undefined
  try {
    if (new URL(next, 'http://localhost').origin !== 'http://localhost') return undefined
  } catch {
    return undefined
  }
  return next
}

/** Guard: needs a session, else → `/login?next=<where you were going>`. */
export async function requireSession(ctx: RouterContext, location: { href: string }): Promise<Me> {
  const me = await loadSession(ctx.queryClient)
  if (!me) throw redirect({ to: '/login', search: { next: safeNext(location.href) } })
  return me
}

/** Guard: needs a membership in `orgId`, else → `/select-org`. Remembers the org for next time. */
export async function requireOrg(
  ctx: RouterContext,
  location: { href: string },
  orgId: string,
): Promise<{ me: Me; org: MeOrganization }> {
  const me = await requireSession(ctx, location)
  const org = findOrg(me, orgId)
  if (!org) throw redirect({ to: '/select-org' })
  rememberOrg(org.id)
  return { me, org }
}
