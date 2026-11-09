import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
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
      await screen.findByRole('heading', { name: 'Privacy notice', level: 2 }),
    ).toBeInTheDocument()
    expect(screen.getByText('Consent directory')).toBeInTheDocument()

    await userEvent.selectOptions(
      screen.getByLabelText('Language', { selector: '#consent-locale' }),
      'tr',
    )
    expect(await screen.findByRole('heading', { name: 'Aydınlatma metni' })).toBeInTheDocument()
  })

  it('says it is display only: there is no accept or decline', async () => {
    await mountApp('/orgs/org-1/me/consents')
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent(/not possible in the web app yet/)
    expect(screen.queryByRole('button', { name: /accept|decline/i })).not.toBeInTheDocument()
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
    expect(within(document.body).getByRole('note')).toBeInTheDocument()
  })
})
