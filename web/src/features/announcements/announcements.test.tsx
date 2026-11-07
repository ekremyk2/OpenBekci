import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'

describe('announcements', () => {
  it('previews recipients and SMS count per channel before publishing, then publishes with the chosen channels', async () => {
    const published: unknown[] = []
    server.use(
      http.post('*/v1/orgs/:org/announcements/:id', async ({ request }) => {
        if (request.url.includes(':publish')) published.push(await request.clone().json())
        return undefined
      }),
    )
    mountApp('/orgs/org-1/announcements/new', 'manager')
    await userEvent.type(await screen.findByLabelText('Title'), 'Water cut on Saturday')
    // the editor is lazy-loaded
    await userEvent.type(
      await screen.findByRole('textbox', { name: /Message/ }),
      'No water from 10:00',
    )

    const preview = await screen.findByRole('status', { name: 'Delivery preview' })
    await waitFor(() => expect(within(preview).getByText('Recipients: 120')).toBeInTheDocument())
    expect(within(preview).queryByText(/SMS messages/)).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('checkbox', { name: 'SMS' }))
    await waitFor(() =>
      expect(
        within(preview).getByText(/SMS messages: 108 \(billed to the site\)/),
      ).toBeInTheDocument(),
    )

    await userEvent.selectOptions(screen.getByLabelText('Audience'), 'Owners only')
    await waitFor(() => expect(within(preview).getByText('Recipients: 70')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(published).toHaveLength(1))
    expect(published[0]).toEqual({ channels: ['in_app', 'sms'] })
    expect(
      await screen.findByRole('heading', { name: 'Water cut on Saturday' }),
    ).toBeInTheDocument()
    expect(db.announcements[0]).toMatchObject({ status: 'published', sms_count: 63 })
  })

  it('requires a title and a message', async () => {
    mountApp('/orgs/org-1/announcements/new', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Publish' }))
    expect(await screen.findByText('Enter a title')).toBeInTheDocument()
    expect(screen.getByText('Write the announcement')).toBeInTheDocument()
    expect(db.announcements).toHaveLength(0)
  })

  it('renders Markdown in the preview tab without raw HTML', async () => {
    mountApp('/orgs/org-1/announcements/new', 'manager')
    await userEvent.type(
      await screen.findByRole('textbox', { name: /Message/ }),
      '**Bold** <script>x</script>',
    )
    await userEvent.click(screen.getByRole('tab', { name: 'Preview' }))
    const strong = await screen.findByText('Bold')
    expect(strong.tagName).toBe('STRONG')
    expect(document.querySelector('script')).toBeNull()
  })

  it('marks an announcement read when a resident opens it', async () => {
    db.announcements.push({
      id: 'a-1',
      org_id: 'org-1',
      site_id: 'site-a',
      title: 'Elevator maintenance',
      body_md: 'Friday **9:00**',
      status: 'published',
      audience: { kind: 'site' },
      publish_at: null,
      expires_at: null,
      pinned: false,
      channels: ['in_app'],
      recipient_count: 120,
      sms_count: 0,
      read_count: 0,
      read_at: null,
      file_ids: [],
      published_at: new Date().toISOString(),
      created_by: 'u-manager',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      version: 1,
    })
    mountApp('/orgs/org-1/announcements/a-1', 'resident')
    expect(await screen.findByRole('heading', { name: 'Elevator maintenance' })).toBeInTheDocument()
    await waitFor(() => expect(db.reads.get('a-1')?.has('p-resident')).toBe(true))
  })

  it('shows read receipts to managers and not to residents', async () => {
    db.announcements.push({
      id: 'a-2',
      org_id: 'org-1',
      site_id: 'site-a',
      title: 'Assembly notice',
      body_md: 'Hello',
      status: 'published',
      audience: { kind: 'site' },
      publish_at: null,
      expires_at: null,
      pinned: true,
      channels: ['in_app'],
      recipient_count: 120,
      sms_count: 0,
      read_count: 2,
      read_at: null,
      file_ids: [],
      published_at: new Date().toISOString(),
      created_by: 'u-manager',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      version: 1,
    })
    mountApp('/orgs/org-1/announcements/a-2', 'manager')
    expect(await screen.findByText('Read receipts')).toBeInTheDocument()
    expect(await screen.findByText(/have opened this announcement/)).toBeInTheDocument()
    expect(screen.getByText('Zeynep Arslan (4)')).toBeInTheDocument()
  })

  it('lists pinned announcements first', async () => {
    const base = {
      org_id: 'org-1',
      site_id: 'site-a',
      body_md: 'x',
      status: 'published',
      audience: { kind: 'site' },
      publish_at: null,
      expires_at: null,
      channels: ['in_app'],
      recipient_count: 1,
      sms_count: 0,
      read_count: 0,
      read_at: null,
      file_ids: [],
      published_at: null,
      created_by: null,
      updated_at: new Date().toISOString(),
      version: 1,
    }
    db.announcements.push(
      {
        ...base,
        id: 'a-old',
        title: 'Pinned old',
        pinned: true,
        created_at: '2026-01-01T00:00:00Z',
      },
      {
        ...base,
        id: 'a-new',
        title: 'Unpinned new',
        pinned: false,
        created_at: '2026-06-01T00:00:00Z',
      },
    )
    mountApp('/orgs/org-1/announcements', 'resident')
    const list = await screen.findByRole('list', { name: 'Announcements' })
    const titles = within(list)
      .getAllByRole('link')
      .map((a) => a.textContent)
    expect(titles[0]).toContain('Pinned old')
    expect(titles[1]).toContain('Unpinned new')
  })

  it('shows an empty state for residents without announcements', async () => {
    mountApp('/orgs/org-1/announcements', 'resident')
    expect(await screen.findByText('No announcements')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /New announcement/ })).not.toBeInTheDocument()
  })
})
