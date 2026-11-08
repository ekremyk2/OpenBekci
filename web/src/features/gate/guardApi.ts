import { useSyncExternalStore } from 'react'
import createClient, { type Middleware } from 'openapi-fetch'
import { i18n } from '@lingui/core'
import type { paths } from '@/api/draft'
import { createMiddleware } from '@/api/client'
import { appErrorFromResponse } from '@/api/errors'
import { idempotentOperations } from '@/api/idempotent.gen'

/**
 * The guard console is a shared booth device: the login belongs to the booth, each action belongs
 * to the guard who entered their PIN (`POST /gate/sessions`). Every guard call carries
 * `X-Guard-Session` through this request wrapper, so no screen can forget it.
 */
export const GUARD_HEADER = 'X-Guard-Session'

export interface GuardSessionState {
  token: string
  personId: string
  displayName: string
  expiresAt: string
}

const KEY = 'ob.guard'
let current: GuardSessionState | null = readStored()
const listeners = new Set<() => void>()

function readStored(): GuardSessionState | null {
  try {
    const raw = sessionStorage.getItem(KEY)
    if (!raw) return null
    const s = JSON.parse(raw) as GuardSessionState
    return Date.parse(s.expiresAt) > Date.now() ? s : null
  } catch {
    return null
  }
}

function emit() {
  for (const l of listeners) l()
}

export function getGuardSession(): GuardSessionState | null {
  return current
}

export function setGuardSession(s: GuardSessionState | null) {
  current = s
  try {
    if (s) sessionStorage.setItem(KEY, JSON.stringify(s))
    else sessionStorage.removeItem(KEY)
  } catch {
    /* private mode: the session lives in memory only */
  }
  emit()
}

export function useGuardSession(): GuardSessionState | null {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => current,
    () => null,
  )
}

/** Operations that happen before a guard session exists must not carry a stale token. */
const NO_GUARD_HEADER = new Set([
  '/v1/orgs/{org_id}/gate/sessions',
  '/v1/orgs/{org_id}/gate/guards',
])

type RequestCtx = Parameters<NonNullable<Middleware['onRequest']>>[0]
type ResponseCtx = Parameters<NonNullable<Middleware['onResponse']>>[0]

/**
 * The shared middleware (Accept-Language, CSRF marker, idempotency enforcement, If-Match
 * translation, reauth and version-conflict events) plus the guard header. Only `invalid_grant` is
 * handled here: it is the answer for a wrong PIN and for an expired or ended guard session, so it
 * must never sign the whole booth out, only ask for a PIN again.
 */
export function createGuardMiddleware(
  getToken: () => string | null,
  onSessionLost: () => void,
): Middleware {
  const shared = createMiddleware({
    getLocale: () => i18n.locale || 'en',
    idempotent: idempotentOperations,
  })
  return {
    onRequest(ctx: RequestCtx) {
      const token = getToken()
      if (token && !NO_GUARD_HEADER.has(ctx.schemaPath))
        ctx.request.headers.set(GUARD_HEADER, token)
      return shared.onRequest!(ctx)
    },
    async onResponse(ctx: ResponseCtx) {
      if (ctx.response.ok) return undefined
      let body: unknown
      try {
        body = await ctx.response.clone().json()
      } catch {
        /* non-JSON error body */
      }
      const error = appErrorFromResponse(ctx.response, body)
      if (error.status === 401 && error.code === 'invalid_grant') {
        if (!NO_GUARD_HEADER.has(ctx.schemaPath)) onSessionLost()
        throw error
      }
      return shared.onResponse!(ctx)
    },
  }
}

export function createGuardApi(
  getToken: () => string | null = () => current?.token ?? null,
  onSessionLost: () => void = () => setGuardSession(null),
  baseUrl = globalThis.location?.origin ?? '',
) {
  const client = createClient<paths>({
    baseUrl,
    credentials: 'include',
    fetch: (request: Request) => globalThis.fetch(request),
  })
  client.use(createGuardMiddleware(getToken, onSessionLost))
  return client
}

/** The client every guard console call goes through. */
export const guardApi = createGuardApi()
