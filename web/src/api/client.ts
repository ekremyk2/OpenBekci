import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from './schema'
import { appErrorFromResponse } from './errors'
import { emitApiEvent } from './events'
import { idempotentOperations } from './idempotent.gen'

/** Header callers set (via `withVersion`) to have the middleware translate it into `If-Match`. */
export const VERSION_HEADER = 'X-Entity-Version'

/** Request options that send `If-Match: "<version>"` from an entity's `version` field. */
export function withVersion(version: number | string) {
  return { headers: { [VERSION_HEADER]: String(version) } }
}

export interface MiddlewareDeps {
  getLocale: () => string
  idempotent: ReadonlySet<string>
  newKey: () => string
}

/** 401s on these paths are expected (bad credentials, "am I signed in?") and handled in place. */
const NO_REDIRECT_PREFIXES = ['/v1/auth/', '/v1/me']

export function createMiddleware(deps: MiddlewareDeps): Middleware {
  return {
    onRequest({ request, schemaPath }) {
      const headers = request.headers
      headers.set('Accept-Language', deps.getLocale())
      headers.set('X-Requested-With', 'openbekci-web') // CSRF marker (backend §6)

      const method = request.method.toUpperCase()
      if (method === 'POST' && idempotentOperations_has(deps.idempotent, method, schemaPath)) {
        if (!headers.has('Idempotency-Key')) headers.set('Idempotency-Key', deps.newKey())
      }

      const version = headers.get(VERSION_HEADER)
      if (version !== null) {
        headers.delete(VERSION_HEADER)
        headers.set('If-Match', `"${version}"`)
      }
      return request
    },

    async onResponse({ response, schemaPath }) {
      if (response.ok) return undefined

      let body: unknown = undefined
      try {
        body = await response.clone().json()
      } catch {
        /* non-JSON error body */
      }
      const error = appErrorFromResponse(response, body)

      if (error.isUnauthorized && !NO_REDIRECT_PREFIXES.some((p) => schemaPath.startsWith(p))) {
        emitApiEvent({ type: 'unauthorized', error })
      } else if (error.isReauthRequired) {
        emitApiEvent({ type: 'reauth-required', error })
      } else if (error.isVersionConflict) {
        emitApiEvent({ type: 'version-conflict', error })
      } else if (error.isRateLimited) {
        emitApiEvent({ type: 'rate-limited', error })
      }
      // 403 insufficient_permission → inline in the calling component; 422 → fieldErrors on the error
      throw error
    },
  }
}

function idempotentOperations_has(set: ReadonlySet<string>, method: string, path: string) {
  return set.has(`${method} ${path}`)
}

let localeProvider: () => string = () => 'en'
/** Wired by app/i18n bootstrap so the client never imports UI state. */
export function setLocaleProvider(fn: () => string) {
  localeProvider = fn
}

export function createApiClient(baseUrl = globalThis.location?.origin ?? '') {
  const client = createClient<paths>({
    baseUrl,
    credentials: 'include',
    // resolved per call so test doubles (MSW) installed after import are honoured
    fetch: (request: Request) => globalThis.fetch(request),
  })
  client.use(
    createMiddleware({
      getLocale: () => localeProvider(),
      idempotent: idempotentOperations,
      newKey: () => crypto.randomUUID(),
    }),
  )
  return client
}

export const api = createApiClient()
