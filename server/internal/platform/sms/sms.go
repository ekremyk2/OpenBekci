// Package sms is the SMS gateway abstraction (backend plan, section 12).
// Drivers: netgsm (production, Turkey) and log (development and tests).
// Packs add providers in 0.4 through the same interface.
package sms

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Gateway sends one text message.
type Gateway interface {
	// Send delivers text to the E.164 number `to` and returns the provider's
	// message id (for delivery reports).
	Send(ctx context.Context, to, text string) (providerID string, err error)
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
