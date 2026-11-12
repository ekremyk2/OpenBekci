import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp } from '@/test/app'
import { db } from '@/test/handlers/operations'
import { useUnreadCount, UNREAD_POLL_MS } from './NotificationBell'

function Probe() {
  const { data } = useUnreadCount('org-1')
  return <output data-testid="count">{data?.unread ?? '-'}</output>
}

describe('unread count polling', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('polls every 60 seconds while the tab is visible and pauses while hidden', async () => {
    let calls = 0
    let unread = 3
    server.use(
      http.get('*/v1/orgs/:org/me/notifications/unread-count', () => {
        calls += 1
        return HttpResponse.json({ unread, visitor_approvals_pending: 0 })
      }),
    )
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('3'))
    expect(calls).toBe(1)

    unread = 5
    await act(async () => {
      await vi.advanceTimersByTimeAsync(UNREAD_POLL_MS + 100)
    })
    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('5'))
    expect(calls).toBe(2)

    // hidden tab: no further requests
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(UNREAD_POLL_MS * 3)
    })
    expect(calls).toBe(2)
  })
})

describe('notification center', () => {
  it('shows the unread badge in the shell and lists notifications', async () => {
    mountApp('/orgs/org-1/notifications', 'resident')
    expect(await screen.findByText('Broken lamp in the entrance')).toBeInTheDocument()
    expect(screen.getByText('Water cut on Saturday')).toBeInTheDocument()
    expect(await screen.findByTestId('unread-badge')).toHaveTextContent('2')
  })

  it('marks one notification read when opened, and all with one click', async () => {
    mountApp('/orgs/org-1/notifications', 'resident')
    await userEvent.click(await screen.findByRole('button', { name: /Water cut on Saturday/ }))
    await waitFor(() => expect(db.notifications.find((n) => n.id === 'n-2')?.read_at).toBeTruthy())

    await userEvent.click(screen.getByRole('button', { name: 'Mark all as read' }))
    await waitFor(() => expect(db.notifications.every((n) => n.read_at)).toBe(true))
    await waitFor(() => expect(screen.queryByTestId('unread-badge')).not.toBeInTheDocument())
  })

  it('shows the empty state when everything is read and only unread are requested', async () => {
    for (const n of db.notifications) n.read_at = new Date().toISOString()
    mountApp('/orgs/org-1/notifications', 'resident')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'Unread only' }))
    expect(await screen.findByText('You are all caught up')).toBeInTheDocument()
  })

  it('mutes a type but never lets sign-in codes be muted', async () => {
    mountApp('/orgs/org-1/notifications', 'resident')
    await userEvent.click(await screen.findByRole('tab', { name: 'Preferences' }))
    const receipts = await screen.findByRole('checkbox', { name: 'Payment receipts' })
    await userEvent.click(receipts)
    await waitFor(() => expect(db.mutes).toEqual(['payment.receipt']))
    expect(screen.getByRole('checkbox', { name: /Sign-in codes/ })).toBeDisabled()
  })
})

describe('site defaults for automatic messages', () => {
  it('edits the channels of a type; locked types are fixed', async () => {
    mountApp('/orgs/org-1/settings/notifications', 'manager')
    const receiptSms = await screen.findByRole('checkbox', { name: 'Payment receipts: SMS' })
    expect(receiptSms).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Sign-in codes: SMS' })).toBeDisabled()
    expect(await screen.findByText(/SMS this month: 412 sent/)).toBeInTheDocument()

    await userEvent.click(receiptSms)
    await userEvent.click(screen.getByRole('button', { name: 'Save defaults' }))
    await waitFor(() =>
      expect(db.defaults.find((d) => d.type === 'payment.receipt')?.channels).toEqual(['in_app']),
    )
  })

  it('is not available to residents', async () => {
    mountApp('/orgs/org-1/settings/notifications', 'resident')
    expect(await screen.findByText('You do not have access to this page')).toBeInTheDocument()
  })
})
