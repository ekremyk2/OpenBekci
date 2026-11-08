import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { delay, http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'
import { PREVIEW, seedDraft, spy } from './test-helpers'

describe('publish now on the detail page (B2)', () => {
  it('shows recipient and SMS counts first and publishes only from the dialog, with explicit channels', async () => {
    seedDraft()
    const publishes = spy('*/v1/orgs/:org/announcements/:id')
    mountApp('/orgs/org-1/announcements/a-draft', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Publish now' }))

    const dialog = await screen.findByRole('alertdialog', { name: 'Publish this announcement?' })
    expect(await within(dialog).findByText('Recipients: 120')).toBeInTheDocument()
    expect(within(dialog).getByText(/SMS messages: 108 \(billed to the site\)/)).toBeInTheDocument()
    expect(publishes.filter((c) => c.url.includes(':publish'))).toHaveLength(0)
    expect(db.announcements[0]).toMatchObject({ status: 'draft' })

    await userEvent.click(within(dialog).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(db.announcements[0]).toMatchObject({ status: 'published' }))
    const sent = publishes.filter((c) => c.url.includes(':publish'))
    expect(sent).toHaveLength(1)
    expect(sent[0]!.body).toEqual({ channels: ['in_app', 'sms'] })
    expect(sent[0]!.key).toBeTruthy()
  })

  it('keeps Publish disabled while the preview loads and when it fails', async () => {
    seedDraft()
    server.use(
      http.post(PREVIEW, async () => {
        await delay(150)
        return HttpResponse.json({ type: 'x', title: 'boom', status: 400 }, { status: 400 })
      }),
    )
    mountApp('/orgs/org-1/announcements/a-draft', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Publish now' }))
    const dialog = await screen.findByRole('alertdialog')
    const confirm = within(dialog).getByRole('button', { name: 'Publish' })
    expect(confirm).toBeDisabled() // counting
    expect(await within(dialog).findByText('Could not count the recipients.')).toBeInTheDocument()
    expect(confirm).toBeDisabled() // failed
    expect(db.announcements[0]).toMatchObject({ status: 'draft' })
  })

  it('reuses the publish key when a failed publish is retried', async () => {
    seedDraft()
    let fail = true
    server.use(
      http.post('*/v1/orgs/:org/announcements/:id', async ({ request }) => {
        if (request.url.includes(':publish') && fail) {
          fail = false
          return HttpResponse.json({ type: 'x', title: 'boom', status: 503 }, { status: 503 })
        }
        return undefined
      }),
    )
    const calls = spy('*/v1/orgs/:org/announcements/:id')
    mountApp('/orgs/org-1/announcements/a-draft', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Publish now' }))
    const dialog = await screen.findByRole('alertdialog')
    await within(dialog).findByText('Recipients: 120')
    const confirm = within(dialog).getByRole('button', { name: 'Publish' })
    await userEvent.click(confirm)
    await waitFor(() => expect(confirm).toBeEnabled())
    await userEvent.click(confirm)
    await waitFor(() => expect(db.announcements[0]).toMatchObject({ status: 'published' }))
    const keys = calls.filter((c) => c.url.includes(':publish')).map((c) => c.key)
    expect(keys.length).toBeGreaterThanOrEqual(2)
    expect(new Set(keys).size).toBe(1)
  })
})
