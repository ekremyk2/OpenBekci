import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { delay, http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'
import { PREVIEW, spy } from './test-helpers'

async function fillNewForm() {
  await userEvent.type(await screen.findByLabelText('Title'), 'Water cut on Saturday')
  await userEvent.type(await screen.findByRole('textbox', { name: /Message/ }), 'No water')
  const panel = await screen.findByRole('status', { name: 'Delivery preview' })
  await waitFor(() => expect(within(panel).getByText('Recipients: 120')).toBeInTheDocument())
  return panel
}

describe('publish from the form (B3)', () => {
  it('disables Publish while the preview is loading, after it failed, and until the new channels are counted', async () => {
    let mode: 'ok' | 'slow' | 'fail' = 'ok'
    server.use(
      http.post(PREVIEW, async ({ request }) => {
        const b = (await request.clone().json()) as { channels: string[] }
        if (mode === 'fail')
          return HttpResponse.json({ type: 'x', title: 'boom', status: 400 }, { status: 400 })
        if (mode === 'slow') await delay(400)
        const n = 120
        return HttpResponse.json({
          recipient_count: n,
          in_app_count: n,
          email_count: 96,
          sms_count: b.channels.includes('sms') ? 108 : 0,
        })
      }),
    )
    mountApp('/orgs/org-1/announcements/new', 'manager')
    // loading: the very first preview is still in flight
    mode = 'slow'
    const publish = await screen.findByRole('button', { name: 'Publish' })
    expect(publish).toBeDisabled()
    const panel = await screen.findByRole('status', { name: 'Delivery preview' })
    await waitFor(() => expect(within(panel).getByText('Recipients: 120')).toBeInTheDocument())
    expect(publish).toBeEnabled()

    // stale: channels changed, the new preview has not arrived yet
    await userEvent.click(screen.getByRole('checkbox', { name: 'SMS' }))
    expect(publish).toBeDisabled()
    await waitFor(() => expect(within(panel).getByText(/SMS messages: 108/)).toBeInTheDocument())
    expect(publish).toBeEnabled()

    // errored
    mode = 'fail'
    await userEvent.click(screen.getByRole('checkbox', { name: 'Email' }))
    expect(await within(panel).findByText('Could not count the recipients.')).toBeInTheDocument()
    expect(publish).toBeDisabled()
    expect(db.announcements).toHaveLength(0)
  })

  it('after a successful create, a failed publish retries only the publish', async () => {
    let failPublish = 2
    server.use(
      http.post('*/v1/orgs/:org/announcements/:id', ({ request }) => {
        if (request.url.includes(':publish') && failPublish-- > 0)
          return HttpResponse.json({ type: 'x', title: 'boom', status: 503 }, { status: 503 })
        return undefined
      }),
    )
    const creates = spy('*/v1/orgs/:org/announcements')
    const publishes = spy('*/v1/orgs/:org/announcements/:id')
    const patches = spy('*/v1/orgs/:org/announcements/:id', 'patch')
    mountApp('/orgs/org-1/announcements/new', 'manager')
    const panel = await fillNewForm()

    // 1st attempt: create succeeds, publish fails
    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(publishes.filter((c) => c.url.includes(':publish'))).toHaveLength(1))
    expect(creates).toHaveLength(1)
    expect(db.announcements).toHaveLength(1)

    // 2nd attempt, nothing edited: no second create, no patch, same publish key
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(publishes.filter((c) => c.url.includes(':publish'))).toHaveLength(2))
    expect(creates).toHaveLength(1)
    expect(patches).toHaveLength(0)
    const sent = publishes.filter((c) => c.url.includes(':publish'))
    expect(sent[1]!.key).toBe(sent[0]!.key)

    // 3rd attempt after editing the title: the saved record is patched (not created again) and the
    // publish goes out; the channels did not change, so the publish key is the same
    await userEvent.type(screen.getByLabelText('Title'), ' (updated)')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled())
    expect(within(panel).getByText('Recipients: 120')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(db.announcements[0]).toMatchObject({ status: 'published' }))
    expect(creates).toHaveLength(1)
    expect(patches).toHaveLength(1)
    expect(db.announcements).toHaveLength(1)
    expect(db.announcements[0]).toMatchObject({ title: 'Water cut on Saturday (updated)' })
  })

  it('uses a new publish key when the channels changed between attempts', async () => {
    let failPublish = 1
    server.use(
      http.post('*/v1/orgs/:org/announcements/:id', ({ request }) => {
        if (request.url.includes(':publish') && failPublish-- > 0)
          return HttpResponse.json({ type: 'x', title: 'boom', status: 503 }, { status: 503 })
        return undefined
      }),
    )
    const calls = spy('*/v1/orgs/:org/announcements/:id')
    mountApp('/orgs/org-1/announcements/new', 'manager')
    const panel = await fillNewForm()
    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(calls.filter((c) => c.url.includes(':publish'))).toHaveLength(1))

    await userEvent.click(screen.getByRole('checkbox', { name: 'SMS' }))
    await waitFor(() => expect(within(panel).getByText(/SMS messages: 108/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(db.announcements[0]).toMatchObject({ status: 'published' }))
    const sent = calls.filter((c) => c.url.includes(':publish'))
    expect(sent).toHaveLength(2)
    expect(sent[0]!.body).toEqual({ channels: ['in_app'] })
    expect(sent[1]!.body).toEqual({ channels: ['in_app', 'sms'] })
    expect(sent[1]!.key).not.toBe(sent[0]!.key)
  })

  it('sends the chat channel when it is ticked', async () => {
    const calls = spy('*/v1/orgs/:org/announcements/:id')
    mountApp('/orgs/org-1/announcements/new', 'manager')
    await fillNewForm()
    await userEvent.click(screen.getByRole('checkbox', { name: 'Chat (Telegram/WhatsApp)' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(calls.filter((c) => c.url.includes(':publish'))).toHaveLength(1))
    expect(calls.find((c) => c.url.includes(':publish'))!.body).toEqual({
      channels: ['in_app', 'chat'],
    })
  })
})
