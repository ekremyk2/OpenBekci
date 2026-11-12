import { i18n, type MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'
import { useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import type { S } from './api'

/* ───────────── server problems → copy ───────────── */

/** `field:code` wins over `code`; the server's field and code names are stable, its English detail is not copy. */
const ISSUES: Record<string, MessageDescriptor> = {
  'amount:invalid_value': msg`Enter an amount above zero.`,
  'amount:required': msg`Enter an amount.`,
  'charge_ids:not_open': msg`One of the chosen charges is no longer open. Reload and choose again.`,
  'charge_ids:too_many': msg`Too many charges chosen. Choose fewer or pay an amount.`,
  'from:must_not_be_in_the_past': msg`The start date cannot be in the past.`,
  'from:invalid_format': msg`Enter the date as year-month-day.`,
  'rate_bps_per_month:too_large': msg`The rate is above the statutory cap.`,
  'rate_bps_per_month:invalid_value': msg`Enter a rate of zero or more.`,
  'grace_days:invalid_value': msg`Enter grace days of zero or more.`,
  'status:invalid_value': msg`This status is not allowed, or the end date is before the start.`,
  'due_day:invalid_value': msg`Enter a due day from 1 to 31.`,
  required: msg`This field is required.`,
  invalid_value: msg`This value is not valid.`,
  invalid_format: msg`This value is not in the expected format.`,
  too_large: msg`This value is too large.`,
  too_long: msg`This text is too long.`,
  must_not_be_in_the_past: msg`This date cannot be in the past.`,
  unknown_field: msg`This field cannot be changed here.`,
  unsupported: msg`This option is not supported.`,
  locked_period: msg`This accounting period is closed and can no longer be changed.`,
}

/** 409 `conflict` carries its reason only as English detail text; these are the known ones. */
const CONFLICTS: [RegExp, MessageDescriptor][] = [
  [/larger than the unpaid late fee/, msg`The waiver is more than the unpaid late fee.`],
  [/no late fee to waive/, msg`There is no late fee to waive.`],
  [
    /already has an entry from/,
    msg`The late fee schedule already has an entry from this date or later. A new entry must start later.`,
  ],
  [/rate overlaps another rate/, msg`This rate overlaps another rate of the plan.`],
  [/only a confirmed payment can be refunded/, msg`Only a confirmed payment can be refunded.`],
  [
    /through a payment provider can be refunded/,
    msg`Only card payments can be refunded this way. Void a cash or bank payment instead.`,
  ],
]

const CODES: Record<string, MessageDescriptor> = {
  payment_provider_unavailable: msg`Card payment is not available for this site right now.`,
  idempotency_mismatch: msg`This request was already sent with different details. Reload the form and try again.`,
  locked_period: msg`This accounting period is closed and can no longer be changed.`,
  version_conflict: msg`Someone else changed this record. Reload to see the latest version.`,
  insufficient_permission: msg`You do not have permission to do this.`,
  rate_limited: msg`Too many requests. Please wait a moment and try again.`,
  pack_error: msg`The payment provider could not complete this. Please try again later.`,
  merchant_change_blocked: msg`The merchant of record cannot change while money is on its way.`,
}

export function issueText(field: string, code: string): string | undefined {
  const d = ISSUES[`${field}:${code}`] ?? ISSUES[code]
  return d ? i18n._(d) : undefined
}

/** Copy for a failed money call: its field issues, then its code, then known 409 reasons; `fallback` otherwise. */
export function moneyErrorText(e: unknown, fallback: string): string {
  if (!(e instanceof AppError)) return fallback
  for (const [field, issues] of Object.entries(e.fieldErrors)) {
    const text = issues[0] && issueText(field, issues[0].code)
    if (text) return text
  }
  const d = CODES[e.code]
  if (d) return i18n._(d)
  const c = CONFLICTS.find(([re]) => re.test(e.message))
  return c ? i18n._(c[1]) : fallback
}

type ChargeType = S['Charge']['charge_type']

export const CHARGE_TYPES: ChargeType[] = [
  'dues',
  'special_assessment',
  'reserve_contribution',
  'utility',
  'booking_fee',
  'legal_cost',
  'deposit',
  'adjustment',
  'settlement',
]

/** The default payer of a charge type (M1): dues are the tenant's, capital works the owner's. */
export function defaultDebtorFor(type: string): 'owner' | 'tenant' {
  return type === 'dues' || type === 'utility' ? 'tenant' : 'owner'
}

/** Translated labels for server-defined enums; unknown ids fall back to the raw string. */
export function useLabels() {
  const { t } = useLingui()
  const chargeType: Record<string, string> = {
    dues: t`Dues (aidat)`,
    special_assessment: t`Special assessment`,
    reserve_contribution: t`Reserve contribution`,
    utility: t`Utility`,
    booking_fee: t`Booking fee`,
    legal_cost: t`Legal cost`,
    deposit: t`Deposit`,
    adjustment: t`Adjustment`,
    settlement: t`Settlement`,
  }
  const frequency: Record<string, string> = {
    monthly: t`Monthly`,
    quarterly: t`Quarterly`,
    yearly: t`Yearly`,
    once: t`Once`,
  }
  const keyKind: Record<string, string> = {
    share: t`Land share (arsa payı)`,
    equal: t`Equal per unit`,
    area: t`By area`,
    table: t`Fixed table`,
  }
  const method: Record<string, string> = {
    cash: t`Cash`,
    bank_transfer: t`Bank transfer`,
    card: t`Card`,
  }
  const stmtKind: Record<string, string> = {
    opening: t`Opening balance`,
    charge: t`Charge`,
    payment: t`Payment`,
    late_fee: t`Late fee`,
    credit: t`Credit`,
    refund: t`Refund`,
    adjustment: t`Adjustment`,
    reversal: t`Reversal`,
  }
  const openingKind: Record<string, string> = {
    unit_receivable: t`Unit receivable`,
    bank: t`Bank balance`,
    cash: t`Cash balance`,
    vendor: t`Vendor payable`,
  }
  const channel: Record<string, string> = {
    in_app: t`In app`,
    email: t`Email`,
    sms: t`SMS`,
    letter: t`Letter`,
  }
  const side = (s: string | null | undefined) =>
    s === 'tenant' ? t`Tenant` : s === 'owner' ? t`Owner` : ''
  return { chargeType, frequency, keyKind, method, stmtKind, openingKind, channel, side }
}
