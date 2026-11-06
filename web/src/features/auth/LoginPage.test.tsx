import { beforeEach, describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Providers, createQueryClient } from '@/app/providers'
import { createAppRouter } from '@/app/router'
import { meFixture } from '@/test/handlers'
import { server } from '@/test/setup'

const problem = (status: number, body: Record<string, unknown>) =>
  HttpResponse.json({ status, ...body }, { status })

/** Anonymous until the sign-in POST succeeds, then /v1/me returns the fixture. */
function sessionServer() {
  let signedIn = false
  const bodies: unknown[] = []
  server.use(
    http.get('*/v1/me', () => (signedIn ? HttpResponse.json(meFixture) : problem(401, {}))),
    http.post('*/v1/auth/session', async ({ request }) => {
      bodies.push(await request.json())
      signedIn = true
      return new HttpResponse(null, { status: 200 })
    }),
  )
  return { bodies, setSignedIn: (v: boolean) => (signedIn = v) }
}

async function mount(path = '/login') {
  localStorage.setItem('ob.locale', 'en') // explicit choice: /v1/me must not switch the language
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  render(
    <Providers queryClient={queryClient} locale="en">
      <RouterProvider router={router} />
    </Providers>,
  )
  return { router, queryClient }
}

beforeEach(() => {
  localStorage.clear()
  i18n.activate('en')
})

async function openPasswordTab() {
  await userEvent.click(await screen.findByRole('tab', { name: 'Password' }))
}

describe('LoginPage', () => {
  it('shows the instance name and the offered methods, phone first', async () => {
    sessionServer()
    await mount()
    expect(await screen.findByRole('heading', { name: 'Test Instance' })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual([
      'SMS code',
      'Password',
      'Email link',
    ])
  })

  it('signs in with a password, sending client:web, and follows a safe next', async () => {
    const { bodies } = sessionServer()
    const { router } = await mount('/login?next=/orgs/org-1/me')
    await openPasswordTab()
    await userEvent.type(screen.getByLabelText('Email or phone'), 'ayse@example.com')
    await userEvent.type(screen.getByLabelText('Password', { selector: 'input' }), 'hunter2!')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/orgs/org-1/me'))
    expect(bodies).toEqual([
      { method: 'password', identifier: 'ayse@example.com', password: 'hunter2!', client: 'web' },
    ])
  })

  it('shows a form-level message on 401 invalid_grant and stays on the page', async () => {
    sessionServer()
    server.use(http.post('*/v1/auth/session', () => problem(401, { code: 'invalid_grant' })))
    await mount()
    await openPasswordTab()
    await userEvent.type(screen.getByLabelText('Email or phone'), 'a@example.com')
    await userEvent.type(screen.getByLabelText('Password', { selector: 'input' }), 'wrong')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(
      await screen.findByText('That did not work. Check your details and try again.'),
    ).toBeInTheDocument()
  })

  it('maps 422 field errors onto the fields', async () => {
    sessionServer()
    server.use(
      http.post('*/v1/auth/session', () =>
        problem(422, {
          code: 'validation_failed',
          errors: [{ field: 'identifier', code: 'invalid', message: 'Not a known account format' }],
        }),
      ),
    )
    await mount()
    await openPasswordTab()
    await userEvent.type(screen.getByLabelText('Email or phone'), 'x')
    await userEvent.type(screen.getByLabelText('Password', { selector: 'input' }), 'y')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('Not a known account format')).toBeInTheDocument()
  })

  it('SMS flow: 202 {status:"sent"} asks for the code, then signs in with it', async () => {
    const { bodies, setSignedIn } = sessionServer()
    server.use(
      http.post('*/v1/auth/session', async ({ request }) => {
        const body = (await request.json()) as { code?: string }
        bodies.push(body)
        if (!body.code) return HttpResponse.json({ status: 'sent' }, { status: 202 })
        setSignedIn(true)
        return new HttpResponse(null, { status: 200 })
      }),
    )
    const { router } = await mount()
    await userEvent.type(await screen.findByLabelText('Phone number'), '+905551112233')
    await userEvent.click(screen.getByRole('button', { name: 'Send me a code' }))
    const code = await screen.findByLabelText('Code from the SMS')
    await userEvent.type(code, '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(bodies).toHaveLength(2))
    expect(bodies[1]).toEqual({
      method: 'sms_otp',
      identifier: '+905551112233',
      code: '123456',
      client: 'web',
    })
    await waitFor(() => expect(router.state.location.pathname).toBe('/orgs/org-1'))
  })

  it('magic link: 202 shows the "link is on its way" notice without signing in', async () => {
    sessionServer()
    server.use(
      http.post('*/v1/auth/session', () => HttpResponse.json({ status: 'sent' }, { status: 202 })),
    )
    await mount()
    await userEvent.click(await screen.findByRole('tab', { name: 'Email link' }))
    await userEvent.type(screen.getByLabelText('Email'), 'a@example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Email me a link' }))
    expect(await screen.findByRole('status')).toHaveTextContent('sign-in link is on its way')
  })

  it('ignores an unsafe next after sign-in and lands on the default page', async () => {
    sessionServer()
    const { router } = await mount('/login?next=//evil.example')
    await openPasswordTab()
    await userEvent.type(screen.getByLabelText('Email or phone'), 'a@example.com')
    await userEvent.type(screen.getByLabelText('Password', { selector: 'input' }), 'pw')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    // single organization: lands on it, never on the attacker-supplied host
    await waitFor(() => expect(router.state.location.pathname).toBe('/orgs/org-1'))
  })
})
