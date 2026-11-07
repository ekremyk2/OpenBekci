package jobs

import "github.com/google/uuid"

// Notify is the notification job: one message of a template to some people,
// delivered through their preferred channels. Modules insert it in the same
// transaction as the change that causes it (River is the outbox); the
// notifications worker (phase D) renders and sends it.
//
// MERGE NOTE: the operations module (D2) defines the same struct; keep one
// definition (field names and tags identical) when the branches meet.
type Notify struct {
	OrgID  uuid.UUID  `json:"org_id"`
	SiteID *uuid.UUID `json:"site_id,omitempty"`
	// PersonIDs are the recipients.
	PersonIDs []uuid.UUID `json:"person_ids"`
	// Template names the message, for example "visitor.approval_requested".
	Template string `json:"template"`
	// Data fills the template; it never holds more than the message shows.
	Data map[string]any `json:"data,omitempty"`
	// Channels limits delivery ("inapp", "sms", "email", "push"); empty means
	// each person's preferred channels.
	Channels []string `json:"channels,omitempty"`
	// DedupeKey makes a repeated insert of the same notification a no-op.
	DedupeKey string `json:"dedupe_key,omitempty"`
}

// Kind implements river.JobArgs.
func (Notify) Kind() string { return "notify" }
