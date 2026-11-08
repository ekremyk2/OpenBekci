import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'

describe('workflow editor: restore default', () => {
  it('only proposes: nothing is saved until the dialog is confirmed, and renamed defaults get their names back', async () => {
    db.workflow.statuses[0]!.name = 'Reported'
    db.workflow.statuses.push({
      id: 'st-wait',
      name: 'Waiting for parts',
      semantic: 'open_like',
      position: 3,
      key: null,
    })
    const puts: unknown[] = []
    server.use(
      http.put('*/v1/orgs/:org/ticket-workflow', async ({ request }) => {
        puts.push(await request.clone().json())
        return undefined
      }),
    )
    mountApp('/orgs/org-1/settings/tickets', 'manager')
    await screen.findByDisplayValue('Reported')

    await userEvent.click(screen.getByRole('button', { name: 'Restore default workflow' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Restore the default workflow?' })
    expect(within(dialog).getByText('Open')).toBeInTheDocument()
    expect(
      within(dialog).getByText(/Tickets of "Waiting for parts" move to "Open"/),
    ).toBeInTheDocument()
    expect(puts).toHaveLength(0)

    // cancel: the editor is untouched
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.getByDisplayValue('Reported')).toBeInTheDocument()
    expect(puts).toHaveLength(0)

    await userEvent.click(screen.getByRole('button', { name: 'Restore default workflow' }))
    await userEvent.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Restore and save',
      }),
    )
    await waitFor(() => expect(puts).toHaveLength(1))
    await waitFor(() =>
      expect(db.workflow.statuses.map((s) => s.name)).toEqual(['Open', 'In progress', 'Done']),
    )
  })
})
