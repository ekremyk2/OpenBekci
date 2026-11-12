import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

/** Audit actions the server writes, as `<area>.<verb>`. The set is open: others are humanized. */
export const AUDIT_ACTIONS: Record<string, MessageDescriptor> = {
  'privacy.consent_accepted': msg`Consent accepted`,
  'privacy.export_request': msg`Data export requested`,
  'unit.import_create': msg`Unit imported`,
  'files.generate': msg`File generated`,
  'files.attach': msg`File attached`,
  'person.create': msg`Person added`,
  'person.updated': msg`Person changed`,
  'occupancy.create': msg`Occupancy added`,
  'occupancy.move_out': msg`Move-out recorded`,
  'move_out.charges': msg`Move-out charges settled`,
  'governance.settings_update': msg`Governance settings changed`,
  'gate.session_start': msg`Guard signed in`,
  'gate.pin_set': msg`Guard PIN set`,
  'gate.pin_failed': msg`Guard PIN failed`,
  'poll.answer': msg`Poll answered`,
  'payment.declare': msg`Payment declared`,
  'payment.record': msg`Payment recorded`,
  'payment.confirm': msg`Payment confirmed`,
  'site.rotate_join_code': msg`Join code changed`,
  'site.update': msg`Site changed`,
  'pack.update_settings': msg`Pack settings changed`,
  'documents.update': msg`Document changed`,
  'documents.generated': msg`Document generated`,
  'invitation.accept': msg`Invitation accepted`,
  'invitation.create': msg`Invitation sent`,
  'tickets.create': msg`Ticket opened`,
  'ticket.transitioned': msg`Ticket status changed`,
  'charge.generate': msg`Charges generated`,
  'announcements.create': msg`Announcement drafted`,
  'announcements.publish': msg`Announcement published`,
  'announcement.published': msg`Announcement published`,
  'announcements.archive': msg`Announcement archived`,
  'booking.create': msg`Booking made`,
  'membership.create': msg`Membership added`,
  'agenda_item.create': msg`Agenda item added`,
  'meeting.close': msg`Meeting closed`,
  'meeting.open_first_call': msg`First call opened`,
  'visitor.cancel': msg`Visitor cancelled`,
  'visitor.rotate_qr': msg`Visitor QR changed`,
  'recurring_visitor.create': msg`Recurring visitor added`,
  'late_fee_policy.set': msg`Late-fee policy set`,
  'late_fee_policy.threshold': msg`Late-fee threshold set`,
  'delivery.receive': msg`Delivery received`,
}

export const AUDIT_TARGETS: Record<string, MessageDescriptor> = {
  file: msg`File`,
  user: msg`User`,
  unit: msg`Unit`,
  person: msg`Person`,
  invitation: msg`Invitation`,
  agenda_item: msg`Agenda item`,
  payment: msg`Payment`,
  occupancy: msg`Occupancy`,
  document: msg`Document`,
  governance_settings: msg`Governance settings`,
  guard_session: msg`Guard session`,
  announcement: msg`Announcement`,
  pack: msg`Pack`,
  meeting: msg`Meeting`,
  charge_plan: msg`Charge plan`,
  charge: msg`Charge`,
  visitor_entry: msg`Visitor`,
  ticket: msg`Ticket`,
  move_out: msg`Move-out`,
  late_fee_policy: msg`Late-fee policy`,
  booking: msg`Booking`,
  membership: msg`Membership`,
  site: msg`Site`,
  recurring_visitor: msg`Recurring visitor`,
  delivery: msg`Delivery`,
  facility: msg`Facility`,
  cash_box: msg`Cash box`,
  import_batch: msg`Import`,
}

type Translate = { _: (d: MessageDescriptor) => string }

/** `unit.import_create` becomes "Unit import create" when no label is known. */
function humanize(id: string): string {
  const s = id.replace(/[._]+/g, ' ').trim()
  return s.charAt(0).toUpperCase() + s.slice(1)
}

export function auditActionLabel(action: string, translate: Translate): string {
  const d = AUDIT_ACTIONS[action]
  return d ? translate._(d) : humanize(action)
}

export function auditTargetLabel(target: string, translate: Translate): string {
  const d = AUDIT_TARGETS[target]
  return d ? translate._(d) : humanize(target)
}
