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
import { agentOnline, manualOpenState } from './openState'
import { OpenButton } from './panels'
import { ExitGate } from './ExitGate'
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
  const point = { enabled: true, online: true, clock_suspect: false }
  const agent = { online: true, clock_suspect: false }
  it('is enabled when permitted, enabled and the agent is online', () => {
    expect(manualOpenState({ canOpen: true, point, agent })).toEqual({
      enabled: true,
      reason: null,
    })
  })
  it('says why it is disabled', () => {
    expect(manualOpenState({ canOpen: false, point, agent }).reason).toBe('no_permission')
    expect(manualOpenState({ canOpen: true, point: { enabled: false }, agent }).reason).toBe(
      'point_disabled',
    )
    expect(
      manualOpenState({ canOpen: true, point, agent: { ...agent, online: false } }).reason,
    ).toBe('agent_offline')
    expect(manualOpenState({ canOpen: true, point, agent: null }).reason).toBe('agent_offline')
    expect(
      manualOpenState({ canOpen: true, point: { ...point, online: false }, agent }).reason,
    ).toBe('point_offline')
    expect(manualOpenState({ canOpen: true, point, agent, clockSuspect: true }).reason).toBe(
      'clock_suspect',
    )
    expect(
      manualOpenState({ canOpen: true, point, agent: { ...agent, clock_suspect: true } }).reason,
    ).toBe('clock_suspect')
    expect(
      manualOpenState({ canOpen: true, point: { ...point, clock_suspect: true }, agent }).reason,
    ).toBe('clock_suspect')
  })
})

describe('agentOnline', () => {
  const now = Date.parse(NOW)
  it('needs a connection and a recent heartbeat', () => {
    expect(agentOnline({ connected: true, last_seen: '2026-03-01T11:59:30Z' }, now)).toBe(true)
    expect(agentOnline({ connected: true, last_seen: '2026-03-01T11:50:00Z' }, now)).toBe(false)
    expect(agentOnline({ connected: false, last_seen: '2026-03-01T11:59:30Z' }, now)).toBe(false)
    expect(agentOnline({ connected: true, last_seen: null }, now)).toBe(false)
  })
})

function renderOpen(over: {
  canOpen?: boolean
  agent?: { connected: boolean; online: boolean; last_seen_at: string | null } | null
  clockSuspect?: boolean
}) {
  const qc = new QueryClient()
  render(
    <I18nProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <OpenButton
          ctx={ctx}
          point={{
            id: 'pt-main',
            name: 'Main gate',
            kind: 'vehicle_gate',
            direction: 'both',
            enabled: true,
            online: true,
            clock_suspect: false,
          }}
          console={{
            server_time: NOW,
            agent:
              over.agent === undefined
                ? {
                    connected: true,
                    online: true,
                    clock_suspect: false,
                    last_seen_at: '2026-03-01T11:59:50Z',
                    points: [],
                  }
                : over.agent && { ...over.agent, clock_suspect: false, points: [] },
          }}
          canOpen={over.canOpen ?? true}
          clockSuspect={over.clockSuspect}
        />
      </QueryClientProvider>
    </I18nProvider>,
  )
}

describe('manual open button', () => {
  it('is usable when everything is fine', () => {
    renderOpen({})
    expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeEnabled()
  })
  it('is disabled with the reason when the agent is offline', () => {
    renderOpen({ agent: { connected: false, online: false, last_seen_at: null } })
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
    const api = createGuardApi(
      () => 'gs-token-1',
      () => undefined,
      'http://localhost',
    )
    await api.GET('/v1/orgs/{org_id}/gate/console', {
      params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } },
    })
    await api.GET('/v1/orgs/{org_id}/gate/guards', {
      params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } },
    })
    const consoleCall = seen.find((r) => r.url.includes('/gate/console'))!
    const guardsCall = seen.find((r) => r.url.includes('/gate/guards'))!
    expect(consoleCall.headers.get(GUARD_HEADER)).toBe('gs-token-1')
    expect(guardsCall.headers.get(GUARD_HEADER)).toBeNull()
  })
  it('an invalid_grant on a guard call drops the session instead of signing the booth out', async () => {
    server.use(
      http.get('*/v1/orgs/:orgId/gate/console', () =>
        HttpResponse.json(
          { status: 401, code: 'invalid_grant' },
          { status: 401, headers: { 'content-type': 'application/problem+json' } },
        ),
      ),
    )
    let lost = 0
    const api = createGuardApi(
      () => 'old',
      () => void lost++,
      'http://localhost',
    )
    await expect(
      api.GET('/v1/orgs/{org_id}/gate/console', {
        params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } },
      }),
    ).rejects.toMatchObject({ status: 401 })
    expect(lost).toBe(1)
  })
})

describe('PIN switch', () => {
  function mount() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const api = createGuardApi(undefined, undefined, 'http://localhost')
    render(
      <I18nProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <PinSwitch ctx={ctx} api={api} />
        </QueryClientProvider>
      </I18nProvider>,
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

describe('manual open, learned block', () => {
  it('clears when the next console snapshot arrives', async () => {
    server.use(
      http.post('*/v1/orgs/:orgId/access-points/:pointAction', () =>
        HttpResponse.json(
          { status: 409, code: 'agent_offline' },
          { status: 409, headers: { 'content-type': 'application/problem+json' } },
        ),
      ),
    )
    const qc = new QueryClient()
    const point = {
      id: 'pt-main',
      name: 'Main gate',
      kind: 'vehicle_gate',
      direction: 'both',
      enabled: true,
      online: true,
      clock_suspect: false,
    } as const
    const mk = (serverTime: string) => (
      <I18nProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <OpenButton
            ctx={ctx}
            point={point}
            console={{
              server_time: serverTime,
              agent: {
                connected: true,
                online: true,
                clock_suspect: false,
                last_seen_at: serverTime,
                points: [],
              },
            }}
            canOpen
          />
        </QueryClientProvider>
      </I18nProvider>
    )
    const { rerender } = render(mk(NOW))
    await userEvent.click(screen.getByRole('button', { name: /Open Main gate/ }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeDisabled(),
    )
    expect(screen.getByText('The gate agent is offline.')).toBeInTheDocument()
    rerender(mk('2026-03-01T12:00:12Z'))
    expect(screen.getByRole('button', { name: /Open Main gate/ })).toBeEnabled()
  })
})

describe('PIN failures', () => {
  it('says "wrong PIN" only for invalid_grant, not for a server error', async () => {
    server.use(
      http.post('*/v1/orgs/:orgId/gate/sessions', () =>
        HttpResponse.json(
          { status: 503, code: 'unavailable' },
          { status: 503, headers: { 'content-type': 'application/problem+json' } },
        ),
      ),
    )
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <I18nProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <PinSwitch ctx={ctx} api={createGuardApi(undefined, undefined, 'http://localhost')} />
        </QueryClientProvider>
      </I18nProvider>,
    )
    await userEvent.click(await screen.findByRole('button', { name: 'Hasan Çelik' }))
    await userEvent.type(screen.getByLabelText('PIN'), '1234')
    await userEvent.click(screen.getByRole('button', { name: 'Start the shift' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('could not be checked')
    expect(alert).not.toHaveTextContent('Wrong PIN')
  })
})

describe('guard middleware composition', () => {
  it('translates the version header into If-Match like the shared client', async () => {
    const seen = recordRequests()
    const api = createGuardApi(
      () => 'gs-token-1',
      () => undefined,
      'http://localhost',
    )
    await api.GET('/v1/orgs/{org_id}/gate/console', {
      params: { path: { org_id: 'org-1' }, query: { site_id: 'site-a' } },
      headers: { 'X-Entity-Version': '3' },
    } as never)
    const call = seen.find((r) => r.url.includes('/gate/console'))!
    expect(call.headers.get('If-Match')).toBe('"3"')
    expect(call.headers.get('X-Entity-Version')).toBeNull()
  })
})

describe('leaving the console', () => {
  it('asks for the PIN and does not leave on a wrong one', async () => {
    setGuardSession({
      token: 't',
      personId: 'p-hasan',
      displayName: 'Hasan Çelik',
      expiresAt: '2999-01-01T00:00:00Z',
    })
    const qc = new QueryClient()
    render(
      <I18nProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <ExitGate
            ctx={ctx}
            guard={getGuardSession()!}
            api={createGuardApi(undefined, undefined, 'http://localhost')}
          />
        </QueryClientProvider>
      </I18nProvider>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Leave the guard console' }))
    await userEvent.type(await screen.findByLabelText('PIN'), '9999')
    await userEvent.click(screen.getByRole('button', { name: 'Leave' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong PIN')
    expect(getGuardSession()).not.toBeNull()
  })
})
