import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

const JOIN: Record<string, MessageDescriptor> = {
  no_account: msg`No account`,
  invited: msg`Invited`,
  joined: msg`Joined`,
}
const SIDE: Record<string, MessageDescriptor> = { owner: msg`Owner side`, tenant: msg`Tenant side` }
const SCOPE: Record<string, MessageDescriptor> = {
  org: msg`Whole organization`,
  site: msg`Site`,
  block: msg`Block`,
}
const OPEN_CHARGES: Record<string, MessageDescriptor> = {
  carry_to_person: msg`Carry them to the leaving person's own account`,
  keep_on_unit: msg`Keep them on the unit for the next owner or tenant`,
  write_off: msg`Write them off`,
}
const INVITE_STATUS: Record<string, MessageDescriptor> = {
  pending: msg`Waiting`,
  accepted: msg`Joined`,
  expired: msg`Expired`,
  revoked: msg`Revoked`,
}
const CLAIM_STATUS: Record<string, MessageDescriptor> = {
  pending: msg`Waiting`,
  approved: msg`Approved`,
  rejected: msg`Rejected`,
}

type Translate = { _: (d: MessageDescriptor) => string }
const pick = (m: Record<string, MessageDescriptor>, key: string, i18n: Translate) => {
  const d = m[key]
  return d ? i18n._(d) : key
}

export const joinStatusLabel = (s: string, i18n: Translate) => pick(JOIN, s, i18n)
export const sideLabel = (s: string, i18n: Translate) => pick(SIDE, s, i18n)
export const scopeLabel = (s: string, i18n: Translate) => pick(SCOPE, s, i18n)
export const openChargesLabel = (s: string, i18n: Translate) => pick(OPEN_CHARGES, s, i18n)
export const inviteStatusLabel = (s: string, i18n: Translate) => pick(INVITE_STATUS, s, i18n)
export const claimStatusLabel = (s: string, i18n: Translate) => pick(CLAIM_STATUS, s, i18n)

export function joinStatusVariant(s: string): 'success' | 'info' | 'secondary' {
  return s === 'joined' ? 'success' : s === 'invited' ? 'info' : 'secondary'
}
