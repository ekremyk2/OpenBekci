package tenancy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// The methods in this file were added with the API contract reconciliation:
// reads and deletes of single resources the draft contract has and the first
// version of the module did not. They only add to the service.

// GetBlock returns one block. It needs sites.read for the block's site.
func (s *Service) GetBlock(ctx context.Context, orgID, blockID uuid.UUID) (Block, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteRead); err != nil {
		return Block{}, err
	}
	var out Block
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if out, err = scanBlock(tx.QueryRow(ctx, `SELECT `+blockColumns+` FROM block WHERE org_id = $1 AND id = $2`, orgID, blockID)); err != nil {
			return err
		}
		return requireSiteErr(ctx, tx, PermSiteRead, out.SiteID)
	})
	if err != nil {
		return Block{}, DBProblem(err, "get block")
	}
	return out, nil
}

// DeleteBlock removes an empty block. It needs sites.manage for the site; a
// block that still has units is a conflict, and so is the last block.
func (s *Service) DeleteBlock(ctx context.Context, orgID, blockID uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermSiteManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		b, err := scanBlock(tx.QueryRow(ctx, `SELECT `+blockColumns+` FROM block WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, blockID))
		if err != nil {
			return err
		}
		if err := requireSiteErr(ctx, tx, PermSiteManage, b.SiteID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, b.SiteID, false, false); err != nil {
			return err
		}
		if b.Version != expectedVersion {
			return ErrVersion
		}
		var units, blocks int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM unit WHERE org_id = $1 AND block_id = $2),
			(SELECT count(*) FROM block WHERE org_id = $1 AND site_id = $3)`, orgID, blockID, b.SiteID).Scan(&units, &blocks); err != nil {
			return err
		}
		if units > 0 {
			return problem.Conflict("the block still has units")
		}
		if blocks <= 1 {
			return problem.Conflict("a site keeps at least one block")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM block WHERE org_id = $1 AND id = $2`, orgID, blockID); err != nil {
			return err
		}
		diff, err := audit.Diff("block", blockAudit{b.Name, b.Hidden, b.SortOrder}, nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &b.SiteID, Action: "block.delete", TargetType: "block", TargetID: blockID, Diff: diff})
	})
	return deleteProblem(err, "block")
}

// deleteProblem maps the error of a delete: a foreign key that still points at
// the row is a conflict, not a validation problem.
func deleteProblem(err error, what string) error {
	if err == nil {
		return nil
	}
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) && pe.SQLState() == "23503" {
		return problem.Conflict("the " + what + " is still referenced")
	}
	if errors.Is(err, ErrVersion) {
		return VersionConflict(what)
	}
	return DBProblem(err, "delete "+what)
}

// DeleteUnit removes a unit that nothing references (no occupancy, vehicle,
// claim, invitation or appurtenance). It needs units.manage for the unit's block.
func (s *Service) DeleteUnit(ctx context.Context, orgID, unitID uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermUnitManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		u, err := GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermUnitManage, u.BlockID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, u.SiteID, false, false); err != nil {
			return err
		}
		if u.Version != expectedVersion {
			return ErrVersion
		}
		for _, q := range []string{
			`DELETE FROM unit_tag WHERE org_id = $1 AND unit_id = $2`,
			`DELETE FROM allocation_key_value WHERE org_id = $1 AND unit_id = $2`,
		} {
			if _, err := tx.Exec(ctx, q, orgID, unitID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM unit WHERE org_id = $1 AND id = $2`, orgID, unitID); err != nil {
			return err
		}
		diff, err := audit.Diff("unit", u.audit(), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &u.SiteID, Action: "unit.delete", TargetType: "unit", TargetID: unitID, Diff: diff})
	})
	return deleteProblem(err, "unit")
}

// DeleteAllocationKey removes a custom allocation key and its values. The
// keys created with the site are kept. It needs sites.manage for the site.
func (s *Service) DeleteAllocationKey(ctx context.Context, orgID, keyID uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermAllocationManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		k, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM allocation_key k WHERE k.org_id = $1 AND k.id = $2 FOR UPDATE OF k`, orgID, keyID))
		if err != nil {
			return err
		}
		if err := requireSiteErr(ctx, tx, PermAllocationManage, k.SiteID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, k.SiteID, false, false); err != nil {
			return err
		}
		if k.Version != expectedVersion {
			return ErrVersion
		}
		if k.IsDefault {
			return problem.Conflict("the allocation keys created with the site cannot be deleted")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM allocation_key_value WHERE org_id = $1 AND key_id = $2`, orgID, keyID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM allocation_key WHERE org_id = $1 AND id = $2`, orgID, keyID); err != nil {
			return err
		}
		diff, err := audit.Diff("allocation_key", map[string]any{"code": k.Code, "name": k.Name, "kind": k.Kind}, nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &k.SiteID, Action: "allocation_key.delete", TargetType: "allocation_key", TargetID: keyID, Diff: diff})
	})
	return deleteProblem(err, "allocation key")
}

// GetVehicle returns one vehicle. It needs vehicles.read for the unit's block.
func (s *Service) GetVehicle(ctx context.Context, orgID, vehicleID uuid.UUID) (Vehicle, error) {
	if err := authz.RequireAnywhere(ctx, PermVehicleRead); err != nil {
		return Vehicle{}, err
	}
	var out Vehicle
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := scanVehicle(tx.QueryRow(ctx, vehicleSelect+` WHERE v.org_id = $1 AND v.id = $2`, orgID, vehicleID))
		if err != nil {
			return err
		}
		var blockID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT block_id FROM unit WHERE org_id = $1 AND id = $2`, orgID, v.UnitID).Scan(&blockID); err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermVehicleRead, blockID); err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		return Vehicle{}, DBProblem(err, "get vehicle")
	}
	return out, nil
}

// UpdateVehicle changes the plate, label or unit of a vehicle if it is still
// at expectedVersion. It needs vehicles.manage for the old and the new unit's block.
func (s *Service) UpdateVehicle(ctx context.Context, orgID, vehicleID uuid.UUID, expectedVersion int, d Doc) (Vehicle, error) {
	if err := authz.RequireAnywhere(ctx, PermVehicleManage); err != nil {
		return Vehicle{}, err
	}
	fields := d.Unknown("plate", "label", "unit_id")
	var plate *string
	if raw, err := d.String("plate"); err != nil {
		return Vehicle{}, err
	} else if raw != nil {
		if p, ok := NormalizePlate(*raw); ok {
			plate = &p
		} else {
			fields = append(fields, problem.Field{Field: "plate", Code: "invalid_format"})
		}
	}
	var unitID *uuid.UUID
	if d.Has("unit_id") && !d.IsNull("unit_id") {
		var u uuid.UUID
		if err := d.Get("unit_id", &u); err != nil {
			return Vehicle{}, err
		}
		unitID = &u
	}
	if len(fields) > 0 {
		return Vehicle{}, problem.Validation(fields...)
	}
	var out Vehicle
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanVehicle(tx.QueryRow(ctx, vehicleSelect+` WHERE v.org_id = $1 AND v.id = $2 FOR UPDATE OF v`, orgID, vehicleID))
		if err != nil {
			return err
		}
		var blockID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT block_id FROM unit WHERE org_id = $1 AND id = $2`, orgID, before.UnitID).Scan(&blockID); err != nil {
			return err
		}
		if err := requireBlockErr(ctx, tx, PermVehicleManage, blockID); err != nil {
			return err
		}
		if _, err := LoadSite(ctx, tx, orgID, before.SiteID, false, false); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return ErrVersion
		}
		next := before
		if plate != nil {
			next.Plate = *plate
		}
		if d.Has("label") {
			next.Label, err = d.String("label")
			if err != nil {
				return err
			}
		}
		if unitID != nil && *unitID != before.UnitID {
			nu, err := GetUnitRow(ctx, tx, orgID, *unitID)
			if err != nil {
				return err
			}
			if nu.SiteID != before.SiteID {
				return problem.Validation(problem.Field{Field: "unit_id", Code: "invalid_value"})
			}
			if err := requireBlockErr(ctx, tx, PermVehicleManage, nu.BlockID); err != nil {
				return err
			}
			next.UnitID = *unitID
		}
		diff, err := audit.Diff("vehicle", map[string]any{"plate": before.Plate, "unit_id": before.UnitID, "label": before.Label},
			map[string]any{"plate": next.Plate, "unit_id": next.UnitID, "label": next.Label})
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE vehicle SET plate = $3, label = $4, unit_id = $5, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, vehicleID, next.Plate, next.Label, next.UnitID); err != nil {
			return err
		}
		if out, err = scanVehicle(tx.QueryRow(ctx, vehicleSelect+` WHERE v.org_id = $1 AND v.id = $2`, orgID, vehicleID)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "vehicle.update", TargetType: "vehicle", TargetID: vehicleID, Diff: diff})
	})
	if errors.Is(err, ErrVersion) {
		return Vehicle{}, VersionConflict("vehicle")
	}
	if err != nil {
		return Vehicle{}, DBProblem(err, "update vehicle")
	}
	return out, nil
}

// GetSiteExport returns one record of a site export or import. It needs sites.export.
func (s *Service) GetSiteExport(ctx context.Context, orgID, exportID uuid.UUID) (SiteExport, error) {
	if err := authz.Require(ctx, PermSiteExport, authz.Scope{OrgID: orgID}); err != nil {
		return SiteExport{}, err
	}
	var out SiteExport
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		out, err = scanExport(tx.QueryRow(ctx, `SELECT `+exportColumns+` FROM site_export WHERE org_id = $1 AND id = $2`, orgID, exportID))
		return err
	})
	if err != nil {
		return SiteExport{}, DBProblem(err, "get site transfer")
	}
	return out, nil
}

// GetBankDetails returns the bank details of a site as stored (empty when none
// were set). It needs sites.bank_details for the site.
func (s *Service) GetBankDetails(ctx context.Context, orgID, siteID uuid.UUID) (map[string]any, int, error) {
	if err := authz.RequireAnywhere(ctx, PermSiteBankDetails); err != nil {
		return nil, 0, err
	}
	var out map[string]any
	var version int
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		site, err := loadSiteFor(ctx, tx, PermSiteBankDetails, orgID, siteID, false, true)
		out, version = site.BankDetails, site.Version
		return err
	})
	if err != nil {
		return nil, 0, DBProblem(err, "get bank details")
	}
	return out, version, nil
}
