import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { mountApp } from '@/test/app'
import { server } from '@/test/setup'
import { PREVIEW } from '@/features/announcements/test-helpers'

const GROUP = '*/v1/orgs/:org/sites/:site/chat-group'
const INFO = '*/v1/orgs/:org/sites/:site/chat-info'

const group = (over: object = {}) => ({
  status: 'none',
  title: null,
  kind: null,
  linked_at: null,
  code_expires_at: null,
  bot_username: 'bekci_bot',
  whatsapp_invite_url: null,
  ...over,
})

describe('site Telegram group card', () => {
  it('shows the code after start, unlinks after confirming and saves the invite link', async () => {
    let state = group()
    const calls: string[] = []
    server.use(
      http.get(GROUP, () => HttpResponse.json(state)),
      http.post('*/v1/orgs/:org/sites/:site/chat-group\\:start', () => {
        state = group({ status: 'pending', code_expires_at: '2026-11-10T10:00:00Z' })
        return HttpResponse.json({
          code: 'K7Q2',
          expires_at: '2026-11-10T10:00:00Z',
          bot_username: 'bekci_bot',
        })
      }),
      http.delete(GROUP, () => {
        calls.push('delete')
        state = group()
        return new HttpResponse(null, { status: 204 })
      }),
      http.patch(GROUP, async ({ request }) => {
        const b = (await request.json()) as { whatsapp_invite_url: string | null }
        calls.push(`patch ${b.whatsapp_invite_url}`)
        if (b.whatsapp_invite_url !== 'https://chat.whatsapp.com/abc')
          return HttpResponse.json(
            { type: 'x', title: 'invalid', status: 422, code: 'validation_failed' },
            { status: 422 },
          )
        state = group({ whatsapp_invite_url: b.whatsapp_invite_url })
        return HttpResponse.json(state)
      }),
    )
    mountApp('/orgs/org-1/settings/site', 'manager')
    await userEvent.click((await screen.findAllByRole('button', { name: 'Get link code' }))[0]!)
    expect(await screen.findByText('/link K7Q2')).toBeInTheDocument()
    expect(screen.getByText('Waiting for /link')).toBeInTheDocument()

    await userEvent.click(screen.getAllByRole('button', { name: 'Unlink' })[0]!)
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/The bot leaves the group/)).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Unlink' }))
    await waitFor(() => expect(calls).toContain('delete'))

    const input = screen.getAllByLabelText('WhatsApp group invite link')[0]!
    await userEvent.type(input, 'nope')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' })[0]!)
    expect(
      await screen.findByText('Must look like https://chat.whatsapp.com/…'),
    ).toBeInTheDocument()
    await userEvent.clear(input)
    await userEvent.type(input, 'https://chat.whatsapp.com/abc')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' })[0]!)
    await waitFor(() => expect(calls).toContain('patch https://chat.whatsapp.com/abc'))
  })

  it('says so when Telegram is not configured', async () => {
    server.use(
      http.get(GROUP, () => HttpResponse.json(group({ bot_username: null }))),
      http.post('*/v1/orgs/:org/sites/:site/chat-group\\:start', () =>
        HttpResponse.json({ type: 'x', title: 'no', status: 409 }, { status: 409 }),
      ),
    )
    mountApp('/orgs/org-1/settings/site', 'manager')
    await userEvent.click((await screen.findAllByRole('button', { name: 'Get link code' }))[0]!)
    expect(
      await screen.findByText(/Telegram is not configured for this organization/),
    ).toBeInTheDocument()
  })
})

describe('announcement composer with the Telegram group', () => {
  const previewWith = (telegram_group: object) =>
    server.use(
      http.get(GROUP, () => HttpResponse.json(group())),
      http.post(PREVIEW, () =>
        HttpResponse.json({
          recipient_count: 120,
          sms_count: 0,
          sms_segments: 0,
          email_count: 0,
          in_app_count: 120,
          telegram_group,
        }),
      ),
    )

  it('shows where it will post', async () => {
    previewWith({ title: 'Sunrise A', will_post: true })
    mountApp('/orgs/org-1/announcements/new', 'manager')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'Telegram group' }))
    expect(await screen.findByText('Will post to Sunrise A')).toBeInTheDocument()
  })

  it('explains a narrower audience', async () => {
    previewWith({ title: 'Sunrise A', will_post: false, reason: 'audience_narrower' })
    mountApp('/orgs/org-1/announcements/new', 'manager')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'Telegram group' }))
    expect(
      await screen.findByText(
        'Not posted to the group: the audience is narrower than the whole site',
      ),
    ).toBeInTheDocument()
  })
})

describe('resident Home', () => {
  it('links to the WhatsApp group only when the site has one', async () => {
    server.use(
      http.get(INFO, () =>
        HttpResponse.json({ whatsapp_invite_url: 'https://chat.whatsapp.com/abc' }),
      ),
    )
    mountApp('/orgs/org-1', 'resident')
    const a = await screen.findByRole('link', { name: "Join the site's WhatsApp group" })
    expect(a).toHaveAttribute('href', 'https://chat.whatsapp.com/abc')
    expect(a).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('shows nothing when no link is set', async () => {
    server.use(http.get(INFO, () => HttpResponse.json({ whatsapp_invite_url: null })))
    mountApp('/orgs/org-1', 'resident')
    await screen.findByText('Your dashboard is on its way')
    expect(screen.queryByRole('link', { name: /WhatsApp/ })).not.toBeInTheDocument()
  })
})
