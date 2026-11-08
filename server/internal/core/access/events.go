package access

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Event is an entry of the opening log as the API shows it.
type Event struct {
	ID               uuid.UUID  `json:"id"`
	OrgID            uuid.UUID  `json:"org_id"`
	SiteID           uuid.UUID  `json:"site_id"`
	PointID          uuid.UUID  `json:"point_id"`
	PointName        *string    `json:"point_name"`
	AgentID          *uuid.UUID `json:"agent_id"`
	CredentialID     *uuid.UUID `json:"credential_id"`
	Decision         string     `json:"decision"`
	Reason           string     `json:"reason"`
	PresentedKind    *string    `json:"presented_kind"`
	ObservedPlate    *string    `json:"observed_plate"`
	UnitDoorNumber   *string    `json:"unit_door_number"`
	ActorUserID      *uuid.UUID `json:"actor_user_id"`
	ActorDisplayName *string    `json:"actor_display_name"`
	CommandID        *uuid.UUID `json:"command_id"`
	VisitorEntryID   *uuid.UUID `json:"visitor_entry_id"`
	OccurredAt       time.Time  `json:"occurred_at"`
	SyncedAt         time.Time  `json:"synced_at"`
	SyncSeq          int64      `json:"sync_seq"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// EventFilter narrows ListEvents.
type EventFilter struct {
	PointID  *uuid.UUID
	Decision string
	Reason   string
	From, To *time.Time
	Plate    string
	After    *int64
}

// ListEvents lists the opening log of a site (permission access.view_events).
// Newest first; with After (a sync_seq, exclusive) it switches to feed order
// (oldest first) for pollers and the cursor continues the feed.
func (s *Service) ListEvents(ctx context.Context, orgID, siteID uuid.UUID, f EventFilter, limit int, cursor string) ([]Event, string, error) {
	if err := authz.RequireInOrg(ctx, PermViewEvents, orgID); err != nil {
		return nil, "", err
	}
	n := pageSize(limit)
	var cur *int64
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err == nil {
			var v int64
			if v, err = strconv.ParseInt(string(b), 10, 64); err == nil {
				cur = &v
			}
		}
		if err != nil {
			return nil, "", problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
		}
	}
	feed := f.After != nil
	from := f.After
	if feed && cur != nil {
		from = cur
	}
	plate := NormalizePlate(f.Plate)
	var out []Event
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if err := siteScope(ctx, tx, PermViewEvents, siteID); err != nil {
			return err
		}
		order, cmp := "DESC", "<"
		if feed {
			order, cmp = "ASC", ">"
		}
		var bound *int64
		if feed {
			bound = from
		} else {
			bound = cur
		}
		rows, err := tx.Query(ctx, `SELECT e.id, e.org_id, e.site_id, e.point_id, p.name, e.agent_id, e.credential_id, e.decision, e.reason, e.presented_kind,
				e.observed_plate, u.door_number, e.actor_user_id, e.actor_display, e.command_id, e.visitor_entry_id, e.occurred_at, e.synced_at, e.sync_seq
			FROM access_event e
			JOIN access_point p ON p.org_id = e.org_id AND p.id = e.point_id
			LEFT JOIN credential c ON c.org_id = e.org_id AND c.id = e.credential_id
			LEFT JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id
			WHERE e.org_id = $1 AND e.site_id = $2
			  AND ($3::bigint IS NULL OR e.sync_seq `+cmp+` $3)
			  AND ($4::uuid IS NULL OR e.point_id = $4) AND ($5 = '' OR e.decision = $5) AND ($6 = '' OR e.reason = $6)
			  AND ($7::timestamptz IS NULL OR e.occurred_at >= $7) AND ($8::timestamptz IS NULL OR e.occurred_at <= $8)
			  AND ($9 = '' OR e.observed_plate LIKE '%' || $9 || '%')
			ORDER BY e.sync_seq `+order+` LIMIT $10`,
			orgID, siteID, bound, f.PointID, f.Decision, f.Reason, f.From, f.To, plate, n+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.ID, &e.OrgID, &e.SiteID, &e.PointID, &e.PointName, &e.AgentID, &e.CredentialID, &e.Decision, &e.Reason, &e.PresentedKind,
				&e.ObservedPlate, &e.UnitDoorNumber, &e.ActorUserID, &e.ActorDisplayName, &e.CommandID, &e.VisitorEntryID, &e.OccurredAt, &e.SyncedAt, &e.SyncSeq); err != nil {
				return err
			}
			e.OccurredAt, e.SyncedAt = e.OccurredAt.UTC(), e.SyncedAt.UTC()
			e.CreatedAt, e.UpdatedAt = e.SyncedAt, e.SyncedAt
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbProblem(err, "access events")
	}
	next := ""
	if len(out) > n {
		out = out[:n]
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(out[n-1].SyncSeq, 10)))
	}
	if out == nil {
		out = []Event{}
	}
	return out, next, nil
}
