import { http, HttpResponse } from 'msw'
import type { components } from '@/api/draft'
import { registerReset } from '../reset'
import { db as tenancyDb } from './db'
import { problem } from './http'
import { getMoneyDb } from './money'
import { applyToCharge, autoAllocate, m, nextId, type Side } from './money-db'
import { db as opsDb } from './operations'

/**
 * MSW handlers for card checkout (release 0.4): checkouts, settlements, the clearing view, the
 * client money payout, the refund of a provider payment, and the merchant of record setting with
 * its 409 `merchant_change_blocked`. Written from api/src/paths/money/{checkouts,me-checkouts,
 * payment-*,payments-payment_id-refund}.yaml over the money model (charges and payments).
 *
 * The "provider" settles a checkout on the second status poll, with the outcome the test or demo
 * picked (`checkoutMock().outcome`), so the pay flow can be walked end to end.
 */
type S = components['schemas']

const MERCHANT_KEY = 'payments.merchant_of_record'
const PROVIDER = 'iyzico'
const FEE_PCT = 0.025

export type CheckoutOutcome = 'paid' | 'failed' | 'declined' | 'expired' | 'mismatch'

interface Model {
  seeded: boolean
  checkouts: S['Checkout'][]
  settlements: S['ProviderSettlement'][]
  polls: Map<string, number>
  /** Provider clearing balance per provider (minor units). */
  clearing: Record<string, number>
  owed: number
  paidOut: number
  /** What the provider does with the next checkouts it settles. */
  outcome: CheckoutOutcome
  /** Polls before the provider settles a pending checkout. */
  pollsToSettle: number
  /** The refund asks for re-authentication until the person confirms. */
  refundNeedsReauth: boolean
  providerAvailable: boolean
  idem: Map<string, { fingerprint: string; status: number; body: unknown }>
}

function fresh(): Model {
  return {
    seeded: false,
    checkouts: [],
    settlements: [],
    polls: new Map(),
    clearing: { [PROVIDER]: 125_000 },
    owed: 40_000,
    paidOut: 0,
    outcome: 'paid',
    pollsToSettle: 2,
    refundNeedsReauth: false,
    providerAvailable: true,
    idem: new Map(),
  }
}

/**
 * In `pnpm dev:mock` (and the mock e2e project) the page is really left for the provider and
 * comes back by a full navigation, which restarts the in-memory model: the checkouts and the
 * provider balances ride along in sessionStorage so the return page finds them.
 */
const KEY = 'ob.mock.checkout'
const persistent = import.meta.env.MODE === 'mock'
function restore(m: Model): Model {
  if (!persistent) return m
  try {
    const raw = sessionStorage.getItem(KEY)
    if (raw) Object.assign(m, JSON.parse(raw))
  } catch {
    /* storage blocked: start from the seed */
  }
  return m
}
function persist() {
  if (!persistent) return
  const { seeded, checkouts, settlements, clearing, owed, paidOut } = model
  try {
    sessionStorage.setItem(
      KEY,
      JSON.stringify({ seeded, checkouts, settlements, clearing, owed, paidOut }),
    )
  } catch {
    /* ignore */
  }
}

let model = restore(fresh())
registerReset(() => {
  model = fresh()
})
/** The mock provider's switches and state, for tests. */
export const checkoutMock = () => model

const iso = () => `${getMoneyDb().today}T09:00:00Z`
const json = (body: unknown, status = 200) => HttpResponse.json(body as never, { status })
const orgOf = (p: unknown) => String(p)

function merchantOf(siteId: string): 'site' | 'organization' {
  const s = tenancyDb.settings[siteId]
  return s?.payments?.merchant_of_record ?? 'site'
}

/** Applies a settled payment to the money model and the provider balances. */
function payFor(c: S['Checkout'], gross: number): S['Payment'] {
  const db = getMoneyDb()
  const side = c.debtor_kind as Side
  const auto = autoAllocate(db, c.unit_id, side, gross)
  const allocations = c.charge_ids.length
    ? auto.allocations.filter((a) => c.charge_ids.includes(a.charge_id))
    : auto.allocations
  for (const a of allocations) {
    const ch = db.charges.find((x) => x.id === a.charge_id)
    if (ch) applyToCharge(ch, a.amount.amount)
  }
  const allocated = allocations.reduce((a, x) => a + x.amount.amount, 0)
  const fee = Math.round(gross * FEE_PCT)
  db.receiptSeq += 1
  const id = nextId(db, 'pay')
  const unit = db.units.find((u) => u.id === c.unit_id)
  const payer = side === 'tenant' ? (unit?.tenantPerson ?? unit?.ownerPerson) : unit?.ownerPerson
  const payment = {
    id,
    org_id: db.orgId,
    site_id: c.site_id,
    payer_person_id: payer ?? null,
    payer_display_name: db.persons.find((p) => p.id === payer)?.display_name ?? null,
    unit_id: c.unit_id,
    debtor_kind: side,
    method: `card:${c.provider}`,
    gross: m(gross),
    fee: m(fee),
    net: m(gross - fee),
    received_on: db.today,
    recorded_by: null,
    asset_account_id: 'a-clearing',
    reference: null,
    note: null,
    status: 'confirmed',
    receipt_no: `${db.today.slice(0, 4)}/${String(db.receiptSeq).padStart(6, '0')}`,
    provider_txn_id: `txn-${id}`,
    journal_entry_id: `je-${id}`,
    allocated: m(allocated),
    unallocated: m(gross - allocated),
    allocations,
    bank_transaction_id: null,
    created_at: iso(),
    updated_at: iso(),
    version: 1,
  } as S['Payment']
  db.payments.push(payment)
  if (c.merchant_of_record === 'organization') model.owed += gross - fee
  else model.clearing[c.provider] = (model.clearing[c.provider] ?? 0) + (gross - fee)
  return payment
}

function settle(c: S['Checkout']) {
  const outcome = model.outcome
  const now = iso()
  if (outcome === 'paid' || outcome === 'mismatch') {
    const confirmed = outcome === 'mismatch' ? Math.round(c.amount.amount * 0.9) : c.amount.amount
    const p = payFor(c, confirmed)
    c.status = 'paid'
    c.payment_id = p.id
    c.completed_at = now
    if (outcome === 'mismatch') {
      c.amount_mismatch = true
      c.confirmed_amount = m(confirmed)
    }
  } else if (outcome === 'expired') {
    c.status = 'expired'
    c.completed_at = now
  } else {
    c.status = 'failed'
    c.failure_code = outcome === 'declined' ? 'provider_declined' : 'provider_error'
    c.completed_at = now
  }
}

function seed() {
  if (model.seeded) return
  model.seeded = true
  const db = getMoneyDb()
  const unit = db.units.find((u) => u.door_number === 'A2') ?? db.units[1]!
  const base = {
    org_id: db.orgId,
    site_id: db.siteId,
    unit_id: unit.id,
    debtor_kind: 'owner' as const,
    charge_ids: [] as string[],
    provider: PROVIDER,
    merchant_of_record: 'site' as const,
    checkout_url: null as string | null,
    return_url: '',
    created_by: 'u1',
    amount_mismatch: false,
  }
  const mk = (id: string, status: S['Checkout']['status'], amount: number, extra = {}) =>
    ({
      ...base,
      id,
      amount: m(amount),
      status,
      expires_at: `${db.today}T10:00:00Z`,
      payment_id: null,
      failure_code: null,
      created_at: `${db.today}T08:00:00Z`,
      completed_at: status === 'pending' ? null : `${db.today}T08:05:00Z`,
      ...extra,
    }) as S['Checkout']
  const paid = mk('co-seed-paid', 'paid', 8_000)
  const keep = { clearing: { ...model.clearing }, owed: model.owed }
  const payment = payFor(paid, 8_000)
  paid.payment_id = payment.id
  model.checkouts.push(
    paid,
    mk('co-seed-failed', 'failed', 12_000, { failure_code: 'provider_declined' }),
    mk('co-seed-expired', 'expired', 5_000),
  )
  // the paid checkout above already moved the balances: put them back to the scene's starting point
  model.clearing = keep.clearing
  model.owed = keep.owed
  model.settlements.push(
    {
      provider: PROVIDER,
      ref: 'STL-2026-10-31',
      site_id: db.siteId,
      net: m(310_000),
      gross: m(318_000),
      fee: m(8_000),
      settled_on: '2026-10-31',
      entry_id: 'je-stl-1',
      merchant_of_record: 'site',
      created_at: '2026-10-31T06:00:00Z',
    },
    {
      provider: PROVIDER,
      ref: 'STL-2026-10-15',
      site_id: db.siteId,
      net: m(152_000),
      gross: m(156_000),
      fee: m(4_000),
      settled_on: '2026-10-15',
      entry_id: 'je-stl-0',
      merchant_of_record: 'organization',
      created_at: '2026-10-15T06:00:00Z',
    },
  )
  persist()
}

/** A pending checkout of A1's owner side, for tests that open the return page directly. */
export function addCheckout(extra: Partial<S['Checkout']> = {}): S['Checkout'] {
  seed()
  const db = getMoneyDb()
  const unit = db.units[0]!
  const id = nextId(db, 'co')
  const c: S['Checkout'] = {
    id,
    org_id: db.orgId,
    site_id: db.siteId,
    unit_id: unit.id,
    debtor_kind: 'owner',
    charge_ids: [],
    provider: PROVIDER,
    merchant_of_record: 'site',
    amount: m(10_000),
    status: 'pending',
    checkout_url: `https://pay.example.test/checkout/${id}`,
    return_url: `http://localhost/orgs/${db.orgId}/pay/return?checkout_id=${id}`,
    expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
    payment_id: null,
    failure_code: null,
    created_by: 'u1',
    created_at: new Date().toISOString(),
    completed_at: null,
    ...extra,
  }
  model.checkouts.push(c)
  persist()
  return c
}

const O = '*/v1/orgs/:orgId'

function clearingBody(siteId: string): S['PaymentClearing'] {
  return {
    site_id: siteId,
    merchant_of_record: merchantOf(siteId),
    providers: Object.entries(model.clearing).map(([provider, balance]) => ({
      provider,
      balance: m(balance),
    })),
    client_money_owed: m(model.owed),
    client_money_paid_out: m(model.paidOut),
  }
}

/** The balances that block a change of the merchant of record (site-a is the one with money). */
function openBalances(siteId: string) {
  const out: { site_id: string; account: string; kind: string; balance: number }[] = []
  for (const [, balance] of Object.entries(model.clearing))
    if (balance !== 0) out.push({ site_id: siteId, account: '1190', kind: 'clearing', balance })
  if (model.owed !== 0)
    out.push({ site_id: siteId, account: '1195', kind: 'client_money', balance: -model.owed })
  return out
}

function blocked(balances: ReturnType<typeof openBalances>) {
  return HttpResponse.json(
    {
      type: 'about:blank#merchant_change_blocked',
      title: 'merchant_change_blocked',
      status: 409,
      code: 'merchant_change_blocked',
      detail:
        'the merchant of record cannot change while a payment provider clearing or client money balance is open; settle or pay it out first',
      errors: [{ field: `settings.${MERCHANT_KEY}`, code: 'open_balance', params: { balances } }],
    },
    { status: 409, headers: { 'Content-Type': 'application/problem+json' } },
  )
}

const ifMatchOf = (request: Request) => request.headers.get('If-Match')?.replaceAll('"', '') ?? null

/** Replays a stored answer for the same key and request, 422 for the same key with another request. */
async function idempotent(request: Request, run: () => Promise<Response> | Response) {
  const key = request.headers.get('Idempotency-Key')
  if (!key) return problem(422, 'validation_failed', 'Idempotency-Key is required')
  const fingerprint = `${request.method} ${new URL(request.url).pathname} ${await request.clone().text()}`
  const seen = model.idem.get(key)
  if (seen) {
    if (seen.fingerprint !== fingerprint) return problem(422, 'idempotency_mismatch')
    return json(seen.body, seen.status)
  }
  const res = await run()
  if (res.status < 400)
    model.idem.set(key, {
      fingerprint,
      status: res.status,
      body: await res.clone().json(),
    })
  return res
}

export const checkoutHandlers = [
  http.post(`${O}/checkouts`, ({ request, params }) =>
    idempotent(request, async () => {
      const body = (await request.clone().json()) as {
        unit_id?: string
        debtor_kind?: 'owner' | 'tenant'
        charge_ids?: string[]
        amount?: S['Money']
      }
      seed()
      const db = getMoneyDb()
      if (!model.providerAvailable) return problem(409, 'payment_provider_unavailable')
      if (!body.unit_id || !body.debtor_kind)
        return problem(422, 'validation_failed', undefined, [
          { field: body.unit_id ? 'debtor_kind' : 'unit_id', code: 'required' },
        ])
      const open = db.charges.filter(
        (c) =>
          c.unit_id === body.unit_id &&
          c.debtor_kind === body.debtor_kind &&
          c.balance.amount > 0 &&
          c.status !== 'cancelled',
      )
      const ids = body.charge_ids ?? []
      if (ids.some((id) => !open.some((c) => c.id === id)))
        return problem(422, 'validation_failed', undefined, [
          { field: 'charge_ids', code: 'not_open' },
        ])
      const amount =
        body.amount?.amount ??
        (ids.length
          ? open.filter((c) => ids.includes(c.id)).reduce((a, c) => a + c.balance.amount, 0)
          : open.reduce((a, c) => a + c.balance.amount, 0))
      if (!(amount > 0))
        return problem(422, 'validation_failed', undefined, [
          { field: 'amount', code: 'nothing_to_pay' },
        ])
      const id = nextId(db, 'co')
      const origin = new URL(request.url).origin
      const checkout: S['Checkout'] = {
        id,
        org_id: orgOf(params.orgId),
        site_id: db.siteId,
        unit_id: body.unit_id,
        debtor_kind: body.debtor_kind,
        charge_ids: ids,
        provider: PROVIDER,
        merchant_of_record: merchantOf(db.siteId),
        amount: m(amount),
        status: 'pending',
        checkout_url: `https://pay.example.test/checkout/${id}`,
        return_url: `${origin}/orgs/${orgOf(params.orgId)}/pay/return?checkout_id=${id}`,
        expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
        payment_id: null,
        failure_code: null,
        created_by: 'u1',
        created_at: new Date().toISOString(),
        completed_at: null,
      }
      model.checkouts.push(checkout)
      persist()
      return json(checkout, 201)
    }),
  ),
  http.get(`${O}/checkouts/:id`, ({ params }) => {
    seed()
    const c = model.checkouts.find((x) => x.id === params.id)
    if (!c) return problem(404, 'not_found')
    if (c.status === 'pending') {
      const n = (model.polls.get(c.id) ?? 0) + 1
      model.polls.set(c.id, n)
      if (Date.parse(c.expires_at) < Date.now()) {
        c.status = 'expired'
        c.completed_at = new Date().toISOString()
      } else if (n >= model.pollsToSettle) settle(c)
      persist()
    }
    return json(c)
  }),
  http.get(`${O}/checkouts`, ({ request }) => {
    seed()
    const url = new URL(request.url)
    const status = url.searchParams.get('status')
    const items = model.checkouts.filter((c) => !status || c.status === status)
    return json({ items, next_cursor: null })
  }),
  http.get(`${O}/me/checkouts`, () => {
    seed()
    return json({ items: model.checkouts, next_cursor: null })
  }),
  http.get(`${O}/payment-settlements`, ({ request }) => {
    seed()
    const provider = new URL(request.url).searchParams.get('provider')
    const items = model.settlements.filter((s) => !provider || s.provider === provider)
    return json({ items, next_cursor: null })
  }),
  http.get(`${O}/payment-clearing`, ({ request }) => {
    seed()
    return json(clearingBody(new URL(request.url).searchParams.get('site_id') ?? 'site-a'))
  }),
  http.post(`${O}/payment-clearing\\:payout`, ({ request }) =>
    idempotent(request, async () => {
      seed()
      const b = (await request.clone().json()) as {
        site_id: string
        amount: S['Money']
        reference?: string
        paid_on?: string
      }
      // the site clock is the browser clock here (the money model has its own fixed "today"); one day of slack covers time zones
      const today = new Date(Date.now() + 86_400_000).toISOString().slice(0, 10)
      const errors: { field: string; code: string }[] = []
      if (!(b.amount?.amount > 0)) errors.push({ field: 'amount', code: 'not_positive' })
      else if (b.amount.amount > model.owed) errors.push({ field: 'amount', code: 'above_balance' })
      if (!b.reference?.trim()) errors.push({ field: 'reference', code: 'required' })
      if (b.paid_on && b.paid_on > today) errors.push({ field: 'paid_on', code: 'in_future' })
      if (errors.length) return problem(422, 'validation_failed', undefined, errors)
      model.owed -= b.amount.amount
      model.paidOut += b.amount.amount
      persist()
      return json(clearingBody(b.site_id), 201)
    }),
  ),
  http.post(`${O}/payments/:id\\:refund`, ({ request, params }) =>
    idempotent(request, () => {
      const db = getMoneyDb()
      const p = db.payments.find((x) => x.id === params.id)
      if (!p) return problem(404, 'not_found')
      if (model.refundNeedsReauth && Date.now() > db.reauthUntil)
        return problem(403, 'reauth_required', 'Confirm it is you to continue.')
      const v = ifMatchOf(request)
      if (v === null) return problem(422, 'validation_failed', 'If-Match is required')
      if (p.status === 'refunded') return json(p)
      if (v !== String((p as { version?: number }).version ?? 1))
        return problem(409, 'version_conflict')
      if (!p.method.startsWith('card:') || p.status !== 'confirmed')
        return problem(409, 'conflict', 'Not a confirmed provider payment')
      for (const a of p.allocations) {
        const ch = db.charges.find((x) => x.id === a.charge_id)
        if (ch) applyToCharge(ch, -a.amount.amount)
      }
      p.status = 'refunded'
      ;(p as { version?: number }).version = ((p as { version?: number }).version ?? 1) + 1
      const co = model.checkouts.find((c) => c.payment_id === p.id)
      if (co) co.status = 'refunded'
      persist()
      const net = p.net.amount
      if (co?.merchant_of_record === 'organization') model.owed -= net
      else model.clearing[co?.provider ?? PROVIDER] = (model.clearing[PROVIDER] ?? 0) - net
      return json(p)
    }),
  ),

  // privacy notice and consent texts per locale (codes.listConsents); read only, no accept operation
  http.get(`${O}/privacy/consents`, ({ request }) => {
    const locale = new URL(request.url).searchParams.get('locale') === 'tr' ? 'tr' : 'en'
    const tr = locale === 'tr'
    return json({
      locale,
      items: [
        {
          id: 'tr.privacy_notice',
          pack: 'tr',
          version: '2026-03',
          locale,
          markdown: tr
            ? '## Aydınlatma metni\n\n6698 sayılı **KVKK** kapsamında kişisel verileriniz yönetim hizmeti için işlenir.'
            : '## Privacy notice\n\nYour personal data is processed to run the building management, under Law 6698 (**KVKK**).',
        },
        {
          id: 'tr.consent_directory',
          pack: 'tr',
          version: '2026-03',
          locale,
          markdown: tr
            ? 'Adımın site sakinleri rehberinde görünmesine **açık rıza** veriyorum.'
            : 'I give **explicit consent** to appear in the residents directory.',
        },
      ],
    })
  }),

  // merchant of record: the organization default (flat key, merged on update)
  http.patch(`${O}`, async ({ request }) => {
    const body = (await request
      .clone()
      .json()
      .catch(() => null)) as {
      settings?: Record<string, unknown>
    } | null
    const next = body?.settings?.[MERCHANT_KEY]
    if (!body?.settings || !(MERCHANT_KEY in body.settings) || !opsDb.org) return undefined
    const org = opsDb.org as unknown as { version: number; settings: Record<string, unknown> }
    const v = ifMatchOf(request)
    if (v === null) return problem(422, 'validation_failed', 'If-Match is required')
    if (v !== String(org.version)) return problem(409, 'version_conflict')
    if (next !== null && next !== 'site' && next !== 'organization')
      return problem(422, 'validation_failed', undefined, [
        { field: `settings.${MERCHANT_KEY}`, code: 'invalid_value' },
      ])
    const before = (org.settings[MERCHANT_KEY] as string | undefined) ?? 'site'
    const after = (next as string | null) ?? 'site'
    if (before !== after) {
      seed()
      const b = openBalances('site-a')
      if (b.length > 0 && !tenancyDb.settings['site-a']?.payments?.merchant_of_record_override)
        return blocked(b)
    }
    if (next === null) delete org.settings[MERCHANT_KEY]
    else org.settings[MERCHANT_KEY] = next
    org.version += 1
    for (const s of Object.values(tenancyDb.settings)) {
      if (s.payments && !s.payments.merchant_of_record_override)
        s.payments = { ...s.payments, merchant_of_record: after as 'site' | 'organization' }
    }
    return json(opsDb.org)
  }),

  // merchant of record: the per-site override
  http.patch(`${O}/sites/:siteId/settings`, async ({ request, params }) => {
    const body = (await request
      .clone()
      .json()
      .catch(() => null)) as {
      merchant_of_record?: 'site' | 'organization' | null
    } | null
    if (!body || !('merchant_of_record' in body)) return undefined
    const siteId = String(params.siteId)
    const site = tenancyDb.sites.find((x) => x.id === siteId)
    const settings = tenancyDb.settings[siteId]
    if (!site || !settings) return problem(404, 'not_found')
    const v = ifMatchOf(request)
    if (v === null) return problem(422, 'validation_failed', 'If-Match is required')
    if (v !== String(site.version)) return problem(409, 'version_conflict')
    const payments = settings.payments ?? {
      card: { available: true, provider: PROVIDER },
      merchant_of_record: 'site' as const,
      merchant_of_record_override: null,
    }
    const orgValue =
      ((opsDb.org?.settings as Record<string, unknown> | undefined)?.[MERCHANT_KEY] as
        'site' | 'organization' | undefined) ?? 'site'
    const override = body.merchant_of_record ?? null
    const effective = override ?? orgValue
    if (effective !== payments.merchant_of_record) {
      seed()
      const b = openBalances(siteId)
      if (b.length > 0) return blocked(b)
    }
    settings.payments = {
      ...payments,
      merchant_of_record_override: override,
      merchant_of_record: effective,
    }
    site.version += 1
    return json({ ...settings, version: site.version })
  }),
]
