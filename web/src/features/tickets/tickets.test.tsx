import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'

const base = '/orgs/org-1/tickets'

describe('tickets', () => {
  it('lists my tickets and common tickets for a resident, hiding other private ones', async () => {
    mountApp(base, 'resident')
    expect(await screen.findByText('Leaking tap in the kitchen')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: 'Common areas' }))
    expect(await screen.findByText('Broken lamp in the entrance')).toBeInTheDocument()
    expect(screen.queryByText('Mould in the bathroom')).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Staff queue' })).not.toBeInTheDocument()
  })

  it('filters by status for managers', async () => {
    mountApp(base, 'manager')
    expect(await screen.findByText('Mould in the bathroom')).toBeInTheDocument()
    await userEvent.selectOptions(await screen.findByLabelText('Status'), 'Done')
    await waitFor(() => expect(screen.queryByText('Mould in the bathroom')).not.toBeInTheDocument())
    expect(screen.getByText('Garden lights replaced')).toBeInTheDocument()
  })

  it('creates a ticket: category sets the default visibility, fields are validated', async () => {
    const bodies: Record<string, unknown>[] = []
    server.use(
      http.post('*/v1/orgs/:org/tickets', async ({ request }) => {
        bodies.push((await request.clone().json()) as Record<string, unknown>)
        return undefined
      }),
    )
    mountApp(`${base}/new`, 'resident')
    await userEvent.click(await screen.findByRole('button', { name: 'Open ticket' }))
    expect((await screen.findAllByRole('alert'))[0]).toHaveTextContent('Choose a category')
    expect(bodies).toHaveLength(0)

    await userEvent.selectOptions(screen.getByLabelText('Category'), 'Elevator')
    expect(
      await screen.findByText('This will be a common ticket, visible to the whole site.'),
    ).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Title'), 'Elevator stuck')
    await userEvent.type(screen.getByLabelText('Description'), 'It stops between floors.')
    await userEvent.click(screen.getByRole('button', { name: 'Open ticket' }))

    // lands on the new ticket's detail page
    expect(await screen.findByRole('heading', { name: 'Elevator stuck' })).toBeInTheDocument()
    expect(bodies[0]).toMatchObject({ title: 'Elevator stuck', category_id: 'cat-3' })
    expect(db.tickets.at(-1)?.visibility).toBe('common')
  })

  it('posts a public comment; internal notes are offered to staff only', async () => {
    mountApp(`${base}/t-2`, 'resident')
    expect(
      await screen.findByRole('heading', { name: 'Leaking tap in the kitchen' }),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText('Comment visibility')).not.toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Comment'), 'It is getting worse')
    await userEvent.click(screen.getByRole('button', { name: 'Post comment' }))
    expect(await screen.findByText('It is getting worse')).toBeInTheDocument()
  })

  it('shows internal notes to a manager and hides them from the reporter', async () => {
    db.role = 'manager'
    db.comments.push({
      id: 'c-int',
      ticket_id: 't-2',
      author_person_id: 'p-manager',
      author_display_name: 'Mehmet Kaya',
      body: 'Plumber is expensive, wait for the board',
      visibility: 'internal',
      attachments: [],
      created_at: new Date().toISOString(),
    })
    mountApp(`${base}/t-2`, 'manager')
    expect(await screen.findByText('Plumber is expensive, wait for the board')).toBeInTheDocument()
    const timeline = screen.getByRole('list', { name: 'Timeline' })
    expect(within(timeline).getByText('Internal note')).toBeInTheDocument()
  })

  it('hides internal notes from the resident', async () => {
    db.comments.push({
      id: 'c-int',
      ticket_id: 't-2',
      author_person_id: 'p-manager',
      author_display_name: 'Mehmet Kaya',
      body: 'Secret note',
      visibility: 'internal',
      attachments: [],
      created_at: new Date().toISOString(),
    })
    mountApp(`${base}/t-2`, 'resident')
    await screen.findByRole('heading', { name: 'Leaking tap in the kitchen' })
    expect(screen.queryByText('Secret note')).not.toBeInTheDocument()
  })

  it('lets staff change the status through the site workflow and the change shows in the timeline', async () => {
    mountApp(`${base}/t-seed`, 'staff')
    await screen.findByRole('heading', { name: 'Broken lamp in the entrance' })
    await userEvent.selectOptions(await screen.findByLabelText('Change status'), 'Done')
    await userEvent.type(screen.getByLabelText('Note with the status change'), 'Lamp replaced')
    await userEvent.click(screen.getByRole('button', { name: 'Update status' }))
    const timeline = await screen.findByRole('list', { name: 'Timeline' })
    await waitFor(() =>
      expect(
        within(timeline).getByText(/Status changed from In progress to Done/),
      ).toBeInTheDocument(),
    )
    expect(within(timeline).getByText('Lamp replaced')).toBeInTheDocument()
  })

  it('does not offer status changes or assignment to a resident', async () => {
    mountApp(`${base}/t-2`, 'resident')
    await screen.findByRole('heading', { name: 'Leaking tap in the kitchen' })
    expect(screen.queryByLabelText('Change status')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Assigned to')).not.toBeInTheDocument()
  })

  it('"+1" on a common ticket raises the count and can be undone', async () => {
    mountApp(`${base}/t-3`, 'resident')
    await screen.findByRole('heading', { name: 'Elevator makes a noise' })
    const button = screen.getByRole('button', { name: /Me too/ })
    expect(button).toHaveTextContent('+1')
    await userEvent.click(button)
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Me too/ })).toHaveTextContent('+2'),
    )
    expect(screen.getByRole('button', { name: /Me too/ })).toHaveAttribute('aria-pressed', 'true')
    await userEvent.click(screen.getByRole('button', { name: /Me too/ }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Me too/ })).toHaveTextContent('+1'),
    )
  })

  it('merges a duplicate into another ticket (manager)', async () => {
    mountApp(`${base}/t-3`, 'manager')
    await screen.findByRole('heading', { name: 'Elevator makes a noise' })
    await userEvent.click(screen.getByRole('button', { name: 'Merge into another ticket' }))
    await userEvent.selectOptions(
      await screen.findByLabelText('Keep this ticket'),
      'Broken lamp in the entrance',
    )
    await userEvent.click(screen.getByRole('button', { name: 'Merge' }))
    expect(await screen.findByText('This ticket was merged into another one.')).toBeInTheDocument()
  })

  it('shows the no-permission state when /tickets answers 403', async () => {
    server.use(
      http.get('*/v1/orgs/:org/tickets', () =>
        HttpResponse.json({ status: 403, code: 'insufficient_permission' }, { status: 403 }),
      ),
    )
    mountApp(base, 'manager')
    expect(await screen.findByText('You do not have access to this')).toBeInTheDocument()
  })

  it('shows the staff queue only to staff', async () => {
    mountApp(`${base}/queue`, 'staff')
    expect(await screen.findByText('Broken lamp in the entrance')).toBeInTheDocument()
    expect(screen.queryByText('Leaking tap in the kitchen')).not.toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Staff queue' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
  })
})
