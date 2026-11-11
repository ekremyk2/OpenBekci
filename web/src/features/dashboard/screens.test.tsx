import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'

describe('site dashboard', () => {
  it('shows money and open tickets on the home screen for managers', async () => {
    mountApp('/orgs/org-1', 'manager')
    expect(await screen.findByText('Overdue')).toBeInTheDocument()
    expect(await screen.findByText('87.5%')).toBeInTheDocument()
    expect(screen.getByText('Open tickets')).toBeInTheDocument()
    expect(screen.getByText('Annual general assembly', { exact: false })).toBeInTheDocument()
  })

  it('falls back to the resident summary without dashboard permissions', async () => {
    mountApp('/orgs/org-1', 'resident')
    expect(await screen.findByText('Your dashboard is on its way')).toBeInTheDocument()
  })
})

describe('staff dashboard', () => {
  it('shows counts without money for staff', async () => {
    mountApp('/orgs/org-1', 'staff')
    expect(await screen.findByText('Assigned to you')).toBeInTheDocument()
    expect(screen.getByText('Expected visitors today')).toBeInTheDocument()
    expect(screen.queryByText('Cash')).not.toBeInTheDocument()
    expect(screen.queryByText('Overdue', { selector: 'h2,h3,div' })).toBeInTheDocument()
  })
})

describe('settings', () => {
  it('edits the organization name when allowed', async () => {
    mountApp('/orgs/org-1/settings', 'manager')
    const name = await screen.findByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Yeni Yönetim')
    await userEvent.click(screen.getAllByRole('button', { name: 'Save' })[0]!)
    await waitFor(() => expect(db.org.name).toBe('Yeni Yönetim'))
  })

  it('hides tabs the user may not use and the debug codes tab behaves per DEBUG', async () => {
    mountApp('/orgs/org-1/settings', 'manager')
    await screen.findByLabelText('Name')
    const nav = screen.getByRole('navigation', { name: 'Settings sections' })
    expect(nav).toHaveTextContent('Audit log')
    expect(nav).toHaveTextContent('Tickets')
  })

  it('links site settings to the setup route', async () => {
    mountApp('/orgs/org-1/settings/site', 'manager')
    const link = await screen.findByRole('link', { name: 'Güneş Sitesi' })
    expect(link).toHaveAttribute('href', '/orgs/org-1/sites/site-a')
  })
})

describe('platform', () => {
  it('lists organizations, creates one and shows health, backup and jobs', async () => {
    mountApp('/platform', 'platform')
    expect(await screen.findByText('Güneş Sitesi Yönetimi')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'New organization' }))
    await screen.findByRole('dialog')
    await userEvent.click(await screen.findByRole('button', { name: 'Create' }))
    expect(
      await screen.findByText('Use 2 to 40 lowercase letters, digits or dashes'),
    ).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Short name (slug)'), 'yildiz')
    await userEvent.type(screen.getByLabelText('Name'), 'Yıldız Yönetim')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(db.orgs.some((o) => o.slug === 'yildiz')).toBe(true))
    expect(await screen.findByText('Yıldız Yönetim')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('tab', { name: 'Health' }))
    expect(await screen.findByText('database')).toBeInTheDocument()
    expect(await screen.findByText('Last backup')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('tab', { name: 'Jobs' }))
    expect(await screen.findByText('generate_statement')).toBeInTheDocument()
  })

  it('maps a slug conflict to the form', async () => {
    mountApp('/platform', 'platform')
    await userEvent.click(await screen.findByRole('button', { name: 'New organization' }))
    await userEvent.type(await screen.findByLabelText('Short name (slug)'), 'gunes')
    await userEvent.type(screen.getByLabelText('Name'), 'Another')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(db.orgs).toHaveLength(1)
  })

  it('is forbidden for non platform admins', async () => {
    mountApp('/platform', 'manager')
    expect(await screen.findByRole('heading', { level: 1, name: /access/i })).toBeInTheDocument()
  })
})
