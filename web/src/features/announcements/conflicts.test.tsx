import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'

function seedDraft() {
  const at = new Date().toISOString()
  db.announcements.push({
    id: 'a-draft',
    org_id: 'org-1',
    site_id: 'site-a',
    title: 'Water cut',
    body_md: 'No water from 10:00',
    status: 'draft',
    audience: { kind: 'site' },
    publish_at: null,
    expires_at: null,
    pinned: false,
    channels: ['in_app'],
    recipient_count: null,
    sms_count: null,
    read_count: 0,
    read_at: null,
    file_ids: [],
    published_at: null,
    created_by: 'u-manager',
    created_at: at,
    updated_at: at,
    version: 1,
  })
}

async function editAndSave() {
  const title = await screen.findByLabelText('Title')
  await waitFor(() => expect(title).toHaveValue('Water cut'))
  await userEvent.type(title, ' (edited)')
  await userEvent.click(await screen.findByRole('button', { name: 'Save draft' }))
}

describe('stale edits', () => {
  it('shows the "This record changed" dialog on a 409 version_conflict (the contract)', async () => {
    seedDraft()
    const sent: (string | null)[] = []
    server.use(
      http.patch('*/v1/orgs/:org/announcements/:id', ({ request }) => {
        sent.push(request.headers.get('If-Match'))
        return undefined
      }),
    )
    mountApp('/orgs/org-1/announcements/a-draft/edit', 'manager')
    // someone else saved in the meantime: the version the form loaded (1) is now stale
    await screen.findByLabelText('Title')
    db.announcements[0]!.version = 2
    await editAndSave()
    expect(await screen.findByRole('dialog', { name: 'This record changed' })).toBeInTheDocument()
    expect(sent).toEqual(['"1"'])
    expect(db.announcements[0]).toMatchObject({ title: 'Water cut', version: 2 })
  })

  it('shows the same dialog on a 412 (the web plan)', async () => {
    seedDraft()
    server.use(
      http.patch('*/v1/orgs/:org/announcements/:id', () =>
        HttpResponse.json(
          { type: 'x', title: 'Precondition failed', status: 412, code: 'version_conflict' },
          { status: 412, headers: { 'Content-Type': 'application/problem+json' } },
        ),
      ),
    )
    mountApp('/orgs/org-1/announcements/a-draft/edit', 'manager')
    await editAndSave()
    expect(await screen.findByRole('dialog', { name: 'This record changed' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument()
  })

  it('confirms a delete in a dialog and sends the loaded version in If-Match', async () => {
    seedDraft()
    const versions: (string | null)[] = []
    server.use(
      http.delete('*/v1/orgs/:org/announcements/:id', ({ request }) => {
        versions.push(request.headers.get('If-Match'))
        return undefined
      }),
    )
    mountApp('/orgs/org-1/announcements/a-draft', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete this announcement?' })
    expect(dialog).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    // two "Delete" buttons exist (page and dialog); the dialog's confirms
    await waitFor(() => expect(db.announcements).toHaveLength(0))
    expect(versions).toEqual(['"1"'])
  })
})
