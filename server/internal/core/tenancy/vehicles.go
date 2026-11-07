package tenancy

import (
	"bytes"
	"context"
	"encoding/csv"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var turkishASCII = strings.NewReplacer("Ç", "C", "ç", "C", "Ğ", "G", "ğ", "G", "İ", "I", "ı", "I", "Ö", "O", "ö", "O", "Ş", "S", "ş", "S", "Ü", "U", "ü", "U")

// NormalizePlate turns a plate as people write it ("34 abc 123", "34-ABC-123")
// into its stored form ("34ABC123"): Turkish letters become their ASCII
// letters, everything but letters and digits is dropped, upper case. It
// reports false for a result that is not 2 to 12 characters.
func NormalizePlate(s string) (string, bool) {
	var b strings.Builder
	for _, r := range turkishASCII.Replace(s) {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case r == ' ' || r == '-' || r == '.' || r == '_' || r == '\t':
		default:
			return "", false
		}
	}
	out := b.String()
	return out, len(out) >= 2 && len(out) <= 12
}

var trPlate = regexp.MustCompile(`^([0-9]{2})([A-Z]{1,3})([0-9]{2,4})$`)

// FormatPlate writes a normalized plate the way it is printed: "34 ABC 123"
// for a Turkish plate, unchanged otherwise.
func FormatPlate(p string) string {
	if m := trPlate.FindStringSubmatch(p); m != nil {
		return m[1] + " " + m[2] + " " + m[3]
	}
	return p
}

// Vehicle is a plate registered to a unit.
type Vehicle struct {
	ID         uuid.UUID `json:"id"`
	SiteID     uuid.UUID `json:"site_id"`
	UnitID     uuid.UUID `json:"unit_id"`
	DoorNumber string    `json:"door_number"`
	Plate      string    `json:"plate"`
	Label      *string   `json:"label"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
}

const vehicleSelect = `SELECT v.id, v.site_id, v.unit_id, u.door_number, v.plate, v.label, v.version, v.created_at
	FROM vehicle v JOIN unit u ON u.org_id = v.org_id AND u.id = v.unit_id`

func scanVehicle(row pgx.Row) (Vehicle, error) {
	var v Vehicle
	err := row.Scan(&v.ID, &v.SiteID, &v.UnitID, &v.DoorNumber, &v.Plate, &v.Label, &v.Version, &v.CreatedAt)
	v.CreatedAt = v.CreatedAt.UTC()
	return v, err
}

// ListVehicles lists a site's vehicles, optionally of one unit or matching a
// plate fragment. It needs vehicle.read for the site or some of its blocks.
func (s *Service) ListVehicles(ctx context.Context, orgID, siteID uuid.UUID, unitID *uuid.UUID, q string, p PageParams) ([]Vehicle, string, error) {
	if err := authz.RequireAnywhere(ctx, PermVehicleRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	norm, _ := NormalizePlate(q)
	var out []Vehicle
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		all, blocks, err := visibleBlocks(ctx, PermVehicleRead, sc)
		if err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, vehicleSelect+` WHERE v.org_id = $1 AND v.site_id = $2 AND v.id > $3 AND ($4 OR u.block_id = ANY($5))
			AND ($6::uuid IS NULL OR v.unit_id = $6) AND ($7 = '' OR v.plate LIKE '%' || $7 || '%') ORDER BY v.id LIMIT $8`,
			orgID, siteID, after, all, blocks, unitID, norm, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanVehicle(rows)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", DBProblem(err, "list vehicles")
	}
	items, next := Page(out, limit, func(v Vehicle) uuid.UUID { return v.ID })
	return items, next, nil
}

// CreateVehicle registers a plate for a unit. It needs vehicle.manage for the unit's block.
func (s *Service) CreateVehicle(ctx context.Context, orgID, unitID uuid.UUID, d Doc) (Vehicle, error) {
	if err := authz.RequireAnywhere(ctx, PermVehicleManage); err != nil {
		return Vehicle{}, err
	}
	fields := d.Unknown("plate", "label")
	plateRaw, err := d.String("plate")
	if err != nil {
		return Vehicle{}, err
	}
	label, err := d.String("label")
	if err != nil {
		return Vehicle{}, err
	}
	var plate string
	if plateRaw == nil {
		fields = append(fields, problem.Field{Field: "plate", Code: "required"})
	} else if p, ok := NormalizePlate(*plateRaw); ok {
		plate = p
	} else {
		fields = append(fields, problem.Field{Field: "plate", Code: "invalid_format"})
	}
	if len(fields) > 0 {
		return Vehicle{}, problem.Validation(fields...)
	}
	var out Vehicle
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		u, err := GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermVehicleManage, u.BlockID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, u.SiteID, false, false); err != nil {
			return err
		}
		vid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO vehicle (id, org_id, site_id, unit_id, plate, label) VALUES ($1, $2, $3, $4, $5, $6)`, vid, orgID, u.SiteID, unitID, plate, label); err != nil {
			return err
		}
		if out, err = scanVehicle(tx.QueryRow(ctx, vehicleSelect+` WHERE v.org_id = $1 AND v.id = $2`, orgID, vid)); err != nil {
			return err
		}
		diff, err := audit.Diff("vehicle", nil, map[string]any{"plate": plate, "unit_id": unitID, "label": label})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &u.SiteID, Action: "vehicle.create", TargetType: "vehicle", TargetID: vid, Diff: diff})
	})
	if err != nil {
		return Vehicle{}, DBProblem(err, "vehicle")
	}
	return out, nil
}

// DeleteVehicle removes a vehicle. It needs vehicle.manage for the unit's block.
func (s *Service) DeleteVehicle(ctx context.Context, orgID, vehicleID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermVehicleManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := scanVehicle(tx.QueryRow(ctx, vehicleSelect+` WHERE v.org_id = $1 AND v.id = $2`, orgID, vehicleID))
		if err != nil {
			return err
		}
		var blockID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT block_id FROM unit WHERE org_id = $1 AND id = $2`, orgID, v.UnitID).Scan(&blockID); err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermVehicleManage, blockID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, v.SiteID, false, false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM vehicle WHERE org_id = $1 AND id = $2`, orgID, vehicleID); err != nil {
			return err
		}
		diff, err := audit.Diff("vehicle", map[string]any{"plate": v.Plate, "unit_id": v.UnitID}, nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &v.SiteID, Action: "vehicle.delete", TargetType: "vehicle", TargetID: vehicleID, Diff: diff})
	})
	return DBProblem(err, "delete vehicle")
}

// ExportVehiclesCSV renders every plate of the site as CSV for the camera
// operator: plate (as printed), door number, block, label. It needs
// vehicle.read for the whole site.
func (s *Service) ExportVehiclesCSV(ctx context.Context, orgID, siteID uuid.UUID) ([]byte, error) {
	if err := authz.RequireAnywhere(ctx, PermVehicleRead); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := loadSiteFor(ctx, tx, PermVehicleRead, orgID, siteID, false, false); err != nil {
			return err
		}
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"plate", "door_number", "block", "label"})
		rows, err := tx.Query(ctx, `SELECT v.plate, u.door_number, b.name, COALESCE(v.label, '')
			FROM vehicle v JOIN unit u ON u.org_id = v.org_id AND u.id = v.unit_id JOIN block b ON b.org_id = u.org_id AND b.id = u.block_id
			WHERE v.org_id = $1 AND v.site_id = $2 ORDER BY v.plate`, orgID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var plate, door, block, label string
			if err := rows.Scan(&plate, &door, &block, &label); err != nil {
				return err
			}
			_ = w.Write([]string{FormatPlate(plate), door, block, label})
		}
		w.Flush()
		return rows.Err()
	})
	if err != nil {
		return nil, DBProblem(err, "export vehicles")
	}
	return buf.Bytes(), nil
}
