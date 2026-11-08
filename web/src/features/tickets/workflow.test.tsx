import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'
import { move, validateRows, type Row } from './WorkflowEditor'

const row = (name: string, semantic: Row['semantic'] = 'open_like'): Row => ({
  uid: name,
  key: null,
  name,
  semantic,
})

describe('workflow helpers', () => {
  it('moves a row and ignores out-of-range moves', () => {
    const rows = [row('a'), row('b'), row('c')]
    expect(move(rows, 2, 0).map((r) => r.name)).toEqual(['c', 'a', 'b'])
    expect(move(rows, 0, -1)).toBe(rows)
  })

  it('validates names and semantics', () => {
    expect(validateRows([row('a'), row(' ')])).toBe('empty_name')
    expect(validateRows([row('a'), row('A')])).toBe('duplicate')
    expect(validateRows([row('a', 'closed_like')])).toBe('no_open')
    expect(validateRows([row('a')])).toBe('no_closed')
    expect(validateRows([row('a'), row('b', 'closed_like')])).toBeNull()
  })
})

describe('workflow editor', () => {
  it('reorders statuses, adds one and saves the complete ordered list with If-Match', async () => {
    let body: { statuses: { id?: string; name: string; semantic: string }[] } | undefined
    let ifMatch: string | null = null
    server.use(
      http.put('*/v1/orgs/:org/ticket-workflow', async ({ request }) => {
        body = (await request.clone().json()) as typeof body
        ifMatch = request.headers.get('If-Match')
        return undefined
      }),
    )
    mountApp('/orgs/org-1/settings/tickets', 'manager')
    await screen.findByDisplayValue('In progress')

    await userEvent.click(screen.getByRole('button', { name: 'Move In progress up' }))
    await userEvent.click(screen.getByRole('button', { name: 'Add status' }))
    const newName = screen.getByLabelText('Status name 4')
    await userEvent.type(newName, 'Waiting for parts')
    await userEvent.click(screen.getByRole('button', { name: 'Save workflow' }))

    await waitFor(() => expect(body).toBeDefined())
    expect(body!.statuses.map((s) => s.name)).toEqual([
      'In progress',
      'Open',
      'Done',
      'Waiting for parts',
    ])
    expect(body!.statuses[3]).toEqual({ name: 'Waiting for parts', semantic: 'open_like' })
    expect(ifMatch).toBe('"1"')
    await waitFor(() =>
      expect(db.workflow.statuses.map((s) => s.name)).toContain('Waiting for parts'),
    )
  })

  it('asks where tickets of a removed status go before saving', async () => {
    mountApp('/orgs/org-1/settings/tickets', 'manager')
    await screen.findByDisplayValue('In progress')
    await userEvent.click(screen.getByRole('button', { name: 'Remove In progress' }))
    expect(await screen.findByText('Where do tickets of removed statuses go?')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save workflow' }))
    expect(
      await screen.findByText(/Choose where the tickets of "In progress" move to/),
    ).toBeInTheDocument()
    expect(db.workflow.version).toBe(1)
    await userEvent.selectOptions(screen.getByLabelText('Replacement for In progress'), 'Open')
    await userEvent.click(screen.getByRole('button', { name: 'Save workflow' }))
    await waitFor(() => expect(db.workflow.version).toBe(2))
  })

  it('blocks saving a workflow without a closed-like status', async () => {
    mountApp('/orgs/org-1/settings/tickets', 'manager')
    await screen.findByDisplayValue('Done')
    await userEvent.selectOptions(screen.getByLabelText('Meaning of status 3'), 'Open-like')
    expect(await screen.findByText('Keep at least one closed-like status.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save workflow' })).toBeDisabled()
  })

  it('saves category defaults', async () => {
    mountApp('/orgs/org-1/settings/tickets', 'manager')
    const select = await screen.findByLabelText('Default visibility of Plumbing')
    await userEvent.selectOptions(select, 'Common by default')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' })[0]!)
    await waitFor(() =>
      expect(db.categories.find((c) => c.name === 'Plumbing')?.default_visibility).toBe('common'),
    )
  })

  it('shows no-permission for a resident', async () => {
    mountApp('/orgs/org-1/settings/tickets', 'resident')
    expect(await screen.findByText('You do not have access to this')).toBeInTheDocument()
  })

  it('shows an error state with retry when the workflow cannot load', async () => {
    server.use(
      http.get('*/v1/orgs/:org/ticket-workflow', () =>
        HttpResponse.json({ status: 500, code: 'internal_error' }, { status: 500 }),
      ),
    )
    mountApp('/orgs/org-1/settings/tickets', 'manager')
    // 5xx is retried twice with back-off before the error state shows
    expect(
      await screen.findByText('Something went wrong', {}, { timeout: 8000 }),
    ).toBeInTheDocument()
  })
})
