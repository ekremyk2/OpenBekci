import { describe, expect, it } from 'vitest'
import { AppError, appErrorFromResponse } from './errors'

function res(status: number, headers: Record<string, string> = {}) {
  return new Response(null, { status, headers })
}

describe('appErrorFromResponse', () => {
  it('maps problem+json code, detail and request id', () => {
    const e = appErrorFromResponse(res(403), {
      status: 403,
      code: 'insufficient_permission',
      detail: 'nope',
      request_id: 'req-1',
    })
    expect(e).toBeInstanceOf(AppError)
    expect(e.code).toBe('insufficient_permission')
    expect(e.status).toBe(403)
    expect(e.message).toBe('nope')
    expect(e.requestId).toBe('req-1')
    expect(e.isForbidden).toBe(true)
    expect(e.isReauthRequired).toBe(false)
  })

  it('flags reauth_required separately from plain 403', () => {
    const e = appErrorFromResponse(res(403), { code: 'reauth_required' })
    expect(e.isReauthRequired).toBe(true)
  })

  it('groups 422 errors by field', () => {
    const e = appErrorFromResponse(res(422), {
      code: 'validation_failed',
      errors: [
        { field: 'amount', code: 'too_small', message: 'Too small' },
        { field: 'amount', code: 'invalid' },
        { field: 'unit_id', code: 'required' },
      ],
    })
    expect(e.isValidation).toBe(true)
    expect(e.fieldErrors.amount).toEqual([
      { code: 'too_small', message: 'Too small' },
      { code: 'invalid', message: undefined },
    ])
    expect(e.fieldErrors.unit_id?.[0]?.code).toBe('required')
  })

  it('treats 412 as a version conflict', () => {
    const e = appErrorFromResponse(res(412), { code: 'version_conflict' })
    expect(e.isVersionConflict).toBe(true)
  })

  it('reads Retry-After seconds on 429', () => {
    const e = appErrorFromResponse(res(429, { 'Retry-After': '17' }), { code: 'rate_limited' })
    expect(e.isRateLimited).toBe(true)
    expect(e.retryAfter).toBe(17)
  })

  it('falls back to a code derived from the status when the body is not problem+json', () => {
    expect(appErrorFromResponse(res(404), undefined).code).toBe('not_found')
    expect(appErrorFromResponse(res(401), 'oops').code).toBe('invalid_grant')
    expect(appErrorFromResponse(res(502), null).code).toBe('internal_error')
  })
})
