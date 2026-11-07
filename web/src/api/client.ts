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
  /** "METHOD /path/template" keys of operations tagged `x-idempotent` in the OpenAPI spec. */
  idempotent: ReadonlySet<string>
}

/**
 * 401s on exactly `/v1/me` ("am I signed in?") and under `/v1/auth/` (bad credentials) are expected
 * and handled in place. Everything else, including `/v1/me/sessions` and `/v1/me/devices`, means the
 * session ended and triggers the sign-out redirect.
 */
export function isUnauthorizedExempt(schemaPath: string): boolean {
  return schemaPath === '/v1/me' || schemaPath.startsWith('/v1/auth/')
}

export function createMiddleware(deps: MiddlewareDeps): Middleware {
  return {
    onRequest({ request, schemaPath }) {
      const headers = request.headers
      headers.set('Accept-Language', deps.getLocale())
      headers.set('X-Requested-With', 'openbekci-web') // CSRF marker (backend §6)

      // The key belongs to one form submission (see useIdempotencyKey), never to one request:
      // minting here would give a retry or a double click a new key, which is false safety.
      if (deps.idempotent.has(`${request.method.toUpperCase()} ${schemaPath}`)) {
        if (!headers.has('Idempotency-Key')) {
          throw new Error(
            `${request.method} ${schemaPath} is idempotent and needs an Idempotency-Key from useIdempotencyKey()`,
          )
        }
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

      if (error.isUnauthorized && !isUnauthorizedExempt(schemaPath)) {
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

let localeProvider: () => string = () => 'en'
/** Wired by app/i18n bootstrap so the client never imports UI state. */
export function setLocaleProvider(fn: () => string) {
  localeProvider = fn
}
export function getLocaleProvider(): () => string {
  return localeProvider
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
    }),
  )
  return client
}

export const api = createApiClient()
