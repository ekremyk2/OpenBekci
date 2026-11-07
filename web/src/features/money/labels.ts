import { useLingui } from '@lingui/react/macro'
import type { S } from './api'

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
  const channel: Record<string, string> = {
    in_app: t`In app`,
    email: t`Email`,
    sms: t`SMS`,
    letter: t`Letter`,
  }
  const side = (s: string | null | undefined) =>
    s === 'tenant' ? t`Tenant` : s === 'owner' ? t`Owner` : ''
  return { chargeType, frequency, keyKind, method, stmtKind, channel, side }
}
