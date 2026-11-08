import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

/*
 * User-facing labels for server-defined ids. The server's enums are open (web plan §4): an id this
 * build does not know falls back to the raw string instead of breaking the screen.
 */

const ROLE_LABELS: Record<string, MessageDescriptor> = {
  org_admin: msg`Organization admin`,
  site_manager: msg`Site manager`,
  board_member: msg`Board member`,
  auditor: msg`Auditor`,
  accountant: msg`Accountant`,
  staff: msg`Staff`,
  guard: msg`Guard`,
}

const OCCUPANCY_KIND_LABELS: Record<string, MessageDescriptor> = {
  owner: msg`Owner`,
  tenant: msg`Tenant`,
  household: msg`Household member`,
}

const PROBLEM_MESSAGES: Record<string, MessageDescriptor> = {
  idempotency_mismatch: msg`This request was already sent with different details. Reload the form and try again.`,
  locked_period: msg`This accounting period is closed and can no longer be changed.`,
  version_conflict: msg`Someone else changed this record. Reload to see the latest version.`,
  insufficient_permission: msg`You do not have permission to do this.`,
  rate_limited: msg`Too many requests. Please wait a moment and try again.`,
}

type Translate = { _: (descriptor: MessageDescriptor) => string }

/** Pass the `i18n` object from `useLingui()`. */
export function roleLabel(role: string, translate: Translate): string {
  const d = ROLE_LABELS[role]
  return d ? translate._(d) : role
}

export function occupancyKindLabel(kind: string, translate: Translate): string {
  const d = OCCUPANCY_KIND_LABELS[kind]
  return d ? translate._(d) : kind
}

/** A user-facing message for a problem `code`, or undefined when there is no dedicated text. */
export function problemMessage(code: string, translate: Translate): string | undefined {
  const d = PROBLEM_MESSAGES[code]
  return d ? translate._(d) : undefined
}
