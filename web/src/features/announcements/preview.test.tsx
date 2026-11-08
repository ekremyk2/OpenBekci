import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { mountApp } from '@/test/app'
import { PREVIEW, spy } from './test-helpers'

describe('delivery preview (B1)', () => {
  it('sends no Idempotency-Key, so changing audience or channels never trips the key check', async () => {
    const calls = spy(PREVIEW)
    mountApp('/orgs/org-1/announcements/new', 'manager')
    const panel = await screen.findByRole('status', { name: 'Delivery preview' })
    await waitFor(() => expect(within(panel).getByText('Recipients: 120')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('checkbox', { name: 'SMS' }))
    await waitFor(() => expect(within(panel).getByText(/SMS messages: 108/)).toBeInTheDocument())
    await userEvent.selectOptions(screen.getByLabelText('Audience'), 'Owners only')
    await waitFor(() => expect(within(panel).getByText('Recipients: 70')).toBeInTheDocument())

    expect(calls.length).toBeGreaterThanOrEqual(3)
    expect(calls.every((c) => c.key === null)).toBe(true)
    expect(within(panel).queryByText('Could not count the recipients.')).not.toBeInTheDocument()
  })

  it('the mock rejects one Idempotency-Key reused with a different body, like the real server', async () => {
    mountApp('/orgs/org-1/announcements', 'manager')
    const send = (channels: string[]) =>
      fetch(new URL('/v1/orgs/org-1/announcements:preview', location.href), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': 'same-key' },
        body: JSON.stringify({ site_id: 'site-a', audience: { kind: 'site' }, channels }),
      })
    expect((await send(['in_app'])).status).toBe(200)
    expect((await send(['in_app'])).status).toBe(200) // same body: a replay
    const clash = await send(['in_app', 'sms'])
    expect(clash.status).toBe(422)
    expect(await clash.json()).toMatchObject({ code: 'idempotency_mismatch' })
  })
})
