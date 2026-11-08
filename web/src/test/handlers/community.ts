import { http, HttpResponse } from 'msw'
import type { components } from '@/api/draft'
import { accessDb } from './access'
import {
  ORG,
  OWNERS,
  SITE,
  list,
  nowIso,
  problem,
  qs,
  uid,
  unitDoor,
  versionMismatch,
} from './gov-util'

/**
 * In-memory model of the community module (draft contract 0.3): facilities and bookings, visitors,
 * recurring visitors, approval requests, parcels and the guard console.
 */
type S = components['schemas']
const BASE = `*/v1/orgs/:orgId`
export const GUARD_PIN = '1234'
const GUARDS = [
  { person_id: 'p-hasan', display_name: 'Hasan Çelik', has_pin: true },
  { person_id: 'p-osman', display_name: 'Osman Tan', has_pin: true },
]

interface State {
  facilities: S['Facility'][]
  blocks: S['FacilityBlock'][]
  bookings: S['Booking'][]
  entries: S['VisitorEntry'][]
  recurring: S['RecurringVisitor'][]
  requests: S['VisitorApprovalRequest'][]
  deliveries: S['Delivery'][]
  failedPins: number
  rotations: number
}

const allDays = [1, 2, 3, 4, 5, 6, 7]

function fresh(): State {
  const t = nowIso()
  const rules = (over: Partial<S['FacilityRules']> = {}): S['FacilityRules'] => ({
    opening_hours: [{ days: allDays, start: '09:00', end: '22:00' }],
    max_duration_minutes: 180,
    max_days_ahead: 30,
    min_notice_minutes: 60,
    quota_per_unit: 4,
    quota_period: 'month',
    who_may_book: ['owner', 'tenant', 'household'],
    fee: { amount: 5000, currency: 'TRY' },
    cancel_refund_cutoff_hours: 24,
    ...over,
  })
  const tomorrow = new Date()
  tomorrow.setUTCDate(tomorrow.getUTCDate() + 1)
  tomorrow.setUTCHours(15, 0, 0, 0)
  return {
    facilities: [
      {
        id: 'f-bbq',
        org_id: ORG,
        site_id: SITE,
        name: 'Barbecue area',
        description: 'Garden terrace, grills provided.',
        rules: rules(),
        enabled: true,
        created_at: t,
        updated_at: t,
        version: 1,
      },
      {
        id: 'f-hall',
        org_id: ORG,
        site_id: SITE,
        name: 'Community hall',
        description: null,
        rules: rules({
          fee: null,
          max_duration_minutes: 240,
          opening_hours: [{ days: [1, 2, 3, 4, 5], start: '10:00', end: '20:00' }],
        }),
        enabled: true,
        created_at: t,
        updated_at: t,
        version: 1,
      },
    ],
    blocks: [],
    bookings: [
      {
        id: 'bk-1',
        org_id: ORG,
        site_id: SITE,
        facility_id: 'f-bbq',
        facility_name: 'Barbecue area',
        unit_id: 'u-a2',
        unit_door_number: 'A2',
        person_id: 'p-mehmet',
        person_display_name: 'Mehmet Demir',
        start: tomorrow.toISOString(),
        end: new Date(tomorrow.getTime() + 2 * 3_600_000).toISOString(),
        status: 'confirmed',
        charge_id: 'chg-1',
        fee: { amount: 5000, currency: 'TRY' },
        refund: null,
        cancelled_reason: null,
        created_at: t,
        updated_at: t,
      },
    ],
    entries: [
      {
        id: 've-1',
        org_id: ORG,
        site_id: SITE,
        unit_id: 'u-a1',
        unit_door_number: 'A1',
        name: 'Selin Koç (plumber)',
        plate: '34 SK 1234',
        window_from: new Date(Date.now() - 3_600_000).toISOString(),
        window_to: new Date(Date.now() + 4 * 3_600_000).toISOString(),
        approval_status: 'pre_approved',
        source: 'pre_registered',
        qr_token: 'obk1.vis.seed1',
        decided_by: null,
        decided_at: null,
        arrived_at: null,
        left_at: null,
        cancelled: false,
        recurring_visitor_id: null,
        created_at: t,
        updated_at: t,
        version: 1,
      },
    ],
    recurring: [
      {
        id: 'rv-1',
        org_id: ORG,
        site_id: SITE,
        unit_id: 'u-a1',
        name: 'Fatma (cleaner)',
        plate: null,
        days: [2, 4],
        start: '09:00',
        end: '15:00',
        valid_from: '2026-01-01',
        valid_to: null,
        status: 'active',
        qr_token: 'obk1.rec.seed1',
        created_at: t,
        updated_at: t,
        version: 1,
      },
    ],
    requests: [],
    deliveries: [
      {
        id: 'dl-1',
        org_id: ORG,
        site_id: SITE,
        unit_id: 'u-a1',
        unit_door_number: 'A1',
        received_at: new Date(Date.now() - 7_200_000).toISOString(),
        received_by_display_name: 'Hasan Çelik',
        description: 'Large box, courier',
        status: 'waiting',
        collected_at: null,
        collected_by_name: null,
        created_at: t,
        updated_at: t,
      },
    ],
    failedPins: 0,
    rotations: 0,
  }
}

let db = fresh()
export function resetCommunityDb() {
  db = fresh()
}
export const communityDb = () => db

const facilityOf = (id: unknown) => db.facilities.find((f) => f.id === id)
const entryOf = (id: unknown) => db.entries.find((e) => e.id === id)

function guardView(e: S['VisitorEntry']): S['GuardVisitor'] {
  const req = db.requests.find((r) => r.entry_id === e.id)
  return {
    id: e.id,
    name: e.name,
    plate: e.plate,
    unit_door_number: e.unit_door_number,
    window_from: e.window_from,
    window_to: e.window_to,
    approval_status: e.approval_status,
    approval_expires_at: req?.status === 'pending' ? req.expires_at : null,
    arrived_at: e.arrived_at,
    left_at: e.left_at,
    recurring: e.source === 'recurring',
  }
}

function expireRequests() {
  const now = Date.now()
  for (const r of db.requests) {
    if (r.status === 'pending' && Date.parse(r.expires_at) < now) {
      r.status = 'expired'
      const e = entryOf(r.entry_id)
      if (e) e.approval_status = 'expired'
    }
  }
}

const toMinutes = (hhmm: string) => {
  const [h, m] = hhmm.split(':').map(Number) as [number, number]
  return h * 60 + m
}

/** Facility rule check shared by the create handler: returns problem errors. */
function checkRules(f: S['Facility'], start: Date, end: Date, unitId: string) {
  const r = f.rules
  const errors: { field: string; code: string; message: string }[] = []
  const minutes = (end.getTime() - start.getTime()) / 60_000
  if (minutes <= 0)
    errors.push({ field: 'end', code: 'end_before_start', message: 'End must be after start.' })
  if (minutes > r.max_duration_minutes)
    errors.push({
      field: 'end',
      code: 'too_long',
      message: `At most ${r.max_duration_minutes} minutes.`,
    })
  if (r.min_notice_minutes !== null && start.getTime() - Date.now() < r.min_notice_minutes * 60_000)
    errors.push({ field: 'start', code: 'too_soon', message: 'Too short notice.' })
  if (start.getTime() - Date.now() > r.max_days_ahead * 86_400_000)
    errors.push({
      field: 'start',
      code: 'too_far_ahead',
      message: `At most ${r.max_days_ahead} days ahead.`,
    })
  const day = start.getUTCDay() === 0 ? 7 : start.getUTCDay()
  const startMin = start.getUTCHours() * 60 + start.getUTCMinutes()
  const endMin = end.getUTCHours() * 60 + end.getUTCMinutes()
  const open = r.opening_hours.some(
    (h) => h.days.includes(day) && startMin >= toMinutes(h.start) && endMin <= toMinutes(h.end),
  )
  if (!open)
    errors.push({ field: 'start', code: 'outside_hours', message: 'Outside opening hours.' })
  if (r.quota_per_unit !== null) {
    const used = db.bookings.filter(
      (b) => b.facility_id === f.id && b.unit_id === unitId && b.status === 'confirmed',
    ).length
    if (used >= r.quota_per_unit)
      errors.push({ field: 'unit_id', code: 'quota_exceeded', message: 'Booking quota used up.' })
  }
  return errors
}

export const communityHandlers = [
  // facilities
  http.get(`${BASE}/facilities`, () => list(db.facilities)),
  http.post(`${BASE}/facilities`, async ({ request }) => {
    const b = (await request.json()) as S['FacilityCreate']
    const t = nowIso()
    const f: S['Facility'] = {
      id: uid('f'),
      org_id: ORG,
      site_id: b.site_id,
      name: b.name,
      description: b.description ?? null,
      rules: b.rules,
      enabled: b.enabled ?? true,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.facilities.push(f)
    return HttpResponse.json(f, { status: 201 })
  }),
  http.get(`${BASE}/facilities/:facilityId`, ({ params }) => {
    const f = facilityOf(params.facilityId)
    return f ? HttpResponse.json(f) : problem(404, 'not_found')
  }),
  http.patch(`${BASE}/facilities/:facilityId`, async ({ params, request }) => {
    const f = facilityOf(params.facilityId)
    if (!f) return problem(404, 'not_found')
    const bad = versionMismatch(request, f.version)
    if (bad) return bad
    Object.assign(f, (await request.json()) as S['FacilityUpdate'])
    f.version += 1
    f.updated_at = nowIso()
    return HttpResponse.json(f)
  }),
  http.delete(`${BASE}/facilities/:facilityId`, ({ params }) => {
    db.facilities = db.facilities.filter((f) => f.id !== params.facilityId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get(`${BASE}/facilities/:facilityId/blocks`, ({ params }) =>
    list(db.blocks.filter((b) => b.facility_id === params.facilityId)),
  ),
  http.post(`${BASE}/facilities/:facilityId/blocks`, async ({ params, request }) => {
    const b = (await request.json()) as S['FacilityBlockCreate']
    const t = nowIso()
    const block: S['FacilityBlock'] = {
      id: uid('blk'),
      org_id: ORG,
      facility_id: String(params.facilityId),
      start: b.start,
      end: b.end,
      reason: b.reason ?? null,
      created_at: t,
      updated_at: t,
    }
    db.blocks.push(block)
    return HttpResponse.json(block, { status: 201 })
  }),
  http.delete(`${BASE}/facilities/:facilityId/blocks/:blockId`, ({ params }) => {
    db.blocks = db.blocks.filter((b) => b.id !== params.blockId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get(`${BASE}/facilities/:facilityId/availability`, ({ params, request }) => {
    const p = qs(request)
    const from = p.get('from') ?? nowIso()
    const to = p.get('to') ?? nowIso()
    // residents see only that a slot is taken, never who booked it (C1)
    const res: S['Availability'] = {
      facility_id: String(params.facilityId),
      from,
      to,
      taken: db.bookings
        .filter((b) => b.facility_id === params.facilityId && b.status === 'confirmed')
        .map((b) => ({ start: b.start, end: b.end })),
      blocked: db.blocks
        .filter((b) => b.facility_id === params.facilityId)
        .map((b) => ({ start: b.start, end: b.end })),
    }
    return HttpResponse.json(res)
  }),

  // bookings
  http.get(`${BASE}/bookings`, ({ request }) => {
    const p = qs(request)
    return list(
      db.bookings.filter(
        (b) =>
          (!p.get('facility_id') || b.facility_id === p.get('facility_id')) &&
          (!p.get('status') || b.status === p.get('status')) &&
          (!p.get('unit_id') || b.unit_id === p.get('unit_id')) &&
          (p.get('mine') !== 'true' || b.unit_id === OWNERS[0]!.unitId),
      ),
    )
  }),
  http.post(`${BASE}/bookings`, async ({ request }) => {
    if (!request.headers.get('Idempotency-Key'))
      return problem(422, 'validation_failed', {
        errors: [{ field: 'Idempotency-Key', code: 'required' }],
      })
    const b = (await request.json()) as S['BookingCreate']
    const f = facilityOf(b.facility_id)
    if (!f) return problem(404, 'not_found')
    const start = new Date(b.start)
    const end = new Date(b.end)
    const errors = checkRules(f, start, end, b.unit_id)
    if (errors.length > 0) return problem(422, 'validation_failed', { errors })
    const overlap = (x: { start: string; end: string }) =>
      Date.parse(x.start) < end.getTime() && Date.parse(x.end) > start.getTime()
    if (
      db.bookings.some((x) => x.facility_id === f.id && x.status === 'confirmed' && overlap(x)) ||
      db.blocks.some((x) => x.facility_id === f.id && overlap(x))
    )
      return problem(409, 'conflict', { detail: 'That slot is taken.' })
    const t = nowIso()
    const booking: S['Booking'] = {
      id: uid('bk'),
      org_id: ORG,
      site_id: f.site_id,
      facility_id: f.id,
      facility_name: f.name,
      unit_id: b.unit_id,
      unit_door_number: unitDoor(b.unit_id),
      person_id: b.person_id ?? OWNERS[0]!.personId,
      person_display_name: OWNERS[0]!.name,
      start: b.start,
      end: b.end,
      status: 'confirmed',
      charge_id: f.rules.fee ? uid('chg') : null,
      fee: f.rules.fee,
      refund: null,
      cancelled_reason: null,
      created_at: t,
      updated_at: t,
    }
    db.bookings.push(booking)
    return HttpResponse.json(booking, { status: 201 })
  }),
  http.get(`${BASE}/bookings/:bookingId`, ({ params }) => {
    const b = db.bookings.find((x) => x.id === params.bookingId)
    return b ? HttpResponse.json(b) : problem(404, 'not_found')
  }),
  http.post(`${BASE}/bookings/:bookingAction`, async ({ params, request }) => {
    const [id, action] = String(params.bookingAction).split(':')
    const b = db.bookings.find((x) => x.id === id)
    if (!b || action !== 'cancel') return problem(404, 'not_found')
    if (!request.headers.get('Idempotency-Key'))
      return problem(422, 'validation_failed', {
        errors: [{ field: 'Idempotency-Key', code: 'required' }],
      })
    const body = (await request.json().catch(() => ({}))) as S['BookingCancel']
    const f = facilityOf(b.facility_id)
    const cutoff = (f?.rules.cancel_refund_cutoff_hours ?? 0) * 3_600_000
    b.status = 'cancelled'
    b.cancelled_reason = body.reason ?? null
    b.refund = b.fee && Date.parse(b.start) - Date.now() > cutoff ? b.fee : null
    b.updated_at = nowIso()
    return HttpResponse.json(b)
  }),

  // visitors
  http.get(`${BASE}/visitor-entries`, ({ request }) => {
    expireRequests()
    const p = qs(request)
    const q = p.get('q')?.toLowerCase()
    return list(
      db.entries.filter(
        (e) =>
          (!p.get('unit_id') || e.unit_id === p.get('unit_id')) &&
          (!p.get('approval_status') || e.approval_status === p.get('approval_status')) &&
          (!q || e.name.toLowerCase().includes(q) || (e.plate ?? '').toLowerCase().includes(q)),
      ),
    )
  }),
  http.post(`${BASE}/visitor-entries`, async ({ request }) => {
    const b = (await request.json()) as S['VisitorEntryCreate']
    const t = nowIso()
    const id = uid('ve')
    const e: S['VisitorEntry'] = {
      id,
      org_id: ORG,
      site_id: b.site_id,
      unit_id: b.unit_id,
      unit_door_number: unitDoor(b.unit_id),
      name: b.name,
      plate: b.plate ?? null,
      window_from: b.window_from,
      window_to: b.window_to,
      approval_status: 'pre_approved',
      source: 'pre_registered',
      qr_token: `obk1.vis.${id}`,
      decided_by: null,
      decided_at: null,
      arrived_at: null,
      left_at: null,
      cancelled: false,
      recurring_visitor_id: null,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.entries.push(e)
    return HttpResponse.json(e, { status: 201 })
  }),
  http.get(`${BASE}/visitor-entries/:entryId`, ({ params }) => {
    const e = entryOf(params.entryId)
    return e ? HttpResponse.json(e) : problem(404, 'not_found')
  }),
  http.post(`${BASE}/visitor-entries/:entryAction`, ({ params }) => {
    const [id, action] = String(params.entryAction).split(':')
    const e = entryOf(id)
    if (e && action === 'rotate-qr') {
      // the old code stops working, the new one is returned once
      e.qr_token = `obk1.vis.${id}.r${++db.rotations}`
      e.updated_at = nowIso()
      return HttpResponse.json(e)
    }
    if (!e || action !== 'cancel') return problem(404, 'not_found')
    e.cancelled = true
    e.updated_at = nowIso()
    return HttpResponse.json(e)
  }),

  // recurring visitors
  http.get(`${BASE}/recurring-visitors`, () => list(db.recurring)),
  http.post(`${BASE}/recurring-visitors`, async ({ request }) => {
    const b = (await request.json()) as S['RecurringVisitorCreate']
    const t = nowIso()
    const id = uid('rv')
    const r: S['RecurringVisitor'] = {
      id,
      org_id: ORG,
      site_id: b.site_id,
      unit_id: b.unit_id,
      name: b.name,
      plate: b.plate ?? null,
      days: b.days,
      start: b.start,
      end: b.end,
      valid_from: b.valid_from,
      valid_to: b.valid_to ?? null,
      status: 'active',
      qr_token: `obk1.rec.${id}`,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.recurring.push(r)
    return HttpResponse.json(r, { status: 201 })
  }),
  http.post(`${BASE}/recurring-visitors/:rvAction`, ({ params }) => {
    const [id, action] = String(params.rvAction).split(':')
    const r = db.recurring.find((x) => x.id === id)
    if (!r || action !== 'revoke') return problem(404, 'not_found')
    r.status = 'revoked'
    r.updated_at = nowIso()
    return HttpResponse.json(r)
  }),

  // approval requests (resident side)
  http.get(`${BASE}/visitor-approval-requests`, ({ request }) => {
    expireRequests()
    const st = qs(request).get('status')
    return list(db.requests.filter((r) => !st || r.status === st))
  }),
  http.post(`${BASE}/visitor-approval-requests/:requestAction`, async ({ params, request }) => {
    const [id, action] = String(params.requestAction).split(':')
    const r = db.requests.find((x) => x.id === id)
    if (!r || action !== 'decide') return problem(404, 'not_found')
    expireRequests()
    if (r.status !== 'pending') return problem(409, 'conflict')
    const b = (await request.json()) as S['VisitorDecision']
    r.status = b.decision === 'approve' ? 'approved' : 'denied'
    r.decided_at = nowIso()
    r.decided_by = OWNERS[0]!.personId
    const e = entryOf(r.entry_id)
    if (e) {
      e.approval_status = r.status === 'approved' ? 'approved' : 'denied'
      e.decided_at = r.decided_at
    }
    return HttpResponse.json(r)
  }),

  // parcels (resident side)
  http.get(`${BASE}/deliveries`, () => list(db.deliveries)),

  // guard console
  http.get(`${BASE}/gate/guards`, () => list(GUARDS)),
  http.post(`${BASE}/gate/sessions`, async ({ request }) => {
    const b = (await request.json()) as S['GuardSessionCreate']
    if (db.failedPins >= 5) {
      return HttpResponse.json(
        { type: 'about:blank', title: 'rate_limited', status: 429, code: 'rate_limited' },
        {
          status: 429,
          headers: { 'content-type': 'application/problem+json', 'Retry-After': '900' },
        },
      )
    }
    const g = GUARDS.find((x) => x.person_id === b.person_id)
    if (!g || b.pin !== GUARD_PIN) {
      db.failedPins += 1
      return problem(401, 'invalid_grant')
    }
    db.failedPins = 0
    const res: S['GuardSession'] = {
      token: `gs-${g.person_id}`,
      expires_at: new Date(Date.now() + 8 * 3_600_000).toISOString(),
      person_id: g.person_id,
      display_name: g.display_name,
    }
    return HttpResponse.json(res, { status: 201 })
  }),
  http.put(`${BASE}/persons/:personId/gate-pin`, () => new HttpResponse(null, { status: 204 })),
  http.get(`${BASE}/gate/console`, () => {
    expireRequests()
    const now = Date.now()
    const a = accessDb().agents.find((x) => x.id === 'agent-1')!
    const res: S['GateConsole'] = {
      server_time: nowIso(),
      expected_visitors: db.entries
        .filter(
          (e) =>
            !e.cancelled &&
            !e.left_at &&
            Date.parse(e.window_to) + 1_800_000 > now &&
            Date.parse(e.window_from) - 1_800_000 < now + 86_400_000,
        )
        .filter((e) => e.approval_status !== 'requested' && e.approval_status !== 'denied')
        .map(guardView),
      pending_approvals: db.entries.filter((e) => e.approval_status === 'requested').map(guardView),
      waiting_deliveries: db.deliveries
        .filter((d) => d.status === 'waiting')
        .map((d) => ({
          id: d.id,
          unit_door_number: d.unit_door_number,
          received_at: d.received_at,
          description: d.description,
          status: d.status,
          collected_by_name: null,
        })),
      agent: {
        connected: a.connected,
        last_seen_at: a.last_seen,
        points: accessDb().points.map((p) => ({
          id: p.id,
          name: p.name,
          kind: p.kind,
          direction: p.direction,
          enabled: p.enabled,
        })),
      },
    }
    return HttpResponse.json(res)
  }),
  http.get(`${BASE}/gate/visitors`, ({ request }) => {
    expireRequests()
    const p = qs(request)
    const q = p.get('q')?.toLowerCase()
    const plate = p.get('plate')?.toLowerCase().replaceAll(' ', '')
    return list(
      db.entries
        .filter((e) => !e.cancelled)
        .filter(
          (e) =>
            (!q ||
              e.name.toLowerCase().includes(q) ||
              (e.plate ?? '').toLowerCase().replaceAll(' ', '').includes(q.replaceAll(' ', ''))) &&
            (!plate || (e.plate ?? '').toLowerCase().replaceAll(' ', '').includes(plate)) &&
            (!p.get('unit_door_number') || e.unit_door_number === p.get('unit_door_number')) &&
            (!p.get('approval_status') || e.approval_status === p.get('approval_status')),
        )
        .map(guardView),
    )
  }),
  http.get(`${BASE}/gate/visitors/:entryId`, ({ params }) => {
    expireRequests()
    const e = entryOf(params.entryId)
    return e ? HttpResponse.json(guardView(e)) : problem(404, 'not_found')
  }),
  http.get(`${BASE}/gate/visitors/:entryId/call-contact`, ({ params }) => {
    const e = entryOf(params.entryId)
    const owner = OWNERS.find((o) => o.unitId === e?.unit_id)
    if (!owner) return problem(404, 'not_found')
    const res: S['CallContact'] = { display_name: owner.name, phone: owner.phone }
    return HttpResponse.json(res)
  }),
  http.post(`${BASE}/gate/visitors\\:scan`, async ({ request }) => {
    const b = (await request.json()) as S['ScanRequest']
    const e = db.entries.find(
      (x) => x.qr_token === b.token.trim() || `obk1.rec.${x.id}` === b.token.trim(),
    )
    const rec = db.recurring.find((x) => x.qr_token === b.token.trim())
    const out = (
      valid: boolean,
      reason: S['ScanResult']['reason'],
      entry: S['VisitorEntry'] | null,
    ): S['ScanResult'] => ({
      valid,
      reason,
      entry: entry ? guardView(entry) : null,
    })
    if (rec) {
      if (rec.status === 'revoked') return HttpResponse.json(out(false, 'revoked', null))
      const t = nowIso()
      const entry: S['VisitorEntry'] = {
        id: uid('ve'),
        org_id: ORG,
        site_id: SITE,
        unit_id: rec.unit_id,
        unit_door_number: unitDoor(rec.unit_id),
        name: rec.name,
        plate: rec.plate,
        window_from: t,
        window_to: new Date(Date.now() + 3_600_000).toISOString(),
        approval_status: 'pre_approved',
        source: 'recurring',
        qr_token: null,
        decided_by: null,
        decided_at: null,
        arrived_at: null,
        left_at: null,
        cancelled: false,
        recurring_visitor_id: rec.id,
        created_at: t,
        updated_at: t,
        version: 1,
      }
      db.entries.push(entry)
      return HttpResponse.json(out(true, 'match', entry))
    }
    if (!e) return HttpResponse.json(out(false, 'unknown', null))
    if (e.cancelled) return HttpResponse.json(out(false, 'revoked', null))
    if (e.arrived_at) return HttpResponse.json(out(false, 'used_up', e))
    const now = Date.now()
    if (now < Date.parse(e.window_from) - 1_800_000 || now > Date.parse(e.window_to) + 1_800_000)
      return HttpResponse.json(out(false, 'outside_window', e))
    return HttpResponse.json(out(true, 'match', e))
  }),
  http.post(`${BASE}/gate/visitors\\:walk-in`, async ({ request }) => {
    const b = (await request.json()) as S['WalkInCreate']
    const t = nowIso()
    const id = uid('ve')
    const e: S['VisitorEntry'] = {
      id,
      org_id: ORG,
      site_id: b.site_id,
      unit_id: b.unit_id,
      unit_door_number: unitDoor(b.unit_id),
      name: b.name,
      plate: b.plate ?? null,
      window_from: t,
      window_to: new Date(Date.now() + 3_600_000).toISOString(),
      approval_status: 'requested',
      source: 'walk_in',
      qr_token: null,
      decided_by: null,
      decided_at: null,
      arrived_at: null,
      left_at: null,
      cancelled: false,
      recurring_visitor_id: null,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.entries.push(e)
    db.requests.push({
      id: uid('ar'),
      org_id: ORG,
      entry_id: id,
      unit_id: b.unit_id,
      unit_door_number: e.unit_door_number,
      visitor_name: b.name,
      plate: b.plate ?? null,
      requested_at: t,
      expires_at: new Date(Date.now() + 120_000).toISOString(),
      status: 'pending',
      decided_by: null,
      decided_at: null,
      created_at: t,
      updated_at: t,
    })
    return HttpResponse.json(guardView(e), { status: 201 })
  }),
  http.post(`${BASE}/gate/visitors/:entryAction`, ({ params }) => {
    const [id, action] = String(params.entryAction).split(':')
    const e = entryOf(id)
    if (!e) return problem(404, 'not_found')
    if (action === 'arrive') {
      if (
        e.approval_status === 'denied' ||
        e.approval_status === 'expired' ||
        e.approval_status === 'requested'
      )
        return problem(409, 'conflict')
      e.arrived_at = nowIso()
    } else if (action === 'leave') {
      if (!e.arrived_at) return problem(409, 'conflict')
      e.left_at = nowIso()
    } else return problem(404, 'not_found')
    return HttpResponse.json(guardView(e))
  }),
  http.get(`${BASE}/gate/plates`, ({ request }) => {
    const q = qs(request).get('q')?.toLowerCase().replaceAll(' ', '') ?? ''
    return list(
      accessDb()
        .credentials.filter(
          (c) => c.kind === 'plate' && c.value.toLowerCase().replaceAll(' ', '').includes(q),
        )
        .map((c) => ({ plate: c.value, unit_door_number: c.unit_door_number ?? '' })),
    )
  }),
  http.get(`${BASE}/gate/deliveries`, ({ request }) => {
    const st = qs(request).get('status')
    return list(
      db.deliveries
        .filter((d) => !st || d.status === st)
        .map((d) => ({
          id: d.id,
          unit_door_number: d.unit_door_number,
          received_at: d.received_at,
          description: d.description,
          status: d.status,
          collected_by_name: d.collected_by_name,
        })),
    )
  }),
  http.post(`${BASE}/gate/deliveries`, async ({ request }) => {
    const b = (await request.json()) as S['DeliveryCreate']
    const t = nowIso()
    const d: S['Delivery'] = {
      id: uid('dl'),
      org_id: ORG,
      site_id: b.site_id,
      unit_id: b.unit_id,
      unit_door_number: unitDoor(b.unit_id),
      received_at: t,
      received_by_display_name: 'Hasan Çelik',
      description: b.description ?? null,
      status: 'waiting',
      collected_at: null,
      collected_by_name: null,
      created_at: t,
      updated_at: t,
    }
    db.deliveries.push(d)
    return HttpResponse.json(
      {
        id: d.id,
        unit_door_number: d.unit_door_number,
        received_at: d.received_at,
        description: d.description,
        status: d.status,
        collected_by_name: null,
      },
      { status: 201 },
    )
  }),
  http.post(`${BASE}/gate/deliveries/:deliveryAction`, async ({ params, request }) => {
    const [id, action] = String(params.deliveryAction).split(':')
    const d = db.deliveries.find((x) => x.id === id)
    if (!d || action !== 'collect') return problem(404, 'not_found')
    if (d.status === 'collected') return problem(409, 'conflict')
    const b = (await request.json()) as S['DeliveryCollect']
    d.status = 'collected'
    d.collected_at = nowIso()
    d.collected_by_name = b.collected_by_name
    return HttpResponse.json({
      id: d.id,
      unit_door_number: d.unit_door_number,
      received_at: d.received_at,
      description: d.description,
      status: d.status,
      collected_by_name: d.collected_by_name,
    })
  }),
]
