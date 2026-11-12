import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

/** Notification types the app knows by name. The set is open: others show their raw id. */
export const NOTIFICATION_TYPES: Record<string, MessageDescriptor> = {
  'payment.reminder': msg`Dues reminders`,
  'payment.receipt': msg`Payment receipts`,
  'payment.confirmed': msg`Payment confirmed`,
  'payment.rejected': msg`Payment rejected`,
  'ticket.update': msg`Ticket updates`,
  'ticket.updated': msg`Ticket updates`,
  'ticket.commented': msg`Ticket comments`,
  'charge.due_soon': msg`Dues due soon`,
  'charge.overdue': msg`Overdue dues`,
  'dunning.reminder': msg`Payment reminders`,
  'payment.declared': msg`Payments declared`,
  'statement.sent': msg`Statements`,
  'claim.requested': msg`Claim requests`,
  'claim.decided': msg`Claim decisions`,
  'meeting.decisions': msg`Meeting decisions`,
  'meeting.assembly_due': msg`Assembly due`,
  'term.expiring': msg`Terms expiring`,
  'visitor.approval_requested': msg`Visitor approvals`,
  'visitor.arrived': msg`Visitor arrivals`,
  'visitor.qr_rotated': msg`Visitor QR changes`,
  'booking.confirmed': msg`Booking confirmations`,
  'booking.cancelled': msg`Booking cancellations`,
  'delivery.received': msg`Deliveries received`,
  'delivery.waiting': msg`Deliveries waiting`,
  'access.agent_silent': msg`Gate agent silent`,
  'access.agent_clock_skew': msg`Gate agent clock off`,
  'access.agent_stopped': msg`Gate agent stopped`,
  'webhook.endpoint_failing': msg`Webhook failing`,
  'privacy.export_ready': msg`Data export ready`,
  'invitation.sent': msg`Invitations`,
  'announcement.published': msg`New announcements`,
  'meeting.called': msg`Meeting notices`,
  'auth.sign_in_code': msg`Sign-in codes`,
  'auth.invitation': msg`Invitations`,
}

// Names the screens used before the server vocabulary settled: labelled, but not listed unless sent.
const LEGACY = new Set(['payment.reminder', 'ticket.update', 'auth.invitation'])

type Translate = { _: (d: MessageDescriptor) => string }

export function notificationTypeLabel(type: string, translate: Translate): string {
  const d = NOTIFICATION_TYPES[type]
  return d ? translate._(d) : type
}

/** Ordered list for the preference screens: known types first, then any the server adds. */
export function orderedTypes(extra: readonly string[] = []): string[] {
  const known = Object.keys(NOTIFICATION_TYPES).filter((k) => !LEGACY.has(k))
  return [...known, ...extra.filter((t) => !known.includes(t))]
}
