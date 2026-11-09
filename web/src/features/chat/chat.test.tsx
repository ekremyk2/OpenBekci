import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { mountApp } from '@/test/render'
import { server } from '@/test/setup'

const link = (over: object = {}) => ({
  id: 'l-1',
  org_id: 'org-1',
  provider: 'telegram',
  status: 'linked',
  linked_at: '2026-11-01T10:00:00Z',
  created_at: '2026-11-01T09:00:00Z',
  ...over,
})

describe('linked chats on my account', () => {
  it('lists links, shows the deep link after start and unlinks', async () => {
    let items = [link(), link({ id: 'l-2', provider: 'whatsapp', status: 'pending' })]
    const deleted: string[] = []
    server.use(
      http.get('*/v1/orgs/:org/me/chat-links', () => HttpResponse.json({ items })),
      http.post('*/v1/orgs/:org/me/chat-links/telegram\\:start', () =>
        HttpResponse.json({
          provider: 'telegram',
          deep_link: 'https://t.me/bot?start=abc',
          expires_at: '2026-11-10T10:00:00Z',
        }),
      ),
      http.delete('*/v1/orgs/:org/me/chat-links/:id', ({ params }) => {
        deleted.push(params.id as string)
        items = items.filter((x) => x.id !== params.id)
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await mountApp('/orgs/org-1/me')
    expect(await screen.findByText('Waiting for your message to the bot')).toBeInTheDocument()
    expect(screen.getByText('Linked')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Link Telegram' }))
    expect(
      await screen.findByRole('link', { name: 'https://t.me/bot?start=abc' }),
    ).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Unlink WhatsApp' }))
    await waitFor(() => expect(deleted).toEqual(['l-2']))
    await waitFor(() =>
      expect(screen.queryByText('Waiting for your message to the bot')).not.toBeInTheDocument(),
    )
  })

  it('says so when the administrator has not set the provider up', async () => {
    server.use(
      http.get('*/v1/orgs/:org/me/chat-links', () => HttpResponse.json({ items: [] })),
      http.post('*/v1/orgs/:org/me/chat-links/whatsapp\\:start', () =>
        HttpResponse.json({ type: 'x', title: 'not configured', status: 409 }, { status: 409 }),
      ),
    )
    await mountApp('/orgs/org-1/me')
    await userEvent.click(await screen.findByRole('button', { name: 'Link WhatsApp' }))
    expect(
      await screen.findByText('This chat service is not set up by your administrator yet.'),
    ).toBeInTheDocument()
  })
})
