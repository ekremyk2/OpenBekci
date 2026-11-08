import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { m } from '@/test/handlers/money-db'
import { server } from '@/test/setup'
import { mountMoney, recordKeys } from '@/test/money-utils'

afterEach(() => server.events.removeAllListeners())

const row = async (name: RegExp) =>
  within(await screen.findByRole('table', { name: 'Accrued late fees' })).findByRole('row', {
    name,
  })

describe('late fees', () => {
  it('shows the accrued fee per unit side', async () => {
    mountMoney('/orgs/org-1/money/late-fees')
    const r = await row(/A3.*Owner/)
    expect(within(r).getAllByText(/₺|TRY/).length).toBeGreaterThan(0)
    expect(within(r).getByRole('button', { name: 'Waive' })).toBeEnabled()
  })

  it('waives above the threshold only after re-authentication, replaying with the same key', async () => {
    const seen = recordKeys()
    const { db } = mountMoney('/orgs/org-1/money/late-fees')
    db.lateFeePolicy.waiver_reauth_threshold = m(1000) // 10.00: any real waiver needs a fresh sign-in
    const r = await row(/A3.*Owner/)
    const accruedBefore = Number(db.waivers.length)
    await userEvent.click(within(r).getByRole('button', { name: 'Waive' }))

    const dialog = await screen.findByRole('dialog', { name: 'Waive late fee' })
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Hardship, agreed with the board')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Waive fee' }))

    const reauth = await screen.findByRole('dialog', { name: 'Confirm it is you' })
    expect(db.waivers).toHaveLength(accruedBefore) // nothing posted yet
    await userEvent.type(within(reauth).getByLabelText('Password'), 'correct horse')
    await userEvent.click(within(reauth).getByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(db.waivers).toHaveLength(accruedBefore + 1))
    const posts = seen.filter((x) => x.method === 'POST' && x.url.endsWith('/late-fee-waivers'))
    expect(posts).toHaveLength(2) // refused once, replayed once
    expect(posts[1]!.key).toBe(posts[0]!.key)
    expect(seen.some((x) => x.url.endsWith('/v1/auth/reauth'))).toBe(true)

    // the waiver lowered the accrued fee: the row is gone (nonzero filter) or shows zero
    await waitFor(() =>
      expect(
        within(screen.getByRole('table', { name: 'Accrued late fees' })).queryByRole('row', {
          name: /A3.*Owner/,
        }),
      ).not.toBeInTheDocument(),
    )
  })

  it('does not ask for re-authentication below the threshold', async () => {
    const { db } = mountMoney('/orgs/org-1/money/late-fees')
    db.lateFeePolicy.waiver_reauth_threshold = m(10_000_000)
    const r = await row(/B2.*Owner/)
    await userEvent.click(within(r).getByRole('button', { name: 'Waive' }))
    const dialog = await screen.findByRole('dialog', { name: 'Waive late fee' })
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Goodwill')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Waive fee' }))
    await waitFor(() => expect(db.waivers).toHaveLength(1))
    expect(screen.queryByRole('dialog', { name: 'Confirm it is you' })).not.toBeInTheDocument()
  })

  it('freezes accrual for a unit side', async () => {
    const { db } = mountMoney('/orgs/org-1/money/late-fees')
    const r = await row(/A3.*Owner/)
    await userEvent.click(within(r).getByRole('button', { name: 'Freeze' }))
    const dialog = await screen.findByRole('dialog', { name: 'Freeze late fees' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Freeze' }))
    expect(await within(dialog).findByText('A reason is required.')).toBeInTheDocument()
    // the form defaults to the real today; the model's clock is fixed, so state the date
    const from = within(dialog).getByLabelText('From')
    await userEvent.clear(from)
    await userEvent.type(from, '2026-11-01')
    await userEvent.type(within(dialog).getByLabelText('Reason'), 'Legal collection')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Freeze' }))
    await waitFor(() => expect(db.freezes).toHaveLength(1))
    // the list refetches after the write: look the row up again until it shows the freeze
    await waitFor(async () => {
      const frozen = await row(/A3.*Owner/)
      expect(within(frozen).getByText('Frozen')).toBeInTheDocument()
      expect(within(frozen).getByRole('button', { name: 'Unfreeze' })).toBeInTheDocument()
    })
  })

  it('rejects a rate above the statutory cap in the policy form', async () => {
    mountMoney('/orgs/org-1/money/late-fees')
    const rate = await screen.findByLabelText('Rate per month (%)')
    await userEvent.clear(rate)
    await userEvent.type(rate, '7')
    await userEvent.click(screen.getByRole('button', { name: 'Save policy' }))
    expect(await screen.findByText(/statutory cap of 5% per month/)).toBeInTheDocument()
  })
})
