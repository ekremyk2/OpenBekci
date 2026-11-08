package ledger

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NotificationJobKind is the River job kind of every money notification. The
// notifications module consumes these jobs and decides channels and wording.
const NotificationJobKind = "notifications.money"

// Notification types emitted by the money modules.
const (
	NotifyPaymentConfirmed = "payment.confirmed" // payload: payment_id, unit_id, debtor_kind, person_id, amount, receipt_no
	NotifyPaymentRejected  = "payment.rejected"  // payload: payment_id, unit_id, debtor_kind, person_id, amount, reason
	NotifyDunningReminder  = "dunning.reminder"  // payload: unit_id, debtor_kind, person_id, stage_id, stage, channel, amount_due
	NotifyStatementSend    = "statement.send"    // payload: debtor, from, to, channel, file_id
	NotifyReceiptSend      = "receipt.send"      // payload: payment_id, channel
)

// Notification is one event for the notifications module.
type Notification struct {
	Type    string          `json:"type"`
	OrgID   uuid.UUID       `json:"org_id"`
	SiteID  uuid.UUID       `json:"site_id"`
	Payload json.RawMessage `json:"payload"`
}

// Notifier enqueues a notification in the business transaction (River InsertTx,
// one job per event, so the job exists exactly when the change committed).
type Notifier interface {
	Enqueue(ctx context.Context, tx pgx.Tx, n Notification) error
}
