import { afterEach, describe, expect, it, vi } from 'vitest'
import createClient from 'openapi-fetch'
import type { paths } from './schema'
import { createMiddleware, isUnauthorizedExempt, withVersion } from './client'
import { AppError } from './errors'
import { onApiEvent, type ApiEvent } from './events'

function setup(
  fetchImpl: (req: Request) => Response | Promise<Response>,
  idempotent: string[] = [],
) {
  const client = createClient<paths>({
    baseUrl: 'http://localhost',
    fetch: async (req: Request) => fetchImpl(req),
  })
  client.use(createMiddleware({ getLocale: () => 'tr', idempotent: new Set(idempotent) }))
  return client
}

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/problem+json', ...headers },
  })

const events: ApiEvent[] = []
const off = onApiEvent((e) => events.push(e))
afterEach(() => {
  events.length = 0
})
afterAll(off)

describe('api client middleware', () => {
  it('adds Accept-Language and the CSRF marker header', async () => {
    const seen: Request[] = []
    const client = setup((req) => {
      seen.push(req)
      return new Response(JSON.stringify({}), { headers: { 'Content-Type': 'application/json' } })
    })
    await client.GET('/v1/me')
    expect(seen[0]?.headers.get('Accept-Language')).toBe('tr')
    expect(seen[0]?.headers.get('X-Requested-With')).toBe('openbekci-web')
  })

  it('passes a caller-supplied Idempotency-Key through and never mints one', async () => {
    const seen: Request[] = []
    const client = setup(
      (req) => {
        seen.push(req)
        return new Response(null, { status: 200 })
      },
      ['POST /v1/auth/session'],
    )
    const body = { method: 'password', identifier: 'a', password: 'b' } as const
    const headers = { 'Idempotency-Key': 'form-key' }
    await client.POST('/v1/auth/session', { body, headers })
    await client.POST('/v1/auth/session', { body, headers }) // retry: same key
    expect(seen.map((r) => r.headers.get('Idempotency-Key'))).toEqual(['form-key', 'form-key'])

    // an idempotent operation without a key is a programming error, not something to paper over
    await expect(client.POST('/v1/auth/session', { body })).rejects.toThrow(/Idempotency-Key/)
    expect(seen).toHaveLength(2)

    const other = setup((req) => {
      seen.push(req)
      return new Response(null, { status: 200 })
    })
    await other.POST('/v1/auth/session', { body })
    expect(seen[2]?.headers.has('Idempotency-Key')).toBe(false)
  })

  it('turns the entity version into If-Match', async () => {
    const seen: Request[] = []
    const client = setup((req) => {
      seen.push(req)
      return new Response(null, { status: 204 })
    })
    await client.DELETE('/v1/auth/session', withVersion(7))
    expect(seen[0]?.headers.get('If-Match')).toBe('"7"')
    expect(seen[0]?.headers.has('X-Entity-Version')).toBe(false)
  })

  it('throws AppError with field errors on 422', async () => {
    const client = setup(() =>
      json(422, { code: 'validation_failed', errors: [{ field: 'identifier', code: 'required' }] }),
    )
    const err = await client
      .POST('/v1/auth/session', { body: { method: 'password', identifier: '' } })
      .catch((e: unknown) => e)
    expect(err).toBeInstanceOf(AppError)
    expect((err as AppError).fieldErrors.identifier?.[0]?.code).toBe('required')
  })

  it('emits unauthorized for 401 on ordinary endpoints but not on /v1/me or /v1/auth', async () => {
    const client = setup(() => json(401, { code: 'invalid_grant' }))
    await client.GET('/v1/me').catch(() => {})
    await client
      .POST('/v1/auth/session', { body: { method: 'password', identifier: 'x', password: 'y' } })
      .catch(() => {})
    expect(events).toHaveLength(0)
  })

  it('emits unauthorized for 401 on /v1/me/* sub-resources (exemption is exact for /v1/me)', async () => {
    expect(isUnauthorizedExempt('/v1/me')).toBe(true)
    expect(isUnauthorizedExempt('/v1/auth/session')).toBe(true)
    expect(isUnauthorizedExempt('/v1/me/sessions')).toBe(false)
    expect(isUnauthorizedExempt('/v1/me/sessions/{id}')).toBe(false)
    expect(isUnauthorizedExempt('/v1/me/devices')).toBe(false)
    expect(isUnauthorizedExempt('/v1/authx')).toBe(false)

    const mw = createMiddleware({ getLocale: () => 'en', idempotent: new Set() })
    const call = (schemaPath: string) =>
      Promise.resolve(
        mw.onResponse!({
          response: json(401, { code: 'invalid_grant' }),
          schemaPath,
        } as never),
      ).catch(() => {})
    await call('/v1/me')
    expect(events).toHaveLength(0)
    await call('/v1/me/sessions')
    expect(events.map((e) => e.type)).toEqual(['unauthorized'])
  })

  it('emits version-conflict (412) and rate-limited (429) events', async () => {
    const spy = vi.fn()
    const stop = onApiEvent(spy)
    await setup(() => json(412, { code: 'version_conflict' }))
      .DELETE('/v1/auth/session')
      .catch(() => {})
    await setup(() => json(429, { code: 'rate_limited' }, { 'Retry-After': '5' }))
      .DELETE('/v1/auth/session')
      .catch(() => {})
    stop()
    expect(spy.mock.calls.map((c) => (c[0] as ApiEvent).type)).toEqual([
      'version-conflict',
      'rate-limited',
    ])
  })
})
