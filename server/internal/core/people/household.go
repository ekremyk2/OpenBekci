package people

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// householdAuth lets managers and the unit's owner or tenant manage its
// household (invitations and household occupancies); it needs invitation.household.
func householdAuth(ctx context.Context, tx pgx.Tx, unit tenancy.Unit) error {
	sc, err := authz.ScopeOfBlock(ctx, tx, unit.BlockID)
	if err != nil {
		return err
	}
	if err := authz.Require(ctx, PermInvitationHouse, sc); err != nil {
		return err
	}
	if authz.Has(ctx, PermInvitationManage, sc) {
		return nil
	}
	own, ok := authz.CurrentOccupancy(ctx, unit.ID)
	if !ok || own.Kind == authz.OccHousehold {
		return problem.Forbidden("only the owner or tenant of this unit can manage its household")
	}
	return nil
}

// ListHouseholdInvitations lists the household invitations of a unit. It
// needs invitation.household (owner or tenant of the unit, or a manager).
func (s *Service) ListHouseholdInvitations(ctx context.Context, orgID, unitID uuid.UUID) ([]Invitation, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationHouse); err != nil {
		return nil, err
	}
	out := []Invitation{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := householdAuth(ctx, tx, unit); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, unit.SiteID, false, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, invSelect+` WHERE i.org_id = $1 AND i.unit_id = $2 AND i.kind = 'household' ORDER BY i.id`, orgID, unitID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			inv, err := scanInv(rows)
			if err != nil {
				return err
			}
			out = append(out, inv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, tenancy.DBProblem(err, "list household invitations")
	}
	return out, nil
}

// RevokeHouseholdInvitation cancels a pending household invitation of the
// unit. It needs invitation.household.
func (s *Service) RevokeHouseholdInvitation(ctx context.Context, orgID, unitID, invID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermInvitationHouse); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := householdAuth(ctx, tx, unit); err != nil {
			return err
		}
		inv, err := scanInv(tx.QueryRow(ctx, invSelect+` WHERE i.org_id = $1 AND i.id = $2 AND i.unit_id = $3 AND i.kind = 'household' FOR UPDATE OF i`, orgID, invID, unitID))
		if err != nil {
			return err
		}
		if inv.Status != "pending" {
			return problem.Conflict("only a pending invitation can be revoked")
		}
		if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'revoked', updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, invID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &unit.SiteID, Action: "invitation.revoke", TargetType: "invitation", TargetID: invID})
	})
	return tenancy.DBProblem(err, "revoke household invitation")
}

// EndHouseholdOccupancy ends a household member's occupancy of the unit as of
// today. It needs invitation.household (owner or tenant of the unit, or a manager).
func (s *Service) EndHouseholdOccupancy(ctx context.Context, orgID, unitID, occID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermInvitationHouse); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if err := householdAuth(ctx, tx, unit); err != nil {
			return err
		}
		occ, err := loadOcc(ctx, tx, orgID, occID, true)
		if err != nil {
			return err
		}
		if occ.UnitID != unitID || occ.Kind != KindHousehold {
			return problem.NotFound("no such household member on this unit")
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, unit.SiteID, false, false); err != nil {
			return err
		}
		if !occ.Current && occ.ValidTo != nil {
			return problem.Conflict("this household member has already left")
		}
		end := s.today()
		if from, _ := time.Parse(dateLayout, occ.ValidFrom); !end.After(from) {
			end = from.AddDate(0, 0, 1)
		}
		if _, err := tx.Exec(ctx, `UPDATE occupancy SET valid_to = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, occID, end); err != nil {
			return err
		}
		after := occ
		v := end.Format(dateLayout)
		after.ValidTo = &v
		diff, err := audit.Diff("occupancy", occ.audit(), after.audit())
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &unit.SiteID, Action: "occupancy.end_household", TargetType: "occupancy", TargetID: occID, Diff: diff}); err != nil {
			return err
		}
		return s.occupancyEnded(ctx, tx, orgID, occ, end)
	})
	return tenancy.DBProblem(err, "end household occupancy")
}
