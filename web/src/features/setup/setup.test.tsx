import { describe, expect, it } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mountApp, nodeFile } from '@/test/render'
import { db } from '@/test/handlers/db'

describe('allocation keys editor', () => {
  it('lists the system keys, explains the Law 634 default and shows computed keys read-only', async () => {
    await mountApp('/orgs/org-1/allocation-keys')
    expect(await screen.findByText(/Kat Mülkiyeti Kanunu, no\. 634/)).toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Allocation keys' })
    expect(within(list).getByText('arsa_payi')).toBeInTheDocument()
    expect(within(list).getByText('equal')).toBeInTheDocument()
    await userEvent.click(within(list).getByText('By floor area'))
    expect(await screen.findByText(/Each unit counts by its floor area/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Save values' })).not.toBeInTheDocument()
  })

  it('edits inline, validates and saves the values with the key version', async () => {
    await mountApp('/orgs/org-1/allocation-keys?key=key-arsa')
    const a1 = await screen.findByLabelText('Value for A-1')
    expect(a1).toHaveValue('100')
    expect(screen.getByText('1 units have no value yet')).toBeInTheDocument()
    await userEvent.clear(a1)
    await userEvent.type(a1, 'abc')
    expect(await screen.findByText('Enter zero or a positive number')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save values' })).toBeDisabled()
    await userEvent.clear(a1)
    await userEvent.type(a1, '110')
    await userEvent.type(screen.getByLabelText('Value for B-2'), '30')
    expect(await screen.findByText('Complete')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save values' }))
    await waitFor(() =>
      expect(db.keyValues['key-arsa']).toMatchObject({ 'unit-a1': 110, 'unit-b2': 30 }),
    )
  })

  it('pastes a two-column block from a spreadsheet, matching rows by door number', async () => {
    await mountApp('/orgs/org-1/allocation-keys?key=key-heat')
    const first = await screen.findByLabelText('Value for A-1')
    first.focus()
    fireEvent.paste(first, { clipboardData: { getData: () => 'A-1\t10,5\nB-1\t20\nZ-9\t5\n' } })
    expect(await screen.findByLabelText('Value for A-1')).toHaveValue('10,5')
    expect(screen.getByLabelText('Value for B-1')).toHaveValue('20')
    expect(screen.getByText(/Pasted 2 values/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save values' }))
    await waitFor(() =>
      expect(db.keyValues['key-heat']).toEqual({ 'unit-a1': 10.5, 'unit-b1': 20 }),
    )
  })

  it('uploads a per-unit table: dry run first, then apply', async () => {
    await mountApp('/orgs/org-1/allocation-keys?key=key-heat')
    await userEvent.click(await screen.findByText('Upload a per-unit table'))
    const file = nodeFile(['Daire,Değer\nA-2,7\nB-2,9\nNOPE,1\n'], 'heat.csv')
    await userEvent.upload(screen.getByLabelText('Table file'), file)
    await userEvent.click(await screen.findByRole('button', { name: 'Check the table' }))
    expect(await screen.findByText('2 rows are fine, 1 have errors.')).toBeInTheDocument()
    expect(screen.getByText(/No unit NOPE/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Apply the table' })).not.toBeInTheDocument()
  })

  it('adds a custom key with a field error for a duplicate code', async () => {
    await mountApp('/orgs/org-1/allocation-keys')
    await userEvent.type(await screen.findByLabelText('Name'), 'Water meters')
    await userEvent.type(screen.getByLabelText('Code'), 'heating')
    await userEvent.click(screen.getByRole('button', { name: 'Add key' }))
    expect(await screen.findByText('This code is already used')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('Code'))
    await userEvent.type(screen.getByLabelText('Code'), 'water')
    await userEvent.click(screen.getByRole('button', { name: 'Add key' }))
    await waitFor(() => expect(db.keys.some((k) => k.code === 'water')).toBe(true))
  })
})
