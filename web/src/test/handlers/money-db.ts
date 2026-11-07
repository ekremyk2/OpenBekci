import type { components } from '@/api/draft'

/**
 * In-memory model behind the money MSW handlers (tests and `pnpm dev:mock`). It is deliberately
 * small but chains like the real thing: a payment reduces charges, a waiver lowers the accrued late
 * fee, a declared payment waits for confirmation, receipt numbers never skip.
 */
type S = components['schemas']

export const CUR = 'TRY'
export const m = (amount: number): S['Money'] => ({ amount, currency: CUR })

export type Side = 'owner' | 'tenant'

export interface MoneyDb {
  today: string
  orgId: string
  siteId: string
  seq: number
  receiptSeq: number
  reauthUntil: number
  idemCache: Map<string, { status: number; body: unknown }>
  idemKeysSeen: string[]
  lateFeePolicy: S['LateFeePolicy']
  blocks: S['Block'][]
  units: (S['Unit'] & {
    weights: Record<string, number>
    tenantPerson: string | null
    ownerPerson: string
  })[]
  persons: S['Person'][]
  vendors: S['Vendor'][]
  keys: S['AllocationKey'][]
  tags: S['UnitTag'][]
  accounts: S['Account'][]
  funds: S['Fund'][]
  cashBoxes: S['CashBox'][]
  fiscalYears: S['FiscalYear'][]
  lockDate: string | null
  plans: S['ChargePlan'][]
  rates: S['ChargePlanRate'][]
  charges: S['Charge'][]
  payments: S['Payment'][]
  credits: S['Credit'][]
  transfers: S['Transfer'][]
  refunds: S['Refund'][]
  waivers: S['LateFeeWaiver'][]
  freezes: S['LateFeeFreeze'][]
  bookings: S['LateFeeBooking'][]
  importers: S['BankImporter'][]
  imports: S['BankStatementImport'][]
  bankTx: S['BankTransaction'][]
  expenses: S['Expense'][]
  recurring: S['RecurringExpense'][]
  bills: S['Bill'][]
  billPayments: S['BillPayment'][]
  stages: S['DunningStage'][]
  events: S['DunningEvent'][]
  cases: S['CollectionCase'][]
  openings: S['OpeningBalance'][]
  budgets: S['Budget'][]
  budgetLines: Record<string, S['BudgetLine'][]>
  journal: S['JournalEntry'][]
  files: S['File'][]
  sent: { kind: string; id: string; channels: string[] }[]
}

const iso = (d: Date) => d.toISOString().slice(0, 10)
export function addMonths(date: string, n: number): string {
  const [y, mo, d] = date.split('-').map(Number) as [number, number, number]
  const dt = new Date(Date.UTC(y, mo - 1 + n, d))
  return iso(dt)
}
export const daysBetween = (a: string, b: string) =>
  Math.round((Date.parse(`${b}T00:00:00Z`) - Date.parse(`${a}T00:00:00Z`)) / 86_400_000)

export function nextId(db: MoneyDb, prefix: string) {
  db.seq += 1
  return `${prefix}-${db.seq}`
}

const stamp = '2026-01-01T00:00:00Z'
const base = { created_at: stamp, updated_at: stamp }

export function createMoneyDb(today = '2026-11-07'): MoneyDb {
  const orgId = 'org-1'
  const siteId = 'site-a'
  const org = { org_id: orgId, site_id: siteId }
  const unitDefs = [
    ['A1', 'a', 10, 80, 'p-ayse', 'p-mehmet'],
    ['A2', 'a', 10, 80, 'p-can', 'p-zeynep'],
    ['A3', 'a', 15, 100, 'p-deniz', null],
    ['B1', 'b', 15, 100, 'p-elif', null],
    ['B2', 'b', 25, 120, 'p-fatma', null],
    ['B3', 'b', 25, 120, 'p-gul', null],
  ] as const
  const personNames: Record<string, string> = {
    'p-ayse': 'Ayşe Yılmaz',
    'p-mehmet': 'Mehmet Kaya',
    'p-can': 'Can Demir',
    'p-zeynep': 'Zeynep Arslan',
    'p-deniz': 'Deniz Çelik',
    'p-elif': 'Elif Şahin',
    'p-fatma': 'Fatma Aydın',
    'p-gul': 'Gül Öztürk',
  }
  const db: MoneyDb = {
    today,
    orgId,
    siteId,
    seq: 1000,
    receiptSeq: 0,
    reauthUntil: 0,
    idemCache: new Map(),
    idemKeysSeen: [],
    lateFeePolicy: {
      site_id: siteId,
      rate_pct_per_month: 3,
      max_rate_pct_per_month: 5,
      grace_days: 5,
      simple: true,
      waiver_reauth_threshold: m(50_000),
    },
    blocks: [
      { id: 'blk-a', ...org, name: 'A Blok', is_default: true, ...base, version: 1 },
      { id: 'blk-b', ...org, name: 'B Blok', is_default: false, ...base, version: 1 },
    ],
    units: unitDefs.map(([door, blk, share, area, owner, tenant]) => ({
      id: `u-${door.toLowerCase()}`,
      ...org,
      block_id: `blk-${blk}`,
      door_number: door,
      legal_ref: null,
      type: 'flat' as const,
      area_m2: area,
      parent_unit_id: null,
      status: 'active' as const,
      vacant: false,
      occupancy: { owners: 1, tenants: tenant ? 1 : 0, household: 0 },
      ...base,
      version: 1,
      weights: { 'k-share': share, 'k-equal': 1, 'k-area': area },
      ownerPerson: owner,
      tenantPerson: tenant,
    })),
    persons: Object.entries(personNames).map(([id, name]) => ({
      id,
      org_id: orgId,
      user_id: null,
      display_name: name,
      contact: {} as S['PersonContact'],
      national_id_masked: null,
      national_id_country: null,
      gate_contact_phone: null,
      notes: null,
      join_status: 'joined' as const,
      directory_consent: [],
      ...base,
      version: 1,
    })),
    vendors: [
      {
        id: 'v-1',
        org_id: orgId,
        name: 'Işık Elektrik',
        tax_id: null,
        contact: {} as S['PersonContact'],
        default_expense_category_id: 'a-exp-electric',
        archived: false,
        ...base,
        version: 1,
      },
    ],
    keys: [
      ['k-share', 'share', 'Arsa payı', 'share'],
      ['k-equal', 'equal', 'Equal per unit', 'equal'],
      ['k-area', 'area', 'By area (m²)', 'area'],
    ].map(([id, code, name, kind]) => ({
      id: id!,
      ...org,
      code: code!,
      name: name!,
      kind: kind as S['AllocationKey']['kind'],
      is_system: true,
      ...base,
      version: 1,
    })),
    tags: [{ id: 'tag-shops', ...org, code: 'shops', name: 'Shops', ...base, version: 1 }],
    accounts: [
      ['a-cash-1', '100.01', 'Cash box – Ali', 'asset', 'cash_box', 'f-op'],
      ['a-cash-2', '100.02', 'Cash box – Veli', 'asset', 'cash_box', 'f-op'],
      ['a-bank', '102.01', 'Garanti current account', 'asset', 'bank', 'f-op'],
      ['a-inc-dues', '600.01', 'Dues income', 'income', 'dues_contribution', null],
      ['a-exp-electric', '770.01', 'Electricity', 'expense', 'expense_category', null],
      ['a-exp-cleaning', '770.02', 'Cleaning', 'expense', 'expense_category', null],
      ['a-exp-repair', '770.03', 'Repairs', 'expense', 'expense_category', null],
    ].map(([id, code, name, type, subtype, fund]) => ({
      id: id!,
      ...org,
      code: code!,
      name: name!,
      type: type as S['Account']['type'],
      subtype: subtype!,
      fund_id: fund ?? null,
      is_system: false,
      archived: false,
      ...base,
      version: 1,
    })),
    funds: [
      {
        id: 'f-op',
        ...org,
        name: 'Operating',
        kind: 'operating',
        is_default: true,
        ...base,
        version: 1,
      },
      {
        id: 'f-res',
        ...org,
        name: 'Reserve (demirbaş)',
        kind: 'reserve',
        is_default: false,
        ...base,
        version: 1,
      },
    ],
    cashBoxes: [
      {
        id: 'cb-1',
        ...org,
        name: 'Ali – cash box',
        account_id: 'a-cash-1',
        collector_person_id: 'p-ayse',
        collector_display_name: 'Ayşe Yılmaz',
        balance: m(0),
        ...base,
        version: 1,
      },
    ],
    fiscalYears: [
      {
        id: 'fy-2026',
        ...org,
        start: '2026-01-01',
        end: '2026-12-31',
        status: 'open',
        approved_by_decision_id: null,
        ...base,
        version: 1,
      },
    ],
    lockDate: null,
    plans: [],
    rates: [],
    charges: [],
    payments: [],
    credits: [],
    transfers: [],
    refunds: [],
    waivers: [],
    freezes: [],
    bookings: [],
    importers: [
      { id: 'generic-csv', name: 'Generic CSV', formats: ['csv'], default_mapping: null },
      { id: 'generic-xlsx', name: 'Generic Excel', formats: ['xlsx'], default_mapping: null },
    ],
    imports: [],
    bankTx: [],
    expenses: [],
    recurring: [],
    bills: [],
    billPayments: [],
    stages: [
      {
        id: 'st-1',
        ...org,
        name: 'Friendly reminder',
        kind: 'reminder',
        days_after_due: 7,
        channels: ['in_app', 'sms'],
        template_key: null,
        position: 1,
        enabled: true,
        ...base,
        version: 1,
      },
      {
        id: 'st-2',
        ...org,
        name: 'Ihtarname',
        kind: 'ihtarname',
        days_after_due: 60,
        channels: ['email'],
        template_key: null,
        position: 2,
        enabled: true,
        ...base,
        version: 1,
      },
    ],
    events: [],
    cases: [],
    openings: [],
    budgets: [],
    budgetLines: {},
    journal: [],
    files: [],
    sent: [],
  }

  // A monthly dues plan (600 TRY a month split by arsa payı) with three months of history.
  const plan: S['ChargePlan'] = {
    id: 'plan-dues',
    ...org,
    name: 'Monthly dues',
    kind: 'recurring',
    charge_type: 'dues',
    default_debtor: 'tenant',
    frequency: 'monthly',
    due_day: 5,
    start: '2026-01-01',
    end: null,
    amount_mode: 'total_allocated',
    allocation_key_id: 'k-share',
    scope: { type: 'site', id: null },
    fund_id: 'f-op',
    income_account_id: 'a-inc-dues',
    status: 'active',
    decision_id: null,
    current_rate: null,
    ...base,
    version: 1,
  }
  const rate: S['ChargePlanRate'] = {
    id: 'rate-1',
    org_id: orgId,
    plan_id: plan.id,
    from: '2026-01-01',
    to: null,
    amount: m(600_000),
    decision_id: null,
    ...base,
  }
  plan.current_rate = rate
  db.plans.push(plan)
  db.rates.push(rate)
  for (const period of ['2026-08', '2026-09', '2026-10', '2026-11']) {
    generateCharges(db, plan, period)
  }
  // A cash box with a little money so hand-over has something to hand over.
  db.cashBoxes[0]!.balance = m(120_000)
  // Declared payment waiting for confirmation (portal "I've paid by transfer").
  const unit = db.units[2]!
  db.payments.push({
    id: 'pay-declared-1',
    ...org,
    payer_person_id: unit.ownerPerson,
    payer_display_name: personNames[unit.ownerPerson]!,
    unit_id: unit.id,
    debtor_kind: 'owner',
    method: 'bank_transfer',
    gross: m(90_000),
    fee: m(0),
    net: m(90_000),
    received_at: '2026-11-04',
    recorded_by: null,
    asset_account_id: 'a-bank',
    reference: 'A3 October dues',
    note: null,
    status: 'pending_declared',
    receipt_no: null,
    provider_txn_id: null,
    journal_entry_id: null,
    allocated: m(0),
    unallocated: m(90_000),
    allocations: [],
    bank_transaction_id: null,
    ...base,
  })
  return db
}

export const personName = (db: MoneyDb, id: string | null) =>
  db.persons.find((p) => p.id === id)?.display_name ?? null

export function debtorFor(db: MoneyDb, unitId: string, side: Side) {
  const u = db.units.find((x) => x.id === unitId)
  const id = side === 'tenant' ? u?.tenantPerson : u?.ownerPerson
  return { id: id ?? null, name: personName(db, id ?? null) }
}

/** Weights of the units a plan or expense split touches. */
export function unitsInScope(db: MoneyDb, scope: S['ChargeScope']) {
  return db.units.filter((u) => {
    if (scope.type === 'block') return u.block_id === scope.id
    if (scope.type === 'unit_tag') return u.door_number.startsWith('B') && scope.id === 'tag-shops'
    return true
  })
}

export function previewSplit(
  db: MoneyDb,
  args: {
    amount: number
    mode: 'per_unit' | 'total_allocated'
    keyId: string
    scope: S['ChargeScope']
    debtor: Side
    period: string
  },
): S['ChargePlanPreview'] {
  const units = unitsInScope(db, args.scope)
  const weights = units.map((u) => u.weights[args.keyId] ?? 1)
  const totalWeight = weights.reduce((a, b) => a + b, 0)
  let amounts: number[]
  let remainder = false
  if (args.mode === 'per_unit') {
    amounts = units.map(() => args.amount)
  } else {
    amounts = weights.map((w) => Math.floor((args.amount * w) / totalWeight))
    let left = args.amount - amounts.reduce((a, b) => a + b, 0)
    remainder = left > 0
    const order = weights.map((w, i) => [w, i] as const).sort((a, b) => b[0] - a[0])
    for (let i = 0; left > 0; i = (i + 1) % order.length, left--) amounts[order[i]![1]]! += 1
  }
  const lines: S['ChargePreviewLine'][] = units.map((u, i) => {
    const side: Side = args.debtor === 'tenant' && u.tenantPerson ? 'tenant' : 'owner'
    const d = debtorFor(db, u.id, side)
    return {
      unit_id: u.id,
      door_number: u.door_number,
      debtor_kind: side,
      debtor_person_id: d.id,
      debtor_display_name: d.name,
      weight: weights[i]!,
      amount: m(amounts[i]!),
    }
  })
  return {
    period: args.period,
    total: m(lines.reduce((a, l) => a + l.amount.amount, 0)),
    unit_count: lines.length,
    lines,
    remainder_distributed: remainder,
  }
}

export function rateAt(db: MoneyDb, planId: string, date: string): S['ChargePlanRate'] | null {
  return (
    db.rates
      .filter((r) => r.plan_id === planId && r.from <= date && (!r.to || r.to >= date))
      .sort((a, b) => b.from.localeCompare(a.from))[0] ?? null
  )
}

export function refreshCurrentRate(db: MoneyDb, plan: S['ChargePlan']) {
  plan.current_rate = rateAt(db, plan.id, db.today)
}

export function generateCharges(db: MoneyDb, plan: S['ChargePlan'], period: string) {
  const first = `${period}-01`
  const rate = rateAt(db, plan.id, first)
  if (!rate) return
  const preview = previewSplit(db, {
    amount: rate.amount.amount,
    mode: plan.amount_mode,
    keyId: plan.allocation_key_id,
    scope: plan.scope,
    debtor: plan.default_debtor,
    period,
  })
  const due = `${period}-${String(plan.due_day ?? 1).padStart(2, '0')}`
  for (const l of preview.lines) {
    const u = db.units.find((x) => x.id === l.unit_id)!
    const id = nextId(db, 'ch')
    // History: everything before the current month is paid for units A1 and B1 only.
    const paidUp =
      period < db.today.slice(0, 7) && (u.door_number === 'A1' || u.door_number === 'B1')
    db.charges.push({
      id,
      org_id: db.orgId,
      site_id: db.siteId,
      plan_id: plan.id,
      unit_id: l.unit_id,
      unit_door_number: l.door_number,
      debtor_kind: l.debtor_kind,
      debtor_person_id: l.debtor_person_id,
      debtor_display_name: l.debtor_display_name,
      charge_type: plan.charge_type,
      period,
      amount: l.amount,
      paid: m(paidUp ? l.amount.amount : 0),
      balance: m(paidUp ? 0 : l.amount.amount),
      due_date: due,
      description: plan.name,
      allocation_basis: {
        key_code: db.keys.find((k) => k.id === plan.allocation_key_id)?.code ?? '',
        weight: l.weight,
        total_weight: preview.lines.reduce((a, x) => a + x.weight, 0),
        remainder_adjusted: preview.remainder_distributed,
      },
      journal_entry_id: `je-${id}`,
      status: paidUp ? 'paid' : 'open',
      cancelled_reason: null,
      created_at: stamp,
      updated_at: stamp,
    })
  }
}

export function applyToCharge(c: S['Charge'], amount: number) {
  c.paid = m(c.paid.amount + amount)
  c.balance = m(c.amount.amount - c.paid.amount)
  c.status = c.balance.amount <= 0 ? 'paid' : c.paid.amount > 0 ? 'partially_paid' : 'open'
}

/** Oldest first, principal before fee; whatever is left over becomes a prepayment credit. */
export function autoAllocate(db: MoneyDb, unitId: string, side: Side, amount: number) {
  const open = db.charges
    .filter(
      (c) =>
        c.unit_id === unitId &&
        c.debtor_kind === side &&
        c.balance.amount > 0 &&
        c.status !== 'cancelled',
    )
    .sort((a, b) => a.due_date.localeCompare(b.due_date))
  const out: S['PaymentAllocation'][] = []
  let left = amount
  for (const c of open) {
    if (left <= 0) break
    const take = Math.min(left, c.balance.amount)
    out.push({
      charge_id: c.id,
      charge_description: `${c.description ?? c.charge_type} ${c.period ?? ''}`.trim(),
      amount: m(take),
    })
    left -= take
  }
  return { allocations: out, credit: m(left) }
}

export function openPrincipal(db: MoneyDb, unitId: string, side: Side, asOf = db.today) {
  return db.charges
    .filter(
      (c) =>
        c.unit_id === unitId &&
        c.debtor_kind === side &&
        c.status !== 'cancelled' &&
        c.due_date <= asOf,
    )
    .reduce((a, c) => a + c.balance.amount, 0)
}

export function isFrozen(db: MoneyDb, unitId: string, side: Side, asOf = db.today) {
  return db.freezes.some(
    (f) =>
      f.unit_id === unitId && f.debtor_kind === side && f.from <= asOf && (!f.to || f.to >= asOf),
  )
}

/** Simple interest on overdue principal, monthly rate prorated by days, minus waivers. */
export function accruedFee(db: MoneyDb, unitId: string, side: Side, asOf = db.today): number {
  if (isFrozen(db, unitId, side, asOf)) return 0
  const pol = db.lateFeePolicy
  let total = 0
  for (const c of db.charges) {
    if (
      c.unit_id !== unitId ||
      c.debtor_kind !== side ||
      c.status === 'cancelled' ||
      c.balance.amount <= 0
    )
      continue
    const late = daysBetween(c.due_date, asOf) - pol.grace_days
    if (late <= 0) continue
    total += Math.round((c.balance.amount * pol.rate_pct_per_month * late) / 100 / 30)
  }
  const waived = db.waivers
    .filter((w) => w.unit_id === unitId && w.debtor_kind === side)
    .reduce((a, w) => a + w.amount.amount, 0)
  return Math.max(0, total - waived)
}

export function sidesWithDebt(db: MoneyDb) {
  const out: { unit: MoneyDb['units'][number]; side: Side }[] = []
  for (const u of db.units) {
    for (const side of ['owner', 'tenant'] as const) {
      if (db.charges.some((c) => c.unit_id === u.id && c.debtor_kind === side))
        out.push({ unit: u, side })
    }
  }
  return out
}

export function seedBankImport(
  db: MoneyDb,
  id: string,
  fileName: string,
): S['BankStatementImport'] {
  const picks = db.charges.filter((c) => c.balance.amount > 0 && c.period === '2026-10').slice(0, 4)
  const txs: S['BankTransaction'][] = picks.map((c, i) => ({
    id: `${id}-tx${i + 1}`,
    org_id: db.orgId,
    site_id: db.siteId,
    import_id: id,
    booked_at: `2026-11-0${i + 1}`,
    amount: c.balance,
    counterparty: c.debtor_display_name,
    description: `Dues ${c.unit_door_number}`,
    match_status: i < 3 ? 'suggested' : 'unmatched',
    payment_id: null,
    expense_id: null,
    suggestions:
      i < 3
        ? [
            {
              unit_id: c.unit_id,
              unit_door_number: c.unit_door_number,
              debtor_kind: c.debtor_kind,
              person_id: c.debtor_person_id,
              score: 0.9 - i * 0.1,
              reason: 'Reference text and amount match',
            },
          ]
        : [],
    created_at: stamp,
    updated_at: stamp,
  }))
  txs.push({
    id: `${id}-tx9`,
    org_id: db.orgId,
    site_id: db.siteId,
    import_id: id,
    booked_at: '2026-11-06',
    amount: m(-45_000),
    counterparty: 'Işık Elektrik',
    description: 'Invoice payment',
    match_status: 'unmatched',
    payment_id: null,
    expense_id: null,
    suggestions: [],
    created_at: stamp,
    updated_at: stamp,
  })
  db.bankTx.push(...txs)
  const total = txs.reduce((a, t) => a + t.amount.amount, 0)
  return {
    id,
    org_id: db.orgId,
    site_id: db.siteId,
    importer: 'generic-csv',
    file_id: fileName,
    asset_account_id: 'a-bank',
    status: 'ready',
    opening_balance: m(1_000_000),
    closing_balance: m(1_000_000 + total),
    period_from: '2026-11-01',
    period_to: '2026-11-07',
    transaction_count: txs.length,
    unmatched_count: txs.length,
    error: null,
    created_at: stamp,
    updated_at: stamp,
  }
}
