import { http, HttpResponse } from 'msw'
import type { Me } from '@/api/schema'
import { registerReset } from '../reset'

/*
 * MSW handlers for the DRAFT operations contract (api/openapi-draft.yaml, modules operations, audit,
 * dashboards, codes, platform) with a small in-memory model. They are written from the spec
 * examples and serve both Vitest and `pnpm dev:mock`. One catch-all handler dispatches through a
 * route table because MSW's path syntax cannot express the `:verb` suffixes of the contract.
 */

export type MockRole = 'manager' | 'staff' | 'resident' | 'platform'

export const ORG = 'org-1'
export const SITE = 'site-a'
const PERSON = { manager: 'p-manager', staff: 'p-staff', resident: 'p-resident', platform: 'p-x' }
const NAME = {
  manager: 'Mehmet Kaya',
  staff: 'Hasan Demir',
  resident: 'Ayşe Yılmaz',
  platform: 'Platform Admin',
}

const PERMS: Record<MockRole, string[]> = {
  manager: [
    'org.read',
    'org.manage',
    'tickets.read',
    'tickets.manage',
    'tickets.open',
    'announcements.read',
    'announcements.publish',
    'documents.read',
    'documents.manage',
    'files.upload',
    'files.read',
    'notifications.read',
    'notifications.manage',
    'audit.read',
    'dashboard.site',
    'codes.read',
    'jobs.read',
  ],
  staff: [
    'tickets.read_assigned',
    'tickets.work',
    'tickets.open',
    'announcements.read',
    'documents.read',
    'files.upload',
    'files.read',
    'notifications.read',
    'dashboard.staff',
  ],
  resident: [
    'tickets.open',
    'announcements.read',
    'documents.read',
    'documents.upload_own',
    'files.upload',
    'files.read',
    'notifications.read',
  ],
  platform: [],
}

export function meForRole(role: MockRole): Me {
  return {
    user: {
      id: `u-${role}`,
      display_name: NAME[role],
      email: `${role}@example.com`,
      phone: '+905551112233',
      locale: 'en',
      is_platform_admin: role === 'platform',
    },
    organizations:
      role === 'platform'
        ? []
        : [
            {
              id: ORG,
              slug: 'gunes',
              name: 'Güneş Sitesi Yönetimi',
              default_locale: 'en',
              roles: [
                {
                  role: role === 'manager' ? 'site_manager' : role === 'staff' ? 'staff' : 'guard',
                  scope_type: 'org',
                },
              ],
              permissions: PERMS[role].map((permission) => ({ permission })),
              sites: [
                { id: SITE, name: 'Güneş Sitesi', timezone: 'Europe/Istanbul', currency: 'TRY' },
              ],
            },
          ],
    occupancies:
      role === 'resident'
        ? [
            {
              id: 'occ-1',
              organization_id: ORG,
              site_id: SITE,
              unit_id: 'unit-12',
              unit_label: 'A Blok 12',
              kind: 'owner',
              may_see_money: true,
            },
          ]
        : [],
    policies: { app_lock_required: false },
  }
}

// ---------------------------------------------------------------------------------------------
// state
// ---------------------------------------------------------------------------------------------

type Json = Record<string, unknown>
interface Db {
  role: MockRole
  seq: number
  workflow: { statuses: Json[]; is_default: boolean; version: number }
  categories: Json[]
  tickets: Json[]
  comments: Json[]
  history: Json[]
  votes: Map<string, Set<string>>
  announcements: Json[]
  reads: Map<string, Set<string>>
  folders: Json[]
  documents: Json[]
  notifications: Json[]
  defaults: Json[]
  mutes: string[]
  files: Map<string, { meta: Json; bytes: ArrayBuffer }>
  audit: Json[]
  orgs: Json[]
  settings: Json
  smsSent: number
  defaultsVersion: number
  org: Json
}

export const db: Db = {} as Db

/**
 * Idempotency model (draft contract, `draft-common.yaml`): the same `Idempotency-Key` replays the
 * first successful response; the same key with a different request is 422 `idempotency_mismatch`.
 * It lets tests catch a client that reuses one key for different bodies.
 */
interface Remembered {
  fingerprint: string
  status: number
  body: string
  headers: [string, string][]
}
const idempotent = new Map<string, Remembered>()
const NOW = () => new Date().toISOString()
const hours = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString()

function id(prefix: string): string {
  db.seq += 1
  return `${prefix}-${db.seq}`
}

const UNITS = [
  { id: 'unit-12', door_number: '12', block_id: 'blk-a', label: 'A Blok 12' },
  { id: 'unit-5', door_number: '5', block_id: 'blk-a', label: 'A Blok 5' },
  { id: 'unit-20', door_number: '20', block_id: 'blk-b', label: 'B Blok 20' },
]

const DEFAULT_TYPES: [string, string[]][] = [
  ['payment.reminder', ['in_app', 'sms']],
  ['payment.receipt', ['in_app', 'sms']],
  ['ticket.update', ['in_app', 'email']],
  ['announcement.published', ['in_app']],
  ['meeting.called', ['in_app']],
  ['auth.sign_in_code', ['sms']],
  ['auth.invitation', ['sms']],
]
const LOCKED = new Set(['auth.sign_in_code', 'auth.invitation'])

export function resetOperationsDb(role: MockRole = 'manager') {
  idempotent.clear()
  db.role = role
  db.seq = 100
  db.workflow = {
    is_default: true,
    version: 1,
    statuses: [
      { id: 'st-open', name: 'Open', semantic: 'open_like', position: 0, key: 'open' },
      {
        id: 'st-prog',
        name: 'In progress',
        semantic: 'open_like',
        position: 1,
        key: 'in_progress',
      },
      { id: 'st-done', name: 'Done', semantic: 'closed_like', position: 2, key: 'done' },
    ],
  }
  const cat = (n: number, name: string, vis: string, role: string | null) => ({
    id: `cat-${n}`,
    org_id: ORG,
    site_id: SITE,
    name,
    default_visibility: vis,
    default_assignee_role: role,
    position: n,
    archived: false,
    created_at: hours(500),
    updated_at: hours(500),
    version: 1,
  })
  db.categories = [
    cat(1, 'Plumbing', 'private', 'staff'),
    cat(2, 'Electrical', 'private', 'staff'),
    cat(3, 'Elevator', 'common', 'site_manager'),
    cat(4, 'Cleaning', 'common', 'staff'),
    cat(5, 'Security', 'common', 'site_manager'),
    cat(6, 'Noise and neighbours', 'private', 'site_manager'),
    cat(7, 'Other', 'private', null),
  ]
  db.tickets = []
  db.comments = []
  db.history = []
  db.votes = new Map()
  db.announcements = []
  db.reads = new Map()
  db.files = new Map()
  db.notifications = [
    {
      id: 'n-1',
      site_id: SITE,
      type: 'ticket.update',
      payload: { title: 'Broken lamp in the entrance', status: 'In progress' },
      read_at: null,
      link: `/orgs/${ORG}/tickets/t-seed`,
      created_at: hours(2),
    },
    {
      id: 'n-2',
      site_id: SITE,
      type: 'announcement.published',
      payload: { title: 'Water cut on Saturday' },
      read_at: null,
      link: `/orgs/${ORG}/announcements/a-seed`,
      created_at: hours(30),
    },
    {
      id: 'n-3',
      site_id: SITE,
      type: 'payment.receipt',
      payload: { number: '2026-0042' },
      read_at: hours(60),
      link: null,
      created_at: hours(70),
    },
  ]
  db.defaults = DEFAULT_TYPES.map(([type, channels]) => ({
    type,
    channels,
    locked: LOCKED.has(type),
  }))
  db.mutes = []
  const folder = (fid: string, name: string, level: string, extra: Json = {}) => ({
    id: fid,
    org_id: ORG,
    site_id: SITE,
    parent_id: null,
    name,
    kind: 'site',
    unit_id: null,
    visibility: { level },
    breadcrumb: [{ id: fid, name }],
    is_system: false,
    created_at: hours(900),
    updated_at: hours(900),
    version: 1,
    ...extra,
  })
  db.folders = [
    folder('f-plan', 'Management plan', 'everyone'),
    folder('f-minutes', 'Assembly minutes', 'owners'),
    folder('f-finance', 'Financial reports', 'board', { is_system: true }),
    folder('f-unit12', 'Unit A Blok 12 (private)', 'units', {
      kind: 'unit',
      unit_id: 'unit-12',
      visibility: { level: 'units', unit_ids: ['unit-12'] },
      is_system: true,
    }),
  ]
  db.documents = [
    {
      id: 'd-1',
      org_id: ORG,
      site_id: SITE,
      folder_id: 'f-finance',
      title: 'Statement March 2026',
      file_id: 'file-seed-pdf',
      mime: 'application/pdf',
      size: 2048,
      visibility: null,
      source: 'generated',
      generated_from: { type: 'statement', id: 'stmt-1' },
      uploaded_by: null,
      created_at: hours(100),
      updated_at: hours(100),
      version: 1,
    },
    {
      id: 'd-2',
      org_id: ORG,
      site_id: SITE,
      folder_id: 'f-plan',
      title: 'Management plan 2026',
      file_id: 'file-seed-pdf2',
      mime: 'application/pdf',
      size: 4096,
      visibility: null,
      source: 'uploaded',
      generated_from: null,
      uploaded_by: 'u-manager',
      created_at: hours(300),
      updated_at: hours(300),
      version: 1,
    },
  ]
  db.audit = Array.from({ length: 30 }, (_, i) => ({
    id: `ae-${i + 1}`,
    org_id: ORG,
    site_id: SITE,
    actor_user_id: i % 2 ? 'u-manager' : 'u-staff',
    actor_display_name: i % 2 ? 'Mehmet Kaya' : 'Hasan Demir',
    action:
      i % 3 === 0
        ? 'ticket.transitioned'
        : i % 3 === 1
          ? 'person.updated'
          : 'announcement.published',
    target_type: i % 3 === 0 ? 'ticket' : i % 3 === 1 ? 'person' : 'announcement',
    target_id: `target-${i + 1}`,
    diff:
      i % 3 === 0
        ? { status: { before: 'Open', after: 'In progress' } }
        : i % 3 === 1
          ? {
              display_name: { before: 'A. Yilmaz', after: 'Ayşe Yılmaz' },
              email: { hash_before: 'sha256:9f2c…a1', hash_after: 'sha256:77b0…e4' },
            }
          : { pinned: true, title: 'Water cut on Saturday' },
    ip: '10.0.0.7',
    request_id: `req-${i + 1}`,
    at: hours(i * 5 + 1),
    created_at: hours(i * 5 + 1),
    updated_at: hours(i * 5 + 1),
  }))
  db.org = {
    id: ORG,
    slug: 'gunes',
    name: 'Güneş Sitesi Yönetimi',
    default_locale: 'tr',
    timezone: 'Europe/Istanbul',
    country_code: 'TR',
    status: 'active',
    settings: {},
    created_at: hours(2000),
    updated_at: hours(2000),
    version: 1,
  }
  db.orgs = [
    {
      id: ORG,
      slug: 'gunes',
      name: 'Güneş Sitesi Yönetimi',
      status: 'active',
      default_locale: 'tr',
      timezone: 'Europe/Istanbul',
      country_code: 'TR',
      site_count: 1,
      created_at: hours(2000),
      updated_at: hours(2000),
      version: 1,
    },
  ]
  db.settings = {
    version: 1,
    instance_name: 'OpenBekci',
    default_locale: 'tr',
    branding: null,
    registration_open: false,
    mail_configured: true,
    sms_configured: true,
  }
  db.smsSent = 412
  db.defaultsVersion = 1
  seedTickets()
}

function seedTickets() {
  const mk = (
    tid: string,
    title: string,
    cat: string,
    status: string,
    visibility: string,
    reporter: string,
    unit: string | null,
    extra: Json = {},
  ) => {
    const st = db.workflow.statuses.find((s) => s.id === status)!
    const t = {
      id: tid,
      org_id: ORG,
      site_id: SITE,
      unit_id: unit,
      unit_door_number: UNITS.find((u) => u.id === unit)?.door_number ?? null,
      reporter_person_id: reporter,
      reporter_display_name: reporter === 'p-resident' ? 'Ayşe Yılmaz' : 'Zeynep Arslan',
      category_id: cat,
      title,
      description: `${title}. Please have a look.`,
      status_id: status,
      status_name: st.name,
      status_semantic: st.semantic,
      visibility,
      assignee: null,
      vote_count: 0,
      voted_by_me: false,
      merged_into_id: null,
      comment_count: 0,
      attachments: [],
      closed_at: null,
      created_at: hours(48),
      updated_at: hours(48),
      version: 1,
      ...extra,
    }
    db.tickets.push(t)
    db.history.push({
      id: id('h'),
      org_id: ORG,
      ticket_id: tid,
      from_status_id: null,
      to_status_id: 'st-open',
      changed_by: null,
      comment: null,
      at: t.created_at,
      created_at: t.created_at,
      updated_at: t.created_at,
    })
    return t
  }
  mk('t-seed', 'Broken lamp in the entrance', 'cat-2', 'st-prog', 'common', 'p-other', null, {
    vote_count: 3,
    assignee: {
      kind: 'person',
      person_id: 'p-staff',
      vendor_id: null,
      display_name: 'Hasan Demir',
    },
  })
  mk('t-2', 'Leaking tap in the kitchen', 'cat-1', 'st-open', 'private', 'p-resident', 'unit-12')
  mk('t-3', 'Elevator makes a noise', 'cat-3', 'st-open', 'common', 'p-other', null, {
    vote_count: 1,
  })
  mk('t-4', 'Garden lights replaced', 'cat-2', 'st-done', 'common', 'p-other', null, {
    closed_at: hours(10),
  })
  mk('t-5', 'Mould in the bathroom', 'cat-1', 'st-open', 'private', 'p-other', 'unit-5')
  db.history.push(
    ...db.tickets
      .filter((t) => t.status_id !== 'st-open')
      .map((t) => ({
        id: id('h'),
        org_id: ORG,
        ticket_id: t.id,
        from_status_id: 'st-open',
        to_status_id: t.status_id,
        changed_by: 'u-staff',
        comment: null,
        at: hours(24),
        created_at: hours(24),
        updated_at: hours(24),
      })),
  )
}

// ---------------------------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------------------------

const personId = () => PERSON[db.role]
const can = (perm: string) => PERMS[db.role].includes(perm)
const isStaffSide = () => can('tickets.read') || can('tickets.work') || can('tickets.manage')

function problem(status: number, code: string, detail?: string, errors?: Json[]) {
  return HttpResponse.json(
    { type: `https://openbekci.org/problems/${code}`, title: code, status, code, detail, errors },
    { status, headers: { 'Content-Type': 'application/problem+json' } },
  )
}
const forbidden = () => problem(403, 'insufficient_permission')
const notFound = () => problem(404, 'not_found')
const json = (body: unknown, status = 200) => HttpResponse.json(body as never, { status })

function page<T>(items: T[], url: URL) {
  const limit = Number(url.searchParams.get('limit') ?? 50)
  const start = Number(url.searchParams.get('cursor') ?? 0)
  const slice = items.slice(start, start + limit)
  const next = start + limit < items.length ? String(start + limit) : null
  return json({ items: slice, next_cursor: next })
}

function etag(request: Request): number | null {
  const v = request.headers.get('If-Match')
  return v ? Number(v.replaceAll('"', '')) : null
}

function stale(entity: Json, request: Request) {
  const v = etag(request)
  if (v === null) return problem(422, 'validation_failed', 'If-Match is required')
  if (v !== entity.version) return problem(409, 'version_conflict')
  return null
}
function touch<T extends Json>(entity: T, patch: Json): T {
  Object.assign(entity, patch, { updated_at: NOW(), version: (entity.version as number) + 1 })
  return entity
}

function statusOf(sid: string) {
  return db.workflow.statuses.find((s) => s.id === sid)
}

function syncTicketStatus(t: Json) {
  const st = statusOf(t.status_id as string)
  if (st) {
    t.status_name = st.name
    t.status_semantic = st.semantic
  }
}

function visibleTicket(t: Json): boolean {
  if (can('tickets.read')) return true
  if (t.visibility === 'common') return true
  if (can('tickets.read_assigned') && (t.assignee as Json | null)?.person_id === personId())
    return true
  return t.reporter_person_id === personId()
}

function ticketView(t: Json): Json {
  const votes = db.votes.get(t.id as string) ?? new Set()
  return {
    ...t,
    vote_count: (t.vote_count as number) + votes.size,
    voted_by_me: votes.has(personId()),
    comment_count: db.comments.filter((c) => c.ticket_id === t.id && commentVisible(c)).length,
  }
}
const commentVisible = (c: Json) => c.visibility === 'public' || isStaffSide()

const AUDIENCE_SIZE: Record<string, number> = { site: 120, block: 60, owners: 70, tenants: 50 }
function recipients(audience: { kind: string; unit_ids?: string[] }) {
  return audience.kind === 'units'
    ? (audience.unit_ids?.length ?? 0) * 2
    : (AUDIENCE_SIZE[audience.kind] ?? 0)
}
function preview(audience: { kind: string; unit_ids?: string[] }, channels: string[]) {
  const n = recipients(audience)
  return {
    recipient_count: n,
    in_app_count: channels.includes('in_app') ? n : 0,
    email_count: channels.includes('email') ? Math.round(n * 0.8) : 0,
    sms_count: channels.includes('sms') ? Math.round(n * 0.9) : 0,
  }
}

function announcementView(a: Json): Json {
  const reads = db.reads.get(a.id as string) ?? new Set()
  const { file_ids: fileIds, ...rest } = a
  return {
    ...rest,
    attachments: ((fileIds as string[] | undefined) ?? []).map((fid) => {
      const f = db.files.get(fid)
      return {
        id: fid,
        name: (f?.meta.name as string) ?? 'file',
        mime: (f?.meta.mime as string) ?? 'image/png',
        size: (f?.meta.size as number) ?? 1,
      }
    }),
    read_count: (a.read_count as number) + reads.size,
    read_at: reads.has(personId()) ? NOW() : null,
  }
}

function auditPush(action: string, target_type: string, target_id: string, diff: Json) {
  const at = NOW()
  db.audit.unshift({
    id: id('ae'),
    org_id: ORG,
    site_id: SITE,
    actor_user_id: `u-${db.role}`,
    actor_display_name: NAME[db.role],
    action,
    target_type,
    target_id,
    diff,
    ip: '127.0.0.1',
    request_id: id('req'),
    at,
    created_at: at,
    updated_at: at,
  })
}

// 1x1 transparent PNG, used for every uploaded or seeded image so previews render
const PNG = Uint8Array.from(
  atob(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  ),
  (c) => c.charCodeAt(0),
)
const PDF = new TextEncoder().encode(
  '%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj 2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj 3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF',
)

// ---------------------------------------------------------------------------------------------
// route table
// ---------------------------------------------------------------------------------------------

interface Ctx {
  p: string[]
  url: URL
  request: Request
}
type Handler = (c: Ctx) => Response | Promise<Response>
const routes: { method: string; re: RegExp; fn: Handler }[] = []
function route(method: string, pattern: string, fn: Handler) {
  const re = new RegExp(
    '^/v1' +
      pattern.replace(/\{[a-z_]+\}/g, '([^/:]+)').replaceAll('/orgs/ORG', '/orgs/[^/]+') +
      '$',
  )
  routes.push({ method, re, fn })
}
const O = '/orgs/ORG'
const body = async <T = Json>(request: Request) => (await request.json()) as T

// -- ticket workflow and categories
route('GET', `${O}/ticket-workflow`, ({ url }) =>
  json({ site_id: url.searchParams.get('site_id'), ...db.workflow }),
)
route('PUT', `${O}/ticket-workflow`, async ({ request, url }) => {
  if (!can('tickets.manage')) return forbidden()
  const bad = stale(db.workflow as unknown as Json, request)
  if (bad) return bad
  const b = await body<{ statuses: Json[]; replacements?: Record<string, string> }>(request)
  const keptIds = new Set(b.statuses.map((s) => s.id))
  const removed = db.workflow.statuses.filter((s) => !keptIds.has(s.id))
  for (const r of removed) {
    const inUse = db.tickets.some((t) => t.status_id === r.id)
    if (inUse && !b.replacements?.[r.id as string])
      return problem(409, 'conflict', 'replacement needed')
  }
  for (const r of removed) {
    const to = b.replacements?.[r.id as string]
    for (const t of db.tickets) {
      if (t.status_id === r.id && to) t.status_id = to
    }
  }
  db.workflow.statuses = b.statuses.map((s, i) => {
    const old = db.workflow.statuses.find((o) => o.id === s.id)
    return {
      id: (s.id as string) ?? id('st'),
      name: s.name,
      semantic: s.semantic,
      position: i,
      key: old?.key ?? null,
    }
  })
  db.workflow.version += 1
  db.workflow.is_default = false
  for (const t of db.tickets) syncTicketStatus(t)
  auditPush('ticket_workflow.updated', 'ticket_workflow', url.searchParams.get('site_id') ?? SITE, {
    statuses: b.statuses.map((s) => s.name),
  })
  return json({ site_id: SITE, ...db.workflow })
})
route('GET', `${O}/ticket-categories`, ({ url }) =>
  page(
    db.categories.filter((c) => {
      const which = url.searchParams.get('archived') ?? 'active'
      return which === 'all' || c.archived === (which === 'archived')
    }),
    url,
  ),
)
route('POST', `${O}/ticket-categories`, async ({ request }) => {
  if (!can('tickets.manage')) return forbidden()
  const b = await body(request)
  if (!String(b.name ?? '').trim())
    return problem(422, 'validation_failed', undefined, [{ field: 'name', code: 'required' }])
  const c = {
    id: id('cat'),
    org_id: ORG,
    site_id: b.site_id,
    name: b.name,
    default_visibility: b.default_visibility ?? 'private',
    default_assignee_role: b.default_assignee_role ?? null,
    position: db.categories.length + 1,
    archived: false,
    created_at: NOW(),
    updated_at: NOW(),
    version: 1,
  }
  db.categories.push(c)
  return json(c, 201)
})
route('GET', `${O}/ticket-categories/{id}`, ({ p }) => {
  const c = db.categories.find((x) => x.id === p[0])
  return c ? json(c) : notFound()
})
route('PATCH', `${O}/ticket-categories/{id}`, async ({ p, request }) => {
  if (!can('tickets.manage')) return forbidden()
  const c = db.categories.find((x) => x.id === p[0])
  if (!c) return notFound()
  const bad = stale(c, request)
  if (bad) return bad
  return json(touch(c, await body(request)))
})

// -- tickets
route('GET', `${O}/tickets`, ({ url }) => {
  const q = url.searchParams
  let items = db.tickets.filter(visibleTicket)
  const eq = (param: string, field: string) => {
    const v = q.get(param)
    if (v) items = items.filter((t) => t[field] === v)
  }
  eq('category_id', 'category_id')
  eq('status_id', 'status_id')
  eq('unit_id', 'unit_id')
  eq('visibility', 'visibility')
  eq('semantic', 'status_semantic')
  const ap = q.get('assignee_person_id')
  if (ap) items = items.filter((t) => (t.assignee as Json | null)?.person_id === ap)
  const av = q.get('assignee_vendor_id')
  if (av) items = items.filter((t) => (t.assignee as Json | null)?.vendor_id === av)
  if (q.get('mine') === 'true') items = items.filter((t) => t.reporter_person_id === personId())
  if (q.get('queue') === 'true') {
    items = items.filter(
      (t) =>
        t.status_semantic === 'open_like' && (t.assignee as Json | null)?.person_id === personId(),
    )
  }
  const text = q.get('q')?.toLowerCase()
  if (text) items = items.filter((t) => String(t.title).toLowerCase().includes(text))
  items = [...items].sort((a, b) => String(b.created_at).localeCompare(String(a.created_at)))
  return page(items.map(ticketView), url)
})
route('POST', `${O}/tickets`, async ({ request }) => {
  if (!can('tickets.open')) return forbidden()
  const b = await body<Json & { attachment_file_ids?: string[] }>(request)
  const errors: Json[] = []
  if (!String(b.title ?? '').trim()) errors.push({ field: 'title', code: 'required' })
  if (!b.category_id) errors.push({ field: 'category_id', code: 'required' })
  if (errors.length) return problem(422, 'validation_failed', undefined, errors)
  const cat = db.categories.find((c) => c.id === b.category_id)
  const open = db.workflow.statuses[0]!
  const reporter = (can('tickets.manage') && (b.reporter_person_id as string)) || personId()
  const created = NOW()
  const tid = id('t')
  const t = {
    id: tid,
    org_id: ORG,
    site_id: b.site_id,
    unit_id: b.unit_id ?? null,
    unit_door_number: UNITS.find((u) => u.id === b.unit_id)?.door_number ?? null,
    reporter_person_id: reporter,
    reporter_display_name: reporter === personId() ? NAME[db.role] : 'Resident',
    category_id: b.category_id,
    title: b.title,
    description: b.description,
    status_id: open.id,
    status_name: open.name,
    status_semantic: open.semantic,
    visibility: (can('tickets.manage') && b.visibility) || cat?.default_visibility || 'private',
    assignee: null,
    vote_count: 0,
    voted_by_me: false,
    merged_into_id: null,
    comment_count: 0,
    attachments: (b.attachment_file_ids ?? []).map((fid) => attachmentFor(tid, fid, null)),
    closed_at: null,
    created_at: created,
    updated_at: created,
    version: 1,
  }
  db.tickets.push(t)
  db.history.push({
    id: id('h'),
    org_id: ORG,
    ticket_id: tid,
    from_status_id: null,
    to_status_id: open.id,
    changed_by: `u-${db.role}`,
    comment: null,
    at: created,
    created_at: created,
    updated_at: created,
  })
  auditPush('ticket.created', 'ticket', tid, { title: t.title })
  return json(ticketView(t), 201)
})

function attachmentFor(ticketId: string, fileId: string, commentId: string | null): Json {
  const f = db.files.get(fileId)
  return {
    id: id('att'),
    org_id: ORG,
    ticket_id: ticketId,
    comment_id: commentId,
    file_id: fileId,
    mime: (f?.meta.mime as string) ?? 'image/png',
    size: (f?.meta.size as number) ?? 1,
    created_at: NOW(),
    updated_at: NOW(),
  }
}

const findTicket = (tid: string | undefined) =>
  db.tickets.find((t) => t.id === tid && visibleTicket(t))
route('GET', `${O}/tickets/{id}`, ({ p }) => {
  const t = findTicket(p[0])
  return t ? json(ticketView(t)) : notFound()
})
route('PATCH', `${O}/tickets/{id}`, async ({ p, request }) => {
  if (!can('tickets.manage')) return forbidden()
  const t = findTicket(p[0])
  if (!t) return notFound()
  const bad = stale(t, request)
  if (bad) return bad
  const b = await body<Json>(request)
  const patch: Json = { ...b }
  delete patch.assignee_person_id
  delete patch.assignee_vendor_id
  if ('assignee_person_id' in b || 'assignee_vendor_id' in b) {
    if (b.assignee_person_id) {
      const name = b.assignee_person_id === 'p-staff' ? 'Hasan Demir' : 'Staff member'
      patch.assignee = {
        kind: 'person',
        person_id: b.assignee_person_id,
        vendor_id: null,
        display_name: name,
      }
    } else if (b.assignee_vendor_id) {
      const name = b.assignee_vendor_id === 'v-1' ? 'Yılmaz Plumbing' : 'Vendor'
      patch.assignee = {
        kind: 'vendor',
        person_id: null,
        vendor_id: b.assignee_vendor_id,
        display_name: name,
      }
    } else patch.assignee = null
  }
  touch(t, patch)
  return json(ticketView(t))
})
route('POST', `${O}/tickets/{id}:transition`, async ({ p, request }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  // tickets.work is "assigned tickets" (PERMISSIONS.md): a work-only user may not touch others
  const mine = (t.assignee as Json | null)?.person_id === personId()
  if (!can('tickets.manage') && !(can('tickets.work') && mine)) return forbidden()
  const b = await body<Json>(request)
  const to = statusOf(b.status_id as string)
  if (!to)
    return problem(422, 'validation_failed', undefined, [{ field: 'status_id', code: 'unknown' }])
  const from = t.status_id
  const at = NOW()
  touch(t, {
    status_id: to.id,
    closed_at: to.semantic === 'closed_like' ? at : null,
  })
  syncTicketStatus(t)
  const comment = typeof b.comment === 'string' && b.comment.trim() ? b.comment.trim() : null
  const visibility = b.comment_visibility ?? 'public'
  db.history.push({
    id: id('h'),
    org_id: ORG,
    ticket_id: t.id,
    from_status_id: from,
    to_status_id: to.id,
    changed_by: `u-${db.role}`,
    comment: visibility === 'public' ? comment : null,
    at,
    created_at: at,
    updated_at: at,
  })
  if (comment && visibility === 'internal') pushComment(t.id as string, comment, 'internal', [])
  auditPush('ticket.transitioned', 'ticket', t.id as string, {
    status: { before: statusOf(from as string)?.name ?? '', after: to.name },
  })
  return json(ticketView(t))
})
route('POST', `${O}/tickets/{id}:merge`, async ({ p, request }) => {
  if (!can('tickets.manage')) return forbidden()
  const t = findTicket(p[0])
  const b = await body<{ into_ticket_id: string }>(request)
  const into = findTicket(b.into_ticket_id)
  if (!t || !into) return notFound()
  const myVotes = db.votes.get(t.id as string) ?? new Set<string>()
  const target = db.votes.get(into.id as string) ?? new Set<string>()
  for (const v of myVotes) target.add(v)
  db.votes.set(into.id as string, target)
  into.vote_count = (into.vote_count as number) + (t.vote_count as number)
  const closed = db.workflow.statuses.find((s) => s.semantic === 'closed_like')!
  touch(t, { merged_into_id: into.id, status_id: closed.id, closed_at: NOW() })
  syncTicketStatus(t)
  return json(ticketView(t))
})
route('PUT', `${O}/tickets/{id}/vote`, ({ p }) => {
  if (!can('tickets.open')) return forbidden()
  const t = findTicket(p[0])
  if (!t) return notFound()
  const set = db.votes.get(t.id as string) ?? new Set<string>()
  set.add(personId())
  db.votes.set(t.id as string, set)
  return json({ ticket_id: t.id, vote_count: ticketView(t).vote_count, voted: true })
})
route('DELETE', `${O}/tickets/{id}/vote`, ({ p }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  db.votes.get(t.id as string)?.delete(personId())
  return json({ ticket_id: t.id, vote_count: ticketView(t).vote_count, voted: false })
})
function pushComment(tid: string, text: string, visibility: string, fileIds: string[]) {
  const cid = id('c')
  const c = {
    id: cid,
    org_id: ORG,
    ticket_id: tid,
    author_person_id: personId(),
    author_display_name: NAME[db.role],
    body: text,
    visibility,
    attachments: fileIds.map((f) => attachmentFor(tid, f, cid)),
    created_at: NOW(),
    updated_at: NOW(),
  }
  db.comments.push(c)
  return c
}
route('GET', `${O}/tickets/{id}/comments`, ({ p, url }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  return page(
    db.comments.filter((c) => c.ticket_id === t.id && commentVisible(c)),
    url,
  )
})
route('POST', `${O}/tickets/{id}/comments`, async ({ p, request }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  const b = await body<{ body: string; visibility?: string; attachment_file_ids?: string[] }>(
    request,
  )
  if (!b.body?.trim())
    return problem(422, 'validation_failed', undefined, [{ field: 'body', code: 'required' }])
  const visibility = isStaffSide() && b.visibility === 'internal' ? 'internal' : 'public'
  const c = pushComment(t.id as string, b.body, visibility, b.attachment_file_ids ?? [])
  return json(c, 201)
})
route('GET', `${O}/tickets/{id}/history`, ({ p, url }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  return page(
    db.history.filter((h) => h.ticket_id === t.id),
    url,
  )
})
route('POST', `${O}/tickets/{id}/attachments`, async ({ p, request }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  const b = await body<{ file_id: string }>(request)
  const a = attachmentFor(t.id as string, b.file_id, null)
  ;(t.attachments as Json[]).push(a)
  return json(a, 201)
})
route('DELETE', `${O}/tickets/{id}/attachments/{aid}`, ({ p }) => {
  const t = findTicket(p[0])
  if (!t) return notFound()
  t.attachments = (t.attachments as Json[]).filter((a) => a.id !== p[1])
  return new HttpResponse(null, { status: 204 })
})

// -- announcements
route('GET', `${O}/announcements`, ({ url }) => {
  const q = url.searchParams
  let items = db.announcements
  if (!can('announcements.publish')) items = items.filter((a) => a.status === 'published')
  const status = q.get('status')
  if (status) items = items.filter((a) => a.status === status)
  if (q.get('pinned') === 'true') items = items.filter((a) => a.pinned)
  const views = items.map(announcementView)
  if (q.get('unread') === 'true') {
    return page(
      views.filter((a) => !a.read_at),
      url,
    )
  }
  views.sort(
    (a, b) =>
      Number(b.pinned) - Number(a.pinned) ||
      String(b.created_at).localeCompare(String(a.created_at)),
  )
  return page(views, url)
})
function applyChannels(b: Json) {
  const channels = (b.channels as string[] | undefined) ?? ['in_app']
  return channels
}
route('POST', `${O}/announcements`, async ({ request }) => {
  if (!can('announcements.publish')) return forbidden()
  const b = await body<Json>(request)
  const errors: Json[] = []
  if (!String(b.title ?? '').trim()) errors.push({ field: 'title', code: 'required' })
  if (!String(b.body_md ?? '').trim()) errors.push({ field: 'body_md', code: 'required' })
  if (errors.length) return problem(422, 'validation_failed', undefined, errors)
  const at = NOW()
  const scheduled = b.publish_at && new Date(b.publish_at as string).getTime() > Date.now()
  const a = {
    id: id('a'),
    org_id: ORG,
    site_id: b.site_id,
    title: b.title,
    body_md: b.body_md,
    status: scheduled ? 'scheduled' : 'draft',
    audience: b.audience,
    publish_at: b.publish_at ?? null,
    expires_at: b.expires_at ?? null,
    pinned: !!b.pinned,
    channels: applyChannels(b),
    recipient_count: null,
    sms_count: null,
    read_count: 0,
    read_at: null,
    file_ids: b.file_ids ?? [],
    published_at: null,
    created_by: `u-${db.role}`,
    created_at: at,
    updated_at: at,
    version: 1,
  }
  db.announcements.push(a)
  return json(announcementView(a), 201)
})
route('POST', `/orgs/ORG/announcements:preview`, async ({ request }) => {
  if (!can('announcements.publish')) return forbidden()
  const b = await body<{ audience: { kind: string; unit_ids?: string[] }; channels: string[] }>(
    request,
  )
  return json(preview(b.audience, b.channels))
})
const findAnn = (aid: string | undefined) =>
  db.announcements.find(
    (a) => a.id === aid && (can('announcements.publish') || a.status === 'published'),
  )
route('GET', `${O}/announcements/{id}`, ({ p }) => {
  const a = findAnn(p[0])
  return a ? json(announcementView(a)) : notFound()
})
route('PATCH', `${O}/announcements/{id}`, async ({ p, request }) => {
  if (!can('announcements.publish')) return forbidden()
  const a = findAnn(p[0])
  if (!a) return notFound()
  const bad = stale(a, request)
  if (bad) return bad
  return json(announcementView(touch(a, await body(request))))
})
route('DELETE', `${O}/announcements/{id}`, ({ p }) => {
  if (!can('announcements.publish')) return forbidden()
  db.announcements = db.announcements.filter((a) => a.id !== p[0])
  return new HttpResponse(null, { status: 204 })
})
route('POST', `${O}/announcements/{id}:publish`, async ({ p, request }) => {
  if (!can('announcements.publish')) return forbidden()
  if (!request.headers.get('Idempotency-Key')) {
    return problem(422, 'validation_failed', 'Idempotency-Key is required')
  }
  const a = findAnn(p[0])
  if (!a) return notFound()
  let b: { channels?: string[] } = {}
  try {
    b = await body(request)
  } catch {
    /* empty body: keep the channels saved on the announcement */
  }
  const channels = b.channels ?? (a.channels as string[])
  const pv = preview(a.audience as { kind: string }, channels)
  db.smsSent += pv.sms_count
  touch(a, {
    status: 'published',
    channels,
    recipient_count: pv.recipient_count,
    sms_count: pv.sms_count,
    published_at: NOW(),
  })
  auditPush('announcement.published', 'announcement', a.id as string, {
    title: a.title,
    channels,
  })
  db.notifications.unshift({
    id: id('n'),
    site_id: SITE,
    type: 'announcement.published',
    payload: { title: a.title },
    read_at: null,
    link: `/orgs/${ORG}/announcements/${a.id as string}`,
    created_at: NOW(),
  })
  return json(announcementView(a))
})
route('POST', `${O}/announcements/{id}:archive`, ({ p }) => {
  if (!can('announcements.publish')) return forbidden()
  const a = findAnn(p[0])
  if (!a) return notFound()
  touch(a, { status: 'archived' })
  return json(announcementView(a))
})
route('GET', `${O}/announcements/{id}/reads`, ({ p, url }) => {
  if (!can('announcements.publish')) return forbidden()
  const a = findAnn(p[0])
  if (!a) return notFound()
  const names = ['Ayşe Yılmaz', 'Zeynep Arslan', 'Can Öztürk', 'Elif Şahin']
  const readers = db.reads.get(a.id as string) ?? new Set<string>()
  const people = names.map((name, i) => ({
    person_id: `p-n${i}`,
    display_name: name,
    unit_door_numbers: [String(i + 3)],
    read_at: i < 2 || readers.has(`p-n${i}`) ? hours(i + 1) : null,
  }))
  if (readers.has(PERSON.resident)) {
    people.push({
      person_id: PERSON.resident,
      display_name: NAME.resident,
      unit_door_numbers: ['12'],
      read_at: NOW(),
    })
  }
  const wantRead = url.searchParams.get('read')
  const items =
    wantRead === null
      ? people
      : people.filter((x) => (x.read_at !== null) === (wantRead === 'true'))
  const limit = Number(url.searchParams.get('limit') ?? 50)
  return json({
    items: items.slice(0, limit),
    next_cursor: null,
    audience_count: recipients(a.audience as { kind: string }),
    read_count: people.filter((x) => x.read_at).length,
  })
})
route('PUT', `${O}/announcements/{id}/read`, ({ p }) => {
  const a = findAnn(p[0])
  if (!a) return notFound()
  const set = db.reads.get(a.id as string) ?? new Set<string>()
  set.add(personId())
  db.reads.set(a.id as string, set)
  return new HttpResponse(null, { status: 204 })
})

// -- folders and documents
const folderVisible = (f: Json) => {
  if (can('documents.manage') || db.role === 'staff') return true
  const level = (f.visibility as { level: string }).level
  if (level === 'everyone') return true
  if (level === 'owners') return db.role === 'resident'
  if (level === 'units') {
    const units = ((f.visibility as { unit_ids?: string[] }).unit_ids ?? []) as string[]
    return db.role === 'resident' && units.includes('unit-12')
  }
  return false
}
route('GET', `${O}/folders`, ({ url }) => {
  const parent = url.searchParams.get('parent_id')
  let items = db.folders
    .filter(folderVisible)
    .filter((f) => (f.parent_id ?? null) === (parent ?? null))
  const kind = url.searchParams.get('kind')
  if (kind) items = items.filter((f) => f.kind === kind)
  return page(items, url)
})
function breadcrumb(f: Json): Json[] {
  const crumbs: Json[] = []
  let cur: Json | undefined = f
  while (cur) {
    crumbs.unshift({ id: cur.id, name: cur.name })
    cur = db.folders.find((x) => x.id === cur!.parent_id)
  }
  return crumbs
}
route('POST', `${O}/folders`, async ({ request }) => {
  if (!can('documents.manage')) return forbidden()
  const b = await body<Json>(request)
  if (!String(b.name ?? '').trim())
    return problem(422, 'validation_failed', undefined, [{ field: 'name', code: 'required' }])
  const parent = db.folders.find((f) => f.id === b.parent_id)
  const f: Json = {
    id: id('f'),
    org_id: ORG,
    site_id: b.site_id,
    parent_id: b.parent_id ?? null,
    name: b.name,
    kind: 'site',
    unit_id: null,
    visibility: b.visibility ?? parent?.visibility ?? { level: 'everyone' },
    breadcrumb: [],
    is_system: false,
    created_at: NOW(),
    updated_at: NOW(),
    version: 1,
  }
  f.breadcrumb = [...(parent ? breadcrumb(parent) : []), { id: f.id, name: f.name }]
  db.folders.push(f)
  return json(f, 201)
})
route('GET', `${O}/folders/{id}`, ({ p }) => {
  const f = db.folders.find((x) => x.id === p[0] && folderVisible(x))
  return f ? json({ ...f, breadcrumb: breadcrumb(f) }) : notFound()
})
route('PATCH', `${O}/folders/{id}`, async ({ p, request }) => {
  if (!can('documents.manage')) return forbidden()
  const f = db.folders.find((x) => x.id === p[0])
  if (!f) return notFound()
  const bad = stale(f, request)
  if (bad) return bad
  touch(f, await body(request))
  f.breadcrumb = breadcrumb(f)
  auditPush('folder.updated', 'folder', f.id as string, { visibility: f.visibility })
  return json(f)
})
route('DELETE', `${O}/folders/{id}`, ({ p }) => {
  if (!can('documents.manage')) return forbidden()
  const f = db.folders.find((x) => x.id === p[0])
  if (!f) return notFound()
  if (f.is_system) return problem(409, 'conflict', 'system folder')
  db.folders = db.folders.filter((x) => x.id !== p[0])
  return new HttpResponse(null, { status: 204 })
})
route('GET', `${O}/documents`, ({ url }) => {
  const q = url.searchParams
  let items = db.documents.filter((d) => {
    const f = db.folders.find((x) => x.id === d.folder_id)
    return f ? folderVisible(f) : false
  })
  if (q.get('folder_id')) items = items.filter((d) => d.folder_id === q.get('folder_id'))
  if (q.get('source')) items = items.filter((d) => d.source === q.get('source'))
  const text = q.get('q')?.toLowerCase()
  if (text) items = items.filter((d) => String(d.title).toLowerCase().includes(text))
  return page(items, url)
})
route('POST', `${O}/documents`, async ({ request }) => {
  if (!can('documents.manage') && !can('documents.upload_own')) return forbidden()
  const b = await body<Json>(request)
  const f = db.files.get(b.file_id as string)
  const d = {
    id: id('d'),
    org_id: ORG,
    site_id: b.site_id,
    folder_id: b.folder_id,
    title: b.title,
    file_id: b.file_id,
    mime: (f?.meta.mime as string) ?? 'application/pdf',
    size: (f?.meta.size as number) ?? 0,
    visibility: b.visibility ?? null,
    source: 'uploaded',
    generated_from: null,
    uploaded_by: `u-${db.role}`,
    created_at: NOW(),
    updated_at: NOW(),
    version: 1,
  }
  db.documents.push(d)
  return json(d, 201)
})
route('GET', `${O}/documents/{id}`, ({ p }) => {
  const d = db.documents.find((x) => x.id === p[0])
  return d ? json(d) : notFound()
})
route('PATCH', `${O}/documents/{id}`, async ({ p, request }) => {
  const d = db.documents.find((x) => x.id === p[0])
  if (!d) return notFound()
  if (!can('documents.manage')) return forbidden()
  const bad = stale(d, request)
  if (bad) return bad
  return json(touch(d, await body(request)))
})
route('DELETE', `${O}/documents/{id}`, ({ p }) => {
  if (!can('documents.manage')) return forbidden()
  db.documents = db.documents.filter((x) => x.id !== p[0])
  return new HttpResponse(null, { status: 204 })
})

// -- files
route('POST', `${O}/files`, async ({ request }) => {
  if (!can('files.upload')) return forbidden()
  const form = await request.formData()
  const file = form.get('file')
  if (!(file instanceof Blob))
    return problem(422, 'validation_failed', undefined, [{ field: 'file', code: 'required' }])
  if (file.size > 25 * 1024 * 1024) return problem(413, 'payload_too_large')
  const fid = id('file')
  const meta = {
    id: fid,
    org_id: ORG,
    site_id: (form.get('site_id') as string) ?? null,
    size: file.size,
    mime: file.type || 'application/octet-stream',
    sha256: 'x'.repeat(64),
    uploaded_by: `u-${db.role}`,
    scope: (form.get('scope') as string) ?? null,
    status: 'ready',
    has_variants: file.type.startsWith('image/'),
    retention_until: null,
    created_at: NOW(),
    updated_at: NOW(),
  }
  db.files.set(fid, { meta, bytes: await file.arrayBuffer() })
  return json(meta, 201)
})
route('GET', `${O}/files/{id}/meta`, ({ p }) => {
  const f = db.files.get(p[0]!)
  return f ? json(f.meta) : notFound()
})
route('GET', `${O}/files/{id}`, ({ p, url }) => {
  const f = db.files.get(p[0]!)
  const variant = url.searchParams.get('variant')
  if (f) {
    const isImage = String(f.meta.mime).startsWith('image/')
    void variant
    void isImage
    return new HttpResponse(f.bytes, {
      headers: { 'Content-Type': f.meta.mime as string },
    })
  }
  if (p[0]!.startsWith('file-seed')) {
    return new HttpResponse(PDF, { headers: { 'Content-Type': 'application/pdf' } })
  }
  if (p[0]!.startsWith('img-'))
    return new HttpResponse(PNG, { headers: { 'Content-Type': 'image/png' } })
  return notFound()
})
route('DELETE', `${O}/files/{id}`, ({ p }) => {
  db.files.delete(p[0]!)
  return new HttpResponse(null, { status: 204 })
})

// -- notifications
route('GET', `${O}/me/notifications`, ({ url }) => {
  let items = db.notifications
  if (url.searchParams.get('unread') === 'true') items = items.filter((n) => !n.read_at)
  const type = url.searchParams.get('type')
  if (type) items = items.filter((n) => n.type === type)
  return page(items, url)
})
route('GET', `${O}/me/notifications/unread-count`, () =>
  json({ unread: db.notifications.filter((n) => !n.read_at).length, visitor_approvals_pending: 0 }),
)
route('POST', `${O}/me/notifications/{id}:read`, ({ p }) => {
  const n = db.notifications.find((x) => x.id === p[0])
  if (!n) return notFound()
  n.read_at = n.read_at ?? NOW()
  return json(n)
})
route('POST', `/orgs/ORG/me/notifications:read-all`, () => {
  for (const n of db.notifications) n.read_at = n.read_at ?? NOW()
  return new HttpResponse(null, { status: 204 })
})
route('GET', `${O}/notification-defaults`, ({ url }) =>
  json({
    site_id: url.searchParams.get('site_id'),
    defaults: db.defaults,
    version: db.defaultsVersion,
  }),
)
route('PUT', `${O}/notification-defaults`, async ({ request, url }) => {
  if (!can('notifications.manage')) return forbidden()
  const bad = stale({ version: db.defaultsVersion }, request)
  if (bad) return bad
  db.defaultsVersion += 1
  const b = await body<{ defaults: { type: string; channels: string[] }[] }>(request)
  for (const d of b.defaults) {
    const row = db.defaults.find((x) => x.type === d.type)
    if (row && !row.locked) row.channels = d.channels
  }
  auditPush('notification_defaults.updated', 'site', url.searchParams.get('site_id') ?? SITE, {
    defaults: b.defaults.length,
  })
  return json({ site_id: url.searchParams.get('site_id'), defaults: db.defaults })
})
route('GET', `${O}/me/notification-mutes`, () =>
  json({ muted_types: db.mutes, unmutable_types: [...LOCKED] }),
)
route('PUT', `${O}/me/notification-mutes`, async ({ request }) => {
  const b = await body<{ muted_types: string[] }>(request)
  if (b.muted_types.some((t) => LOCKED.has(t))) return problem(422, 'validation_failed')
  db.mutes = b.muted_types
  return json({ muted_types: db.mutes, unmutable_types: [...LOCKED] })
})
route('GET', `${O}/sms-usage`, ({ url }) =>
  json({
    site_id: url.searchParams.get('site_id'),
    month: url.searchParams.get('month') ?? new Date().toISOString().slice(0, 7),
    sent: db.smsSent,
    delivered: Math.round(db.smsSent * 0.97),
    failed: Math.round(db.smsSent * 0.01),
    monthly_limit: 1000,
  }),
)

// -- jobs, exports
route('GET', `${O}/jobs`, ({ url }) =>
  page(
    [
      {
        id: 'job-1',
        org_id: ORG,
        kind: 'statement',
        status: 'succeeded',
        progress: 1,
        result_file_id: null,
        result: null,
        error: null,
        started_at: hours(3),
        finished_at: hours(3),
        created_by: 'u-manager',
        created_at: hours(3),
        updated_at: hours(3),
      },
    ],
    url,
  ),
)

// -- audit
route('GET', `${O}/audit-events`, ({ url }) => {
  if (!can('audit.read')) return forbidden()
  const q = url.searchParams
  let items = db.audit
  const eq = (param: string, field: string) => {
    const v = q.get(param)
    if (v) items = items.filter((e) => e[field] === v)
  }
  eq('actor_user_id', 'actor_user_id')
  eq('action', 'action')
  eq('target_type', 'target_type')
  eq('target_id', 'target_id')
  const from = q.get('from')
  if (from) items = items.filter((e) => String(e.at) >= from)
  const to = q.get('to')
  if (to) items = items.filter((e) => String(e.at) <= to)
  return page(items, url)
})
route('GET', `${O}/audit-events/{id}`, ({ p }) => {
  const e = db.audit.find((x) => x.id === p[0])
  return e ? json(e) : notFound()
})

// -- dashboards, codes, organization
route('GET', `${O}/sites/{id}/staff-dashboard`, ({ p }) => {
  if (!can('dashboard.staff')) return forbidden()
  const open = db.tickets.filter((t) => t.status_semantic === 'open_like' && !t.merged_into_id)
  return json({
    site_id: p[0],
    as_of: NOW(),
    assigned_open_tickets: open.filter((t) => (t.assignee as Json | null)?.person_id === personId())
      .length,
    assigned_overdue_tickets: 0,
    open_common_tickets: open.filter((t) => t.visibility === 'common').length,
    expected_visitors_today: 4,
    waiting_deliveries: 2,
  })
})
route('GET', `${O}/sites/{id}/dashboard`, ({ p }) => {
  if (!can('dashboard.site')) return forbidden()
  const money = (amount: number) => ({ amount, currency: 'TRY' })
  const open = db.tickets.filter((t) => t.status_semantic === 'open_like' && !t.merged_into_id)
  return json({
    site_id: p[0],
    as_of: NOW(),
    receivable_owner: money(18_450_000),
    receivable_tenant: money(32_200_000),
    overdue_total: money(9_600_000),
    overdue_units: 14,
    cash: money(2_500_000),
    bank: money(61_820_000),
    collection_rate_pct: 87.5,
    accrued_late_fees: money(412_000),
    open_tickets: open.length,
    open_common_tickets: open.filter((t) => t.visibility === 'common').length,
    pending_unit_claims: 2,
    pending_declared_payments: 3,
    waiting_deliveries: 0,
    pending_visitor_approvals: 0,
    upcoming_meeting: {
      id: 'm-1',
      title: 'Annual general assembly',
      scheduled_at: new Date(Date.now() + 12 * 86_400_000).toISOString(),
      status: 'called',
    },
    expiring_terms: [
      {
        membership_id: 'ms-1',
        person_display_name: 'Selin Aydın',
        role: 'board_member',
        valid_to: new Date(Date.now() + 40 * 86_400_000).toISOString().slice(0, 10),
      },
    ],
  })
})
route('GET', `${O}/codes`, ({ url }) =>
  json({
    locale: url.searchParams.get('locale') ?? 'en',
    enums: {
      occupancy_kind: [
        { code: 'owner', label: 'Owner' },
        { code: 'tenant', label: 'Tenant' },
      ],
      notification_type: DEFAULT_TYPES.map(([code]) => ({ code, label: code })),
    },
    errors: [
      { code: 'insufficient_permission', label: 'You do not have permission to do this.' },
      { code: 'version_conflict', label: 'Someone else changed this record.' },
    ],
    ticket_statuses: [
      { code: 'open', label: 'Open' },
      { code: 'in_progress', label: 'In progress' },
      { code: 'done', label: 'Done' },
    ],
  }),
)
route('GET', `/orgs/ORG`, () => json(db.org))
route('PATCH', `/orgs/ORG`, async ({ request }) => {
  if (!can('org.manage')) return forbidden()
  const bad = stale(db.org, request)
  if (bad) return bad
  touch(db.org, await body(request))
  return json(db.org)
})

// units, blocks, memberships, vendors and persons are answered by the tenancy and people models
// (handlers/tenancy.ts, handlers/people.ts); this model keeps only what the operations screens own.

// -- platform
route('GET', `/platform/orgs`, ({ url }) => page(db.orgs, url))
route('POST', `/platform/orgs`, async ({ request }) => {
  const b = await body<Json>(request)
  if (!String(b.slug ?? '').trim() || !String(b.name ?? '').trim()) {
    return problem(422, 'validation_failed', undefined, [
      ...(!b.slug ? [{ field: 'slug', code: 'required' }] : []),
      ...(!b.name ? [{ field: 'name', code: 'required' }] : []),
    ])
  }
  if (db.orgs.some((o) => o.slug === b.slug)) {
    return problem(409, 'conflict', undefined, [{ field: 'slug', code: 'taken' }])
  }
  const o = {
    id: id('org'),
    slug: b.slug,
    name: b.name,
    status: 'active',
    default_locale: b.default_locale ?? 'tr',
    timezone: b.timezone ?? 'Europe/Istanbul',
    country_code: b.country_code ?? 'TR',
    site_count: 0,
    created_at: NOW(),
    updated_at: NOW(),
    version: 1,
  }
  db.orgs.push(o)
  return json(o, 201)
})
route('PATCH', `/platform/orgs/{id}`, async ({ p, request }) => {
  const o = db.orgs.find((x) => x.id === p[0])
  if (!o) return notFound()
  const bad = stale(o, request)
  if (bad) return bad
  return json(touch(o, await body(request)))
})
route('GET', `/platform/settings`, () => json(db.settings))
route('PATCH', `/platform/settings`, async ({ request }) => {
  const bad = stale(db.settings, request)
  if (bad) return bad
  return json(touch(db.settings, await body(request)))
})
route('GET', `/platform/health`, () =>
  json({
    status: 'ok',
    server_version: '0.1.0',
    checks: [
      { name: 'database', status: 'ok', detail: null, latency_ms: 3 },
      { name: 'storage', status: 'ok', detail: null, latency_ms: 8 },
      { name: 'mail', status: 'degraded', detail: 'queue is slow', latency_ms: 420 },
    ],
    jobs_queued: 2,
    jobs_failed: 0,
  }),
)
route('GET', `/platform/backup-status`, () =>
  json({
    last_backup_at: hours(9),
    last_backup_status: 'ok',
    last_backup_size_bytes: 184_320_000,
    next_backup_at: new Date(Date.now() + 15 * 3_600_000).toISOString(),
  }),
)
route('GET', `/platform/jobs`, ({ url }) =>
  page(
    [
      {
        id: '1001',
        kind: 'send_notification',
        queue: 'default',
        state: 'completed',
        attempts: 1,
        org_id: ORG,
        created_at: hours(2),
        finalized_at: hours(2),
        error: null,
      },
      {
        id: '1002',
        kind: 'generate_statement',
        queue: 'default',
        state: 'retryable',
        attempts: 3,
        org_id: ORG,
        created_at: hours(1),
        finalized_at: null,
        error: 'timeout',
      },
    ],
    url,
  ),
)

// ---------------------------------------------------------------------------------------------

resetOperationsDb()
registerReset(() => resetOperationsDb())

/** Role-aware `/v1/me` for mock mode and tests; list it before the generic fixture handler. */
export const meByRoleHandler = http.get('*/v1/me', () => HttpResponse.json(meForRole(db.role)))

async function fingerprintOf(request: Request): Promise<string> {
  const head = `${request.method} ${new URL(request.url).pathname}`
  const type = request.headers.get('Content-Type') ?? ''
  if (type.startsWith('multipart/')) {
    // boundaries differ per request: compare the parts instead
    const form = await request.clone().formData()
    const parts = [...form.entries()].map(([k, v]) =>
      v instanceof Blob ? `${k}=${(v as File).name}:${v.size}:${v.type}` : `${k}=${String(v)}`,
    )
    return `${head} ${parts.join('&')}`
  }
  return `${head} ${await request.clone().text()}`
}

export const operationsHandlers = [
  http.all('*/v1/*', async ({ request }) => {
    const url = new URL(request.url)
    const path = url.pathname
    for (const r of routes) {
      if (r.method !== request.method) continue
      const m = r.re.exec(path)
      if (!m) continue
      const ctx = { p: m.slice(1).map(decodeURIComponent), url, request }
      const key = request.headers.get('Idempotency-Key')
      if (!key) return r.fn(ctx)
      const fingerprint = await fingerprintOf(request)
      const seen = idempotent.get(key)
      if (seen) {
        if (seen.fingerprint !== fingerprint)
          return problem(
            422,
            'idempotency_mismatch',
            'This Idempotency-Key was used with a different request.',
          )
        return new HttpResponse(seen.body || null, { status: seen.status, headers: seen.headers })
      }
      const res = await r.fn(ctx)
      if (res.status >= 200 && res.status < 300) {
        idempotent.set(key, {
          fingerprint,
          status: res.status,
          body: await res.clone().text(),
          headers: [...res.headers.entries()],
        })
      }
      return res
    }
    return undefined
  }),
]
