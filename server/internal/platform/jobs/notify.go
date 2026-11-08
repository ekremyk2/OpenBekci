package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// The notification job contract (backend plan sections 11 and 12). Every
// module that tells a person something (tickets, announcements, money,
// governance, community) inserts one NotifyArgs job in the transaction of the
// business change, through a Notifier. The notifications module consumes it:
// it resolves the recipients, applies mutes and the site's channel defaults,
// writes the in-app rows and sends email and SMS.
//
// MERGE NOTE: copied verbatim from the operations branch (D2), which owns the
// dispatcher; the definitions below are identical there, so the merge keeps
// that file.

// Channels of a notification.
const (
	ChannelInApp = "in_app"
	ChannelEmail = "email"
	ChannelSMS   = "sms"
	ChannelPush  = "push" // 0.4
)

// Recipient is one person to notify, named by person id or by user id (the
// sign-in account; resolved to the person of OrgID). Set exactly one.
type Recipient struct {
	PersonID *uuid.UUID `json:"person_id,omitempty"`
	UserID   *uuid.UUID `json:"user_id,omitempty"`
}

// NotifyArgs is the arguments of notifications.deliver.
type NotifyArgs struct {
	OrgID uuid.UUID `json:"org_id"`
	// SiteID is the site the message is about; it selects the site's channel
	// defaults and the SMS counter. Nil: the organization's built-in defaults.
	SiteID *uuid.UUID `json:"site_id,omitempty"`
	// Type is a value of the NotificationType vocabulary (ticket.updated,
	// payment.confirmed, announcement.published ...). It selects the template
	// and the default channels.
	Type       string      `json:"type"`
	Recipients []Recipient `json:"recipients"`
	// Payload feeds the templates and is returned with the in-app notification.
	// Keep it small and free of secrets.
	Payload map[string]any `json:"payload,omitempty"`
	// Link is the app path the notification opens (/tickets/<id>).
	Link string `json:"link,omitempty"`
	// Channels are chosen by the sender for authored messages (announcements,
	// manual reminders). Nil or empty: the site's defaults for Type (O5).
	Channels []string `json:"channels,omitempty"`
	// Locale forces a language; empty: the recipient's, else the organization's.
	Locale string `json:"locale,omitempty"`
	// Key makes redelivery idempotent: a retried job never duplicates a
	// notification. Notifier.Notify fills it when empty.
	Key string `json:"key"`
	// Unmutable ignores the recipients' mutes (sign-in codes, invitations).
	Unmutable bool `json:"unmutable,omitempty"`
}

// Kind implements river.JobArgs.
func (NotifyArgs) Kind() string { return "notifications.deliver" }

// InsertOpts implements river.JobArgsWithInsertOpts: a failed delivery is
// retried with River's backoff for a while.
func (NotifyArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 8} }

// Notifier enqueues notifications. Modules depend on this interface so they
// can be tested without a queue; tx is the business transaction, so the
// notification exists if and only if the change commits.
type Notifier interface {
	Notify(ctx context.Context, tx pgx.Tx, a NotifyArgs) error
}

// Deliverer runs a NotifyArgs job (*notifications.Service).
type Deliverer interface {
	Deliver(ctx context.Context, a NotifyArgs, attempt int) error
}

// QueueNotifier is the Notifier backed by the River client.
type QueueNotifier struct{ Client *Client }

// Notify implements Notifier.
func (q QueueNotifier) Notify(ctx context.Context, tx pgx.Tx, a NotifyArgs) error {
	if len(a.Recipients) == 0 {
		return nil
	}
	if a.Key == "" {
		a.Key = uuid.NewString()
	}
	if _, err := q.Client.InsertTx(ctx, tx, a, nil); err != nil {
		return fmt.Errorf("enqueue notification %s: %w", a.Type, err)
	}
	return nil
}

// DiscardNotifier drops notifications; for tests and for processes that only
// run the API without the queue.
type DiscardNotifier struct{}

// Notify implements Notifier.
func (DiscardNotifier) Notify(context.Context, pgx.Tx, NotifyArgs) error { return nil }

// notifyLogWorker is the default consumer of notifications.deliver until the
// notifications module (operations) registers the real dispatcher: it only logs.
type notifyLogWorker struct {
	river.WorkerDefaults[NotifyArgs]
	log *slog.Logger
}

func (w *notifyLogWorker) Work(ctx context.Context, job *river.Job[NotifyArgs]) error {
	w.log.InfoContext(ctx, "notifications.deliver (no dispatcher installed)", "type", job.Args.Type, "recipients", len(job.Args.Recipients), "channels", job.Args.Channels)
	return nil
}
