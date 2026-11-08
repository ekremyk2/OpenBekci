import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { AppError } from '@/api/errors'
import { emitApiEvent } from '@/api/events'
import { clearToasts } from '@/lib/toast'
import { server } from '@/test/setup'
import { Providers, createQueryClient } from './providers'
import { createAppRouter } from './router'

const expired = () =>
  http.get('*/v1/me', () =>
    HttpResponse.json({ status: 401, code: 'invalid_grant' }, { status: 401 }),
  )

async function mount(path: string, queryClient: QueryClient = createQueryClient()) {
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  await waitFor(() => expect(router.state.status).toBe('idle'))
  return { router, queryClient }
}

const error = (status: number, code: string, retryAfter?: number) =>
  new AppError({ status, code, retryAfter })

beforeEach(() => {
  localStorage.clear()
  i18n.activate('en')
})
afterEach(() => act(() => clearToasts()))

describe('ErrorEffects', () => {
  it('on 401 clears the whole query cache and redirects to /login?next=', async () => {
    const { router, queryClient } = await mount('/orgs/org-1/me')
    expect(await screen.findByText('Ayşe Yılmaz', {}, { timeout: 5000 })).toBeInTheDocument()
    queryClient.setQueryData(['org', 'org-1', 'units'], [1, 2, 3])

    // A 401 means the session is gone, so /v1/me answers 401 from here on; with a live session the
    // login route would send the user straight back to `next` and the test would race that redirect.
    server.use(expired())
    act(() => emitApiEvent({ type: 'unauthorized', error: error(401, 'invalid_grant') }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(router.state.location.search).toEqual({ next: '/orgs/org-1/me' })
    expect(queryClient.getQueryData(['me'])).toBeUndefined()
    expect(queryClient.getQueryData(['org', 'org-1', 'units'])).toBeUndefined()
  })

  it('signs the shell out when the /v1/me refetch (window focus) returns 401', async () => {
    const { router, queryClient } = await mount('/orgs/org-1/me')
    expect(await screen.findByText('Ayşe Yılmaz', {}, { timeout: 5000 })).toBeInTheDocument()

    server.use(expired())
    await act(() => queryClient.refetchQueries({ queryKey: ['me'] }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(router.state.location.search).toEqual({ next: '/orgs/org-1/me' })
    expect(queryClient.getQueryData(['me'])).toBeUndefined()
  })

  it('does not redirect when already on the login page', async () => {
    server.use(expired())
    const { router } = await mount('/login')
    act(() => emitApiEvent({ type: 'unauthorized', error: error(401, 'invalid_grant') }))
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.location.search).toEqual({})
  })

  it('shows the reload dialog on a version conflict', async () => {
    server.use(expired())
    await mount('/login')
    act(() => emitApiEvent({ type: 'version-conflict', error: error(412, 'version_conflict') }))
    expect(await screen.findByText('This record changed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument()
  })

  it('toasts the Retry-After wait on 429', async () => {
    server.use(expired())
    await mount('/login')
    act(() => emitApiEvent({ type: 'rate-limited', error: error(429, 'rate_limited', 4.2) }))
    expect(await screen.findByText(/Try again in 5 seconds/)).toBeInTheDocument()
  })

  it('opens the re-authentication dialog on reauth_required', async () => {
    server.use(expired())
    await mount('/login')
    act(() => emitApiEvent({ type: 'reauth-required', error: error(403, 'reauth_required') }))
    expect(await screen.findByRole('heading', { name: /confirm it is you/i })).toBeInTheDocument()
  })
})
