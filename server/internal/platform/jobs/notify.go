package jobs

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Notify is the job that sends one notification to a set of people through
// the channels the sender chose (decision O5). Modules enqueue it in their own
// transaction (river InsertTx), so a notification exists exactly when the change
// that caused it committed. The operations module owns the dispatcher that
// consumes it; until it is installed the default worker below only logs.
//
// MERGE NOTE: the operations module (D2) defines the same struct under the same
// name and fields in this package; keep one definition and its worker.
type Notify struct {
	OrgID  uuid.UUID  `json:"org_id"`
	SiteID *uuid.UUID `json:"site_id,omitempty"`
	// Topic names the cause, for example governance.call, governance.decisions,
	// governance.term_expiry. It selects the template and the in-app category.
	Topic     string      `json:"topic"`
	PersonIDs []uuid.UUID `json:"person_ids"`
	// Channels is a subset of in_app, email, sms.
	Channels []string `json:"channels"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	// RefType and RefID point at the record the notification is about.
	RefType string    `json:"ref_type,omitempty"`
	RefID   uuid.UUID `json:"ref_id,omitempty"`
}

// Kind implements river.JobArgs.
func (Notify) Kind() string { return "notify.send" }

type notifyLogWorker struct {
	river.WorkerDefaults[Notify]
	log *slog.Logger
}

func (w *notifyLogWorker) Work(ctx context.Context, job *river.Job[Notify]) error {
	w.log.InfoContext(ctx, "notify.send (no dispatcher installed)", "topic", job.Args.Topic, "people", len(job.Args.PersonIDs), "channels", job.Args.Channels)
	return nil
}
