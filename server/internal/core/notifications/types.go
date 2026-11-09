// Package notifications is the notification dispatcher (backend plan 4.12 and
// section 12, decision O5): the notifications.deliver job turns a
// jobs.NotifyArgs into in-app rows, emails and SMS; the API lists a person's
// notifications, their mutes and a site's channel defaults.
package notifications

import (
	"slices"

	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// Notification types (the NotificationType vocabulary of the spec).
const (
	TypeAnnouncementPublished = "announcement.published"
	TypeChargeDueSoon         = "charge.due_soon"
	TypeChargeOverdue         = "charge.overdue"
	TypeDunningReminder       = "dunning.reminder"
	TypePaymentReceipt        = "payment.receipt"
	TypePaymentDeclared       = "payment.declared"
	TypePaymentConfirmed      = "payment.confirmed"
	TypePaymentRejected       = "payment.rejected"
	TypeStatementSent         = "statement.sent"
	TypeTicketUpdated         = "ticket.updated"
	TypeTicketCommented       = "ticket.commented"
	TypeClaimRequested        = "claim.requested"
	TypeClaimDecided          = "claim.decided"
	TypeMeetingCalled         = "meeting.called"
	TypeMeetingDecisions      = "meeting.decisions"
	TypeAssemblyDue           = "meeting.assembly_due"
	TypeTermExpiring          = "term.expiring"
	TypeVisitorApproval       = "visitor.approval_requested"
	TypeVisitorArrived        = "visitor.arrived"
	TypeBookingConfirmed      = "booking.confirmed"
	TypeBookingCancelled      = "booking.cancelled"
	TypeDeliveryReceived      = "delivery.received"
	TypeDeliveryWaiting       = "delivery.waiting"
	TypeVisitorQRRotated      = "visitor.qr_rotated"
	TypeAgentSilent           = "access.agent_silent"
	TypeAgentClockSkew        = "access.agent_clock_skew"
	TypeAgentStopped          = "access.agent_stopped"
	// TypeWebhookFailing tells the organization's administrators that an
	// outbound webhook endpoint keeps failing (integrations module).
	TypeWebhookFailing = "webhook.endpoint_failing"
	// TypeDataExportReady tells a person (and the manager who asked) that the
	// export of their data is filed in the documents (privacy module).
	TypeDataExportReady = "privacy.export_ready"

	// Always delivered, cannot be muted, channels fixed (SMS when the person has
	// a phone, otherwise email). The identity and people modules send these
	// directly; the types are listed so clients can label them.
	TypeSignInCode = "auth.sign_in_code"
	TypeInvitation = "invitation.sent"
)

// Vocabulary lists the types a person may mute or a site may configure, in
// display order.
var Vocabulary = []string{
	TypeAnnouncementPublished, TypeChargeDueSoon, TypeChargeOverdue, TypeDunningReminder, TypePaymentReceipt, TypePaymentDeclared,
	TypePaymentConfirmed, TypePaymentRejected, TypeStatementSent, TypeTicketUpdated, TypeTicketCommented,
	TypeClaimRequested, TypeClaimDecided, TypeMeetingCalled, TypeMeetingDecisions, TypeAssemblyDue, TypeTermExpiring,
	TypeVisitorApproval, TypeVisitorArrived, TypeBookingConfirmed, TypeBookingCancelled, TypeDeliveryReceived, TypeDeliveryWaiting, TypeVisitorQRRotated,
	TypeAgentSilent, TypeAgentClockSkew, TypeAgentStopped, TypeWebhookFailing, TypeDataExportReady,
}

// Unmutable are the types no preference and no site default can change.
var Unmutable = []string{TypeSignInCode, TypeInvitation}

// IsUnmutable reports whether t is sign-in or invitation.
func IsUnmutable(t string) bool { return slices.Contains(Unmutable, t) }

// Known reports whether t is a vocabulary or unmutable type.
func Known(t string) bool { return slices.Contains(Vocabulary, t) || IsUnmutable(t) }

var validChannels = []string{jobs.ChannelInApp, jobs.ChannelEmail, jobs.ChannelSMS, jobs.ChannelPush}

// ValidChannel reports whether c is a channel name.
func ValidChannel(c string) bool { return slices.Contains(validChannels, c) }

// DefaultChannels is the built-in channel set of an automatic message (the O5
// table): dues reminders and payment receipts in-app plus SMS (the site pays
// for them); statements and payment confirmations or rejections, ticket
// updates and comments in-app plus email; everything else in-app only. SMS is
// off by default for all the rest; a site turns it on per type
// (site_channel_default). Sign-in codes and invitations are not in this table:
// they go to one address (see UnmutableChannel).
func DefaultChannels(t string) []string {
	switch t {
	case TypeChargeDueSoon, TypeChargeOverdue, TypeDunningReminder, TypePaymentReceipt:
		return []string{jobs.ChannelInApp, jobs.ChannelSMS}
	case TypeStatementSent, TypePaymentConfirmed, TypePaymentRejected, TypeTicketUpdated, TypeTicketCommented, TypeDataExportReady:
		return []string{jobs.ChannelInApp, jobs.ChannelEmail}
	case TypeSignInCode, TypeInvitation:
		return []string{jobs.ChannelSMS, jobs.ChannelEmail} // candidates; UnmutableChannel picks one
	}
	return []string{jobs.ChannelInApp}
}

// UnmutableChannel is the one channel a sign-in code or an invitation takes
// (O5): SMS when the person has a phone, otherwise email, never both.
func UnmutableChannel(hasPhone bool) string {
	if hasPhone {
		return jobs.ChannelSMS
	}
	return jobs.ChannelEmail
}

// NormalizeChannels drops duplicates and unknown names and orders the rest.
func NormalizeChannels(in []string) []string {
	var out []string
	for _, c := range validChannels {
		if slices.Contains(in, c) {
			out = append(out, c)
		}
	}
	return out
}
