import { beforeEach, describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { Providers, createQueryClient } from '@/app/providers'
import { orgNext } from '@/app/guards'
import { createAppRouter } from '@/app/router'
import { meFixture } from '@/test/handlers'
import { server } from '@/test/setup'

const ORG = meFixture.organizations[0]!.id

function exchangeServer(opts: { ok: boolean }) {
  let signedIn = false
  const bodies: unknown[] = []
  server.use(
    http.get('*/v1/me', () =>
      signedIn ? HttpResponse.json(meFixture) : HttpResponse.json({ status: 401 }, { status: 401 }),
    ),
    http.post('*/v1/auth/web-login-link\\:exchange', async ({ request }) => {
      bodies.push(await request.json())
      if (!opts.ok) {
        return HttpResponse.json({ status: 401, code: 'invalid_grant' }, { status: 401 })
      }
      signedIn = true
      return new HttpResponse(null, { status: 200 })
    }),
  )
  return bodies
}

async function mount(path: string) {
  localStorage.setItem('ob.locale', 'en')
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  return router
}

beforeEach(() => {
  localStorage.clear()
  i18n.activate('en')
})

describe('/login/link/$token', () => {
  it('exchanges the token once and lands on next inside the org tree', async () => {
    const bodies = exchangeServer({ ok: true })
    const router = await mount(
      `/login/link/tok-123?next=${encodeURIComponent(`/orgs/${ORG}/tickets`)}`,
    )
    await waitFor(() => expect(router.state.location.pathname).toBe(`/orgs/${ORG}/tickets`))
    expect(bodies).toEqual([{ token: 'tok-123' }])
  })

  it.each([
    'https://evil.example/orgs/' + ORG,
    '//evil.example',
    '/login',
    '/settings',
    `/orgs/${ORG}/../../login`,
  ])('ignores an external or out-of-tree next (%s)', async (next) => {
    exchangeServer({ ok: true })
    const router = await mount(`/login/link/tok-1?next=${encodeURIComponent(next)}`)
    await waitFor(() => expect(router.state.location.pathname).not.toMatch(/^\/login\/link/))
    expect(router.state.location.href).not.toContain('evil.example')
    expect(router.state.location.pathname.startsWith('/login')).toBe(false)
  })

  it('tells the user when the link no longer works', async () => {
    exchangeServer({ ok: false })
    await mount('/login/link/used-token')
    expect(await screen.findByText('This link no longer works')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to sign in' })).toBeInTheDocument()
  })
})

describe('orgNext', () => {
  it('accepts only /orgs/{id}/… paths', () => {
    expect(orgNext(`/orgs/${ORG}`)).toBe(`/orgs/${ORG}`)
    expect(orgNext(`/orgs/${ORG}/tickets?tab=a`)).toBe(`/orgs/${ORG}/tickets?tab=a`)
    for (const bad of [
      undefined,
      '',
      '/',
      '/orgs',
      '/app/orgs/' + ORG,
      `/orgs/${ORG}/%2e%2e/x`,
      `/orgs/${ORG}/a/../b`,
      `/orgs/${ORG}#x`,
      `/orgs/${ORG}/ü`,
      `https://x.example/orgs/${ORG}`,
    ]) {
      expect(orgNext(bad), String(bad)).toBeUndefined()
    }
  })
})
