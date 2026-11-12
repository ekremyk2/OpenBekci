import { beforeEach, describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { meFixture } from '@/test/handlers'
import { server } from '@/test/setup'

function mount(path: string) {
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

describe('public auth pages', () => {
  it('reset: h1 inside one main landmark, with a way forward', async () => {
    mount('/reset/abc')
    expect(await screen.findByRole('heading', { level: 1 })).toBeInTheDocument()
    expect(screen.getAllByRole('main')).toHaveLength(1)
    expect(screen.getByRole('link', { name: /e-mail link/i })).toBeInTheDocument()
  })

  it('magic link: exchanges the token once, then leaves the page', async () => {
    const bodies: unknown[] = []
    let signedIn = false
    server.use(
      http.get('*/v1/me', () =>
        signedIn
          ? HttpResponse.json(meFixture)
          : HttpResponse.json({ status: 401 }, { status: 401 }),
      ),
      http.post('*/v1/auth/magic-link/consume', async ({ request }) => {
        bodies.push(await request.json())
        signedIn = true
        return HttpResponse.json({})
      }),
    )
    const router = mount('/login/magic?token=tok-1')
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/orgs\//))
    expect(bodies).toEqual([{ token: 'tok-1', client: 'web' }])
  })

  it('magic link without a token shows the dead-link page', async () => {
    mount('/login/magic')
    expect(
      await screen.findByRole('heading', { level: 1, name: /no longer works/i }),
    ).toBeInTheDocument()
  })
})
