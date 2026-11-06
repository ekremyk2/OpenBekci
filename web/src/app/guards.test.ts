import { beforeEach, describe, expect, it } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { meFixture } from '@/test/handlers'
import { server } from '@/test/setup'
import { ForbiddenError, requireOrg, requireSession, safeNext } from './guards'

beforeEach(() => localStorage.clear())

describe('safeNext', () => {
  it('keeps same-origin paths, with query and hash', () => {
    expect(safeNext('/orgs/org-1/me')).toBe('/orgs/org-1/me')
    expect(safeNext('/orgs/org-1?tab=a#x')).toBe('/orgs/org-1?tab=a#x')
  })

  it('rejects missing values, absolute URLs and protocol-relative URLs', () => {
    expect(safeNext(undefined)).toBeUndefined()
    expect(safeNext(null)).toBeUndefined()
    expect(safeNext('')).toBeUndefined()
    expect(safeNext('orgs/org-1')).toBeUndefined()
    expect(safeNext('https://evil.example/')).toBeUndefined()
    expect(safeNext('javascript:alert(1)')).toBeUndefined()
    expect(safeNext('//evil.example')).toBeUndefined()
  })

  it('rejects backslashes and control characters browsers would normalise away', () => {
    expect(safeNext('/\\evil.example')).toBeUndefined()
    expect(safeNext('/a\\b')).toBeUndefined()
    expect(safeNext('/\t/evil.example')).toBeUndefined()
    expect(safeNext('/\n/evil.example')).toBeUndefined()
    expect(safeNext('/ok\r\nSet-Cookie: x')).toBeUndefined()
    expect(safeNext('/ok\u007f')).toBeUndefined()
  })

  it('rejects the login page itself (redirect loop) but not look-alikes', () => {
    expect(safeNext('/login')).toBeUndefined()
    expect(safeNext('/login?next=/x')).toBeUndefined()
    expect(safeNext('/login/magic')).toBeUndefined()
    expect(safeNext('/loginx')).toBe('/loginx')
  })
})

function noSession() {
  server.use(
    http.get('*/v1/me', () =>
      HttpResponse.json({ status: 401, code: 'invalid_grant' }, { status: 401 }),
    ),
  )
}

async function redirectOf(p: Promise<unknown>) {
  try {
    await p
  } catch (e) {
    return e as { options?: { to?: string; search?: unknown } }
  }
  return undefined
}

describe('route guards', () => {
  it('requireSession sends anonymous visitors to /login with a sanitised next', async () => {
    noSession()
    const ctx = { queryClient: new QueryClient() }
    const thrown = await redirectOf(requireSession(ctx, { href: '/orgs/org-1/me' }))
    expect(thrown?.options?.to).toBe('/login')
    expect(thrown?.options?.search).toEqual({ next: '/orgs/org-1/me' })

    const evil = await redirectOf(requireSession(ctx, { href: '//evil.example' }))
    expect(evil?.options?.search).toEqual({ next: undefined })
  })

  it('requireSession returns the user when signed in', async () => {
    const me = await requireSession({ queryClient: new QueryClient() }, { href: '/' })
    expect(me.user.id).toBe(meFixture.user.id)
  })

  it('requireOrg lets members in and remembers the org', async () => {
    const res = await requireOrg({ queryClient: new QueryClient() }, { href: '/' }, 'org-1')
    expect(res.org.id).toBe('org-1')
    expect(localStorage.getItem('ob.lastOrg')).toBe('org-1')
  })

  it('requireOrg sends non-members to /select-org and remembers nothing', async () => {
    const thrown = await redirectOf(
      requireOrg({ queryClient: new QueryClient() }, { href: '/' }, 'org-x'),
    )
    expect(thrown?.options?.to).toBe('/select-org')
    expect(localStorage.getItem('ob.lastOrg')).toBeNull()
  })

  it('ForbiddenError is an Error subclass the router maps to the 403 page', () => {
    expect(new ForbiddenError()).toBeInstanceOf(Error)
    expect(new ForbiddenError().name).toBe('ForbiddenError')
  })
})
