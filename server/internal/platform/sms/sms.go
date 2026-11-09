// Package sms is the SMS gateway abstraction (backend plan, section 12).
// Drivers: netgsm (production, Turkey) and log (development and tests).
// Packs add providers in 0.4 through the same interface.
package sms

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// Gateway sends one text message.
type Gateway interface {
	// Send delivers text to the E.164 number `to` and returns the provider's
	// message id (for delivery reports).
	Send(ctx context.Context, to, text string) (providerID string, err error)
}

type orgKey struct{}

// WithOrg returns ctx carrying the organization a message is sent for. Callers
// that know the organization set it so that Routed can pick the provider the
// organization chose; sign-in codes, which have no organization, use the
// instance gateway.
func WithOrg(ctx context.Context, org uuid.UUID) context.Context {
	return context.WithValue(ctx, orgKey{}, org)
}

// OrgFrom returns the organization set by WithOrg.
func OrgFrom(ctx context.Context) (uuid.UUID, bool) {
	o, ok := ctx.Value(orgKey{}).(uuid.UUID)
	return o, ok && o != uuid.Nil
}

// Picker chooses a gateway for an organization, for example one a pack
// provides and the organization selected; false means "use the default".
type Picker interface {
	Pick(ctx context.Context, org uuid.UUID) (Gateway, bool)
}

// Routed is the gateway the server wires when packs exist: it sends through
// the organization's chosen provider when the Picker names one, else through
// Default.
type Routed struct {
	Default Gateway
	Picker  Picker
}

// Send implements Gateway.
func (r Routed) Send(ctx context.Context, to, text string) (string, error) {
	if org, ok := OrgFrom(ctx); ok && r.Picker != nil {
		if g, ok := r.Picker.Pick(ctx, org); ok {
			return g.Send(ctx, to, text)
		}
	}
	return r.Default.Send(ctx, to, text)
}

// Errors drivers map provider failures to, so callers can react (alert the
// operator) without parsing provider codes.
var (
	ErrCredentials = errors.New("sms: provider rejected the credentials")
	ErrSender      = errors.New("sms: sender id (message header) is not allowed")
	ErrRejected    = errors.New("sms: provider rejected the message")
	ErrQuota       = errors.New("sms: provider quota or send limit exceeded")
	ErrUnavailable = errors.New("sms: provider unavailable")
)

// Log is the development driver: it writes the message to the log instead of
// sending it. Never use it in production, the log then holds sign-in codes.
type Log struct{ Log *slog.Logger }

// Send implements Gateway.
func (l Log) Send(ctx context.Context, to, text string) (string, error) {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	log.InfoContext(ctx, "sms (log driver, not sent)", "to", to, "text", text)
	return "log", nil
}

// Message is a captured message.
type Message struct{ To, Text string }

// Recorder is a Gateway that keeps what it was asked to send; for tests.
type Recorder struct {
	mu   sync.Mutex
	msgs []Message
	// Err, when set, is returned by Send instead of recording.
	Err error
}

// Send implements Gateway.
func (r *Recorder) Send(_ context.Context, to, text string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return "", r.Err
	}
	r.msgs = append(r.msgs, Message{To: to, Text: text})
	return "recorded", nil
}

// Messages returns a copy of the recorded messages.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.msgs...)
}

// Last returns the most recent message, or false if none.
func (r *Recorder) Last() (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return Message{}, false
	}
	return r.msgs[len(r.msgs)-1], true
}
