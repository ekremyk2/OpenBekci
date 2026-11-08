import { http, HttpResponse } from 'msw'
import { parseCsv } from '@/lib/csv'
import { registerReset } from '../reset'
import type { components } from '@/api/draft'
import {
  CUR,
  effectiveOf,
  accruedFee,
  addMonths,
  applyToCharge,
  autoAllocate,
  createMoneyDb,
  daysBetween,
  debtorFor,
  generateCharges,
  isFrozen,
  m,
  nextId,
  openPrincipal,
  personName,
  previewSplit,
  refreshCurrentRate,
  seedBankImport,
  sidesWithDebt,
  type Side,
} from './money-db'

/**
 * MSW handlers for the money module, written from the draft spec (api/src/paths/money/*.yaml) over
 * the in-memory model in money-db.ts. Used by Vitest and by `pnpm dev:mock`.
 */
type S = components['schemas']

let db = createMoneyDb()
export const getMoneyDb = () => db
export function resetMoneyDb(today?: string) {
  db = createMoneyDb(today)
  moneyScope = true
  return db
}

// jsdom and Node have different File classes; duck-type instead of instanceof
const isFile = (x: unknown): x is File =>
  typeof x === 'object' && x !== null && typeof (x as File).text === 'function'

class Reply {
  constructor(
    readonly status: number,
    readonly body: unknown,
  ) {}
}
const ok = (body: unknown) => new Reply(200, body)
const created = (body: unknown) => new Reply(201, body)
const noContent = () => new Reply(204, null)
const fail = (
  status: number,
  code: string,
  errors?: { field: string; code: string; message?: string }[],
) =>
  new Reply(status, {
    type: `https://openbekci.org/problems/${code}`,
    title: code,
    status,
    code,
    errors,
  })
const list = <T>(items: T[]) => ok({ items, next_cursor: null })

interface Ctx {
  params: Record<string, string>
  query: URLSearchParams
  body: any // eslint-disable-line @typescript-eslint/no-explicit-any
  form: FormData | null
  request: Request
}

type Handler = (c: Ctx) => Reply | Promise<Reply>

/** The contract caps `limit` (components/parameters/Limit) at 200. */
export const MAX_LIMIT = 200
const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/
const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/

/**
 * Spec-validation layer: the model is hand-written, so the rules a real server enforces from the
 * OpenAPI document are checked here, where every request passes. Returns a 422 reply or null.
 */
export function validateRequest(query: URLSearchParams, body: unknown): Reply | null {
  const errors: { field: string; code: string; message?: string }[] = []
  const limit = query.get('limit')
  if (limit !== null) {
    const n = Number(limit)
    if (!Number.isInteger(n) || n < 1 || n > MAX_LIMIT)
      errors.push({
        field: 'limit',
        code: 'out_of_range',
        message: `limit must be 1..${MAX_LIMIT}`,
      })
  }
  // Request fields are checked against the contract types: `*_at` is `date-time` (RFC 3339 with an
  // offset), `*_on` is `date`. A bare `YYYY-MM-DD` in a `date-time` field is what a strict server
  // rejects, so it is rejected here too.
  if (body && typeof body === 'object') {
    for (const [field, value] of Object.entries(body as Record<string, unknown>)) {
      if (typeof value !== 'string') continue
      if (field.endsWith('_at') && !RFC3339.test(value))
        errors.push({ field, code: 'invalid_date_time', message: 'expected an RFC 3339 date-time' })
      if (field.endsWith('_on') && !DATE_ONLY.test(value))
        errors.push({ field, code: 'invalid_date', message: 'expected a YYYY-MM-DD date' })
    }
  }
  if (errors.length > 0) return fail(422, 'validation_failed', errors)
  return null
}

/** Applies `limit` and `cursor` (an offset token) to a list reply, like the real cursor paging. */
function paginate(r: Reply, query: URLSearchParams): Reply {
  const limit = query.get('limit')
  const b = r.body as { items?: unknown[]; next_cursor?: string | null } | null
  if (limit === null || r.status !== 200 || !b || !Array.isArray(b.items)) return r
  const start = Number(query.get('cursor') ?? 0) || 0
  const end = start + Number(limit)
  return new Reply(200, {
    ...b,
    items: b.items.slice(start, end),
    next_cursor: end < b.items.length ? String(end) : null,
  })
}

function reply(r: Reply) {
  if (r.status === 204) return new HttpResponse(null, { status: 204 })
  return HttpResponse.json(r.body as never, {
    status: r.status,
    headers: r.status >= 400 ? { 'content-type': 'application/problem+json' } : undefined,
  })
}

type Method = 'get' | 'post' | 'patch' | 'put' | 'delete'

function route(method: Method, path: string, fn: Handler, scoped = false) {
  return http[method](`*/v1/orgs/:orgId/${path}`, async ({ request, params }) => {
    if (scoped && !inMoneyScope(request)) return undefined // another module's model answers
    const url = new URL(request.url)
    const ct = request.headers.get('content-type') ?? ''
    let body: unknown = null
    let form: FormData | null = null
    if (ct.includes('application/json')) body = await request.json().catch(() => null)
    else if (ct.includes('multipart/form-data')) form = await request.formData().catch(() => null)
    if (body && typeof body === 'object' && 'debtor' in body && body.debtor) {
      // DebtorRef: exactly one of {unit_id + debtor_kind} or {person_id}
      const d = (body as any).debtor // eslint-disable-line @typescript-eslint/no-explicit-any
      const b = body as any // eslint-disable-line @typescript-eslint/no-explicit-any
      b.unit_id = d.unit_id
      b.debtor_kind = d.debtor_kind
      if (d.person_id) {
        b.target_person_id = d.person_id
        const c = db.charges.find((ch) => ch.debtor_person_id === d.person_id)
        if (c) {
          b.unit_id = c.unit_id
          b.debtor_kind = c.debtor_kind
        }
      }
    }
    const invalid = validateRequest(url.searchParams, body)
    if (invalid) return reply(invalid)
    const key = request.headers.get('Idempotency-Key')
    if (method === 'post' && key) {
      db.idemKeysSeen.push(key)
      const cached = db.idemCache.get(key)
      if (cached) return reply(new Reply(cached.status, cached.body))
    }
    const r = paginate(
      await fn({
        params: params as Record<string, string>,
        query: url.searchParams,
        body,
        form,
        request,
      }),
      url.searchParams,
    )
    if (method === 'post' && key && r.status < 400)
      db.idemCache.set(key, { status: r.status, body: r.body })
    return reply(r)
  })
}

let moneyScope = false
/** Money tests turn the scope on for the whole test; the browser mock goes by the page URL. */
export function inMoneyScope(request: Request): boolean {
  if (moneyScope) return true
  try {
    return new URL(request.referrer).pathname.includes('/money')
  } catch {
    return false
  }
}
registerReset(() => {
  resetMoneyDb()
  moneyScope = false
})

const scopedRoute = (method: Method, path: string, fn: Handler) => route(method, path, fn, true)

const need = (v: unknown, field: string) =>
  v === undefined || v === null || v === '' ? { field, code: 'required' } : null
function validate(...checks: ({ field: string; code: string } | null)[]) {
  const errors = checks.filter((c): c is { field: string; code: string } => c !== null)
  return errors.length > 0 ? fail(422, 'validation_failed', errors) : null
}

const stamp = () => `${db.today}T09:00:00Z`
const side = (v: unknown): Side => (v === 'tenant' ? 'tenant' : 'owner')

function personOf(unitId: string, s: Side) {
  return debtorFor(db, unitId, s)
}

function lastReceipt() {
  db.receiptSeq += 1
  return `${db.today.slice(0, 4)}/${String(db.receiptSeq).padStart(6, '0')}`
}

function toPayment(c: Ctx, status: S['Payment']['status']): S['Payment'] {
  const b = c.body
  const unitId = b.unit_id as string
  const s = side(b.debtor_kind)
  const amount = b.amount.amount as number
  const pre = b.allocations
    ? { allocations: b.allocations as S['AllocationInput'][], credit: m(0) }
    : null
  const auto = autoAllocate(db, unitId, s, amount, b.target_person_id)
  const allocs: S['PaymentAllocation'][] = pre
    ? pre.allocations.map((a) => ({
        charge_id: a.charge_id,
        charge_description: db.charges.find((x) => x.id === a.charge_id)?.description ?? null,
        amount: a.amount,
      }))
    : auto.allocations
  const allocated = allocs.reduce((a, x) => a + x.amount.amount, 0)
  const pid = nextId(db, 'pay')
  const d = personOf(unitId, s)
  const pay: S['Payment'] = {
    id: pid,
    org_id: db.orgId,
    site_id: db.siteId,
    payer_person_id: b.payer_person_id ?? d.id,
    payer_display_name: personName(db, b.payer_person_id ?? d.id),
    unit_id: unitId,
    debtor_kind: s,
    method: b.method,
    gross: m(amount),
    fee: m(0),
    net: m(amount),
    received_at: b.received_at,
    recorded_by: 'u1',
    asset_account_id: b.asset_account_id,
    reference: b.reference ?? null,
    note: b.note ?? null,
    status,
    receipt_no: status === 'confirmed' ? lastReceipt() : null,
    provider_txn_id: null,
    journal_entry_id: status === 'confirmed' ? `je-${pid}` : null,
    allocated: m(allocated),
    unallocated: m(amount - allocated),
    allocations: allocs,
    bank_transaction_id: null,
    created_at: stamp(),
    updated_at: stamp(),
  }
  return pay
}

/** Confirms a declared payment; with a bank line the pair is linked in both directions. */
function confirmDeclared(
  p: S['Payment'],
  accountId: string,
  allocations: S['AllocationInput'][] | undefined,
  line?: S['BankTransaction'],
): void {
  const auto = autoAllocate(db, p.unit_id!, p.debtor_kind as Side, p.gross.amount)
  p.asset_account_id = accountId
  p.allocations = allocations?.length
    ? allocations.map((a) => ({
        charge_id: a.charge_id,
        charge_description: null,
        amount: a.amount,
      }))
    : auto.allocations
  p.allocated = m(p.allocations.reduce((a, x) => a + x.amount.amount, 0))
  p.unallocated = m(p.gross.amount - p.allocated.amount)
  p.status = 'confirmed'
  p.receipt_no = lastReceipt()
  postPayment(p)
  if (line) {
    p.bank_transaction_id = line.id
    line.payment_id = p.id
    line.match_status = 'matched'
  }
}

function postPayment(p: S['Payment']) {
  for (const a of p.allocations) {
    const ch = db.charges.find((x) => x.id === a.charge_id)
    if (ch) applyToCharge(ch, a.amount.amount)
  }
  if (p.unallocated.amount > 0) {
    db.credits.push({
      id: nextId(db, 'cr'),
      org_id: db.orgId,
      site_id: db.siteId,
      unit_id: p.unit_id,
      debtor_kind: p.debtor_kind,
      person_id: p.payer_person_id,
      balance: p.unallocated,
      source_payment_id: p.id,
      created_at: stamp(),
      updated_at: stamp(),
    })
  }
  const acct = db.accounts.find((a) => a.id === p.asset_account_id)
  const box = db.cashBoxes.find((cb) => cb.account_id === acct?.id)
  if (box) box.balance = m(box.balance.amount + p.gross.amount)
}

function statementFor(
  unitId: string | null,
  s: Side | null,
  personId: string | null,
  from: string,
  to: string,
): S['Statement'] {
  type Row = {
    date: string
    kind: S['StatementLine']['kind']
    description: string
    principal: number
    fee: number
    ref: string
    side: Side
  }
  const rows: Row[] = []
  const matches = (uId: string, sd: Side, pid: string | null) =>
    unitId ? uId === unitId && sd === s : personId ? pid === personId : false
  for (const c of db.charges) {
    if (!matches(c.unit_id, c.debtor_kind, c.debtor_person_id)) continue
    rows.push({
      date: c.due_date,
      kind: 'charge',
      description: `${c.description ?? c.charge_type} ${c.period ?? ''}`.trim(),
      principal: c.amount.amount,
      fee: 0,
      ref: c.id,
      side: c.debtor_kind,
    })
    if (c.status === 'cancelled')
      rows.push({
        date: c.due_date,
        kind: 'reversal',
        description: `Reversal: ${c.cancelled_reason ?? ''}`,
        principal: -c.amount.amount,
        fee: 0,
        ref: c.id,
        side: c.debtor_kind,
      })
  }
  for (const p of db.payments) {
    if (p.status !== 'confirmed' || !p.unit_id || !p.debtor_kind) continue
    if (!matches(p.unit_id, p.debtor_kind, p.payer_person_id)) continue
    rows.push({
      date: p.received_at.slice(0, 10),
      kind: 'payment',
      description: `Payment ${p.receipt_no ?? ''}`.trim(),
      principal: -p.allocated.amount,
      fee: 0,
      ref: p.id,
      side: p.debtor_kind,
    })
  }
  for (const w of db.waivers) {
    if (!matches(w.unit_id, w.debtor_kind, null) && !personId) continue
    if (unitId && !matches(w.unit_id, w.debtor_kind, null)) continue
    rows.push({
      date: w.created_at.slice(0, 10),
      kind: 'adjustment',
      description: `Late fee waived: ${w.reason}`,
      principal: 0,
      fee: -w.amount.amount,
      ref: w.id,
      side: w.debtor_kind,
    })
  }
  rows.sort((a, b) => a.date.localeCompare(b.date))
  let bal = 0
  const lines: S['StatementLine'][] = []
  let opening = 0
  for (const r of rows) {
    const delta = r.principal + r.fee
    if (r.date < from) {
      opening += delta
      bal += delta
      continue
    }
    if (r.date > to) continue
    bal += delta
    lines.push({
      date: r.date,
      kind: r.kind,
      description: r.description,
      ref_type: r.kind === 'payment' ? 'payment' : 'charge',
      ref_id: r.ref,
      debtor_kind: r.side,
      debtor_display_name: null,
      principal: m(r.principal),
      late_fee: m(r.fee),
      debit: m(delta > 0 ? delta : 0),
      credit: m(delta < 0 ? -delta : 0),
      balance: m(bal),
    })
  }
  const accrued =
    unitId && s
      ? accruedFee(db, unitId, s)
      : personId
        ? sidesWithDebt(db)
            .filter(({ unit, side: sd }) => debtorFor(db, unit.id, sd).id === personId)
            .reduce((a, { unit, side: sd }) => a + accruedFee(db, unit.id, sd), 0)
        : 0
  const credit = db.credits
    .filter((c) =>
      unitId ? c.unit_id === unitId && c.debtor_kind === s : c.person_id === personId,
    )
    .reduce((a, c) => a + c.balance.amount, 0)
  return {
    debtor: unitId
      ? { unit_id: unitId, debtor_kind: s ?? undefined }
      : { person_id: personId ?? undefined },
    from,
    to,
    opening_balance: m(opening),
    lines,
    closing_balance: m(bal),
    accrued_late_fee: m(accrued),
    total_due: m(Math.max(0, bal) + accrued),
    credit: m(credit),
  }
}

const binary = (type: string, name: string) =>
  new HttpResponse(type === 'application/pdf' ? '%PDF-1.4 mock' : 'mock', {
    headers: { 'content-type': type, 'content-disposition': `attachment; filename="${name}"` },
  })

function maybeExport(c: Ctx, name: string) {
  const f = c.query.get('format')
  if (f === 'pdf') return binary('application/pdf', `${name}.pdf`)
  if (f === 'csv') return binary('text/csv', `${name}.csv`)
  if (f === 'xlsx')
    return binary(
      'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      `${name}.xlsx`,
    )
  return null
}

function reportRoute(path: string, name: string, fn: Handler) {
  return http.get(`*/v1/orgs/:orgId/${path}`, async ({ request, params }) => {
    const url = new URL(request.url)
    const c: Ctx = {
      params: params as Record<string, string>,
      query: url.searchParams,
      body: null,
      form: null,
      request,
    }
    const ex = maybeExport(c, name)
    if (ex) return ex
    return reply(await fn(c))
  })
}

function overdueUnits() {
  return sidesWithDebt(db)
    .map(({ unit, side: sd }) => {
      const principal = openPrincipal(db, unit.id, sd)
      const fee = accruedFee(db, unit.id, sd)
      const oldest = db.charges
        .filter(
          (c) =>
            c.unit_id === unit.id &&
            c.debtor_kind === sd &&
            c.balance.amount > 0 &&
            c.due_date <= db.today,
        )
        .sort((a, b) => a.due_date.localeCompare(b.due_date))[0]
      return {
        unit,
        sd,
        principal,
        fee,
        months: oldest ? Math.floor(daysBetween(oldest.due_date, db.today) / 30) : 0,
      }
    })
    .filter((r) => r.principal > 0)
}

function agingBuckets(asOf: string): S['ReceivablesAgingReport'] {
  const labels = ['0-30', '31-60', '61-90', '90+']
  const rows: S['AgingRow'][] = []
  for (const { unit, side: sd } of sidesWithDebt(db)) {
    const b = [0, 0, 0, 0]
    for (const c of db.charges) {
      if (
        c.unit_id !== unit.id ||
        c.debtor_kind !== sd ||
        c.balance.amount <= 0 ||
        c.status === 'cancelled'
      )
        continue
      const age = daysBetween(c.due_date, asOf)
      if (age < 0) continue
      b[age <= 30 ? 0 : age <= 60 ? 1 : age <= 90 ? 2 : 3]! += c.balance.amount
    }
    const total = b.reduce((a, x) => a + x, 0)
    if (total > 0)
      rows.push({
        unit_id: unit.id,
        unit_door_number: unit.door_number,
        debtor_kind: sd,
        buckets: b.map((v, i) => ({ bucket: labels[i]!, amount: m(v) })),
        total: m(total),
      })
  }
  return {
    as_of: asOf,
    buckets: labels,
    rows,
    totals: labels.map((l, i) => ({
      bucket: l,
      amount: m(rows.reduce((a, r) => a + r.buckets[i]!.amount.amount, 0)),
    })),
  }
}

function withPayments(b: S['Bill']): S['Bill'] {
  return { ...b, payments: db.billPayments.filter((p) => p.bill_id === b.id) }
}

function bankBalance() {
  return (
    1_000_000 +
    db.payments
      .filter((p) => p.asset_account_id === 'a-bank' && p.status === 'confirmed')
      .reduce((a, p) => a + p.gross.amount, 0) -
    db.expenses
      .filter((e) => e.paid_from_account_id === 'a-bank' && e.status === 'posted')
      .reduce((a, e) => a + e.amount.amount, 0)
  )
}

/** The contract's `q` filter: case-insensitive substring of the entity's display text. */
function searched<T>(items: T[], query: URLSearchParams, text: (x: T) => string): T[] {
  const q = query.get('q')?.toLowerCase()
  return q ? items.filter((x) => text(x).toLowerCase().includes(q)) : items
}

/** Which unit a CSV row names: by `unit_id`, else by `block` + `door_number` (door numbers repeat). */
function unitOfRow(r: Record<string, string>): { unit?: S['Unit']; reason?: string } {
  if (r.unit_id) {
    const unit = db.units.find((u) => u.id === r.unit_id)
    return unit ? { unit } : { reason: `Unknown unit_id ${r.unit_id}` }
  }
  const door = r.door_number ?? r.unit
  const hits = db.units.filter(
    (u) =>
      u.door_number === door &&
      (!r.block || db.blocks.find((b) => b.id === u.block_id)?.name === r.block),
  )
  if (hits.length === 1) return { unit: hits[0] }
  return {
    reason: hits.length === 0 ? `Unknown unit ${door}` : `Ambiguous door ${door}: add block`,
  }
}

/** Import jobs: the first poll says running, later ones succeeded (like a real queue). */
const jobPolls = new Map<string, number>()

/**
 * Endpoints money shares with other modules' models: reference data the money screens read (units,
 * persons, vendors, blocks, tags, allocation keys), re-authentication, file upload, the
 * opening-balances import and job polling. The money model seeds its own site (A1 to B3) and its
 * ledger is keyed by those ids, so while a money screen is on display (or a money test is mounted)
 * these answer first; on any other page the tenancy, people and operations models answer. Spread
 * FIRST in `handlers`.
 */
export const moneyScopedHandlers = [
  http.post('*/v1/auth/otp', ({ request }) =>
    inMoneyScope(request) ? new HttpResponse(null, { status: 202 }) : undefined,
  ),
  http.post('*/v1/auth/reauth', ({ request }) => {
    if (!inMoneyScope(request)) return undefined
    db.reauthUntil = Date.now() + 10 * 60_000
    return HttpResponse.json({ reauth_until: new Date(db.reauthUntil).toISOString() })
  }),

  scopedRoute('get', 'units', ({ query }) => list(searched(db.units, query, (u) => u.door_number))),
  scopedRoute('get', 'persons', ({ query }) =>
    list(searched(db.persons, query, (x) => x.display_name)),
  ),
  scopedRoute('get', 'vendors', ({ query }) => list(searched(db.vendors, query, (v) => v.name))),
  scopedRoute('get', 'blocks', () => list(db.blocks)),
  scopedRoute('get', 'unit-tags', () => list(db.tags)),
  scopedRoute('get', 'allocation-keys', () => list(db.keys)),
  scopedRoute('post', 'files', async (c) => {
    const file = c.form?.get('file')
    const f: S['File'] = {
      id: nextId(db, 'file'),
      org_id: db.orgId,
      site_id: db.siteId,
      size: isFile(file) ? file.size : 0,
      mime: isFile(file) ? file.type : 'application/octet-stream',
      sha256: 'x',
      uploaded_by: 'u1',
      scope: 'expense_receipt',
      status: 'ready',
      has_variants: false,
      retention_until: null,
      ...{ created_at: stamp(), updated_at: stamp() },
    }
    db.files.push(f)
    return created(f)
  }),
  http.post('*/v1/orgs/:orgId/opening-balances\\:import', async ({ request }) => {
    if (!inMoneyScope(request)) return undefined // the setup import wizard answers elsewhere
    const url = new URL(request.url)
    const dry = url.searchParams.get('dry_run') === 'true'
    const form = await request.formData()
    const file = form.get('file')
    const text = isFile(file) ? await file.text() : ''
    const rows = parseCsv(text).map((r, i) => {
      const errors: S['ImportRowError'][] = []
      const kind = r.kind ?? 'unit_receivable'
      if (kind === 'unit_receivable') {
        const hit = unitOfRow(r)
        if (!hit.unit)
          errors.push({
            field: 'unit',
            code: 'unknown_unit',
            message: hit.reason ?? 'Unknown unit',
            params: null,
          })
      }
      if (!/^\d+(\.\d+)?$/.test(r.principal ?? ''))
        errors.push({
          field: 'principal',
          code: 'invalid_amount',
          message: 'Not an amount',
          params: null,
        })
      return {
        row: i + 2,
        status: (errors.length ? 'error' : 'ok') as 'ok' | 'error',
        errors,
        data: r,
      }
    })
    const errorRows = rows.filter((r) => r.status === 'error').length
    const report: S['ImportReport'] = {
      dry_run: dry,
      total_rows: rows.length,
      ok_rows: rows.length - errorRows,
      warning_rows: 0,
      error_rows: errorRows,
      rows,
      rows_truncated: false,
      job_id: dry ? null : 'job-1',
    }
    // the whole operation is sensitive (M7), the dry run included
    if (Date.now() > db.reauthUntil)
      return HttpResponse.json(
        { status: 403, code: 'reauth_required' },
        { status: 403, headers: { 'content-type': 'application/problem+json' } },
      )
    if (dry) return HttpResponse.json(report)
    if (Date.now() > db.reauthUntil)
      return HttpResponse.json(
        { status: 403, code: 'reauth_required' },
        { status: 403, headers: { 'content-type': 'application/problem+json' } },
      )
    if (errorRows > 0)
      return HttpResponse.json(
        { status: 422, code: 'validation_failed' },
        { status: 422, headers: { 'content-type': 'application/problem+json' } },
      )
    for (const r of rows) {
      const d = r.data as Record<string, string>
      const u = d.kind && d.kind !== 'unit_receivable' ? undefined : unitOfRow(d).unit
      db.openings.push({
        id: nextId(db, 'ob'),
        org_id: db.orgId,
        site_id: db.siteId,
        as_of: String(form.get('as_of')),
        kind: (d.kind ?? 'unit_receivable') as S['OpeningBalance']['kind'],
        unit_id: u?.id ?? null,
        debtor_kind: u ? side(d.side) : null,
        person_id: null,
        account_id: d.account_id ?? null,
        vendor_id: null,
        principal: m(Math.round(Number(d.principal) * 100)),
        late_fee: m(Math.round(Number(d.late_fee ?? 0) * 100)),
        journal_entry_id: 'je-ob',
        locked: false,
        created_at: stamp(),
        updated_at: stamp(),
        status: 'active',
        voided_at: null,
        void_reason: null,
      })
    }
    jobPolls.set('job-1', 0)
    return HttpResponse.json(report, { status: 202 })
  }),
  scopedRoute('get', 'jobs/:id', ({ params }) => {
    const polls = (jobPolls.get(params.id!) ?? 0) + 1
    jobPolls.set(params.id!, polls)
    return ok({
      id: params.id!,
      org_id: db.orgId,
      kind: 'import',
      status: polls < 2 ? 'running' : 'succeeded',
      progress: polls < 2 ? 0.5 : 1,
      result_file_id: null,
      result: null,
      error: null,
      started_at: stamp(),
      finished_at: polls < 2 ? null : stamp(),
      created_by: 'u1',
      created_at: stamp(),
      updated_at: stamp(),
    } satisfies S['Job'])
  }),
]

export const moneyHandlers = [
  // ---- accounts, funds, cash boxes, fiscal years, lock date
  route('get', 'accounts', ({ query }) => {
    const sub = query.get('subtype')
    return list(
      db.accounts.filter(
        (a) =>
          (!sub || a.subtype === sub) &&
          (query.get('archived') !== 'false' || !a.archived) &&
          (!query.get('q') ||
            `${a.code} ${a.name}`.toLowerCase().includes(query.get('q')!.toLowerCase())),
      ),
    )
  }),
  route('post', 'accounts', ({ body }) => {
    const e = validate(need(body.code, 'code'), need(body.name, 'name'))
    if (e) return e
    const a: S['Account'] = {
      id: nextId(db, 'a'),
      org_id: db.orgId,
      site_id: db.siteId,
      is_system: false,
      archived: false,
      subtype: null,
      fund_id: null,
      ...body,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.accounts.push(a)
    return created(a)
  }),
  route('get', 'funds', () => list(db.funds)),
  route('post', 'funds', ({ body }) => {
    const f: S['Fund'] = {
      id: nextId(db, 'f'),
      org_id: db.orgId,
      site_id: db.siteId,
      is_default: false,
      ...body,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.funds.push(f)
    return created(f)
  }),
  route('get', 'cash-boxes', () => list(db.cashBoxes)),
  route('post', 'cash-boxes', ({ body }) => {
    const acc: S['Account'] = {
      id: nextId(db, 'a'),
      org_id: db.orgId,
      site_id: db.siteId,
      code: `100.0${db.cashBoxes.length + 1}`,
      name: body.name,
      type: 'asset',
      subtype: 'cash_box',
      fund_id: 'f-op',
      is_system: false,
      archived: false,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.accounts.push(acc)
    const cb: S['CashBox'] = {
      id: nextId(db, 'cb'),
      org_id: db.orgId,
      site_id: db.siteId,
      name: body.name,
      account_id: acc.id,
      collector_person_id: body.collector_person_id ?? null,
      collector_display_name: personName(db, body.collector_person_id ?? null),
      balance: m(0),
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.cashBoxes.push(cb)
    return created(cb)
  }),
  route('get', 'fiscal-years', () => list(db.fiscalYears)),
  route('get', 'sites/:siteId/lock-date', () =>
    ok({
      site_id: db.siteId,
      lock_date: db.lockDate,
      changed_by: null,
      changed_at: null,
      version: 1,
    } satisfies S['LockDate']),
  ),
  route('put', 'sites/:siteId/lock-date', ({ body }) => {
    db.lockDate = body.lock_date
    return ok({
      site_id: db.siteId,
      lock_date: db.lockDate,
      changed_by: 'u1',
      changed_at: stamp(),
      version: 2,
    } satisfies S['LockDate'])
  }),

  // ---- charge plans, rates, preview
  route('get', 'charge-plans', ({ query }) => {
    for (const p of db.plans) refreshCurrentRate(db, p)
    const st = query.get('status')
    return list(db.plans.filter((p) => !st || p.status === st))
  }),
  route('post', 'charge-plans', ({ body }) => {
    const e = validate(
      need(body.name, 'name'),
      need(body.allocation_key_id, 'allocation_key_id'),
      need(body.start, 'start'),
      need(body.amount?.amount, 'amount'),
    )
    if (e) return e
    const id = nextId(db, 'plan')
    const plan: S['ChargePlan'] = {
      id,
      org_id: db.orgId,
      site_id: db.siteId,
      name: body.name,
      kind: body.kind,
      charge_type: body.charge_type,
      default_debtor: body.default_debtor ?? (body.charge_type === 'dues' ? 'tenant' : 'owner'),
      frequency: body.frequency ?? 'monthly',
      due_day: body.due_day ?? 5,
      start: body.start,
      end: body.end ?? null,
      amount_mode: body.amount_mode,
      allocation_key_id: body.allocation_key_id,
      scope: body.scope,
      fund_id: body.fund_id ?? 'f-op',
      income_account_id: body.income_account_id ?? 'a-inc-dues',
      status: 'active',
      decision_id: body.decision_id ?? null,
      current_rate: null,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.plans.push(plan)
    db.rates.push({
      id: nextId(db, 'rate'),
      org_id: db.orgId,
      plan_id: id,
      from: body.start,
      to: null,
      amount: body.amount,
      decision_id: body.decision_id ?? null,
      created_at: stamp(),
      updated_at: stamp(),
    })
    refreshCurrentRate(db, plan)
    // the hourly catch-up job: generate the current period right away in the mock
    generateCharges(db, plan, db.today.slice(0, 7))
    return created(plan)
  }),
  route('get', 'charge-plans/:id', ({ params }) => {
    const p = db.plans.find((x) => x.id === params.id)
    if (!p) return fail(404, 'not_found')
    refreshCurrentRate(db, p)
    return ok(p)
  }),
  route('patch', 'charge-plans/:id', ({ params, body, request }) => {
    const p = db.plans.find((x) => x.id === params.id)
    if (!p) return fail(404, 'not_found')
    if (request.headers.get('If-Match') !== `"${p.version}"`) return fail(412, 'version_conflict')
    Object.assign(p, body, { version: p.version + 1, updated_at: stamp() })
    return ok(p)
  }),
  route('get', 'charge-plans/:id/rates', ({ params }) =>
    list(
      db.rates.filter((r) => r.plan_id === params.id).sort((a, b) => b.from.localeCompare(a.from)),
    ),
  ),
  route('post', 'charge-plans/:id/rates', ({ params, body }) => {
    const e = validate(need(body.from, 'from'), need(body.amount?.amount, 'amount'))
    if (e) return e
    const plan = db.plans.find((x) => x.id === params.id)
    if (!plan) return fail(404, 'not_found')
    // closing the open rate the day before the new one starts
    for (const r of db.rates.filter((x) => x.plan_id === params.id && !x.to && x.from < body.from))
      r.to = new Date(Date.parse(`${body.from}T00:00:00Z`) - 86_400_000).toISOString().slice(0, 10)
    const rate: S['ChargePlanRate'] = {
      id: nextId(db, 'rate'),
      org_id: db.orgId,
      plan_id: params.id!,
      from: body.from,
      to: body.to ?? null,
      amount: body.amount,
      decision_id: body.decision_id ?? null,
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.rates.push(rate)
    refreshCurrentRate(db, plan)
    return created(rate)
  }),
  route('delete', 'charge-plans/:id/rates/:rateId', ({ params }) => {
    db.rates = db.rates.filter((r) => r.id !== params.rateId)
    return noContent()
  }),
  route('post', 'charge-plans\\:preview', ({ body }) => {
    const src = body.plan_id ? db.plans.find((p) => p.id === body.plan_id) : null
    const pl = body.plan ?? src
    if (!pl) return fail(422, 'validation_failed', [{ field: 'plan', code: 'required' }])
    const amount =
      body.amount?.amount ?? body.plan?.amount?.amount ?? src?.current_rate?.amount.amount ?? 0
    return ok(
      previewSplit(db, {
        amount,
        mode: pl.amount_mode,
        keyId: pl.allocation_key_id,
        scope: pl.scope,
        debtor: pl.default_debtor ?? 'owner',
        period: body.period ?? db.today.slice(0, 7),
      }),
    )
  }),
  route('get', 'charge-generation', () =>
    ok({
      up_to_date: true,
      last_run_at: stamp(),
      next_run_at: null,
      pending: [],
    } satisfies S['ChargeGenerationStatus']),
  ),
  route('post', 'expense-allocations\\:preview', ({ body }) =>
    ok(
      previewSplit(db, {
        amount: body.total.amount,
        mode: 'total_allocated',
        keyId: body.allocation_key_id,
        scope: body.scope,
        debtor: body.debtor_kind ?? 'owner',
        period: body.due_date.slice(0, 7),
      }),
    ),
  ),
  route('post', 'expense-allocations', ({ body }) => {
    const e = validate(
      need(body.description, 'description'),
      need(body.allocation_key_id, 'allocation_key_id'),
    )
    if (e) return e
    const pv = previewSplit(db, {
      amount: body.total.amount,
      mode: 'total_allocated',
      keyId: body.allocation_key_id,
      scope: body.scope,
      debtor: body.debtor_kind ?? 'owner',
      period: body.due_date.slice(0, 7),
    })
    const ids: string[] = []
    for (const l of pv.lines) {
      const id = nextId(db, 'ch')
      ids.push(id)
      db.charges.push({
        id,
        org_id: db.orgId,
        site_id: db.siteId,
        plan_id: null,
        unit_id: l.unit_id,
        unit_door_number: l.door_number,
        debtor_kind: l.debtor_kind,
        debtor_person_id: l.debtor_person_id,
        debtor_display_name: l.debtor_display_name,
        effective_debtor: l.effective_debtor,
        charge_type: body.charge_type ?? 'special_assessment',
        period: null,
        amount: l.amount,
        paid: m(0),
        balance: l.amount,
        due_date: body.due_date,
        description: body.description,
        allocation_basis: null,
        journal_entry_id: `je-${id}`,
        status: 'open',
        cancelled_reason: null,
        created_at: stamp(),
        updated_at: stamp(),
      })
    }
    return created({
      id: nextId(db, 'ea'),
      site_id: db.siteId,
      expense_id: null,
      total: body.total,
      charge_ids: ids,
      created_at: stamp(),
    } satisfies S['ExpenseAllocation'])
  }),

  // ---- charges
  route('get', 'charges', ({ query }) => {
    let items = db.charges
    for (const [k, f] of [
      ['unit_id', 'unit_id'],
      ['debtor_kind', 'debtor_kind'],
      ['status', 'status'],
      ['period', 'period'],
      ['charge_type', 'charge_type'],
      ['plan_id', 'plan_id'],
    ] as const) {
      const v = query.get(k)
      if (v) items = items.filter((c) => (c as Record<string, unknown>)[f] === v)
    }
    return list(items.slice().sort((a, b) => b.due_date.localeCompare(a.due_date)))
  }),
  route('post', 'charges', ({ body }) => {
    const e = validate(
      need(body.unit_id, 'unit_id'),
      need(body.amount?.amount, 'amount'),
      need(body.due_date, 'due_date'),
    )
    if (e) return e
    const s = side(body.debtor_kind)
    const d = personOf(body.unit_id, s)
    const id = nextId(db, 'ch')
    const u = db.units.find((x) => x.id === body.unit_id)!
    const ch: S['Charge'] = {
      id,
      org_id: db.orgId,
      site_id: db.siteId,
      plan_id: null,
      unit_id: u.id,
      unit_door_number: u.door_number,
      debtor_kind: s,
      debtor_person_id: d.id,
      debtor_display_name: d.name,
      effective_debtor: effectiveOf(s, d),
      charge_type: body.charge_type,
      period: null,
      amount: body.amount,
      paid: m(0),
      balance: body.amount,
      due_date: body.due_date,
      description: body.description ?? null,
      allocation_basis: null,
      journal_entry_id: `je-${id}`,
      status: 'open',
      cancelled_reason: null,
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.charges.push(ch)
    return created(ch)
  }),
  route('get', 'charges/:id', ({ params }) => {
    const c = db.charges.find((x) => x.id === params.id)
    return c ? ok(c) : fail(404, 'not_found')
  }),
  route('post', 'charges/:id\\:cancel', ({ params, body }) => {
    const c = db.charges.find((x) => x.id === params.id)
    if (!c) return fail(404, 'not_found')
    const e = validate(need(body.reason, 'reason'))
    if (e) return e
    if (db.lockDate && c.due_date <= db.lockDate) return fail(409, 'locked_period')
    c.status = 'cancelled'
    c.cancelled_reason = body.reason
    c.balance = m(0)
    return ok(c)
  }),
  route('post', 'charges/:id\\:adjust', ({ params, body }) => {
    const c = db.charges.find((x) => x.id === params.id)
    if (!c) return fail(404, 'not_found')
    const e = validate(need(body.reason, 'reason'), need(body.amount?.amount, 'amount'))
    if (e) return e
    c.amount = body.amount
    c.balance = m(Math.max(0, body.amount.amount - c.paid.amount))
    c.status = c.balance.amount === 0 ? 'paid' : c.paid.amount > 0 ? 'partially_paid' : 'open'
    return ok(c)
  }),

  // ---- late fees
  route('get', 'late-fee-policy', () => ok(db.lateFeePolicy)),
  route('put', 'late-fee-policy', ({ body }) => {
    if (body.rate_pct_per_month > db.lateFeePolicy.max_rate_pct_per_month)
      return fail(422, 'validation_failed', [
        {
          field: 'rate_pct_per_month',
          code: 'exceeds_statutory_cap',
          message: 'Above the statutory cap',
        },
      ])
    db.lateFeePolicy = {
      ...db.lateFeePolicy,
      rate_pct_per_month: body.rate_pct_per_month,
      grace_days: body.grace_days,
      waiver_reauth_threshold: body.waiver_reauth_threshold ?? null,
    }
    return ok(db.lateFeePolicy)
  }),
  route('get', 'late-fee-accruals', ({ query }) => {
    const asOf = query.get('as_of') ?? db.today
    const items: S['LateFeeAccrual'][] = sidesWithDebt(db)
      .map(({ unit, side: sd }) => ({
        unit_id: unit.id,
        unit_door_number: unit.door_number,
        debtor_kind: sd,
        as_of: asOf,
        open_principal: m(openPrincipal(db, unit.id, sd, asOf)),
        accrued: m(accruedFee(db, unit.id, sd, asOf)),
        booked: m(
          db.bookings
            .filter((b) => b.unit_id === unit.id && b.debtor_kind === sd)
            .reduce((a, b) => a + b.amount.amount, 0),
        ),
        frozen: isFrozen(db, unit.id, sd, asOf),
        waived: m(
          db.waivers
            .filter((w) => w.unit_id === unit.id && w.debtor_kind === sd)
            .reduce((a, w) => a + w.amount.amount, 0),
        ),
      }))
      .filter((a) => query.get('nonzero') !== 'true' || a.accrued.amount > 0 || a.frozen)
    return list(items)
  }),
  route('get', 'late-fee-bookings', () => {
    if (db.bookings.length === 0) {
      // last month's crystallisation of what had accrued by month end
      for (const { unit, side: sd } of sidesWithDebt(db)) {
        const fee = accruedFee(db, unit.id, sd, '2026-10-31')
        if (fee <= 0) continue
        db.bookings.push({
          id: nextId(db, 'lfb'),
          org_id: db.orgId,
          site_id: db.siteId,
          unit_id: unit.id,
          debtor_kind: sd,
          period: '2026-10',
          amount: m(fee),
          charge_id: null,
          expected_to_date: m(fee),
          booked_to_date: m(fee),
          delta: m(0),
          created_at: stamp(),
          updated_at: stamp(),
        })
      }
    }
    return list(
      db.bookings.map((b) => ({
        ...b,
        unit_door_number: db.units.find((u) => u.id === b.unit_id)?.door_number,
      })),
    )
  }),
  route('get', 'late-fee-waivers', () => list(db.waivers)),
  route('post', 'late-fee-waivers', ({ body }) => {
    const e = validate(
      need(body.unit_id, 'unit_id'),
      need(body.reason, 'reason'),
      need(body.amount?.amount, 'amount'),
    )
    if (e) return e
    const th = db.lateFeePolicy.waiver_reauth_threshold?.amount ?? 0
    if (body.amount.amount > th && Date.now() > db.reauthUntil) return fail(403, 'reauth_required')
    const accrued = accruedFee(db, body.unit_id, side(body.debtor_kind))
    if (body.amount.amount > accrued)
      return fail(422, 'validation_failed', [
        { field: 'amount', code: 'exceeds_accrued', message: 'More than the accrued fee' },
      ])
    const w: S['LateFeeWaiver'] = {
      id: nextId(db, 'w'),
      org_id: db.orgId,
      site_id: db.siteId,
      unit_id: body.unit_id,
      debtor_kind: side(body.debtor_kind),
      amount: body.amount,
      reason: body.reason,
      by: 'u1',
      journal_entry_id: 'je-w',
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.waivers.push(w)
    return created(w)
  }),
  route('get', 'late-fee-freezes', ({ query }) =>
    list(db.freezes.filter((f) => query.get('active') !== 'true' || !f.to || f.to >= db.today)),
  ),
  route('post', 'late-fee-freezes', ({ body }) => {
    const e = validate(
      need(body.unit_id, 'unit_id'),
      need(body.reason, 'reason'),
      need(body.from, 'from'),
    )
    if (e) return e
    const f: S['LateFeeFreeze'] = {
      id: nextId(db, 'fz'),
      org_id: db.orgId,
      site_id: db.siteId,
      unit_id: body.unit_id,
      debtor_kind: side(body.debtor_kind),
      from: body.from,
      to: body.to ?? null,
      reason: body.reason,
      case_id: null,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.freezes.push(f)
    return created(f)
  }),
  route('patch', 'late-fee-freezes/:id', ({ params, body }) => {
    const f = db.freezes.find((x) => x.id === params.id)
    if (!f) return fail(404, 'not_found')
    Object.assign(f, body, { version: f.version + 1 })
    return ok(f)
  }),

  // ---- payments
  route('get', 'payments', ({ query }) => {
    let items = db.payments
    for (const k of ['status', 'method', 'unit_id', 'receipt_no', 'person_id'] as const) {
      const v = query.get(k)
      if (v)
        items = items.filter(
          (p) => (k === 'person_id' ? p.payer_person_id : (p as Record<string, unknown>)[k]) === v,
        )
    }
    return list(items.slice().reverse())
  }),
  route('post', 'payments', (c) => {
    const b = c.body
    const e = validate(
      need(b.unit_id, 'unit_id'),
      need(b.amount?.amount, 'amount'),
      need(b.received_at, 'received_at'),
      need(b.asset_account_id, 'asset_account_id'),
    )
    if (e) return e
    if (b.amount.amount <= 0)
      return fail(422, 'validation_failed', [{ field: 'amount', code: 'must_be_positive' }])
    if (db.lockDate && b.received_at.slice(0, 10) <= db.lockDate) return fail(409, 'locked_period')
    const p = toPayment(c, 'confirmed')
    postPayment(p)
    db.payments.push(p)
    if (b.send_receipt)
      db.sent.push({ kind: 'receipt', id: p.id, channels: b.send_receipt.channels })
    return created(p)
  }),
  route('get', 'payments/:id', ({ params }) => {
    const p = db.payments.find((x) => x.id === params.id)
    return p ? ok(p) : fail(404, 'not_found')
  }),
  route('post', 'payments/:id\\:void', ({ params, body }) => {
    const p = db.payments.find((x) => x.id === params.id)
    if (!p) return fail(404, 'not_found')
    const e = validate(need(body.reason, 'reason'))
    if (e) return e
    for (const a of p.allocations) {
      const ch = db.charges.find((x) => x.id === a.charge_id)
      if (ch) applyToCharge(ch, -a.amount.amount)
    }
    p.status = 'voided'
    return ok(p)
  }),
  http.get('*/v1/orgs/:orgId/payments/:id/receipt', ({ params }) =>
    binary('application/pdf', `receipt-${params.id as string}.pdf`),
  ),
  route('post', 'payments/:id\\:send-receipt', ({ params, body }) => {
    db.sent.push({ kind: 'receipt', id: params.id!, channels: body.channels })
    return noContent()
  }),
  route('put', 'payments/:id/allocations', ({ params, body }) => {
    const p = db.payments.find((x) => x.id === params.id)
    if (!p) return fail(404, 'not_found')
    for (const a of p.allocations) {
      const ch = db.charges.find((x) => x.id === a.charge_id)
      if (ch) applyToCharge(ch, -a.amount.amount)
    }
    p.allocations = (body.allocations as S['AllocationInput'][]).map((a) => ({
      charge_id: a.charge_id,
      charge_description: db.charges.find((x) => x.id === a.charge_id)?.description ?? null,
      amount: a.amount,
    }))
    for (const a of p.allocations) {
      const ch = db.charges.find((x) => x.id === a.charge_id)
      if (ch) applyToCharge(ch, a.amount.amount)
    }
    p.allocated = m(p.allocations.reduce((a, x) => a + x.amount.amount, 0))
    p.unallocated = m(p.gross.amount - p.allocated.amount)
    return ok(p)
  }),
  route('post', 'payments\\:preview-allocation', ({ body }) =>
    ok(
      autoAllocate(
        db,
        body.unit_id,
        side(body.debtor_kind),
        body.amount.amount,
        body.target_person_id,
      ) satisfies S['AllocationPreview'],
    ),
  ),
  route('post', 'payments\\:declare', ({ body }) => {
    const e = validate(
      need(body.unit_id, 'unit_id'),
      need(body.amount?.amount, 'amount'),
      need(body.paid_on, 'paid_on'),
    )
    if (e) return e
    const s = side(body.debtor_kind)
    const d = personOf(body.unit_id, s)
    const p: S['Payment'] = {
      id: nextId(db, 'pay'),
      org_id: db.orgId,
      site_id: db.siteId,
      payer_person_id: d.id,
      payer_display_name: d.name,
      unit_id: body.unit_id,
      debtor_kind: s,
      method: 'bank_transfer',
      gross: body.amount,
      fee: m(0),
      net: body.amount,
      received_at: `${body.paid_on}T00:00:00+03:00`,
      recorded_by: null,
      asset_account_id: 'a-bank',
      reference: body.reference ?? null,
      note: body.note ?? null,
      status: 'pending_declared',
      receipt_no: null,
      provider_txn_id: null,
      journal_entry_id: null,
      allocated: m(0),
      unallocated: body.amount,
      allocations: [],
      bank_transaction_id: null,
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.payments.push(p)
    return created(p)
  }),
  route('post', 'payments/:id\\:confirm', ({ params, body }) => {
    const p = db.payments.find((x) => x.id === params.id)
    if (!p) return fail(404, 'not_found')
    if (p.status !== 'pending_declared') return fail(409, 'payment_not_pending')
    let line: S['BankTransaction'] | undefined
    if (body.bank_transaction_id) {
      line = db.bankTx.find((x) => x.id === body.bank_transaction_id)
      if (!line) return fail(404, 'not_found')
      if (line.match_status === 'matched') return fail(409, 'already_matched')
    }
    confirmDeclared(p, body.asset_account_id, body.allocations, line)
    return ok(p)
  }),
  route('post', 'payments/:id\\:reject', ({ params, body }) => {
    const p = db.payments.find((x) => x.id === params.id)
    if (!p) return fail(404, 'not_found')
    const e = validate(need(body.reason, 'reason'))
    if (e) return e
    p.status = 'voided'
    return ok(p)
  }),
  route('get', 'credits', ({ query }) =>
    list(db.credits.filter((c) => query.get('has_balance') !== 'true' || c.balance.amount > 0)),
  ),
  route('post', 'credits/:id\\:apply', ({ params, body }) => {
    const cr = db.credits.find((x) => x.id === params.id)
    if (!cr) return fail(404, 'not_found')
    for (const a of body.allocations as S['AllocationInput'][]) {
      const ch = db.charges.find((x) => x.id === a.charge_id)
      if (ch) applyToCharge(ch, a.amount.amount)
      cr.balance = m(cr.balance.amount - a.amount.amount)
    }
    return ok(cr)
  }),
  route('get', 'transfers', () => list(db.transfers.slice().reverse())),
  route('post', 'transfers', ({ body }) => {
    const e = validate(
      need(body.from_account_id, 'from_account_id'),
      need(body.to_account_id, 'to_account_id'),
      need(body.amount?.amount, 'amount'),
    )
    if (e) return e
    const from = db.cashBoxes.find((cb) => cb.account_id === body.from_account_id)
    if (from && from.balance.amount < body.amount.amount)
      return fail(422, 'validation_failed', [
        { field: 'amount', code: 'insufficient_balance', message: 'More than the cash box holds' },
      ])
    if (from) from.balance = m(from.balance.amount - body.amount.amount)
    const to = db.cashBoxes.find((cb) => cb.account_id === body.to_account_id)
    if (to) to.balance = m(to.balance.amount + body.amount.amount)
    const t: S['Transfer'] = {
      id: nextId(db, 'tr'),
      org_id: db.orgId,
      site_id: db.siteId,
      from_account_id: body.from_account_id,
      to_account_id: body.to_account_id,
      amount: body.amount,
      transferred_at: body.transferred_at,
      memo: body.memo ?? null,
      kind: body.kind ?? 'other',
      journal_entry_id: 'je-tr',
      created_by: 'u1',
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.transfers.push(t)
    return created(t)
  }),
  route('get', 'refunds', () => list(db.refunds.slice().reverse())),
  route('post', 'refunds', ({ body }) => {
    const e = validate(
      need(body.amount?.amount, 'amount'),
      need(body.reason, 'reason'),
      need(body.paid_from_account_id, 'paid_from_account_id'),
    )
    if (e) return e
    const cr = db.credits.find((x) => x.id === body.credit_id)
    if (cr) cr.balance = m(Math.max(0, cr.balance.amount - body.amount.amount))
    const r: S['Refund'] = {
      id: nextId(db, 'rf'),
      org_id: db.orgId,
      site_id: db.siteId,
      person_id: body.person_id ?? null,
      unit_id: body.unit_id ?? null,
      debtor_kind: body.debtor_kind ?? null,
      amount: body.amount,
      reason: body.reason,
      kind: body.kind,
      paid_from_account_id: body.paid_from_account_id,
      refunded_at: body.refunded_at,
      credit_id: body.credit_id ?? null,
      payment_id: body.payment_id ?? null,
      journal_entry_id: 'je-rf',
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.refunds.push(r)
    return created(r)
  }),

  // ---- bank statement import and matching
  route('get', 'bank-importers', () => list(db.importers)),
  route('get', 'bank-statement-imports', () => list(db.imports.slice().reverse())),
  route('post', 'bank-statement-imports', ({ form }) => {
    const file = form?.get('file')
    if (!isFile(file)) return fail(422, 'validation_failed', [{ field: 'file', code: 'required' }])
    const id = nextId(db, 'imp')
    const imp = seedBankImport(db, id, file.name)
    imp.importer = String(form?.get('importer') ?? 'generic-csv')
    imp.asset_account_id = String(form?.get('asset_account_id') ?? 'a-bank')
    db.imports.push(imp)
    return created(imp)
  }),
  route('get', 'bank-statement-imports/:id', ({ params }) => {
    const i = db.imports.find((x) => x.id === params.id)
    if (!i) return fail(404, 'not_found')
    i.unmatched_count = db.bankTx.filter(
      (t) =>
        t.import_id === i.id && (t.match_status === 'unmatched' || t.match_status === 'suggested'),
    ).length
    return ok(i)
  }),
  route('delete', 'bank-statement-imports/:id', ({ params }) => {
    db.imports = db.imports.filter((x) => x.id !== params.id)
    db.bankTx = db.bankTx.filter((t) => t.import_id !== params.id)
    return noContent()
  }),
  route('get', 'bank-statement-imports/:id/reconciliation', ({ params }) => {
    const i = db.imports.find((x) => x.id === params.id)
    if (!i) return fail(404, 'not_found')
    const matched = db.bankTx
      .filter((t) => t.import_id === i.id && t.match_status === 'matched')
      .reduce((a, t) => a + t.amount.amount, 0)
    const ledgerOpening = i.opening_balance?.amount ?? 0
    const ledgerClosing = ledgerOpening + matched
    return ok({
      import_id: i.id,
      statement_opening: i.opening_balance,
      statement_closing: i.closing_balance,
      ledger_opening: m(ledgerOpening),
      ledger_closing: m(ledgerClosing),
      difference: m((i.closing_balance?.amount ?? 0) - ledgerClosing),
    } satisfies S['ImportReconciliation'])
  }),
  route('get', 'bank-transactions', ({ query }) => {
    let items = db.bankTx
    const imp = query.get('import_id')
    const ms = query.get('match_status')
    if (imp) items = items.filter((t) => t.import_id === imp)
    if (ms) items = items.filter((t) => t.match_status === ms)
    return list(items)
  }),
  route('post', 'bank-transactions/:id\\:match', ({ params, body }) => {
    const t = db.bankTx.find((x) => x.id === params.id)
    if (!t) return fail(404, 'not_found')
    if (t.match_status === 'matched') return fail(409, 'already_matched')
    if (body.payment_id) {
      // confirm the declared payment WITH the line: one booking, both sides linked
      const dp = db.payments.find((x) => x.id === body.payment_id)
      if (!dp) return fail(404, 'not_found')
      if (dp.status !== 'pending_declared') return fail(409, 'payment_not_pending')
      confirmDeclared(dp, 'a-bank', undefined, t)
      return ok(t)
    }
    if (body.expense_id || t.amount.amount < 0) {
      t.match_status = 'matched'
      return ok(t)
    }
    const e = validate(need(body.unit_id, 'unit_id'))
    if (e) return e
    const s = side(body.debtor_kind)
    const d = personOf(body.unit_id, s)
    const fake: Ctx = {
      params: {},
      query: new URLSearchParams(),
      form: null,
      request: new Request('http://x'),
      body: {
        unit_id: body.unit_id,
        debtor_kind: s,
        method: 'bank_transfer',
        amount: t.amount,
        received_at: `${t.booked_on}T00:00:00+03:00`,
        asset_account_id: 'a-bank',
        payer_person_id: body.payer_person_id ?? d.id,
        allocations: body.allocations,
      },
    }
    const p = toPayment(fake, 'confirmed')
    p.bank_transaction_id = t.id
    postPayment(p)
    db.payments.push(p)
    t.match_status = 'matched'
    t.payment_id = p.id
    return ok(t)
  }),
  route('post', 'bank-transactions/:id\\:unmatch', ({ params }) => {
    const t = db.bankTx.find((x) => x.id === params.id)
    if (!t) return fail(404, 'not_found')
    const p = db.payments.find((x) => x.id === t.payment_id)
    if (p) {
      for (const a of p.allocations) {
        const ch = db.charges.find((x) => x.id === a.charge_id)
        if (ch) applyToCharge(ch, -a.amount.amount)
      }
      p.status = 'voided'
    }
    t.match_status = 'unmatched'
    t.payment_id = null
    return ok(t)
  }),
  route('post', 'bank-transactions/:id\\:ignore', ({ params }) => {
    const t = db.bankTx.find((x) => x.id === params.id)
    if (!t) return fail(404, 'not_found')
    t.match_status = 'ignored'
    return ok(t)
  }),

  // ---- expenses, recurring, bills
  route('get', 'expenses', ({ query }) => {
    let items = db.expenses
    for (const k of ['category_account_id', 'fund_id', 'status', 'vendor_id'] as const) {
      const v = query.get(k)
      if (v) items = items.filter((x) => (x as Record<string, unknown>)[k] === v)
    }
    const q = query.get('q')?.toLowerCase()
    if (q) items = items.filter((x) => x.description.toLowerCase().includes(q))
    return list(items.slice().reverse())
  }),
  route('post', 'expenses', ({ body }) => {
    const e = validate(
      need(body.description, 'description'),
      need(body.amount?.amount, 'amount'),
      need(body.category_account_id, 'category_account_id'),
      need(body.paid_from_account_id, 'paid_from_account_id'),
    )
    if (e) return e
    if (db.lockDate && body.paid_on <= db.lockDate) return fail(409, 'locked_period')
    const id = nextId(db, 'exp')
    const x: S['Expense'] = {
      id,
      org_id: db.orgId,
      site_id: db.siteId,
      category_account_id: body.category_account_id,
      fund_id: body.fund_id,
      amount: body.amount,
      paid_on: body.paid_on,
      paid_from_account_id: body.paid_from_account_id,
      vendor_id: body.vendor_id ?? null,
      vendor_name: db.vendors.find((v) => v.id === body.vendor_id)?.name ?? null,
      description: body.description,
      document_no: body.document_no ?? null,
      file_id: body.file_id ?? null,
      recurring_expense_id: null,
      status: 'posted',
      journal_entry_id: `je-${id}`,
      voided_reason: null,
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.expenses.push(x)
    const cb = db.cashBoxes.find((c) => c.account_id === body.paid_from_account_id)
    if (cb) cb.balance = m(cb.balance.amount - body.amount.amount)
    return created(x)
  }),
  route('get', 'expenses/:id', ({ params }) => {
    const x = db.expenses.find((e) => e.id === params.id)
    return x ? ok(x) : fail(404, 'not_found')
  }),
  route('post', 'expenses/:id\\:void', ({ params, body }) => {
    const x = db.expenses.find((e) => e.id === params.id)
    if (!x) return fail(404, 'not_found')
    x.status = 'voided'
    x.voided_reason = body.reason
    return ok(x)
  }),
  route('get', 'recurring-expenses', () => list(db.recurring)),
  route('post', 'recurring-expenses', ({ body }) => {
    const e = validate(need(body.description, 'description'), need(body.amount?.amount, 'amount'))
    if (e) return e
    const r: S['RecurringExpense'] = {
      id: nextId(db, 'rec'),
      org_id: db.orgId,
      site_id: db.siteId,
      category_account_id: body.category_account_id,
      fund_id: body.fund_id,
      amount: body.amount,
      paid_from_account_id: body.paid_from_account_id,
      vendor_id: body.vendor_id ?? null,
      description: body.description,
      day_of_month: body.day_of_month,
      start: body.start,
      end: body.end ?? null,
      status: 'active',
      next_run_on: addMonths(body.start, 1),
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.recurring.push(r)
    return created(r)
  }),
  route('patch', 'recurring-expenses/:id', ({ params, body }) => {
    const r = db.recurring.find((x) => x.id === params.id)
    if (!r) return fail(404, 'not_found')
    Object.assign(r, body, { version: r.version + 1 })
    return ok(r)
  }),
  route('delete', 'recurring-expenses/:id', ({ params }) => {
    const r = db.recurring.find((x) => x.id === params.id)
    if (r) r.status = 'ended'
    return noContent()
  }),
  route('get', 'bills', () => list(db.bills.map(withPayments))),
  route('post', 'bills', ({ body }) => {
    const e = validate(
      need(body.vendor_id, 'vendor_id'),
      need(body.amount?.amount, 'amount'),
      need(body.due_date, 'due_date'),
    )
    if (e) return e
    const id = nextId(db, 'bill')
    const b: S['Bill'] = {
      id,
      org_id: db.orgId,
      site_id: db.siteId,
      vendor_id: body.vendor_id,
      document_no: body.document_no ?? null,
      issue_date: body.issue_date,
      due_date: body.due_date,
      amount: body.amount,
      paid: m(0),
      balance: body.amount,
      category_account_id: body.category_account_id,
      fund_id: body.fund_id,
      file_id: null,
      description: body.description ?? null,
      status: 'open',
      journal_entry_id: `je-${id}`,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
      payments: [],
    }
    db.bills.push(b)
    return created(b)
  }),
  route('get', 'bills/:id', ({ params }) => {
    const b = db.bills.find((x) => x.id === params.id)
    return b ? ok(withPayments(b)) : fail(404, 'not_found')
  }),
  route('post', 'bills/:id/payments', ({ params, body }) => {
    const b = db.bills.find((x) => x.id === params.id)
    if (!b) return fail(404, 'not_found')
    if (body.amount.amount > b.balance.amount)
      return fail(422, 'validation_failed', [
        { field: 'amount', code: 'exceeds_balance', message: 'More than the bill balance' },
      ])
    b.paid = m(b.paid.amount + body.amount.amount)
    b.balance = m(b.amount.amount - b.paid.amount)
    b.status = b.balance.amount === 0 ? 'paid' : 'partially_paid'
    const p: S['BillPayment'] = {
      id: nextId(db, 'bp'),
      org_id: db.orgId,
      bill_id: b.id,
      amount: body.amount,
      paid_on: body.paid_on,
      paid_from_account_id: body.paid_from_account_id,
      reference: body.reference ?? null,
      journal_entry_id: 'je-bp',
      created_at: stamp(),
      updated_at: stamp(),
    }
    db.billPayments.push(p)
    return created(p)
  }),

  // ---- collections
  route('get', 'dunning-stages', () =>
    list(db.stages.slice().sort((a, b) => a.position - b.position)),
  ),
  route('post', 'dunning-stages', ({ body }) => {
    const e = validate(need(body.name, 'name'), need(body.days_after_due, 'days_after_due'))
    if (e) return e
    const s: S['DunningStage'] = {
      id: nextId(db, 'st'),
      org_id: db.orgId,
      site_id: db.siteId,
      name: body.name,
      kind: body.kind,
      days_after_due: body.days_after_due,
      channels: body.channels ?? ['in_app'],
      template_key: null,
      position: body.position ?? db.stages.length + 1,
      enabled: body.enabled ?? true,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.stages.push(s)
    return created(s)
  }),
  route('patch', 'dunning-stages/:id', ({ params, body }) => {
    const s = db.stages.find((x) => x.id === params.id)
    if (!s) return fail(404, 'not_found')
    Object.assign(s, body, { version: s.version + 1 })
    return ok(s)
  }),
  route('delete', 'dunning-stages/:id', ({ params }) => {
    db.stages = db.stages.filter((s) => s.id !== params.id)
    return noContent()
  }),
  route('post', 'dunning-runs\\:preview', ({ body }) => {
    const rows = overdueUnits()
      .filter((r) => !body.debtor_kind || r.sd === body.debtor_kind)
      .filter((r) => !body.min_balance || r.principal >= body.min_balance.amount)
    const frozen = rows.filter((r) => isFrozen(db, r.unit.id, r.sd)).length
    const n = rows.length - frozen
    return ok({
      recipient_count: n,
      sms_count: body.channels.includes('sms') ? n : 0,
      skipped_frozen: frozen,
    } satisfies S['DunningRunPreview'])
  }),
  route('post', 'dunning-runs', ({ body }) => {
    const rows = overdueUnits()
      .filter((r) => !body.debtor_kind || r.sd === body.debtor_kind)
      .filter((r) => !isFrozen(db, r.unit.id, r.sd))
    const runId = nextId(db, 'run')
    for (const r of rows) {
      for (const ch of body.channels as S['DunningEvent']['channel'][]) {
        db.events.push({
          id: nextId(db, 'ev'),
          org_id: db.orgId,
          site_id: db.siteId,
          run_id: runId,
          unit_id: r.unit.id,
          debtor_kind: r.sd,
          person_id: debtorFor(db, r.unit.id, r.sd).id,
          stage_id: body.stage_id,
          sent_at: stamp(),
          channel: ch,
          document_file_id: null,
          created_at: stamp(),
          updated_at: stamp(),
        })
      }
    }
    return created({
      id: runId,
      site_id: db.siteId,
      stage_id: body.stage_id,
      event_count: rows.length * body.channels.length,
      sms_count: body.channels.includes('sms') ? rows.length : 0,
      created_at: stamp(),
    } satisfies S['DunningRun'])
  }),
  route('get', 'dunning-events', ({ query }) => {
    let items = db.events
    const u = query.get('unit_id')
    const d = query.get('debtor_kind')
    if (u) items = items.filter((e) => e.unit_id === u)
    if (d) items = items.filter((e) => e.debtor_kind === d)
    return list(items)
  }),
  route('get', 'collection-cases', ({ query }) =>
    list(db.cases.filter((c) => !query.get('status') || c.status === query.get('status'))),
  ),
  route('post', 'collection-cases', ({ body }) => {
    const e = validate(need(body.unit_id, 'unit_id'), need(body.opened_on, 'opened_on'))
    if (e) return e
    const c: S['CollectionCase'] = {
      id: nextId(db, 'case'),
      org_id: db.orgId,
      site_id: db.siteId,
      unit_id: body.unit_id,
      debtor_kind: side(body.debtor_kind),
      debtor_person_id:
        body.debtor_person_id ?? debtorFor(db, body.unit_id, side(body.debtor_kind)).id ?? '',
      opened_on: body.opened_on,
      lawyer: body.lawyer ?? null,
      file_no: body.file_no ?? null,
      status: 'open',
      closed_on: null,
      costs_charged: m(0),
      notes: body.notes ?? null,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.cases.push(c)
    // legal collection stops fee accrual (M4)
    db.freezes.push({
      id: nextId(db, 'fz'),
      org_id: db.orgId,
      site_id: db.siteId,
      unit_id: c.unit_id,
      debtor_kind: c.debtor_kind,
      from: c.opened_on,
      to: null,
      reason: 'Legal collection',
      case_id: c.id,
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    })
    return created(c)
  }),
  route('get', 'collection-cases/:id', ({ params }) => {
    const c = db.cases.find((x) => x.id === params.id)
    return c ? ok(c) : fail(404, 'not_found')
  }),
  route('patch', 'collection-cases/:id', ({ params, body }) => {
    const c = db.cases.find((x) => x.id === params.id)
    if (!c) return fail(404, 'not_found')
    Object.assign(c, body, { version: c.version + 1 })
    return ok(c)
  }),
  route('post', 'collection-cases/:id\\:close', ({ params, body }) => {
    const c = db.cases.find((x) => x.id === params.id)
    if (!c) return fail(404, 'not_found')
    c.status = 'closed'
    c.closed_on = db.today
    if (body.costs) {
      c.costs_charged = body.costs
      const id = nextId(db, 'ch')
      const d = debtorFor(db, c.unit_id, c.debtor_kind)
      db.charges.push({
        id,
        org_id: db.orgId,
        site_id: db.siteId,
        plan_id: null,
        unit_id: c.unit_id,
        unit_door_number: db.units.find((u) => u.id === c.unit_id)?.door_number ?? '',
        debtor_kind: c.debtor_kind,
        debtor_person_id: d.id,
        debtor_display_name: d.name,
        effective_debtor: effectiveOf(c.debtor_kind, d),
        charge_type: 'legal_cost',
        period: null,
        amount: body.costs,
        paid: m(0),
        balance: body.costs,
        due_date: db.today,
        description: `Legal costs ${c.file_no ?? ''}`.trim(),
        allocation_basis: null,
        journal_entry_id: `je-${id}`,
        status: 'open',
        cancelled_reason: null,
        created_at: stamp(),
        updated_at: stamp(),
      })
    }
    for (const f of db.freezes.filter((x) => x.case_id === c.id)) f.to = db.today
    return ok(c)
  }),
  reportRoute('debt-breakdown', 'debt-breakdown', ({ query }) => {
    const unitId = query.get('unit_id')!
    const s = side(query.get('debtor_kind'))
    const lines: S['DebtBreakdownLine'][] = db.charges
      .filter(
        (c) =>
          c.unit_id === unitId &&
          c.debtor_kind === s &&
          c.balance.amount > 0 &&
          c.status !== 'cancelled',
      )
      .map((c) => ({
        period: c.period,
        charge_id: c.id,
        due_date: c.due_date,
        principal: c.balance,
        late_fee: m(0),
      }))
    return ok({
      debtor: { unit_id: unitId, debtor_kind: s },
      as_of: db.today,
      lines,
      total_principal: m(lines.reduce((a, l) => a + l.principal.amount, 0)),
      total_late_fee: m(accruedFee(db, unitId, s)),
    } satisfies S['DebtBreakdown'])
  }),

  // ---- statements
  reportRoute('units/:unitId/statements/:side', 'statement', ({ params, query }) =>
    ok(
      statementFor(
        params.unitId!,
        side(params.side),
        null,
        query.get('from') ?? '2026-01-01',
        query.get('to') ?? db.today,
      ),
    ),
  ),
  reportRoute('persons/:personId/statement', 'statement', ({ params, query }) =>
    ok(
      statementFor(
        null,
        null,
        params.personId!,
        query.get('from') ?? '2026-01-01',
        query.get('to') ?? db.today,
      ),
    ),
  ),
  route('post', 'statements\\:send', ({ body }) => {
    db.sent.push({ kind: 'statement', id: body.unit_id ?? body.person_id, channels: body.channels })
    return ok({
      document_id: 'doc-1',
      file_id: 'file-1',
      recipient_count: 1,
      sms_count: body.channels.includes('sms') ? 1 : 0,
    } satisfies S['StatementSent'])
  }),
  http.get('*/v1/orgs/:orgId/me/balances', () => {
    // the signed-in resident owns A1 (owner side) and A1's tenant side is somebody else's
    const u = db.units[0]!
    const sides = (['owner'] as Side[]).map((s) => {
      const principal = openPrincipal(db, u.id, s)
      const fee = accruedFee(db, u.id, s)
      const credit = db.credits
        .filter((c) => c.unit_id === u.id && c.debtor_kind === s)
        .reduce((a, c) => a + c.balance.amount, 0)
      return {
        unit_id: u.id,
        unit_door_number: u.door_number,
        debtor_kind: s,
        principal: m(principal),
        accrued_late_fee: m(fee),
        total: m(principal + fee),
        credit: m(credit),
        next_due_date: addMonths(db.today.slice(0, 7) + '-05', 1),
        bank_details: {
          iban: 'TR33 0006 1005 1978 6457 8413 26',
          holder: 'Güneş Sitesi Yönetimi',
          bank_name: 'Garanti BBVA',
          reference_hint: 'Door number and month',
          suggested_reference: `${u.door_number} aidat`,
        },
      } satisfies S['MySide']
    })
    return HttpResponse.json({ as_of: db.today, sides } satisfies S['MyBalances'])
  }),

  // ---- opening balances
  route('get', 'opening-balances', () => list(db.openings)),
  route('post', 'opening-balances', ({ body }) => {
    const e = validate(need(body.as_of, 'as_of'), need(body.principal?.amount, 'principal'))
    if (e) return e
    if (Date.now() > db.reauthUntil) return fail(403, 'reauth_required')
    const ob: S['OpeningBalance'] = {
      id: nextId(db, 'ob'),
      org_id: db.orgId,
      site_id: db.siteId,
      as_of: body.as_of,
      kind: body.kind,
      unit_id: body.unit_id ?? null,
      debtor_kind: body.debtor_kind ?? null,
      person_id: body.person_id ?? null,
      account_id: body.account_id ?? null,
      vendor_id: null,
      principal: body.principal,
      late_fee: body.late_fee ?? m(0),
      journal_entry_id: 'je-ob',
      locked: false,
      created_at: stamp(),
      updated_at: stamp(),
      status: 'active',
      voided_at: null,
      void_reason: null,
    }
    db.openings.push(ob)
    return created(ob)
  }),
  route('post', 'opening-balances/:id\\:void', ({ params, body }) => {
    const ob = db.openings.find((x) => x.id === params.id)
    if (!ob) return fail(404, 'not_found')
    const e = validate(need(body.reason, 'reason'))
    if (e) return e
    if (ob.locked) return fail(409, 'locked_period')
    if (Date.now() > db.reauthUntil) return fail(403, 'reauth_required')
    ob.status = 'voided'
    ob.voided_at = stamp()
    ob.void_reason = body.reason
    return ok(ob)
  }),
  // ---- budgets
  route('get', 'budgets', () => list(db.budgets)),
  route('post', 'budgets', ({ body }) => {
    const b: S['Budget'] = {
      id: nextId(db, 'bud'),
      org_id: db.orgId,
      site_id: db.siteId,
      fiscal_year_id: body.fiscal_year_id,
      status: 'draft',
      decision_id: body.decision_id ?? null,
      total: m(0),
      created_at: stamp(),
      updated_at: stamp(),
      version: 1,
    }
    db.budgets.push(b)
    db.budgetLines[b.id] = []
    return created(b)
  }),
  route('get', 'budgets/:id', ({ params }) => {
    const b = db.budgets.find((x) => x.id === params.id)
    return b ? ok(b) : fail(404, 'not_found')
  }),
  route('get', 'budgets/:id/lines', ({ params }) =>
    ok({
      lines: db.budgetLines[params.id!] ?? [],
      version: db.budgets.find((x) => x.id === params.id)?.version ?? 1,
    } satisfies S['BudgetLines']),
  ),
  route('put', 'budgets/:id/lines', ({ params, body }) => {
    db.budgetLines[params.id!] = body.lines
    const b = db.budgets.find((x) => x.id === params.id)
    if (b) b.total = m((body.lines as S['BudgetLine'][]).reduce((a, l) => a + l.amount.amount, 0))
    return ok({ lines: body.lines, version: (b?.version ?? 1) + 1 })
  }),
  route('post', 'budgets/:id\\:finalize', ({ params }) => {
    const b = db.budgets.find((x) => x.id === params.id)
    if (!b) return fail(404, 'not_found')
    b.status = 'final'
    return ok(b)
  }),
  route('get', 'journal-entries', () => list(db.journal)),

  // ---- reports and overview
  reportRoute('reports/income-expense', 'income-expense', ({ query }) => {
    const from = query.get('from') ?? '2026-01-01'
    const to = query.get('to') ?? db.today
    const dues = db.payments
      .filter(
        (p) =>
          p.status === 'confirmed' &&
          p.received_at.slice(0, 10) >= from &&
          p.received_at.slice(0, 10) <= to,
      )
      .reduce((a, p) => a + p.allocated.amount, 0)
    const byCat = new Map<string, number>()
    for (const e of db.expenses.filter(
      (x) => x.status === 'posted' && x.paid_on >= from && x.paid_on <= to,
    ))
      byCat.set(e.category_account_id, (byCat.get(e.category_account_id) ?? 0) + e.amount.amount)
    const expense = [...byCat].map(([id, amount]) => {
      const a = db.accounts.find((x) => x.id === id)!
      return { account_id: id, code: a.code, name: a.name, amount: m(amount) }
    })
    const income =
      dues > 0
        ? [{ account_id: 'a-inc-dues', code: '600.01', name: 'Dues income', amount: m(dues) }]
        : []
    const te = expense.reduce((a, r) => a + r.amount.amount, 0)
    return ok({
      from,
      to,
      basis: 'cash',
      income,
      expense,
      total_income: m(dues),
      total_expense: m(te),
      net: m(dues - te),
    } satisfies S['IncomeExpenseReport'])
  }),
  reportRoute('reports/debtors', 'debtors', ({ query }) => {
    const min = Number(query.get('min_months') ?? 0)
    const rows: S['DebtorRow'][] = overdueUnits()
      .filter((r) => r.months >= min)
      .map((r) => ({
        unit_id: r.unit.id,
        unit_door_number: r.unit.door_number,
        debtor_kind: r.sd,
        debtor_person_id: debtorFor(db, r.unit.id, r.sd).id,
        debtor_display_name: debtorFor(db, r.unit.id, r.sd).name,
        principal: m(r.principal),
        late_fee: m(r.fee),
        total: m(r.principal + r.fee),
        months_overdue: r.months,
        in_collection: db.cases.some(
          (c) => c.unit_id === r.unit.id && c.debtor_kind === r.sd && c.status === 'open',
        ),
      }))
    const p = rows.reduce((a, r) => a + r.principal.amount, 0)
    const f = rows.reduce((a, r) => a + r.late_fee.amount, 0)
    return ok({
      as_of: db.today,
      rows: rows.sort((a, b) => b.total.amount - a.total.amount),
      total_principal: m(p),
      total_late_fee: m(f),
      total: m(p + f),
    } satisfies S['DebtorReport'])
  }),
  reportRoute('reports/budget-vs-actual', 'budget-vs-actual', ({ query }) => {
    const bud = db.budgets.find((b) => b.fiscal_year_id === query.get('fiscal_year_id'))
    const lines = bud ? (db.budgetLines[bud.id] ?? []) : []
    const rows: S['BudgetActualRow'][] = lines.map((l) => {
      const a = db.accounts.find((x) => x.id === l.expense_category_account_id)!
      const actual = db.expenses
        .filter((e) => e.status === 'posted' && e.category_account_id === a.id)
        .reduce((s, e) => s + e.amount.amount, 0)
      return {
        account_id: a.id,
        code: a.code,
        name: a.name,
        fund_id: l.fund_id,
        budget: l.amount,
        actual: m(actual),
        variance: m(l.amount.amount - actual),
      }
    })
    return ok({
      fiscal_year_id: query.get('fiscal_year_id') ?? '',
      rows,
      total_budget: m(rows.reduce((a, r) => a + r.budget.amount, 0)),
      total_actual: m(rows.reduce((a, r) => a + r.actual.amount, 0)),
    } satisfies S['BudgetVsActualReport'])
  }),
  reportRoute('reports/trial-balance', 'trial-balance', () => {
    const recv = db.charges
      .filter((c) => c.status !== 'cancelled')
      .reduce((a, c) => a + c.balance.amount, 0)
    const rows: S['TrialBalanceRow'][] = [
      {
        account_id: 'a-bank',
        code: '102.01',
        name: 'Garanti current account',
        type: 'asset',
        debit: m(bankBalance()),
        credit: m(0),
      },
      {
        account_id: 'recv',
        code: '120.01',
        name: 'Receivables',
        type: 'asset',
        debit: m(recv),
        credit: m(0),
      },
      {
        account_id: 'eq',
        code: '500.01',
        name: 'Opening equity',
        type: 'equity',
        debit: m(0),
        credit: m(bankBalance() + recv),
      },
    ]
    return ok({
      as_of: db.today,
      rows,
      total_debit: m(bankBalance() + recv),
      total_credit: m(bankBalance() + recv),
    } satisfies S['TrialBalanceReport'])
  }),
  reportRoute('reports/receivables-aging', 'receivables-aging', ({ query }) =>
    ok(agingBuckets(query.get('as_of') ?? db.today)),
  ),
  reportRoute('reports/collector-reconciliation', 'collector-reconciliation', ({ query }) => {
    const from = query.get('from') ?? '2026-01-01'
    const to = query.get('to') ?? db.today
    const rows: S['CollectorRow'][] = db.cashBoxes.map((cb) => {
      const received = db.payments
        .filter(
          (p) =>
            p.asset_account_id === cb.account_id &&
            p.status === 'confirmed' &&
            p.received_at.slice(0, 10) >= from &&
            p.received_at.slice(0, 10) <= to,
        )
        .reduce((a, p) => a + p.gross.amount, 0)
      const handed = db.transfers
        .filter(
          (t) =>
            t.from_account_id === cb.account_id &&
            t.transferred_at.slice(0, 10) >= from &&
            t.transferred_at.slice(0, 10) <= to,
        )
        .reduce((a, t) => a + t.amount.amount, 0)
      return {
        cash_box_id: cb.id,
        collector_person_id: cb.collector_person_id,
        collector_display_name: cb.collector_display_name,
        received: m(received),
        handed_over: m(handed),
        balance: cb.balance,
      }
    })
    return ok({ from, to, rows } satisfies S['CollectorReconciliationReport'])
  }),
  reportRoute('reports/bank-reconciliation', 'bank-reconciliation', ({ query }) => {
    const unmatched = db.bankTx.filter(
      (t) => t.match_status === 'unmatched' || t.match_status === 'suggested',
    )
    const inn = unmatched
      .filter((t) => t.amount.amount > 0)
      .reduce((a, t) => a + t.amount.amount, 0)
    const out = unmatched
      .filter((t) => t.amount.amount < 0)
      .reduce((a, t) => a + t.amount.amount, 0)
    const ledger = bankBalance()
    const stmt = db.imports.at(-1)?.closing_balance ?? null
    return ok({
      account_id: query.get('account_id') ?? 'a-bank',
      as_of: db.today,
      ledger_balance: m(ledger),
      statement_balance: stmt,
      unmatched_in: m(inn),
      unmatched_out: m(-out),
      difference: m((stmt?.amount ?? ledger) - ledger - inn - out),
    } satisfies S['BankReconciliationReport'])
  }),
  reportRoute('reports/fund-balances', 'fund-balances', () => {
    const op = bankBalance() + db.cashBoxes.reduce((a, c) => a + c.balance.amount, 0)
    const rows: S['FundBalanceRow'][] = [
      { fund_id: 'f-op', name: 'Operating', kind: 'operating', balance: m(op) },
      { fund_id: 'f-res', name: 'Reserve (demirbaş)', kind: 'reserve', balance: m(0) },
    ]
    return ok({ as_of: db.today, rows, total: m(op) } satisfies S['FundBalancesReport'])
  }),
  route('get', 'money/overview', () => {
    const sum = (s: Side) =>
      db.charges
        .filter((c) => c.debtor_kind === s && c.status !== 'cancelled')
        .reduce((a, c) => a + c.balance.amount, 0)
    const month = db.today.slice(0, 7)
    const due = db.charges
      .filter((c) => c.period === month && c.status !== 'cancelled')
      .reduce((a, c) => a + c.amount.amount, 0)
    const paid = db.charges
      .filter((c) => c.period === month && c.status !== 'cancelled')
      .reduce((a, c) => a + c.paid.amount, 0)
    return ok({
      as_of: db.today,
      receivable_owner: m(sum('owner')),
      receivable_tenant: m(sum('tenant')),
      cash: m(db.cashBoxes.reduce((a, c) => a + c.balance.amount, 0)),
      bank: m(bankBalance()),
      accrued_late_fees: m(
        sidesWithDebt(db).reduce((a, { unit, side: sd }) => a + accruedFee(db, unit.id, sd), 0),
      ),
      collection_rate_pct: due === 0 ? 0 : Math.round((paid / due) * 100),
      aging: agingBuckets(db.today),
    } satisfies S['MoneyOverview'])
  }),
]

export { CUR }
