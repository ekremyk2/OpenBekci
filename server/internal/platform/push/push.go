// Package push sends content-free push messages (backend plan section 12,
// infra plan section 4). A message carries only the allowlisted keys of
// Payload: never a title, a body or a name. FCM and APNs tokens go through a
// relay (an HTTP service that holds the store credentials; protocol in the
// infra plan); UnifiedPush endpoints are posted to directly.
package push

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// Push kinds of a device.
const (
	KindFCM         = "fcm"
	KindAPNs        = "apns"
	KindUnifiedPush = "unifiedpush"
)

// Payload is the whole content of a push message. These six keys are the
// allowlist of backend section 12; the relay rejects anything else.
type Payload struct {
	InstanceID     string `json:"instance_id"`
	OrgID          string `json:"org_id"`
	NotificationID string `json:"notification_id"`
	Type           string `json:"type"`
	CollapseKey    string `json:"collapse_key"`
	Badge          int    `json:"badge"`
}

// Message is one push to one device.
type Message struct {
	// Kind is KindFCM, KindAPNs or KindUnifiedPush.
	Kind string
	// Token is the FCM or APNs device token, or the UnifiedPush endpoint URL.
	Token string
	// RelayID names the relay for fcm and apns; ignored for unifiedpush.
	RelayID string
	Data    Payload
}

// Outcome is what happened to one message.
type Outcome string

// Outcomes. InvalidToken means the device is gone and must be disabled;
// RateLimited and Error are worth a retry; NoRoute (no relay configured for
// the device's relay id, or push disabled) is not.
const (
	OK           Outcome = "ok"
	InvalidToken Outcome = "invalid_token"
	RateLimited  Outcome = "rate_limited"
	Error        Outcome = "error"
	NoRoute      Outcome = "no_route"
	// Rejected is a non-retryable error (the relay refused the message for good:
	// bad field, wrong app, disabled server). It is logged and dropped.
	Rejected Outcome = "rejected"
)

// Result is the outcome of the message at the same index.
type Result struct {
	Outcome Outcome
	Detail  string
}

// Retryable reports whether sending the message again may succeed.
func (r Result) Retryable() bool { return r.Outcome == RateLimited || r.Outcome == Error }

// Sender sends messages and returns one Result per message, in order. A
// non-nil error means nothing is known about any message (treated as Error
// for all of them).
type Sender interface {
	Send(ctx context.Context, msgs []Message) ([]Result, error)
}

// ErrNoRoute is returned (inside Results) detail text for an unknown relay.
var ErrNoRoute = errors.New("push: no relay configured for this relay id")

// Log is the development sender: it writes the payload to the log and reports
// every message as delivered. Tokens are not logged.
type Log struct{ Log *slog.Logger }

// Send implements Sender.
func (l Log) Send(ctx context.Context, msgs []Message) ([]Result, error) {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	out := make([]Result, len(msgs))
	for i, m := range msgs {
		log.InfoContext(ctx, "push (log driver, not sent)", "kind", m.Kind, "relay_id", m.RelayID,
			"org_id", m.Data.OrgID, "notification_id", m.Data.NotificationID, "type", m.Data.Type, "badge", m.Data.Badge)
		out[i] = Result{Outcome: OK}
	}
	return out, nil
}

// GenericTitleKey is the key of the app's generic per-type title for APNs
// ("ticket.updated" becomes "push.ticket_updated").
func GenericTitleKey(t string) string {
	return "push." + strings.NewReplacer(".", "_", "-", "_").Replace(t)
}
