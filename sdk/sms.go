package sdk

import "context"

// SMSProvider sends text messages. The core SMS gateway picks a provider from
// the core drivers and the providers of the packs enabled for the
// organization (the organization setting sms_provider names it).
//
// Core call sites: the core sms gateway (sign-in codes of members, invitations,
// notification delivery). Map failures to the errors of this package
// (ErrCredentials, ErrRejected, ErrUnavailable) so the core can alert and retry.
type SMSProvider interface {
	// ID is the provider id ("<packid>" or "<packid>.<name>").
	ID() string
	// Send delivers text to the E.164 number to and returns the provider's
	// message id (for delivery reports).
	Send(ctx context.Context, to, text string) (providerID string, err error)
}
