package access

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Point kinds and directions.
const (
	PointVehicleGate    = "vehicle_gate"
	PointPedestrianDoor = "pedestrian_door"
)

// Point is a gate or door driven by an agent.
type Point struct {
	ID        uuid.UUID           `json:"id"`
	OrgID     uuid.UUID           `json:"org_id"`
	SiteID    uuid.UUID           `json:"site_id"`
	Name      string              `json:"name"`
	Kind      string              `json:"kind"`
	Direction string              `json:"direction"`
	AgentID   *uuid.UUID          `json:"agent_id"`
	DriverRef *protocol.DriverRef `json:"driver_ref"`
	Enabled   bool                `json:"enabled"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
	Version   int                 `json:"version"`
}

const pointColumns = `id, org_id, site_id, name, kind, direction, agent_id, driver_ref, enabled, created_at, updated_at, version`

func scanPoint(row pgx.Row) (Point, error) {
	var p Point
	var ref []byte
	err := row.Scan(&p.ID, &p.OrgID, &p.SiteID, &p.Name, &p.Kind, &p.Direction, &p.AgentID, &ref, &p.Enabled, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if len(ref) > 0 && string(ref) != "null" {
		var d protocol.DriverRef
		if json.Unmarshal(ref, &d) == nil {
			p.DriverRef = &d
		}
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

func (p Point) audited() map[string]any {
	return map[string]any{"name": p.Name, "kind": p.Kind, "direction": p.Direction, "agent_id": p.AgentID, "driver_ref": p.DriverRef, "enabled": p.Enabled}
}

// ListPoints lists the access points of a site (permission access.read).
func (s *Service) ListPoints(ctx context.Context, orgID, siteID uuid.UUID, agentID *uuid.UUID, enabled *bool, limit int, cursor string) ([]Point, string, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return nil, "", err
	}
	after, err := decodeIDCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	n := pageSize(limit)
	var out []Point
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if err := siteScope(ctx, tx, PermRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND site_id = $2 AND id > $3
			AND ($4::uuid IS NULL OR agent_id = $4) AND ($5::boolean IS NULL OR enabled = $5) ORDER BY id LIMIT $6`,
			orgID, siteID, after, agentID, enabled, n+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPoint(rows)
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbProblem(err, "access points")
	}
	items, next := pageOf(out, n, func(p Point) uuid.UUID { return p.ID })
	return items, next, nil
}

// GetPoint returns one access point.
func (s *Service) GetPoint(ctx context.Context, orgID, pointID uuid.UUID) (Point, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return Point{}, err
	}
	var p Point
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if p, err = scanPoint(tx.QueryRow(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND id = $2`, orgID, pointID)); err != nil {
			return err
		}
		return siteScope(ctx, tx, PermRead, p.SiteID)
	})
	return p, dbProblem(err, "access point")
}

var (
	pointKinds     = []string{PointVehicleGate, PointPedestrianDoor}
	pointDirection = []string{"in", "out", "both"}
)

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// pointInput is a validated create or patch.
type pointInput struct {
	name            *string
	kind, direction *string
	agentID         *uuid.UUID
	agentSet        bool
	driver          *protocol.DriverRef
	driverSet       bool
	enabled         *bool
	siteID          *uuid.UUID
}

func parsePoint(d tenancy.Doc, create bool) (pointInput, error) {
	allowed := []string{"name", "kind", "direction", "agent_id", "driver_ref", "enabled"}
	if create {
		allowed = append(allowed, "site_id")
	}
	fields := d.Unknown(allowed...)
	var in pointInput
	var err error
	if in.name, err = d.String("name"); err != nil {
		return in, err
	}
	if in.name != nil {
		t := strings.TrimSpace(*in.name)
		in.name = &t
		if n := len([]rune(t)); n < 1 || n > 200 {
			fields = append(fields, problem.Field{Field: "name", Code: "invalid_value"})
		}
	}
	if in.kind, err = d.String("kind"); err != nil {
		return in, err
	}
	if in.kind != nil && !oneOf(*in.kind, pointKinds) {
		fields = append(fields, problem.Field{Field: "kind", Code: "invalid_value"})
	}
	if in.direction, err = d.String("direction"); err != nil {
		return in, err
	}
	if in.direction != nil && !oneOf(*in.direction, pointDirection) {
		fields = append(fields, problem.Field{Field: "direction", Code: "invalid_value"})
	}
	if d.Has("agent_id") {
		in.agentSet = true
		if !d.IsNull("agent_id") {
			var u uuid.UUID
			if err := d.Get("agent_id", &u); err != nil {
				return in, err
			}
			in.agentID = &u
		}
	}
	if d.Has("driver_ref") {
		in.driverSet = true
		if !d.IsNull("driver_ref") {
			var ref protocol.DriverRef
			if err := d.Get("driver_ref", &ref); err != nil {
				return in, err
			}
			if strings.TrimSpace(ref.Instance) == "" || len(ref.Instance) > 100 || len(ref.Channel) > 100 {
				fields = append(fields, problem.Field{Field: "driver_ref", Code: "invalid_value"})
			}
			in.driver = &ref
		}
	}
	if d.Has("enabled") {
		var e bool
		if err := d.Get("enabled", &e); err != nil {
			return in, err
		}
		in.enabled = &e
	}
	if create {
		if d.Has("site_id") {
			var u uuid.UUID
			if err := d.Get("site_id", &u); err != nil {
				return in, err
			}
			in.siteID = &u
		}
		for name, missing := range map[string]bool{"site_id": in.siteID == nil, "name": in.name == nil, "kind": in.kind == nil, "direction": in.direction == nil} {
			if missing {
				fields = append(fields, problem.Field{Field: name, Code: "required"})
			}
		}
	}
	if len(fields) > 0 {
		return in, problem.Validation(fields...)
	}
	return in, nil
}

// CreatePoint creates an access point (permission access.manage). The allow-list
// is re-projected: credentials whose scope covers the new gate gain it.
func (s *Service) CreatePoint(ctx context.Context, orgID uuid.UUID, d tenancy.Doc) (Point, error) {
	if err := authz.RequireInOrg(ctx, PermManage, orgID); err != nil {
		return Point{}, err
	}
	in, err := parsePoint(d, true)
	if err != nil {
		return Point{}, err
	}
	enabled := true
	if in.enabled != nil {
		enabled = *in.enabled
	}
	var out Point
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		siteID := *in.siteID
		if err := siteScope(ctx, tx, PermManage, siteID); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		pid := id.New()
		var ref []byte
		if in.driver != nil {
			ref, _ = json.Marshal(in.driver)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO access_point (id, org_id, site_id, name, kind, direction, agent_id, driver_ref, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, pid, orgID, siteID, *in.name, *in.kind, *in.direction, in.agentID, ref, enabled); err != nil {
			return err
		}
		if out, err = scanPoint(tx.QueryRow(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND id = $2`, orgID, pid)); err != nil {
			return err
		}
		diff, err := audit.Diff("access_point", nil, out.audited())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "access.point_create", TargetType: "access_point", TargetID: pid, Diff: diff}); err != nil {
			return err
		}
		_, err = resyncSite(ctx, tx, orgID, siteID)
		return err
	})
	if err != nil {
		return Point{}, dbProblem(err, "access point")
	}
	s.WakeSite(out.SiteID)
	return out, nil
}

// UpdatePoint changes an access point (If-Match: expectedVersion) and
// re-projects the allow-list.
func (s *Service) UpdatePoint(ctx context.Context, orgID, pointID uuid.UUID, expectedVersion int, d tenancy.Doc) (Point, error) {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return Point{}, err
	}
	in, err := parsePoint(d, false)
	if err != nil {
		return Point{}, err
	}
	var out Point
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanPoint(tx.QueryRow(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, pointID))
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return errVersion
		}
		next := before
		if in.name != nil {
			next.Name = *in.name
		}
		if in.kind != nil {
			next.Kind = *in.kind
		}
		if in.direction != nil {
			next.Direction = *in.direction
		}
		if in.agentSet {
			next.AgentID = in.agentID
		}
		if in.driverSet {
			next.DriverRef = in.driver
		}
		if in.enabled != nil {
			next.Enabled = *in.enabled
		}
		diff, err := audit.Diff("access_point", before.audited(), next.audited())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		var ref []byte
		if next.DriverRef != nil {
			ref, _ = json.Marshal(next.DriverRef)
		}
		if _, err := tx.Exec(ctx, `UPDATE access_point SET name = $3, kind = $4, direction = $5, agent_id = $6, driver_ref = $7, enabled = $8,
				updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, pointID, next.Name, next.Kind, next.Direction, next.AgentID, ref, next.Enabled); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "access.point_update", TargetType: "access_point", TargetID: pointID, Diff: diff}); err != nil {
			return err
		}
		if _, err := resyncSite(ctx, tx, orgID, before.SiteID); err != nil {
			return err
		}
		out, err = scanPoint(tx.QueryRow(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND id = $2`, orgID, pointID))
		return err
	})
	if err != nil {
		return Point{}, dbProblem(err, "access point")
	}
	s.WakeSite(out.SiteID)
	return out, nil
}

// DeletePoint removes an access point that has no opening log or commands (a
// point with history is disabled instead) and re-projects the allow-list.
func (s *Service) DeletePoint(ctx context.Context, orgID, pointID uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return err
	}
	var siteID uuid.UUID
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanPoint(tx.QueryRow(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, pointID))
		if err != nil {
			return err
		}
		siteID = before.SiteID
		if err := siteScope(ctx, tx, PermManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return errVersion
		}
		if _, err := tx.Exec(ctx, `DELETE FROM access_point WHERE org_id = $1 AND id = $2`, orgID, pointID); err != nil {
			if isPG(err, "23503") {
				return problem.Conflict("the access point has an opening log or commands; disable it instead of deleting it")
			}
			return err
		}
		diff, err := audit.Diff("access_point", before.audited(), nil)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "access.point_delete", TargetType: "access_point", TargetID: pointID, Diff: diff}); err != nil {
			return err
		}
		_, err = resyncSite(ctx, tx, orgID, before.SiteID)
		return err
	})
	if err != nil {
		return dbProblem(err, "access point")
	}
	s.WakeSite(siteID)
	return nil
}
