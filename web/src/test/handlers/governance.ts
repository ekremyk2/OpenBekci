import { http, HttpResponse } from 'msw'
import { registerReset } from '../reset'
import { createScope } from './scope'
import type { components } from '@/api/draft'
import {
  ORG,
  SITE,
  OWNERS,
  daysFromNow,
  list,
  nowIso,
  pdf,
  problem,
  qs,
  uid,
  unitDoor,
  versionMismatch,
} from './gov-util'

/**
 * In-memory model of the governance module (draft contract 0.2) for tests and `pnpm dev:mock`.
 * Answers are built from the spec examples; the rules (timing checks, quorum, thresholds) are
 * small re-implementations so flows chain without a backend.
 */
type S = components['schemas']

const BASE = `*/v1/orgs/:orgId`

/** The governance screens: where the governance model answers before the shared ones (scope.ts). */
export const govScope = createScope(['/meetings', '/bookings', '/visitors', '/access'])
const scopedGet = (path: string, resolver: Parameters<typeof http.get>[1]) =>
  http.get(path, (info) => (govScope.has(info.request) ? resolver(info) : undefined))
const scopedPost = (path: string, resolver: Parameters<typeof http.post>[1]) =>
  http.post(path, (info) => (govScope.has(info.request) ? resolver(info) : undefined))
const TOTAL_UNITS = OWNERS.length
const TOTAL_SHARE = OWNERS.reduce((a, o) => a + o.share, 0)

function lawSettings(): S['GovernanceSettingsSet'] {
  return {
    quorum: {
      first_call_by_count_pct: 50,
      first_call_by_share_pct: 50,
      second_call_requires_quorum: false,
    },
    thresholds: [
      { topic: 'general', rule: 'majority_count_and_share', note: 'Law 634 art. 32 (verify)' },
      { topic: 'manager_election', rule: 'majority_count_and_share', note: null },
      { topic: 'budget', rule: 'majority_count_and_share', note: null },
      { topic: 'dues_rate', rule: 'majority_count_and_share', note: null },
      { topic: 'special_assessment', rule: 'majority_count_and_share', note: null },
      { topic: 'common_area_change', rule: 'four_fifths', note: 'Law 634 art. 19 (verify)' },
      { topic: 'change_of_use', rule: 'unanimity', note: 'Law 634 art. 28 (verify)' },
      { topic: 'noting', rule: 'simple_majority_present', note: null },
    ],
    max_votes_per_person_pct: 33.33,
    proxy_cap_pct: 5,
    proxy_cap_small_building_max: 2,
    small_building_unit_limit: 40,
    call_min_days: 15,
    second_call_min_days: 7,
    second_call_max_days: 15,
    term_reminder_days: 60,
  }
}

const PRESET_NOTES: S['PresetNote'][] = [
  {
    key: 'quorum',
    note: 'Law 634 art. 29: majority by number and by land share (verify).',
    verify: true,
  },
  {
    key: 'proxy_cap',
    note: 'A person may represent at most 5% of all votes (verify).',
    verify: true,
  },
  {
    key: 'call_timing',
    note: 'Call at least 15 days ahead; second meeting 7 to 15 days later (verify).',
    verify: true,
  },
  {
    key: 'max_votes',
    note: 'One person holds at most one third of all votes (art. 31).',
    verify: false,
  },
]

interface State {
  settings: S['GovernanceSettings']
  meetings: S['Meeting'][]
  agenda: S['AgendaItem'][]
  polls: Map<string, S['Poll']>
  pollAnswers: S['PollAnswer'][]
  voteCounts: Map<string, S['VoteCount']>
  attendance: S['Attendance'][]
  notices: S['MeetingNotice'][]
  decisions: S['Decision'][]
  files: S['File'][]
  reminders: S['TermReminder'][]
}

function fresh(): State {
  const settings: S['GovernanceSettings'] = {
    site_id: SITE,
    preset: 'tr-634',
    settings: lawSettings(),
    preset_notes: PRESET_NOTES,
    version: 1,
  }
  const t = nowIso()
  const closed: S['Meeting'] = {
    id: 'meeting-1',
    org_id: ORG,
    site_id: SITE,
    kind: 'general_assembly',
    scope: 'site',
    title: 'Ordinary assembly 2026',
    scheduled_at: daysFromNow(-90),
    second_call_at: daysFromNow(-80),
    location: 'Community hall',
    status: 'closed',
    eligibility_snapshot: null,
    call_letter_document_id: 'doc-1',
    minutes_document_id: 'doc-2',
    called_at: daysFromNow(-110),
    held_call: 'first',
    first_call_failed_at: null,
    created_at: t,
    updated_at: t,
    version: 4,
  }
  const upcoming: S['Meeting'] = {
    ...closed,
    id: 'meeting-2',
    title: 'Roof repair assembly',
    kind: 'extraordinary',
    scheduled_at: daysFromNow(21),
    second_call_at: daysFromNow(32),
    status: 'called',
    call_letter_document_id: 'doc-3',
    minutes_document_id: null,
    called_at: t,
    version: 2,
  }
  const draft: S['Meeting'] = {
    ...closed,
    id: 'meeting-3',
    title: 'Elections 2027',
    scheduled_at: daysFromNow(40),
    second_call_at: daysFromNow(50),
    status: 'draft',
    call_letter_document_id: null,
    minutes_document_id: null,
    called_at: null,
    version: 1,
  }
  const s: State = {
    settings,
    meetings: [closed, upcoming, draft],
    agenda: [
      {
        id: 'ag-1',
        org_id: ORG,
        meeting_id: 'meeting-2',
        position: 1,
        title: 'Approve the roof repair budget',
        description: null,
        will_be_voted: true,
        topic: 'budget',
        decision_id: null,
        created_at: t,
        updated_at: t,
        version: 1,
      },
      {
        id: 'ag-2',
        org_id: ORG,
        meeting_id: 'meeting-2',
        position: 2,
        title: 'Elect the site manager',
        description: 'One year term',
        will_be_voted: true,
        topic: 'manager_election',
        decision_id: null,
        created_at: t,
        updated_at: t,
        version: 1,
      },
    ],
    polls: new Map(),
    pollAnswers: [],
    voteCounts: new Map(),
    attendance: [],
    notices: [],
    decisions: [
      {
        id: 'dec-1',
        org_id: ORG,
        site_id: SITE,
        meeting_id: 'meeting-1',
        agenda_item_id: null,
        sequence_no: 1,
        year: new Date().getUTCFullYear(),
        text: 'The 2026 budget is approved.',
        outcome: 'accepted',
        vote_count: null,
        dissents: [],
        concerns_residents: true,
        notified_at: null,
        effect: null,
        created_at: t,
        updated_at: t,
        version: 1,
      },
    ],
    files: [],
    reminders: [
      {
        id: 'tr-1',
        org_id: ORG,
        site_id: SITE,
        membership_id: 'mem-1',
        person_display_name: 'Ayşe Yılmaz',
        role: 'site_manager',
        valid_to: daysFromNow(35).slice(0, 10),
        days_left: 35,
        status: 'upcoming',
        notified_at: null,
        created_at: t,
        updated_at: t,
      },
    ],
  }
  // the called meeting already has one pending notice per owner
  s.notices = noticesFor('meeting-2')
  return s
}

function noticesFor(meetingId: string): S['MeetingNotice'][] {
  const t = nowIso()
  return OWNERS.map((o) => ({
    id: uid('notice'),
    org_id: ORG,
    meeting_id: meetingId,
    person_id: o.personId,
    person_display_name: o.name,
    phase: 'call' as const,
    method: null,
    status: 'pending' as const,
    sent_at: null,
    tracking_no: null,
    proof_file_id: null,
    note: null,
    created_at: t,
    updated_at: t,
  }))
}

let db = fresh()
export function resetGovernanceDb() {
  db = fresh()
  govScope.force()
}
registerReset(() => {
  resetGovernanceDb()
  govScope.release()
})
/** Test access to the model. */
export const governanceDb = () => db

function bump<T extends { version: number; updated_at: string }>(o: T): T {
  o.version += 1
  o.updated_at = nowIso()
  return o
}

const tokenOf = (meetingId: string, personId: string) => `obk1.att.${meetingId}.${personId}`

/** Who could vote, with the weight each owner votes with (one unit, one vote, arsa payi as share). */
function snapshotOf(): S['EligibilitySnapshot'] {
  const at = nowIso()
  return {
    taken_at: at,
    as_of: at.slice(0, 10),
    vote_weight: 'per_unit',
    share_basis: 'arsa_payi',
    unit_count: TOTAL_UNITS,
    total_votes: TOTAL_UNITS,
    total_share: TOTAL_SHARE,
    owners: OWNERS.map((o) => ({
      person_id: o.personId,
      display_name: o.name,
      unit_ids: [o.unitId],
      unit_labels: [o.door],
      share: String(o.share),
      vote_weight: 1,
      representative_of_unit_ids: [o.unitId],
    })),
    unrepresented_units: [],
  }
}

function quorumOf(meeting: S['Meeting']): S['Quorum'] {
  const rows = db.attendance.filter((a) => a.meeting_id === meeting.id)
  const units = new Set(rows.flatMap((a) => a.unit_ids))
  const present_count = units.size
  const present_share = OWNERS.filter((o) => units.has(o.unitId)).reduce((a, o) => a + o.share, 0)
  // the call held is recorded by the server when the session opens, never read off the clock
  const call = meeting.held_call ?? 'first'
  const q = db.settings.settings.quorum
  const first = call === 'first'
  const reached = first
    ? present_count * 100 > TOTAL_UNITS * q.first_call_by_count_pct &&
      present_share * 100 > TOTAL_SHARE * q.first_call_by_share_pct
    : !q.second_call_requires_quorum || present_count > 0
  return {
    meeting_id: meeting.id,
    call,
    present_count,
    eligible_count: TOTAL_UNITS,
    present_share,
    eligible_share: TOTAL_SHARE,
    required_by_count_pct: first ? q.first_call_by_count_pct : null,
    required_by_share_pct: first ? q.first_call_by_share_pct : null,
    reached,
    as_of: nowIso(),
  }
}

function hintFor(
  item: S['AgendaItem'] | undefined,
  yes: number,
  no: number,
  abstain: number,
  yesShare: number,
) {
  const rule = db.settings.settings.thresholds.find((t) => t.topic === item?.topic)?.rule
  const present = yes + no + abstain
  if (present === 0) return 'undetermined' as const
  switch (rule) {
    case 'four_fifths':
      return yes * 5 >= TOTAL_UNITS * 4 ? ('accepted' as const) : ('rejected' as const)
    case 'unanimity':
      return yes === TOTAL_UNITS ? ('accepted' as const) : ('rejected' as const)
    case 'simple_majority_present':
      return yes > no ? ('accepted' as const) : ('rejected' as const)
    default:
      return yes * 2 > TOTAL_UNITS && yesShare * 2 > TOTAL_SHARE
        ? ('accepted' as const)
        : ('rejected' as const)
  }
}

const meetingOf = (id: unknown) => db.meetings.find((m) => m.id === id)
const itemOf = (id: unknown) => db.agenda.find((a) => a.id === id)

export const governanceHandlers = [
  // settings
  http.get(`${BASE}/sites/:siteId/governance-settings`, () => HttpResponse.json(db.settings)),
  http.put(`${BASE}/sites/:siteId/governance-settings`, async ({ request }) => {
    const bad = versionMismatch(request, db.settings.version)
    if (bad) return bad
    const body = (await request.json()) as S['GovernanceSettingsSet']
    db.settings = { ...db.settings, settings: body, version: db.settings.version + 1 }
    return HttpResponse.json(db.settings)
  }),
  http.get(`${BASE}/governance-presets`, () =>
    list<S['GovernancePreset']>([
      {
        id: 'tr-634',
        country_code: 'TR',
        name: 'Turkey, Law 634 (Kat Mülkiyeti Kanunu)',
        settings: lawSettings(),
        notes: PRESET_NOTES,
      },
    ]),
  ),

  // meetings
  http.get(`${BASE}/meetings`, ({ request }) => {
    const status = qs(request).get('status')
    const kind = qs(request).get('kind')
    return list(
      db.meetings
        .filter((m) => (!status || m.status === status) && (!kind || m.kind === kind))
        .sort((a, b) => b.scheduled_at.localeCompare(a.scheduled_at)),
    )
  }),
  http.post(`${BASE}/meetings`, async ({ request }) => {
    const b = (await request.json()) as S['MeetingCreate']
    const t = nowIso()
    const m: S['Meeting'] = {
      id: uid('meeting'),
      org_id: ORG,
      site_id: b.site_id,
      kind: b.kind,
      scope: b.scope ?? 'site',
      title: b.title ?? null,
      scheduled_at: b.scheduled_at,
      second_call_at: b.second_call_at,
      location: b.location,
      status: 'draft',
      eligibility_snapshot: null,
      call_letter_document_id: null,
      minutes_document_id: null,
      called_at: null,
      held_call: null,
      first_call_failed_at: null,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.meetings.push(m)
    return HttpResponse.json(m, { status: 201 })
  }),
  http.get(`${BASE}/meetings/:meetingId`, ({ params }) => {
    const m = meetingOf(params.meetingId)
    return m ? HttpResponse.json(m) : problem(404, 'not_found')
  }),
  http.patch(`${BASE}/meetings/:meetingId`, async ({ params, request }) => {
    const m = meetingOf(params.meetingId)
    if (!m) return problem(404, 'not_found')
    const bad = versionMismatch(request, m.version)
    if (bad) return bad
    Object.assign(m, (await request.json()) as S['MeetingUpdate'])
    return HttpResponse.json(bump(m))
  }),
  http.delete(`${BASE}/meetings/:meetingId`, ({ params }) => {
    db.meetings = db.meetings.filter((m) => m.id !== params.meetingId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post(`${BASE}/meetings/:meetingAction`, async ({ params, request }) => {
    const [id, action] = String(params.meetingAction).split(':')
    const m = meetingOf(id)
    if (!m) return problem(404, 'not_found')
    if (action === 'call') {
      const s = db.settings.settings
      const lead = (Date.parse(m.scheduled_at) - Date.now()) / 86_400_000
      const gap = (Date.parse(m.second_call_at) - Date.parse(m.scheduled_at)) / 86_400_000
      const errors: { field: string; code: string; message: string }[] = []
      if (lead < s.call_min_days)
        errors.push({
          field: 'scheduled_at',
          code: 'call_too_late',
          message: `The call must go out at least ${s.call_min_days} days before the meeting.`,
        })
      if (gap < s.second_call_min_days || gap > s.second_call_max_days)
        errors.push({
          field: 'second_call_at',
          code: 'second_call_timing',
          message: `The second meeting must be ${s.second_call_min_days} to ${s.second_call_max_days} days after the first.`,
        })
      if (errors.length > 0) return problem(422, 'validation_failed', { errors })
      m.status = 'called'
      m.called_at = nowIso()
      m.call_letter_document_id = uid('doc')
      m.eligibility_snapshot = snapshotOf()
      db.notices = db.notices.filter((n) => n.meeting_id !== m.id).concat(noticesFor(m.id))
      return HttpResponse.json(bump(m))
    }
    if (action === 'open-first-call') {
      if (m.status !== 'called') return problem(409, 'conflict')
      m.status = 'in_session'
      m.held_call = 'first'
      m.eligibility_snapshot = snapshotOf()
      return HttpResponse.json(bump(m))
    }
    if (action === 'open-second-call') {
      if (m.status !== 'in_session' || m.held_call !== 'first') return problem(409, 'conflict')
      if (quorumOf(m).reached) return problem(409, 'conflict')
      m.held_call = 'second'
      m.first_call_failed_at = nowIso()
      // attendance of the first call does not carry over: everybody checks in again
      db.attendance = db.attendance.filter((a) => a.meeting_id !== m.id)
      return HttpResponse.json(bump(m))
    }
    if (action === 'close') {
      if (m.status !== 'in_session') return problem(409, 'conflict')
      m.status = 'closed'
      return HttpResponse.json(bump(m))
    }
    if (action === 'cancel') {
      m.status = 'cancelled'
      return HttpResponse.json(bump(m))
    }
    if (action === 'notify-decisions') {
      const dry = qs(request).get('dry_run') === 'true'
      const absent = OWNERS.filter(
        (o) => !db.attendance.some((a) => a.meeting_id === m.id && a.unit_ids.includes(o.unitId)),
      )
      const body = (await request.json()) as S['DecisionNotify']
      if (!dry) {
        for (const d of db.decisions.filter((d) => d.meeting_id === m.id)) d.notified_at = nowIso()
        for (const o of absent)
          db.notices.push({
            ...noticesFor(m.id)[0]!,
            person_id: o.personId,
            person_display_name: o.name,
            phase: 'decisions',
            method: body.channels.includes('sms') ? 'sms' : 'email',
            status: 'recorded',
            sent_at: nowIso(),
          })
      }
      return HttpResponse.json({
        dry_run: dry,
        recipient_count: absent.length,
        sms_count: body.channels.includes('sms') ? absent.length : 0,
      })
    }
    return problem(404, 'not_found')
  }),
  http.get(`${BASE}/meetings/:meetingId/quorum`, ({ params }) => {
    const m = meetingOf(params.meetingId)
    return m ? HttpResponse.json(quorumOf(m)) : problem(404, 'not_found')
  }),
  http.get(`${BASE}/meetings/:meetingId/call-letter`, () => pdf()),
  http.get(`${BASE}/meetings/:meetingId/attendance-sheet`, () => pdf()),
  http.get(`${BASE}/meetings/:meetingId/me`, ({ params }) => {
    const m = meetingOf(params.meetingId)
    if (!m) return problem(404, 'not_found')
    const me = OWNERS[0]!
    const res: S['MyMeeting'] = {
      meeting: m,
      eligible_unit_ids: [me.unitId],
      notice: db.notices.find((n) => n.meeting_id === m.id && n.person_id === me.personId) ?? null,
      attendance_token: tokenOf(m.id, me.personId),
      attendance_qr: tokenOf(m.id, me.personId),
      checked_in: db.attendance.some((a) => a.meeting_id === m.id && a.person_id === me.personId),
      polls: db.agenda
        .filter((a) => a.meeting_id === m.id && db.polls.get(a.id)?.status === 'open')
        .map((a) => ({
          agenda_item_id: a.id,
          title: a.title,
          answered_units: db.pollAnswers
            .filter((p) => p.agenda_item_id === a.id && p.answered_by === me.personId)
            .map((p) => p.unit_id),
          answers: [],
        })),
    }
    return HttpResponse.json(res)
  }),
  http.post(`${BASE}/meetings/:meetingId/minutes\\:generate`, ({ params }) => {
    const m = meetingOf(params.meetingId)
    if (!m) return problem(404, 'not_found')
    m.minutes_document_id = m.minutes_document_id ?? uid('doc')
    return HttpResponse.json({
      meeting_id: m.id,
      document_id: m.minutes_document_id,
      file_id: 'file-minutes',
    })
  }),
  http.get(`${BASE}/meetings/:meetingId/minutes`, ({ params }) => {
    const m = meetingOf(params.meetingId)
    if (!m?.minutes_document_id) return problem(404, 'not_found')
    return HttpResponse.json({
      meeting_id: m.id,
      document_id: m.minutes_document_id,
      file_id: 'file-minutes',
    })
  }),

  // notices (G3)
  http.get(`${BASE}/meetings/:meetingId/notices`, ({ params, request }) => {
    const phase = qs(request).get('phase')
    return list(
      db.notices.filter((n) => n.meeting_id === params.meetingId && (!phase || n.phase === phase)),
    )
  }),
  http.put(`${BASE}/meetings/:meetingId/notices/:personId`, async ({ params, request }) => {
    const b = (await request.json()) as S['MeetingNoticeRecord']
    let n = db.notices.find(
      (x) =>
        x.meeting_id === params.meetingId && x.person_id === params.personId && x.phase === b.phase,
    )
    if (!n) {
      const owner = OWNERS.find((o) => o.personId === params.personId)
      n = {
        ...noticesFor(String(params.meetingId))[0]!,
        person_id: String(params.personId),
        person_display_name: owner?.name ?? 'Unknown',
        phase: b.phase,
      }
      db.notices.push(n)
    }
    Object.assign(n, {
      method: b.method,
      sent_at: b.sent_at,
      tracking_no: b.tracking_no ?? null,
      proof_file_id: b.proof_file_id ?? null,
      note: b.note ?? null,
      status: 'recorded',
      updated_at: nowIso(),
    })
    return HttpResponse.json(n)
  }),
  http.post(`${BASE}/meetings/:meetingId/notices\\:bulk`, async ({ params, request }) => {
    const b = (await request.json()) as S['MeetingNoticeBulk']
    for (const id of b.person_ids) {
      const n = db.notices.find(
        (x) => x.meeting_id === params.meetingId && x.person_id === id && x.phase === b.phase,
      )
      if (n) Object.assign(n, { method: b.method, sent_at: b.sent_at, status: 'recorded' })
    }
    return HttpResponse.json({ recorded: b.person_ids.length })
  }),

  // agenda
  http.get(`${BASE}/meetings/:meetingId/agenda-items`, ({ params }) =>
    list(
      db.agenda
        .filter((a) => a.meeting_id === params.meetingId)
        .sort((a, b) => a.position - b.position),
    ),
  ),
  http.post(`${BASE}/meetings/:meetingId/agenda-items`, async ({ params, request }) => {
    const b = (await request.json()) as S['AgendaItemCreate']
    const t = nowIso()
    const position = db.agenda.filter((a) => a.meeting_id === params.meetingId).length + 1
    const item: S['AgendaItem'] = {
      id: uid('ag'),
      org_id: ORG,
      meeting_id: String(params.meetingId),
      position,
      title: b.title,
      description: b.description ?? null,
      will_be_voted: b.will_be_voted ?? false,
      topic: b.topic ?? null,
      decision_id: null,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.agenda.push(item)
    return HttpResponse.json(item, { status: 201 })
  }),
  http.put(`${BASE}/meetings/:meetingId/agenda-items\\:order`, async ({ params, request }) => {
    const b = (await request.json()) as S['AgendaOrder']
    b.item_ids.forEach((id, i) => {
      const a = itemOf(id)
      if (a && a.meeting_id === params.meetingId) a.position = i + 1
    })
    return new HttpResponse(null, { status: 204 })
  }),
  http.patch(`${BASE}/agenda-items/:itemId`, async ({ params, request }) => {
    const a = itemOf(params.itemId)
    if (!a) return problem(404, 'not_found')
    const bad = versionMismatch(request, a.version)
    if (bad) return bad
    Object.assign(a, (await request.json()) as S['AgendaItemUpdate'])
    return HttpResponse.json(bump(a))
  }),
  http.delete(`${BASE}/agenda-items/:itemId`, ({ params }) => {
    db.agenda = db.agenda.filter((a) => a.id !== params.itemId)
    return new HttpResponse(null, { status: 204 })
  }),

  // polls (G2): non-binding
  http.get(`${BASE}/agenda-items/:itemId/poll`, ({ params }) => {
    const p = db.polls.get(String(params.itemId))
    return p ? HttpResponse.json(p) : problem(404, 'not_found')
  }),
  http.put(`${BASE}/agenda-items/:itemId/poll`, async ({ params, request }) => {
    const b = (await request.json()) as S['PollSet']
    const id = String(params.itemId)
    const prev = db.polls.get(id)
    const now = Date.now()
    const status: S['Poll']['status'] =
      Date.parse(b.closes_at) <= now ? 'closed' : Date.parse(b.opens_at) <= now ? 'open' : 'draft'
    const p: S['Poll'] = {
      agenda_item_id: id,
      question: b.question ?? null,
      opens_at: b.opens_at,
      closes_at: b.closes_at,
      status,
      version: (prev?.version ?? 0) + 1,
    }
    db.polls.set(id, p)
    return HttpResponse.json(p)
  }),
  http.put(`${BASE}/agenda-items/:itemId/poll/answer`, async ({ params, request }) => {
    const b = (await request.json()) as S['PollAnswerSet']
    const id = String(params.itemId)
    if (db.polls.get(id)?.status !== 'open') return problem(409, 'conflict')
    db.pollAnswers = db.pollAnswers.filter(
      (a) => !(a.agenda_item_id === id && a.unit_id === b.unit_id),
    )
    const a: S['PollAnswer'] = {
      agenda_item_id: id,
      unit_id: b.unit_id,
      unit_door_number: unitDoor(b.unit_id),
      answer: b.answer,
      answered_by: OWNERS[0]!.personId,
      answered_at: nowIso(),
    }
    db.pollAnswers.push(a)
    return HttpResponse.json(a)
  }),
  http.get(`${BASE}/agenda-items/:itemId/poll/results`, ({ params }) => {
    const rows = db.pollAnswers.filter((a) => a.agenda_item_id === params.itemId)
    const n = (x: string) => rows.filter((r) => r.answer === x).length
    const res: S['PollResults'] = {
      agenda_item_id: String(params.itemId),
      yes: n('yes'),
      no: n('no'),
      abstain: n('abstain'),
      units_answered: rows.length,
      units_total: TOTAL_UNITS,
      binding: false,
    }
    return HttpResponse.json(res)
  }),
  http.get(`${BASE}/agenda-items/:itemId/poll/answers`, ({ params }) =>
    list(db.pollAnswers.filter((a) => a.agenda_item_id === params.itemId)),
  ),

  // in-room counts
  http.get(`${BASE}/agenda-items/:itemId/vote-counts`, ({ params }) => {
    const v = db.voteCounts.get(String(params.itemId))
    if (v) return HttpResponse.json(v)
    const empty: S['VoteCount'] = {
      agenda_item_id: String(params.itemId),
      yes_count: 0,
      no_count: 0,
      abstain_count: 0,
      yes_share: null,
      no_share: null,
      abstain_share: null,
      recorded_by: null,
      recorded_at: null,
      outcome_hint: 'undetermined',
    }
    return HttpResponse.json(empty)
  }),
  http.put(`${BASE}/agenda-items/:itemId/vote-counts`, async ({ params, request }) => {
    const b = (await request.json()) as S['VoteCountSet']
    const id = String(params.itemId)
    const yesShare = b.yes_share ?? (b.yes_count / TOTAL_UNITS) * TOTAL_SHARE
    const v: S['VoteCount'] = {
      agenda_item_id: id,
      yes_count: b.yes_count,
      no_count: b.no_count,
      abstain_count: b.abstain_count,
      yes_share: yesShare,
      no_share: b.no_share ?? null,
      abstain_share: b.abstain_share ?? null,
      recorded_by: OWNERS[0]!.personId,
      recorded_at: nowIso(),
      outcome_hint: hintFor(itemOf(id), b.yes_count, b.no_count, b.abstain_count, yesShare),
    }
    db.voteCounts.set(id, v)
    return HttpResponse.json(v)
  }),

  // attendance
  http.get(`${BASE}/meetings/:meetingId/attendance`, ({ params }) =>
    list(db.attendance.filter((a) => a.meeting_id === params.meetingId)),
  ),
  http.post(`${BASE}/meetings/:meetingId/attendance`, async ({ params, request }) => {
    const b = (await request.json()) as S['AttendanceCreate']
    const m = meetingOf(params.meetingId)
    if (!m) return problem(404, 'not_found')
    if (m.status !== 'called' && m.status !== 'in_session') return problem(409, 'conflict')
    if (
      db.attendance.some(
        (a) => a.meeting_id === m.id && a.unit_ids.some((u) => b.unit_ids.includes(u)),
      )
    )
      return problem(409, 'conflict')
    const owner = OWNERS.find((o) => o.personId === b.person_id)
    const row: S['Attendance'] = {
      id: uid('att'),
      org_id: ORG,
      meeting_id: m.id,
      person_id: b.person_id,
      person_display_name: owner?.name ?? b.person_id,
      unit_ids: b.unit_ids,
      unit_labels: b.unit_ids.map(unitDoor),
      mode: b.mode,
      call: m.held_call ?? 'first',
      proxy_for_person_id: b.proxy_for_person_id ?? null,
      source: 'desk',
      checked_in_at: nowIso(),
      checked_in_by: OWNERS[0]!.personId,
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.attendance.push(row)
    return HttpResponse.json(row, { status: 201 })
  }),
  http.delete(`${BASE}/meetings/:meetingId/attendance/:attId`, ({ params }) => {
    db.attendance = db.attendance.filter((a) => a.id !== params.attId)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post(`${BASE}/meetings/:meetingId/attendance\\:checkin`, async ({ params, request }) => {
    const b = (await request.json()) as S['AttendanceCheckin']
    const m = meetingOf(params.meetingId)
    if (!m) return problem(404, 'not_found')
    const match = OWNERS.find((o) => tokenOf(m.id, o.personId) === b.token.trim())
    if (!match)
      return problem(422, 'validation_failed', { errors: [{ field: 'token', code: 'invalid' }] })
    if (db.attendance.some((a) => a.meeting_id === m.id && a.unit_ids.includes(match.unitId)))
      return problem(409, 'conflict')
    const row: S['Attendance'] = {
      id: uid('att'),
      org_id: ORG,
      meeting_id: m.id,
      person_id: match.personId,
      person_display_name: match.name,
      unit_ids: [match.unitId],
      unit_labels: [match.door],
      mode: 'in_person',
      call: m.held_call ?? 'first',
      proxy_for_person_id: null,
      source: 'qr',
      checked_in_at: nowIso(),
      checked_in_by: null,
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.attendance.push(row)
    return HttpResponse.json(row, { status: 201 })
  }),

  // decisions
  http.get(`${BASE}/decisions`, ({ request }) => {
    const p = qs(request)
    const q = p.get('q')?.toLowerCase()
    return list(
      db.decisions
        .filter(
          (d) =>
            (!p.get('meeting_id') || d.meeting_id === p.get('meeting_id')) &&
            (!p.get('year') || String(d.year) === p.get('year')) &&
            (!p.get('outcome') || d.outcome === p.get('outcome')) &&
            (!q || d.text.toLowerCase().includes(q)),
        )
        .sort((a, b) => b.year - a.year || b.sequence_no - a.sequence_no),
    )
  }),
  http.post(`${BASE}/decisions`, async ({ request }) => {
    const b = (await request.json()) as S['DecisionCreate']
    const year = new Date().getUTCFullYear()
    const t = nowIso()
    const vc = b.agenda_item_id ? (db.voteCounts.get(b.agenda_item_id) ?? null) : null
    const d: S['Decision'] = {
      id: uid('dec'),
      org_id: ORG,
      site_id: b.site_id,
      meeting_id: b.meeting_id,
      agenda_item_id: b.agenda_item_id ?? null,
      sequence_no: db.decisions.filter((x) => x.year === year).length + 1,
      year,
      text: b.text,
      outcome: b.outcome,
      vote_count: vc,
      dissents: (b.dissents ?? []).map((x) => ({
        person_id: x.person_id,
        display_name: OWNERS.find((o) => o.personId === x.person_id)?.name ?? null,
        text: x.text,
      })),
      concerns_residents: b.concerns_residents ?? false,
      notified_at: null,
      effect: null,
      created_at: t,
      updated_at: t,
      version: 1,
    }
    db.decisions.push(d)
    const item = b.agenda_item_id ? itemOf(b.agenda_item_id) : undefined
    if (item) item.decision_id = d.id
    return HttpResponse.json(d, { status: 201 })
  }),
  http.get(`${BASE}/decisions/:decisionId`, ({ params }) => {
    const d = db.decisions.find((x) => x.id === params.decisionId)
    return d ? HttpResponse.json(d) : problem(404, 'not_found')
  }),
  http.patch(`${BASE}/decisions/:decisionId`, async ({ params, request }) => {
    const d = db.decisions.find((x) => x.id === params.decisionId)
    if (!d) return problem(404, 'not_found')
    const bad = versionMismatch(request, d.version)
    if (bad) return bad
    const b = (await request.json()) as S['DecisionUpdate']
    if (b.text !== undefined) d.text = b.text
    if (b.outcome !== undefined) d.outcome = b.outcome
    if (b.concerns_residents !== undefined) d.concerns_residents = b.concerns_residents
    return HttpResponse.json(bump(d))
  }),
  http.post(`${BASE}/decisions/:decisionAction`, async ({ params, request }) => {
    const [id, action] = String(params.decisionAction).split(':')
    const d = db.decisions.find((x) => x.id === id)
    if (!d || action !== 'apply') return problem(404, 'not_found')
    if (!request.headers.get('Idempotency-Key'))
      return problem(422, 'validation_failed', {
        errors: [{ field: 'Idempotency-Key', code: 'required' }],
      })
    if (d.effect) return problem(409, 'conflict')
    const b = (await request.json()) as S['DecisionApply']
    const dry = qs(request).get('dry_run') === 'true'
    const kinds = {
      dues_rate: 'charge_plan_rate',
      membership: 'membership',
      budget_final: 'budget',
      special_assessment: 'charge_plan',
    } as const
    const preview: Record<string, unknown> = { ...b }
    if (dry) return HttpResponse.json({ dry_run: true, preview, decision: null })
    d.effect = {
      kind: b.kind as S['DecisionEffect']['kind'],
      record_type: kinds[b.kind as keyof typeof kinds] ?? 'record',
      record_id: uid('rec'),
      applied_at: nowIso(),
      applied_by: OWNERS[0]!.personId,
    }
    bump(d)
    return HttpResponse.json({ dry_run: false, preview: null, decision: d })
  }),
  http.get(`${BASE}/term-reminders`, () => list(db.reminders)),

  // files used as notice proof (operations.uploadFile)
  scopedPost(`${BASE}/files`, async () => {
    const f: S['File'] = {
      id: uid('file'),
      org_id: ORG,
      site_id: SITE,
      name: 'proof.pdf',
      size: 1024,
      mime: 'application/pdf',
      sha256: 'x'.repeat(64),
      uploaded_by: null,
      scope: 'meeting_notice_proof',
      status: 'ready',
      source: 'uploaded',
      has_variants: false,
      retention_until: null,
      variants: {},
      links: { original: null, variants: {}, expires_at: nowIso() },
      created_at: nowIso(),
      updated_at: nowIso(),
    }
    db.files.push(f)
    return HttpResponse.json(f, { status: 201 })
  }),
]

/** Reference data the governance screens read from the 0.1 modules (units, people, plans...). */
export const directoryHandlers = [
  scopedGet(`${BASE}/units`, () =>
    list(
      OWNERS.map((o) => ({
        id: o.unitId,
        org_id: ORG,
        site_id: SITE,
        block_id: 'blk-1',
        door_number: o.door,
        legal_ref: null,
        type: 'flat',
        area_m2: 90,
        parent_unit_id: null,
        status: 'active',
        vacant: false,
        occupancy: { owners: 1, tenants: 0, household: 0 },
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
        payer_overrides: {},
      })),
    ),
  ),
  scopedGet(`${BASE}/persons`, () =>
    list(
      OWNERS.map((o) => ({
        id: o.personId,
        org_id: ORG,
        user_id: null,
        display_name: o.name,
        contact: {},
        national_id_masked: null,
        national_id_country: null,
        gate_contact_phone: o.phone,
        notes: null,
        join_status: 'joined',
        directory_consent: [],
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
      })),
    ),
  ),
  scopedGet(`${BASE}/occupancies`, () =>
    list(
      OWNERS.map((o) => ({
        id: `occ-${o.personId}`,
        org_id: ORG,
        person_id: o.personId,
        person_display_name: o.name,
        unit_id: o.unitId,
        unit_door_number: o.door,
        kind: 'owner',
        side: 'owner',
        share_pct: 100,
        is_correspondent: true,
        may_see_money: true,
        valid_from: '2020-01-01',
        valid_to: null,
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
      })),
    ),
  ),
  scopedGet(`${BASE}/charge-plans`, () =>
    list([
      {
        id: 'plan-dues',
        org_id: ORG,
        site_id: SITE,
        name: 'Monthly dues',
        kind: 'recurring',
        charge_type: 'dues',
        default_debtor: 'owner',
        frequency: 'monthly',
        due_day: 5,
        start: '2024-01-01',
        end: null,
        amount_mode: 'per_unit',
        allocation_key_id: 'key-equal',
        scope: { type: 'site', id: null },
        fund_id: 'fund-1',
        income_account_id: 'acc-1',
        status: 'active',
        decision_id: null,
        current_rate: null,
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
      },
    ]),
  ),
  scopedGet(`${BASE}/budgets`, () =>
    list([
      {
        id: 'budget-2027',
        org_id: ORG,
        site_id: SITE,
        fiscal_year_id: 'fy-2027',
        status: 'draft',
        decision_id: null,
        total: { amount: 120000000, currency: 'TRY' },
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
      },
    ]),
  ),
  scopedGet(`${BASE}/allocation-keys`, () =>
    list([
      {
        id: 'key-equal',
        org_id: ORG,
        site_id: SITE,
        code: 'equal',
        name: 'Equal per unit',
        kind: 'equal',
        is_system: true,
        created_at: nowIso(),
        updated_at: nowIso(),
        version: 1,
      },
    ]),
  ),
]
