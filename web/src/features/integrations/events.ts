import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

/**
 * Webhook event types a receiver can subscribe to (docs/integrations.md, "Events"). The API's list
 * is an open set without an enum; an event this build does not know is shown by its raw type.
 * `ping` is sent by the test operation and cannot be subscribed to.
 */
export const WEBHOOK_EVENTS: { type: string; label: MessageDescriptor }[] = [
  { type: 'payment.recorded', label: msg`A payment is recorded` },
  { type: 'payment.confirmed', label: msg`A declared payment is confirmed` },
  { type: 'charge.created', label: msg`A one-off charge is created` },
  { type: 'charges.generated', label: msg`A charge plan billed a period` },
  { type: 'ticket.created', label: msg`A ticket is opened` },
  { type: 'ticket.status_changed', label: msg`A ticket changes status` },
  { type: 'announcement.published', label: msg`An announcement is published` },
  { type: 'meeting.called', label: msg`A meeting is called` },
  { type: 'decision.applied', label: msg`A decision is applied` },
  { type: 'visitor.approved', label: msg`A visitor request is approved` },
  { type: 'delivery.received', label: msg`A parcel is logged at the gate` },
]

export const KNOWN_EVENT_TYPES = new Set(WEBHOOK_EVENTS.map((e) => e.type))
