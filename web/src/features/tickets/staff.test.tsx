import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mountApp } from '@/test/app'

const base = '/orgs/org-1/tickets'

describe('staff (tickets.work)', () => {
  it('lands on the staff queue by default, not on "My tickets"', async () => {
    mountApp(base, 'staff')
    expect(await screen.findByText('Broken lamp in the entrance')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Staff queue' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
    expect(screen.getByRole('tab', { name: 'My tickets' })).toHaveAttribute(
      'aria-selected',
      'false',
    )
    expect(screen.queryByText('Elevator makes a noise')).not.toBeInTheDocument()
  })

  it('offers status changes on a ticket assigned to them', async () => {
    mountApp(`${base}/t-seed`, 'staff')
    await screen.findByRole('heading', { name: 'Broken lamp in the entrance' })
    expect(await screen.findByLabelText('Change status')).toBeInTheDocument()
  })

  it('offers no status control on a common ticket they do not hold (it would be a 403)', async () => {
    mountApp(`${base}/t-3`, 'staff')
    await screen.findByRole('heading', { name: 'Elevator makes a noise' })
    // the queue lookup settles before the page can know it is not theirs
    await new Promise((r) => setTimeout(r, 300))
    expect(screen.queryByLabelText('Change status')).not.toBeInTheDocument()
  })

  it('keeps the status control after closing their own ticket (it leaves the queue)', async () => {
    mountApp(`${base}/t-seed`, 'staff')
    await screen.findByRole('heading', { name: 'Broken lamp in the entrance' })
    await userEvent.selectOptions(await screen.findByLabelText('Change status'), 'Done')
    await userEvent.click(screen.getByRole('button', { name: 'Update status' }))
    await waitFor(() => expect(screen.getAllByText('Done').length).toBeGreaterThan(0))
    expect(await screen.findByLabelText('Change status')).toBeInTheDocument()
  })
})
