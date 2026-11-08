import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'

describe('documents', () => {
  it('shows folders with their visibility and hides board-only folders from residents', async () => {
    mountApp('/orgs/org-1/documents', 'resident')
    expect(await screen.findByRole('link', { name: /Management plan/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Assembly minutes/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Unit A Blok 12 \(private\)/ })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Financial reports/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'New folder' })).not.toBeInTheDocument()
  })

  it('shows generated documents with their source badge and a breadcrumb', async () => {
    mountApp('/orgs/org-1/documents/f/f-finance', 'manager')
    expect(await screen.findByText('Statement March 2026')).toBeInTheDocument()
    expect(screen.getByText('Generated: statement')).toBeInTheDocument()
    const crumbs = screen.getByRole('navigation', { name: 'Folder path' })
    expect(within(crumbs).getByText('Financial reports')).toHaveAttribute('aria-current', 'page')
  })

  it('edits folder visibility: chosen units need at least one unit, then saves with If-Match', async () => {
    let body: unknown
    let ifMatch: string | null = null
    server.use(
      http.patch('*/v1/orgs/:org/folders/:id', async ({ request }) => {
        body = await request.clone().json()
        ifMatch = request.headers.get('If-Match')
        return undefined
      }),
    )
    mountApp('/orgs/org-1/documents/f/f-plan', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Folder settings' }))
    const dialog = await screen.findByRole('dialog')
    const visibleTo = within(dialog).getByRole('combobox', { name: 'Visible to' })
    expect(visibleTo).toHaveValue('everyone')

    await userEvent.selectOptions(visibleTo, 'Chosen units')
    expect(await within(dialog).findByText('Choose at least one unit.')).toBeInTheDocument()
    const save = within(dialog).getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()

    await userEvent.click(await within(dialog).findByRole('checkbox', { name: 'A-1' }))
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'A-2' }))
    expect(save).toBeEnabled()
    await userEvent.click(save)

    await waitFor(() => expect(body).toBeDefined())
    expect(body).toMatchObject({ visibility: { level: 'units', unit_ids: ['unit-a1', 'unit-a2'] } })
    expect(ifMatch).toBe('"1"')
    await waitFor(() =>
      expect(db.folders.find((f) => f.id === 'f-plan')?.visibility).toEqual({
        level: 'units',
        unit_ids: ['unit-a1', 'unit-a2'],
      }),
    )
  })

  it('creates a folder inside the current one', async () => {
    mountApp('/orgs/org-1/documents/f/f-plan', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'New folder' }))
    await userEvent.type(await screen.findByLabelText('Name'), 'Insurance')
    await userEvent.click(screen.getByRole('button', { name: 'Create folder' }))
    await waitFor(() => expect(db.folders.some((f) => f.name === 'Insurance')).toBe(true))
    expect(db.folders.find((f) => f.name === 'Insurance')).toMatchObject({
      parent_id: 'f-plan',
      visibility: { level: 'everyone' },
    })
  })

  it('previews a PDF in an iframe from the content URL', async () => {
    mountApp('/orgs/org-1/documents/f/f-plan', 'manager')
    await userEvent.click(await screen.findByRole('button', { name: 'Management plan 2026' }))
    const frame = await screen.findByTitle('Preview of Management plan 2026')
    expect(frame).toHaveAttribute('src', '/v1/orgs/org-1/files/file-seed-pdf2')
  })

  it('lets a resident upload into their own unit folder only', async () => {
    mountApp('/orgs/org-1/documents/f/f-unit12', 'resident')
    expect(await screen.findByText('Choose files')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'New folder' })).not.toBeInTheDocument()
  })

  it('does not offer upload in a site folder to a resident', async () => {
    mountApp('/orgs/org-1/documents/f/f-plan', 'resident')
    expect(await screen.findByText('Management plan 2026')).toBeInTheDocument()
    expect(screen.queryByText('Choose files')).not.toBeInTheDocument()
  })
})
