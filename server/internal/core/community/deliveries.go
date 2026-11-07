package community

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Delivery is a parcel the guard logged against a unit (decision C3).
type Delivery struct {
	ID                    uuid.UUID  `json:"id"`
	OrgID                 uuid.UUID  `json:"org_id"`
	SiteID                uuid.UUID  `json:"site_id"`
	UnitID                uuid.UUID  `json:"unit_id"`
	UnitDoorNumber        string     `json:"unit_door_number"`
	ReceivedAt            time.Time  `json:"received_at"`
	ReceivedByDisplayName *string    `json:"received_by_display_name"`
	Description           *string    `json:"description"`
	Status                string     `json:"status"`
	CollectedAt           *time.Time `json:"collected_at"`
	CollectedByName       *string    `json:"collected_by_name"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

const deliverySelect = `SELECT d.id, d.org_id, d.site_id, d.unit_id, u.door_number, d.received_at, p.display_name, d.description,
		CASE WHEN d.collected_at IS NULL THEN 'waiting' ELSE 'collected' END, d.collected_at, d.collected_by_name, d.created_at, d.updated_at
	FROM delivery d JOIN unit u ON u.org_id = d.org_id AND u.id = d.unit_id LEFT JOIN person p ON p.org_id = d.org_id AND p.id = d.received_by`

func scanDelivery(row pgx.Row) (Delivery, error) {
	var d Delivery
	err := row.Scan(&d.ID, &d.OrgID, &d.SiteID, &d.UnitID, &d.UnitDoorNumber, &d.ReceivedAt, &d.ReceivedByDisplayName, &d.Description,
		&d.Status, &d.CollectedAt, &d.CollectedByName, &d.CreatedAt, &d.UpdatedAt)
	d.ReceivedAt, d.CreatedAt, d.UpdatedAt = d.ReceivedAt.UTC(), d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	utcPtr(&d.CollectedAt)
	return d, err
}

func (d Delivery) gate() GateDelivery {
	return GateDelivery{ID: d.ID, UnitDoorNumber: d.UnitDoorNumber, ReceivedAt: d.ReceivedAt, Description: d.Description, Status: d.Status, CollectedByName: d.CollectedByName}
}

// listGateDeliveries is the guard's parcel list: door number, time, description, status.
// A collected parcel stays visible to the guard for 7 days.
func listGateDeliveries(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, door, status string, after uuid.UUID, limit int, now time.Time) ([]GateDelivery, []uuid.UUID, error) {
	rows, err := tx.Query(ctx, deliverySelect+` WHERE d.org_id = $1 AND d.site_id = $2 AND d.id > $3 AND ($4 = '' OR u.door_number = $4)
		AND ($5 = '' OR (CASE WHEN d.collected_at IS NULL THEN 'waiting' ELSE 'collected' END) = $5)
		AND (d.collected_at IS NULL OR d.collected_at > $7) ORDER BY d.id LIMIT $6`, orgID, siteID, after, door, status, limit, now.AddDate(0, 0, -guardParcelDays))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []GateDelivery{}
	var ids []uuid.UUID
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, d.gate())
		ids = append(ids, d.ID)
	}
	return out, ids, rows.Err()
}

// ListGateDeliveries lists a site's parcels for the booth (gate.operate).
func (s *Service) ListGateDeliveries(ctx context.Context, orgID, siteID uuid.UUID, door, status, guardToken string, p tenancy.PageParams) ([]GateDelivery, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var items []GateDelivery
	var ids []uuid.UUID
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := s.gateAt(ctx, tx, orgID, siteID, guardToken); err != nil {
			return err
		}
		var err error
		items, ids, err = listGateDeliveries(ctx, tx, orgID, siteID, door, status, after, limit+1, s.now())
		return err
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list deliveries")
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		_, next = tenancy.Page(ids, limit, func(i uuid.UUID) uuid.UUID { return i })
	}
	return items, next, nil
}

// CreateGateDelivery logs a parcel against a unit and notifies the unit's
// people (gate.operate): in-app plus their preferred channel.
func (s *Service) CreateGateDelivery(ctx context.Context, orgID, siteID, unitID uuid.UUID, description *string, guardToken string) (GateDelivery, error) {
	var desc *string
	if description != nil {
		if t := strings.TrimSpace(*description); t != "" {
			if len([]rune(t)) > 300 {
				return GateDelivery{}, validation("description", "too_long")
			}
			desc = &t
		}
	}
	now := s.now()
	var out GateDelivery
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		g, err := s.gateAt(ctx, tx, orgID, siteID, guardToken)
		if err != nil {
			return err
		}
		si, err := loadSite(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		unit, err := loadUnit(ctx, tx, orgID, siteID, unitID, "unit_id")
		if err != nil {
			return err
		}
		deliveryID := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO delivery (id, org_id, site_id, unit_id, received_at, received_by, description) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			deliveryID, orgID, siteID, unitID, now, g.Person, desc); err != nil {
			return err
		}
		people, err := unitOccupants(ctx, tx, orgID, unitID, now.In(si.Loc))
		if err != nil {
			return err
		}
		if len(people) > 0 {
			data := map[string]any{"delivery_id": deliveryID, "unit_door_number": unit.DoorNumber}
			if desc != nil {
				data["description"] = *desc
			}
			if err := s.cfg.Notifier.Notify(ctx, tx, jobs.Notify{OrgID: orgID, SiteID: &siteID, PersonIDs: people, Template: "delivery.received", Data: data,
				DedupeKey: "delivery.received:" + deliveryID.String()}); err != nil {
				return err
			}
		}
		d, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+` WHERE d.org_id = $1 AND d.id = $2`, orgID, deliveryID))
		if err != nil {
			return err
		}
		out = d.gate()
		return gateAudit(ctx, tx, orgID, siteID, g, "delivery.receive", "delivery", deliveryID, audit.Changes{"unit_id": {After: mustJSON(unitID.String())}})
	})
	if err != nil {
		return GateDelivery{}, tenancy.DBProblem(err, "delivery")
	}
	return out, nil
}

// CollectGateDelivery marks a parcel collected, and by whom (gate.operate).
func (s *Service) CollectGateDelivery(ctx context.Context, orgID, deliveryID uuid.UUID, collectedBy, guardToken string) (GateDelivery, error) {
	name := strings.TrimSpace(collectedBy)
	if n := len([]rune(name)); n < 1 || n > 120 {
		return GateDelivery{}, validation("collected_by_name", "invalid_value")
	}
	now := s.now()
	var out GateDelivery
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		d, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+` WHERE d.org_id = $1 AND d.id = $2 FOR UPDATE OF d`, orgID, deliveryID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such delivery")
		}
		if err != nil {
			return err
		}
		g, err := s.gateAt(ctx, tx, orgID, d.SiteID, guardToken)
		if err != nil {
			return err
		}
		if d.Status == "collected" {
			return problem.Conflict("the parcel was already collected")
		}
		if _, err := tx.Exec(ctx, `UPDATE delivery SET collected_at = $3, collected_by_name = $4, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, deliveryID, now, name); err != nil {
			return err
		}
		after, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+` WHERE d.org_id = $1 AND d.id = $2`, orgID, deliveryID))
		if err != nil {
			return err
		}
		out = after.gate()
		return gateAudit(ctx, tx, orgID, d.SiteID, g, "delivery.collect", "delivery", deliveryID, nil)
	})
	if err != nil {
		return GateDelivery{}, tenancy.DBProblem(err, "delivery")
	}
	return out, nil
}

// ListDeliveries lists parcels for the portal (deliveries.read: the caller's
// own units) or the whole site (visitors.read).
func (s *Service) ListDeliveries(ctx context.Context, orgID, siteID uuid.UUID, unitID *uuid.UUID, status string, p tenancy.PageParams) ([]Delivery, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Delivery
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := siteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := requireAny(ctx, sc, PermVisitorsRead, PermDeliveriesRead); err != nil {
			return err
		}
		all := authz.Has(ctx, PermVisitorsRead, sc)
		var units []uuid.UUID
		if !all {
			units = residentUnits(ctx, siteID)
			if unitID != nil {
				if err := authz.CanActOnUnit(ctx, *unitID); err != nil {
					return err
				}
			}
		}
		rows, err := tx.Query(ctx, deliverySelect+` WHERE d.org_id = $1 AND d.site_id = $2 AND d.id > $3 AND ($4::uuid IS NULL OR d.unit_id = $4)
			AND ($5 = '' OR (CASE WHEN d.collected_at IS NULL THEN 'waiting' ELSE 'collected' END) = $5) AND ($6 OR d.unit_id = ANY($7))
			ORDER BY d.id LIMIT $8`, orgID, siteID, after, unitID, status, all, units, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDelivery(rows)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list deliveries")
	}
	items, next := tenancy.Page(out, limit, func(d Delivery) uuid.UUID { return d.ID })
	return items, next, nil
}
