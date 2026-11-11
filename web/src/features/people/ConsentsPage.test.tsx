import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { mountApp } from '@/test/render'
import { server } from '@/test/setup'

describe('privacy notice and consents', () => {
  it('shows each text with its version, in the language picked', async () => {
    await mountApp('/orgs/org-1/me/consents')
    expect(
      await screen.findByRole('heading', { name: 'Privacy notice and consents' }),
    ).toBeInTheDocument()
    expect((await screen.findAllByText('Version 2026-03')).length).toBe(2)
    expect(
      await screen.findByRole('heading', { name: /^Privacy notice/, level: 2 }),
    ).toBeInTheDocument()
    expect(screen.getByText('Consent directory')).toBeInTheDocument()

    await userEvent.selectOptions(
      screen.getByLabelText('Language', { selector: '#consent-locale' }),
      'tr',
    )
    expect(await screen.findByRole('heading', { name: 'Aydınlatma metni' })).toBeInTheDocument()
  })

  it('records an accept for the version shown and then shows it as accepted', async () => {
    const seen: { url: string; body: Promise<unknown> }[] = []
    server.events.on('request:start', ({ request }) => {
      if (request.method === 'POST' && request.url.includes('/privacy/consents/'))
        seen.push({ url: request.url, body: request.clone().json() })
    })
    await mountApp('/orgs/org-1/me/consents')
    const [accept] = await screen.findAllByRole('button', { name: 'Accept' })
    await userEvent.click(accept!)
    await waitFor(() => expect(seen).toHaveLength(1))
    expect(decodeURIComponent(seen[0]!.url)).toMatch(/tr\.privacy_notice:accept$/)
    expect(await seen[0]!.body).toEqual({ version: '2026-03' })
    expect(await screen.findByText('Accepted')).toBeInTheDocument()
    server.events.removeAllListeners()
  })

  it('asks for the chosen locale', async () => {
    const asked: (string | null)[] = []
    server.use(
      http.get('*/v1/orgs/:orgId/privacy/consents', ({ request }) => {
        asked.push(new URL(request.url).searchParams.get('locale'))
        return HttpResponse.json({ locale: 'en', items: [] })
      }),
    )
    await mountApp('/orgs/org-1/me/consents')
    expect(
      await screen.findByText('No privacy texts are published for this organization.'),
    ).toBeInTheDocument()
    await userEvent.selectOptions(
      screen.getByLabelText('Language', { selector: '#consent-locale' }),
      'tr',
    )
    await waitFor(() => expect(asked).toEqual(['en', 'tr']))
  })

  it('shows an error with a retry when the texts cannot be loaded', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/privacy/consents', () =>
        HttpResponse.json(
          { status: 500, code: 'internal_error' },
          { status: 500, headers: { 'content-type': 'application/problem+json' } },
        ),
      ),
    )
    await mountApp('/orgs/org-1/me/consents')
    expect(await screen.findByRole('button', { name: /try again|retry/i })).toBeInTheDocument()
  })

  it('is linked from my account', async () => {
    await mountApp('/orgs/org-1/me')
    const link = await screen.findByRole('link', { name: 'Privacy notice and consent texts' })
    expect(link).toHaveAttribute('href', '/orgs/org-1/me/consents')
    await userEvent.click(link)
    expect(
      await screen.findByRole('heading', { name: 'Privacy notice and consents' }),
    ).toBeInTheDocument()
  })
})

describe('profile name and language', () => {
  it('sends only what changed, with If-Match from the user version', async () => {
    const seen: { ifMatch: string | null; body: Promise<unknown> }[] = []
    server.events.on('request:start', ({ request }) => {
      if (request.method === 'PATCH' && request.url.endsWith('/v1/me/profile'))
        seen.push({ ifMatch: request.headers.get('If-Match'), body: request.clone().json() })
    })
    await mountApp('/orgs/org-1/me')
    const name = await screen.findByLabelText('Display name')
    expect(screen.getByRole('button', { name: 'Save profile' })).toBeDisabled()
    await userEvent.clear(name)
    await userEvent.type(name, 'Ayşe K.')
    await userEvent.click(screen.getByRole('button', { name: 'Save profile' }))
    await waitFor(() => expect(seen).toHaveLength(1))
    expect(seen[0]!.ifMatch).toBe('"1"')
    expect(await seen[0]!.body).toEqual({ display_name: 'Ayşe K.' })
    server.events.removeAllListeners()
  })

  it('says so when the account changed meanwhile', async () => {
    server.use(
      http.patch('*/v1/me/profile', () =>
        HttpResponse.json(
          { status: 409, code: 'version_conflict' },
          { status: 409, headers: { 'content-type': 'application/problem+json' } },
        ),
      ),
    )
    await mountApp('/orgs/org-1/me')
    await userEvent.type(await screen.findByLabelText('Display name'), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Save profile' }))
    expect(await screen.findByText(/Someone else changed this record/)).toBeInTheDocument()
  })
})
