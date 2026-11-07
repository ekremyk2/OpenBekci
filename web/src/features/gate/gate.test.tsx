import { afterEach, describe, expect, it } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { recordRequests } from '@/test/gov-utils'
import { createGuardApi, getGuardSession, GUARD_HEADER, setGuardSession } from './guardApi'
import { manualOpenState } from './openState'
import { OpenButton } from './panels'
import { PinSwitch } from './PinSwitch'
import type { SiteCtx } from '@/features/meetings/kit/api'

afterEach(() => {
  server.events.removeAllListeners()
  setGuardSession(null)
})

const ctx: SiteCtx = {
  orgId: 'org-1',
  siteId: 'site-a',
  siteName: 'Site',
  currency: 'TRY',
  timeZone: 'Europe/Istanbul',
  sites: [],
  setSiteId: () => undefined,
}
const NOW = '2026-03-01T12:00:00Z'

describe('manualOpenState', () => {
  const point = { enabled: true }
  const agent = { connected: true, last_seen_at: '2026-03-01T11:59:30Z' }
  it('is enabled when permitted, enabled and the agent was heard recently', () => {
    expect(manualOpenState({ canOpen: true, point, agent, serverTime: NOW })).toEqual({ enabled: true, reason: null })
  })
  it('says why it is disabled', () => {
    expect(manualOpenState({ canOpen: false, point, agent, serverTime: NOW }).reason).toBe('no_permission')
    expect(manualOpenState({ canOpen: true, point: { enabled: false }, agent, serverTime: NOW }).reason).toBe('point_disabled')
    expect(manualOpenState({ canOpen: true, point, agent: { ...agent, connected: false }, serverTime: NOW }).reason).toBe('agent_offline')
    expect(manualOpenState({ canOpen: true, point, agent: { connected: true, last_seen_at: '2026-03-01T11:50:00Z' }, serverTime: NOW }).reason).toBe('agent_offline')
    expect(manualOpenState({ canOpen: true, point, agent: null, serverTime: NOW }).reason).toBe('agent_offline')
    expect(manualOpenState({ canOpen: true, point, agent, serverTime: NOW, clockSuspect: true }).reason).toBe('clock_suspect')
  })
})

function renderOpen(over: { canOpen?: boolean; agent?: { connected: boolean; last_seen_at: string | null } | null; clockSuspect?: boolean }) {
  const qc = new QueryClient()
  render(
    <I18nProvider i18n={i18n}><QueryClientProvider client={qc}>
      <OpenButton
        ctx={ctx}
        point={{ id: 'pt-main', name: 'Main gate', kind: 'vehicle_gate', direction: 'both', enabled: true }}
        console={{ server_time: NOW, agent: over.agent === undefined ? { connected: true, last_seen_at: '2026-03-01T11:59:50Z', points: [] } : over.agent && { ...over.agent, points: [] } }}
        canOpen={over.canOpen ?? true}
        clockSuspect={over.clockSuspect}
      />
    </QueryClientProvider></I18nProvider>,
  )
}

describe('manual open button', () => {
  it('is usable when everything is fine', () => {
    renderOpen({})
    expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeEnabled()
  })
  it('is disabled with the reason when the agent is offline', () => {
    renderOpen({ agent: { connected: false, last_seen_at: null } })
    expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeDisabled()
    expect(screen.getByText('The gate agent is offline.')).toBeInTheDocument()
  })
  it('is disabled with the reason when the clock is suspect', () => {
    renderOpen({ clockSuspect: true })
    expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeDisabled()
    expect(screen.getByText(/clock is wrong/)).toBeInTheDocument()
  })
  it('is disabled without access.open', () => {
    renderOpen({ canOpen: false })
    expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeDisabled()
    expect(screen.getByText('You may not open gates.')).toBeInTheDocument()
  })
})

describe('guard session header injection', () => {
  it('adds X-Guard-Session to guard calls, but not to the PIN endpoints', async () => {
    const seen = recordRequests()
    const api = createGuardApi(() => 'gs-token-1', () => undefined, 'http://localhost')
    await api.GET('/v1/orgs/{org_id}/gate/console', { params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } } })
    await api.GET('/v1/orgs/{org_id}/gate/guards', { params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } } })
    const consoleCall = seen.find((r) => r.url.includes('/gate/console'))!
    const guardsCall = seen.find((r) => r.url.includes('/gate/guards'))!
    expect(consoleCall.headers.get(GUARD_HEADER)).toBe('gs-token-1')
    expect(guardsCall.headers.get(GUARD_HEADER)).toBeNull()
  })
  it('an invalid_grant on a guard call drops the session instead of signing the booth out', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/gate/console', () =>
        HttpResponse.json({ status: 401, code: 'invalid_grant' }, { status: 401, headers: { 'content-type': 'application/problem+json' } }),
      ),
    )
    let lost = 0
    const api = createGuardApi(() => 'old', () => void lost++, 'http://localhost')
    await expect(
      api.GET('/v1/orgs/{org_id}/gate/console', { params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } } }),
    ).rejects.toMatchObject({ status: 401 })
    expect(lost).toBe(1)
  })
})

describe('PIN switch', () => {
  function mount() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const api = createGuardApi(undefined, undefined, 'http://localhost')
    render(
      <I18nProvider i18n={i18n}><QueryClientProvider client={qc}>
        <PinSwitch ctx={ctx} api={api} />
      </QueryClientProvider></I18nProvider>,
    )
  }
  it('picks a guard, rejects a wrong PIN and starts a session with the right one', async () => {
    mount()
    await userEvent.click(await screen.findByRole('button', { name: 'Hasan Çelik' }))
    await userEvent.type(screen.getByLabelText('PIN'), '9999')
    await userEvent.click(screen.getByRole('button', { name: 'Start the shift' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong PIN')
    expect(getGuardSession()).toBeNull()

    await userEvent.type(screen.getByLabelText('PIN'), '1234')
    await userEvent.click(screen.getByRole('button', { name: 'Start the shift' }))
    await waitFor(() => expect(getGuardSession()?.displayName).toBe('Hasan Çelik'))
  })
})
