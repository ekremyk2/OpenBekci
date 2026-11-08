package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
)

// Event types a webhook endpoint can subscribe to. The payload of each carries
// ids only (see Event.IDs), never records: the receiver reads the API with its
// own key. EventPing is sent by the test operation and cannot be subscribed to.
const (
	EventPaymentRecorded     = "payment.recorded"
	EventPaymentConfirmed    = "payment.confirmed"
	EventChargeCreated       = "charge.created"
	EventChargesGenerated    = "charges.generated"
	EventTicketCreated       = "ticket.created"
	EventTicketStatusChanged = "ticket.status_changed"
	EventAnnouncementPublish = "announcement.published"
	EventMeetingCalled       = "meeting.called"
	EventDecisionApplied     = "decision.applied"
	EventVisitorApproved     = "visitor.approved"
	EventDeliveryReceived    = "delivery.received"
	EventPing                = "ping"
)

// Request headers of a delivery and the replay tolerance receivers should apply.
const (
	HeaderSignature  = "OpenBekci-Signature"
	HeaderDeliveryID = "OpenBekci-Delivery-Id"
	HeaderEvent      = "OpenBekci-Event"
	// SignatureTolerance is the age (or clock skew) beyond which a receiver
	// should reject a signature as a replay.
	SignatureTolerance = 5 * time.Minute
)

const (
	eventVersion         = 1
	maxEventsPerEndpoint = 50
	secretPrefix         = "whsec_"
	secretCipherPurpose  = "integrations.webhook_secret"
)

// EventTypes lists the subscribable event types in display order.
func EventTypes() []string {
	return []string{
		EventPaymentRecorded, EventPaymentConfirmed, EventChargeCreated, EventChargesGenerated, EventTicketCreated, EventTicketStatusChanged,
		EventAnnouncementPublish, EventMeetingCalled, EventDecisionApplied, EventVisitorApproved, EventDeliveryReceived,
	}
}

// Event is the core event a domain action emits: it is stored as the body of
// one delivery per subscribed endpoint. The JSON form is the webhook payload.
type Event struct {
	// ID identifies the event; deliveries of the same event to the same
	// endpoint are deduplicated on it. The audit event's id for emitted events.
	ID         uuid.UUID `json:"id"`
	Type       string    `json:"type"`
	Version    int       `json:"version"`
	OrgID      uuid.UUID `json:"org_id"`
	OccurredAt time.Time `json:"occurred_at"`
	// IDs names what the event is about, as <thing>_id: UUID. Ids only.
	IDs map[string]uuid.UUID `json:"ids"`
	// Period and ChargeIDs belong to charges.generated only: the billing period
	// ("2026-11") and the ids of the charges the plan created for it.
	Period    string      `json:"period,omitempty"`
	ChargeIDs []uuid.UUID `json:"charge_ids,omitempty"`
}

// Sign returns the OpenBekci-Signature header value for body sent at time t:
// "t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>".
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// VerifySignature checks a header produced by Sign: the v1 tag must match and
// the timestamp must be within tolerance of now (replay protection; the
// documented tolerance is SignatureTolerance, five minutes). Receivers do the
// same; this is the reference implementation the tests and docs follow.
func VerifySignature(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var ts string
	var tags []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		switch {
		case !ok:
		case k == "t":
			ts = v
		case k == "v1":
			tags = append(tags, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(tags) == 0 {
		return fmt.Errorf("malformed %s header", HeaderSignature)
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("timestamp outside the %s tolerance", tolerance)
	}
	want := Sign(secret, time.Unix(sec, 0), body)
	_, wantTag, _ := strings.Cut(want, ",v1=")
	for _, tag := range tags {
		if hmac.Equal([]byte(tag), []byte(wantTag)) {
			return nil
		}
	}
	return fmt.Errorf("signature mismatch")
}

// fromAudit maps an audited domain action to a webhook event, or reports that
// the action emits none. It reads ids from the audit record and, for two
// events, one extra id from the database; business logic stays in the domain
// services, which already audit these actions.
func fromAudit(ctx context.Context, tx pgx.Tx, e audit.Event) (Event, bool, error) {
	typ := ""
	idKey := e.TargetType + "_id"
	switch e.Action {
	case "payment.record":
		typ = EventPaymentRecorded
	case "payment.confirm":
		typ = EventPaymentConfirmed
	case "charge.create":
		typ = EventChargeCreated
	case "charge.generate":
		typ = EventChargesGenerated
		idKey = "plan_id"
	case "tickets.create":
		typ = EventTicketCreated
	case "tickets.transition":
		typ = EventTicketStatusChanged
	case "announcements.publish":
		typ = EventAnnouncementPublish
	case "meeting.call":
		typ = EventMeetingCalled
	case "decision.apply":
		typ = EventDecisionApplied
	case "delivery.receive":
		typ = EventDeliveryReceived
	case "visitor.decide":
		if c, ok := e.Diff["status"]; !ok || strings.Trim(string(c.After), `"`) != "approved" {
			return Event{}, false, nil
		}
		typ = EventVisitorApproved
		idKey = "visitor_approval_request_id"
	default:
		return Event{}, false, nil
	}
	ev := Event{ID: e.ID, Type: typ, Version: eventVersion, OrgID: e.OrgID, OccurredAt: time.Now().UTC(),
		IDs: map[string]uuid.UUID{idKey: e.TargetID}}
	if e.SiteID != nil {
		ev.IDs["site_id"] = *e.SiteID
	}
	switch typ {
	case EventChargesGenerated:
		if c, ok := e.Diff["period"]; ok {
			ev.Period = strings.Trim(string(c.After), `"`)
		}
		rows, err := tx.Query(ctx, `SELECT id FROM charge WHERE org_id = $1 AND plan_id = $2 AND period = $3 AND kind = 'principal' ORDER BY id`, e.OrgID, e.TargetID, ev.Period)
		if err != nil {
			return Event{}, false, err
		}
		for rows.Next() {
			var cid uuid.UUID
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return Event{}, false, err
			}
			ev.ChargeIDs = append(ev.ChargeIDs, cid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Event{}, false, err
		}
	case EventVisitorApproved:
		var entry uuid.UUID
		err := tx.QueryRow(ctx, `SELECT entry_id FROM visitor_approval_request WHERE org_id = $1 AND id = $2`, e.OrgID, e.TargetID).Scan(&entry)
		if err == nil {
			ev.IDs["visitor_entry_id"] = entry
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return Event{}, false, err
		}
	case EventDeliveryReceived:
		if c, ok := e.Diff["unit_id"]; ok {
			if u, err := uuid.Parse(strings.Trim(string(c.After), `"`)); err == nil {
				ev.IDs["unit_id"] = u
			}
		}
	}
	return ev, true, nil
}

func (e Event) body() ([]byte, error) { return json.Marshal(e) }

func validEventTypes(types []string) bool {
	if len(types) == 0 || len(types) > maxEventsPerEndpoint {
		return false
	}
	known := EventTypes()
	seen := map[string]bool{}
	for _, t := range types {
		if !slices.Contains(known, t) || seen[t] {
			return false
		}
		seen[t] = true
	}
	return true
}
