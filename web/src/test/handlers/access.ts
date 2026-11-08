import { http, HttpResponse } from 'msw'
import type { components } from '@/api/draft'
import { ORG, SITE, list, nowIso, problem, qs, uid, versionMismatch } from './gov-util'

/**
 * In-memory model of the access module (draft contract 0.3): agents, access points, derived and
 * manual credentials, the opening log and manual opening. Shared with the community handlers
 * (the guard console reads agent status and the opening feed from here).
 */
type S = components['schemas']
const BASE = `*/v1/orgs/:orgId`

interface State {
  agents: S['Agent'][]
  points: S['AccessPoint'][]
  credentials: S['Credential'][]
  events: S['AccessEvent'][]
  commands: Map<string, S['AgentCommand'] & { polls: number }>
  seq: number
}

function agent(partial: Partial<S['Agent']> & Pick<S['Agent'], 'id' | 'name'>): S['Agent'] {
  const t = nowIso()
  return {
    org_id: ORG,
    site_id: SITE,
    status: 'active',
    connected: true,
    version: 1,
    platform: 'linux/amd64',
    protocol_version: 1,
    last_seen: t,
    snapshot_version: 12,
    queue_depth: 0,
    drivers: [{ instance: 'gate-relay', state: 'ok', mock: true, detail: 'mock driver' }],
    clock_offset_ms: 40,
    clock_suspect: false,
    rotate_by: null,
    enrol_expires_at: null,
    revoked_at: null,
    created_by: null,
    created_at: t,
    updated_at: t,
    lost_events: 0,
    store_error: null,
    dead_letters: 0,
    ...partial,
  }
}

function point(
  partial: Partial<S['AccessPoint']> & Pick<S['AccessPoint'], 'id' | 'name'>,
): S['AccessPoint'] {
  const t = nowIso()
  return {
    org_id: ORG,
    site_id: SITE,
    kind: 'vehicle_gate',
    direction: 'both',
    agent_id: 'agent-1',
    driver_ref: { instance: 'gate-relay', channel: '1' },
    enabled: true,
    created_at: t,
    updated_at: t,
    version: 1,
    ...partial,
  }
}

function fresh(): State {
  const t = nowIso()
  const cred = (
    c: Partial<S['Credential']> & Pick<S['Credential'], 'id' | 'kind' | 'value' | 'source_type'>,
  ): S['Credential'] => ({
    org_id: ORG,
    site_id: SITE,
    source_id: null,
    unit_id: null,
    unit_door_number: null,
    person_id: null,
    point_scope: 'all_vehicle',
    point_ids: [],
    valid_from: t,
    valid_to: null,
    recurring: null,
    max_uses: null,
    uses: 0,
    status: 'active',
    status_reason: null,
    created_at: t,
    updated_at: t,
    ...c,
  })
  return {
    agents: [
      agent({ id: 'agent-1', name: 'Gate booth PC' }),
      agent({
        id: 'agent-2',
        name: 'Service gate Pi',
        connected: false,
        last_seen: new Date(Date.now() - 3_600_000).toISOString(),
        clock_suspect: true,
        clock_offset_ms: 420_000,
      }),
    ],
    points: [
      point({ id: 'pt-main', name: 'Main vehicle gate' }),
      point({
        id: 'pt-door',
        name: 'Lobby door',
        kind: 'pedestrian_door',
        agent_id: 'agent-1',
        driver_ref: { instance: 'gate-relay', channel: '2' },
      }),
      point({
        id: 'pt-service',
        name: 'Service gate',
        agent_id: 'agent-2',
        driver_ref: { instance: 'gate-relay', channel: '1' },
      }),
    ],
    credentials: [
      cred({
        id: 'cr-1',
        kind: 'plate',
        value: '34 ABC 123',
        source_type: 'vehicle',
        unit_id: 'u-a1',
        unit_door_number: 'A1',
      }),
      cred({
        id: 'cr-2',
        kind: 'plate',
        value: '06 XYZ 987',
        source_type: 'vehicle',
        unit_id: 'u-b1',
        unit_door_number: 'B1',
      }),
      cred({
        id: 'cr-3',
        kind: 'visitor_qr',
        value: 'obk1.…',
        source_type: 'visitor_entry',
        max_uses: 1,
      }),
      cred({
        id: 'cr-4',
        kind: 'tag',
        value: 'TAG-00A1',
        source_type: 'manual',
        point_scope: 'all',
      }),
    ],
    events: [
      {
        id: 'ev-1',
        org_id: ORG,
        site_id: SITE,
        point_id: 'pt-main',
        point_name: 'Main vehicle gate',
        agent_id: 'agent-1',
        credential_id: 'cr-1',
        decision: 'allow',
        reason: 'match',
        presented_kind: 'plate',
        observed_plate: '34 ABC 123',
        unit_door_number: 'A1',
        actor_user_id: null,
        actor_display_name: null,
        command_id: null,
        visitor_entry_id: null,
        occurred_at: new Date(Date.now() - 600_000).toISOString(),
        synced_at: t,
        sync_seq: 1,
        created_at: t,
        updated_at: t,
      },
      {
        id: 'ev-2',
        org_id: ORG,
        site_id: SITE,
        point_id: 'pt-main',
        point_name: 'Main vehicle gate',
        agent_id: 'agent-1',
        credential_id: null,
        decision: 'deny',
        reason: 'unknown',
        presented_kind: 'plate',
        observed_plate: '35 ZZZ 001',
        unit_door_number: null,
        actor_user_id: null,
        actor_display_name: null,
        command_id: null,
        visitor_entry_id: null,
        occurred_at: new Date(Date.now() - 300_000).toISOString(),
        synced_at: t,
        sync_seq: 2,
        created_at: t,
        updated_at: t,
      },
    ],
    commands: new Map(),
    seq: 2,
  }
}

let db = fresh()
export function resetAccessDb() {
  db = fresh()
}
export const accessDb = () => db

/** Appends an opening event (used by the guard console flows in the community handlers too). */
export function pushAccessEvent(
  e: Partial<S['AccessEvent']> & Pick<S['AccessEvent'], 'point_id' | 'decision' | 'reason'>,
) {
  const t = nowIso()
  db.seq += 1
  db.events.push({
    id: uid('ev'),
    org_id: ORG,
    site_id: SITE,
    point_name: db.points.find((p) => p.id === e.point_id)?.name ?? null,
    agent_id: null,
    credential_id: null,
    presented_kind: null,
    observed_plate: null,
    unit_door_number: null,
    actor_user_id: null,
    actor_display_name: null,
    command_id: null,
    visitor_entry_id: null,
    occurred_at: t,
    synced_at: t,
    sync_seq: db.seq,
    created_at: t,
    updated_at: t,
    ...e,
  })
}

function withEnrolment(a: S['Agent']): S['AgentEnrolment'] {
  return {
    agent: a,
    enrolment_token: `obk-enrol-${a.id}-${Math.random().toString(36).slice(2, 10)}`,
    enrolment_expires_at: new Date(Date.now() + 3_600_000).toISOString(),
  }
}

export const accessHandlers = [
  // agents
  http.get(`${BASE}/agents`, ({ request }) => {
    const st = qs(request).get('status')
    return list(db.agents.filter((a) => !st || a.status === st))
  }),
  http.post(`${BASE}/agents`, async ({ request }) => {
    const b = (await request.json()) as S['AgentCreate']
    const a = agent({
      id: uid('agent'),
      name: b.name,
      site_id: b.site_id,
      status: 'pending',
      connected: false,
      last_seen: null,
      drivers: [],
      platform: null,
      protocol_version: null,
      snapshot_version: null,
      queue_depth: null,
      clock_offset_ms: null,
      lost_events: null,
      dead_letters: null,
    })
    db.agents.push(a)
    return HttpResponse.json(withEnrolment(a), { status: 201 })
  }),
  http.get(`${BASE}/agents/:agentId`, ({ params }) => {
    const a = db.agents.find((x) => x.id === params.agentId)
    return a ? HttpResponse.json(a) : problem(404, 'not_found')
  }),
  http.patch(`${BASE}/agents/:agentId`, async ({ params, request }) => {
    const a = db.agents.find((x) => x.id === params.agentId)
    if (!a) return problem(404, 'not_found')
    const bad = versionMismatch(request, a.version)
    if (bad) return bad
    Object.assign(a, (await request.json()) as S['AgentUpdate'])
    a.version += 1
    return HttpResponse.json(a)
  }),
  http.post(`${BASE}/agents/:agentAction`, ({ params }) => {
    const [id, action] = String(params.agentAction).split(':')
    const a = db.agents.find((x) => x.id === id)
    if (!a) return problem(404, 'not_found')
    if (action === 'reenrol') {
      a.status = 'pending'
      a.connected = false
      return HttpResponse.json(withEnrolment(a))
    }
    if (action === 'revoke') {
      a.status = 'revoked'
      a.connected = false
      a.revoked_at = nowIso()
      return HttpResponse.json(a)
    }
    if (action === 'rotate-token') {
      a.rotate_by = new Date(Date.now() + 30 * 86_400_000).toISOString()
      return HttpResponse.json(a)
    }
    return problem(404, 'not_found')
  }),
  http.post(`${BASE}/access/allowlist\\:rebuild`, () =>
    HttpResponse.json(
      { id: uid('job'), kind: 'allowlist_rebuild', status: 'queued', created_at: nowIso() },
      { status: 202 },
    ),
  ),

  // access points
  http.get(`${BASE}/access-points`, ({ request }) => {
    const en = qs(request).get('enabled')
    return list(db.points.filter((p) => en === null || String(p.enabled) === en))
  }),
  http.post(`${BASE}/access-points`, async ({ request }) => {
    const b = (await request.json()) as S['AccessPointCreate']
    const p = point({
      id: uid('pt'),
      name: b.name,
      kind: b.kind,
      direction: b.direction,
      agent_id: b.agent_id ?? null,
      driver_ref: b.driver_ref ?? null,
      enabled: b.enabled ?? true,
    })
    db.points.push(p)
    return HttpResponse.json(p, { status: 201 })
  }),
  http.get(`${BASE}/access-points/:pointId`, ({ params }) => {
    const p = db.points.find((x) => x.id === params.pointId)
    return p ? HttpResponse.json(p) : problem(404, 'not_found')
  }),
  http.patch(`${BASE}/access-points/:pointId`, async ({ params, request }) => {
    const p = db.points.find((x) => x.id === params.pointId)
    if (!p) return problem(404, 'not_found')
    const bad = versionMismatch(request, p.version)
    if (bad) return bad
    Object.assign(p, (await request.json()) as S['AccessPointUpdate'])
    p.version += 1
    return HttpResponse.json(p)
  }),
  http.delete(`${BASE}/access-points/:pointId`, ({ params }) => {
    db.points = db.points.filter((p) => p.id !== params.pointId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post(`${BASE}/access-points/:pointAction`, async ({ params, request }) => {
    const [id, action] = String(params.pointAction).split(':')
    const p = db.points.find((x) => x.id === id)
    if (!p || action !== 'open') return problem(404, 'not_found')
    if (!request.headers.get('Idempotency-Key'))
      return problem(422, 'validation_failed', {
        errors: [{ field: 'Idempotency-Key', code: 'required' }],
      })
    const a = db.agents.find((x) => x.id === p.agent_id)
    if (!a?.connected) return problem(409, 'agent_offline')
    const t = Date.now()
    const cmd = {
      id: uid('cmd'),
      point_id: p.id,
      command: 'open_point' as const,
      status: 'pending' as const,
      reason: null,
      issued_at: new Date(t).toISOString(),
      expires_at: new Date(t + 15_000).toISOString(),
      completed_at: null,
      polls: 0,
    }
    db.commands.set(cmd.id, cmd)
    pushAccessEvent({
      point_id: p.id,
      agent_id: a.id,
      decision: 'manual_open',
      reason: request.headers.get('X-Guard-Session') ? 'guard' : 'manager',
      actor_display_name: request.headers.get('X-Guard-Session') ? 'Hasan Çelik' : 'Ayşe Yılmaz',
      command_id: cmd.id,
    })
    return HttpResponse.json(cmd, { status: 202 })
  }),
  http.get(`${BASE}/access/commands/:commandId`, ({ params }) => {
    const c = db.commands.get(String(params.commandId))
    if (!c) return problem(404, 'not_found')
    c.polls += 1
    if (c.polls >= 2) {
      c.status = 'done'
      c.completed_at = nowIso()
    } else c.status = 'delivered'
    return HttpResponse.json(c)
  }),

  // credentials
  http.get(`${BASE}/credentials`, ({ request }) => {
    const p = qs(request)
    const q = p.get('q')?.toLowerCase()
    return list(
      db.credentials.filter(
        (c) =>
          (!p.get('kind') || c.kind === p.get('kind')) &&
          (!p.get('status') || c.status === p.get('status')) &&
          (!p.get('source_type') || c.source_type === p.get('source_type')) &&
          (!q || c.value.toLowerCase().includes(q)),
      ),
    )
  }),
  http.post(`${BASE}/credentials`, async ({ request }) => {
    const b = (await request.json()) as S['CredentialCreate']
    const t = nowIso()
    const c: S['Credential'] = {
      id: uid('cr'),
      org_id: ORG,
      site_id: b.site_id,
      kind: b.kind,
      value: b.kind === 'pin' ? '••••' : b.value,
      source_type: 'manual',
      source_id: null,
      unit_id: b.unit_id ?? null,
      unit_door_number: null,
      person_id: b.person_id ?? null,
      point_scope: b.point_scope,
      point_ids: b.point_ids ?? [],
      valid_from: b.valid_from ?? t,
      valid_to: b.valid_to ?? null,
      recurring: b.recurring ?? null,
      max_uses: b.max_uses ?? null,
      uses: 0,
      status: 'active',
      status_reason: null,
      created_at: t,
      updated_at: t,
    }
    db.credentials.push(c)
    return HttpResponse.json(c, { status: 201 })
  }),
  http.get(`${BASE}/credentials/:credentialId`, ({ params }) => {
    const c = db.credentials.find((x) => x.id === params.credentialId)
    return c ? HttpResponse.json(c) : problem(404, 'not_found')
  }),
  http.delete(`${BASE}/credentials/:credentialId`, ({ params }) => {
    const c = db.credentials.find((x) => x.id === params.credentialId)
    if (c) {
      c.status = 'revoked'
      c.status_reason = 'manager'
    }
    return new HttpResponse(null, { status: 204 })
  }),

  // opening log
  http.get(`${BASE}/access/events`, ({ request }) => {
    const p = qs(request)
    const plate = p.get('plate')?.toLowerCase().replaceAll(' ', '')
    return list(
      db.events
        .filter(
          (e) =>
            (!p.get('point_id') || e.point_id === p.get('point_id')) &&
            (!p.get('decision') || e.decision === p.get('decision')) &&
            (!p.get('reason') || e.reason === p.get('reason')) &&
            (!plate || (e.observed_plate ?? '').toLowerCase().replaceAll(' ', '').includes(plate)),
        )
        .sort((a, b) => b.sync_seq - a.sync_seq),
    )
  }),
  http.get(`${BASE}/gate/events`, ({ request }) => {
    const after = Number(qs(request).get('after') ?? 0)
    const items: S['GuardAccessEvent'][] = db.events
      .filter((e) => e.sync_seq > after)
      .sort((a, b) => b.sync_seq - a.sync_seq)
      .slice(0, 20)
      .map((e) => ({
        sync_seq: e.sync_seq,
        occurred_at: e.occurred_at,
        point: { id: e.point_id, name: e.point_name ?? e.point_id },
        decision: e.decision,
        reason: e.reason,
        plate: e.observed_plate,
        who: e.unit_door_number
          ? { kind: 'resident' as const, unit_label: e.unit_door_number, visitor_name: null }
          : { kind: 'unknown' as const, unit_label: null, visitor_name: null },
      }))
    return list(items)
  }),
]
