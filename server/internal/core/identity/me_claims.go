package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// MyUnitClaim is one of the caller's unit claims, in any organization, with
// the labels a resident needs to recognise it ("A-12, Green Park, waiting for
// approval").
type MyUnitClaim struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	OrgName      string
	SiteID       uuid.UUID
	SiteName     string
	UnitID       uuid.UUID
	DoorNumber   string
	BlockName    string
	PersonName   string
	Kind         string
	Status       string
	Message      *string
	DecidedBy    *uuid.UUID
	DecidedAt    *time.Time
	DecisionNote *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// maxMyClaims bounds one account's claim list; a resident files a handful.
const maxMyClaims = 500

// MyUnitClaims lists the caller's unit claims across organizations, ordered
// by id (time-ordered) so that cursors are stable. status filters to pending,
// approved or rejected; empty means all.
// It reads through the platform pool because the claims are in several
// organizations; every row is the caller's own (user_id).
func (s *Service) MyUnitClaims(ctx context.Context, userID uuid.UUID, status string) ([]MyUnitClaim, error) {
	switch status {
	case "", "pending", "approved", "rejected":
	default:
		return nil, problem.Validation(problem.Field{Field: "status", Code: "invalid_value"})
	}
	out := []MyUnitClaim{}
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.org_id, o.name, c.site_id, s.name, c.unit_id, u.door_number, b.name, p.display_name, c.kind, c.status,
			       c.message, c.decided_by, c.decided_at, c.decision_note, c.created_at, c.updated_at
			FROM unit_claim c
			JOIN organization o ON o.id = c.org_id
			JOIN site s ON s.org_id = c.org_id AND s.id = c.site_id
			JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id
			JOIN block b ON b.org_id = u.org_id AND b.id = u.block_id
			JOIN person p ON p.org_id = c.org_id AND p.id = c.person_id
			WHERE c.user_id = $1 AND ($2 = '' OR c.status = $2)
			ORDER BY c.id LIMIT $3`, userID, status, maxMyClaims)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c MyUnitClaim
			if err := rows.Scan(&c.ID, &c.OrgID, &c.OrgName, &c.SiteID, &c.SiteName, &c.UnitID, &c.DoorNumber, &c.BlockName, &c.PersonName,
				&c.Kind, &c.Status, &c.Message, &c.DecidedBy, &c.DecidedAt, &c.DecisionNote, &c.CreatedAt, &c.UpdatedAt); err != nil {
				return err
			}
			c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, problem.Internal(fmt.Errorf("list my unit claims: %w", err))
	}
	return out, nil
}
