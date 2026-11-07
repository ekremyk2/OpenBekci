import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

/** Notification types the app knows by name. The set is open: others show their raw id. */
export const NOTIFICATION_TYPES: Record<string, MessageDescriptor> = {
  'payment.reminder': msg`Dues reminders`,
  'payment.receipt': msg`Payment receipts`,
  'payment.confirmed': msg`Payment confirmed`,
  'payment.rejected': msg`Payment rejected`,
  'ticket.update': msg`Ticket updates`,
  'announcement.published': msg`New announcements`,
  'meeting.called': msg`Meeting notices`,
  'auth.sign_in_code': msg`Sign-in codes`,
  'auth.invitation': msg`Invitations`,
}

type Translate = { _: (d: MessageDescriptor) => string }

export function notificationTypeLabel(type: string, translate: Translate): string {
  const d = NOTIFICATION_TYPES[type]
  return d ? translate._(d) : type
}

/** Ordered list for the preference screens: known types first, then any the server adds. */
export function orderedTypes(extra: readonly string[] = []): string[] {
  const known = Object.keys(NOTIFICATION_TYPES)
  return [...known, ...extra.filter((t) => !known.includes(t))]
}
