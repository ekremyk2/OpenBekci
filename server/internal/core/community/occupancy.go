package community

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
)

// Visitor credentials belong to a unit, and the unit's people are who vouch
// for them. When a person's occupancy of a unit ends, what they registered
// there ends with it (OnOccupancyEnded, plus a sweep for end dates that lie in
// the future), and the gate and the credentials list additionally require that
// the unit has a current occupancy at evaluation time (unitOccupied).

// departedRegistrant is the SQL predicate, for rows x of visitor_entry or
// recurring_visitor, "was registered by a person whose occupancy of x's unit has
// ended and who has no current one there". $2 is now, $3 an optional unit and
// $4 an optional person to narrow it to.
const departedRegistrant = `x.registered_by IS NOT NULL
	AND ($3::uuid IS NULL OR x.unit_id = $3) AND ($4::uuid IS NULL OR x.registered_by = $4)
	AND EXISTS (SELECT 1 FROM occupancy o JOIN site st ON st.org_id = o.org_id AND st.id = o.site_id
		WHERE o.org_id = x.org_id AND o.unit_id = x.unit_id AND o.person_id = x.registered_by
		  AND o.valid_to IS NOT NULL AND o.valid_to <= ($2::timestamptz AT TIME ZONE st.timezone)::date)
	AND NOT EXISTS (SELECT 1 FROM occupancy o JOIN site st ON st.org_id = o.org_id AND st.id = o.site_id
		WHERE o.org_id = x.org_id AND o.unit_id = x.unit_id AND o.person_id = x.registered_by
		  AND o.valid_from <= ($2::timestamptz AT TIME ZONE st.timezone)::date
		  AND (o.valid_to IS NULL OR o.valid_to > ($2::timestamptz AT TIME ZONE st.timezone)::date))`

type changedRow struct {
	ID, SiteID uuid.UUID
}

func scanChanged(rows pgx.Rows) ([]changedRow, error) {
	defer rows.Close()
	var out []changedRow
	for rows.Next() {
		var r changedRow
		if err := rows.Scan(&r.ID, &r.SiteID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OnOccupancyEnded is the people module's OnOccupancyEnded hook (the wiring
// maps it): it runs in the transaction of the move-out or edit. The recurring
// visitors the person registered at the unit are revoked and their open
// pre-registrations and visits cancelled, so a new occupant does not inherit a
// key to the building. If the end date still lies in the future nothing
// happens yet; the expiry job finishes it once the occupancy is over.
func (s *Service) OnOccupancyEnded(ctx context.Context, tx pgx.Tx, orgID, unitID, personID uuid.UUID) error {
	_, err := s.revokeDeparted(ctx, tx, orgID, &unitID, &personID)
	return err
}

// revokeDeparted revokes the visitor credentials of departed registrants,
// narrowed to a unit and person for the hook, or everywhere for the job. It
// returns how many rows it changed.
func (s *Service) revokeDeparted(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, unitID, personID *uuid.UUID) (int, error) {
	now := s.now()
	rows, err := tx.Query(ctx, `UPDATE recurring_visitor x SET status = 'revoked', updated_at = now(), version = version + 1
		WHERE x.org_id = $1 AND x.status = 'active' AND `+departedRegistrant+` RETURNING x.id, x.site_id`, orgID, now, unitID, personID)
	if err != nil {
		return 0, err
	}
	revoked, err := scanChanged(rows)
	if err != nil {
		return 0, err
	}
	rvIDs := make([]uuid.UUID, len(revoked))
	for i, r := range revoked {
		rvIDs[i] = r.ID
		if err := s.credentialAudit(ctx, tx, orgID, r, "recurring_visitor.revoke", "recurring_visitor", "occupancy_ended"); err != nil {
			return 0, err
		}
		if err := s.visitorChanged(ctx, tx, r.SiteID, "recurring_visitor", r.ID); err != nil {
			return 0, err
		}
	}
	n := len(revoked)
	rows, err = tx.Query(ctx, `UPDATE visitor_entry x SET cancelled = true, updated_at = now(), version = version + 1
		WHERE x.org_id = $1 AND NOT x.cancelled AND x.arrived_at IS NULL AND x.approval_status IN ('pre_approved', 'approved')
		  AND x.source = 'pre_registered' AND `+departedRegistrant+` RETURNING x.id, x.site_id`, orgID, now, unitID, personID)
	if err != nil {
		return n, err
	}
	cancelled, err := scanChanged(rows)
	if err != nil {
		return n, err
	}
	n += len(cancelled)
	for _, r := range cancelled {
		if err := s.credentialAudit(ctx, tx, orgID, r, "visitor.cancel", "visitor_entry", "occupancy_ended"); err != nil {
			return n, err
		}
		if err := s.visitorChanged(ctx, tx, r.SiteID, "visitor_entry", r.ID); err != nil {
			return n, err
		}
	}
	visits, err := s.cancelOpenVisits(ctx, tx, orgID, rvIDs, "occupancy_ended")
	return n + visits, err
}

// credentialAudit records that a credential was ended by the system for a reason.
func (s *Service) credentialAudit(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, r changedRow, action, targetType, reason string) error {
	ev := audit.Event{OrgID: orgID, SiteID: &r.SiteID, Action: action, TargetType: targetType, TargetID: r.ID,
		Diff: audit.Changes{"reason": {After: mustJSON(reason)}}}
	if principalUser(ctx) == nil {
		ev.ActorKind = audit.ActorJob
	}
	return audit.Record(ctx, tx, ev)
}

// cancelOpenVisits cancels the visits (entries) of recurring visitors that
// have not arrived yet, so a revoked recurring visitor cannot be admitted
// through a visit a scan opened before. It returns how many it cancelled.
func (s *Service) cancelOpenVisits(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, rvIDs []uuid.UUID, reason string) (int, error) {
	if len(rvIDs) == 0 {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `UPDATE visitor_entry SET cancelled = true, updated_at = now(), version = version + 1
		WHERE org_id = $1 AND recurring_visitor_id = ANY($2) AND NOT cancelled AND arrived_at IS NULL RETURNING id, site_id`, orgID, rvIDs)
	if err != nil {
		return 0, err
	}
	cancelled, err := scanChanged(rows)
	if err != nil {
		return 0, err
	}
	for _, r := range cancelled {
		if err := s.credentialAudit(ctx, tx, orgID, r, "visitor.cancel", "visitor_entry", reason); err != nil {
			return 0, err
		}
		if err := s.visitorChanged(ctx, tx, r.SiteID, "visitor_entry", r.ID); err != nil {
			return 0, err
		}
	}
	return len(cancelled), nil
}

// unitOccupied reports whether the unit has a current occupancy on the local
// day of t in loc: the gate and the credentials list refuse a credential of a
// unit nobody lives in.
func unitOccupied(ctx context.Context, tx pgx.Tx, orgID, unitID uuid.UUID, t time.Time, loc *time.Location) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND valid_from <= $3::date AND (valid_to IS NULL OR valid_to > $3::date))`,
		orgID, unitID, t.In(loc).Format("2006-01-02")).Scan(&ok)
	return ok, err
}
