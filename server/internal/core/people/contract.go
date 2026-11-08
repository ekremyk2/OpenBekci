package people

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// The methods in this file were added with the API contract reconciliation:
// single-resource reads, a few deletes and the caller's own ("me") views that
// the draft contract has. They only add to the service.

// GetOccupancy returns one occupancy. It needs occupancies.read for the unit's
// block, or the caller must be an owner or tenant of the unit (their own
// household and co-occupants).
func (s *Service) GetOccupancy(ctx context.Context, orgID, occID uuid.UUID) (Occupancy, error) {
	var out Occupancy
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		o, err := loadOcc(ctx, tx, orgID, occID, false)
		if err != nil {
			return err
		}
		sc, err := o.scope(ctx, tx)
		if err != nil {
			return err
		}
		if !authz.Has(ctx, PermOccupancyRead, sc) {
			own, ok := authz.CurrentOccupancy(ctx, o.UnitID)
			if !ok || own.Kind == authz.OccHousehold {
				return problem.Forbidden("you do not have the " + string(PermOccupancyRead) + " permission here")
			}
		}
		out = o
		return nil
	})
	if err != nil {
		return Occupancy{}, tenancy.DBProblem(err, "get occupancy")
	}
	return out, nil
}

// DeleteOccupancy removes an occupancy entered by mistake (one that has no
// move-out record) if it is still at expectedVersion. It needs occupancies.manage.
func (s *Service) DeleteOccupancy(ctx context.Context, orgID, occID uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermOccupancyManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		o, err := loadOcc(ctx, tx, orgID, occID, true)
		if err != nil {
			return err
		}
		sc, err := o.scope(ctx, tx)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermOccupancyManage, sc); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, o.SiteID, false, false); err != nil {
			return err
		}
		if o.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		var moved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM move_out WHERE org_id = $1 AND (occupancy_id = $2 OR successor_occupancy_id = $2))
			OR EXISTS (SELECT 1 FROM unit_claim WHERE org_id = $1 AND occupancy_id = $2)`, orgID, occID).Scan(&moved); err != nil {
			return err
		}
		if moved {
			return problem.Conflict("the occupancy is referenced by a move-out or a unit claim; end it with a move-out instead")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM occupancy WHERE org_id = $1 AND id = $2`, orgID, occID); err != nil {
			return err
		}
		diff, err := audit.Diff("occupancy", o.audit(), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &o.SiteID, Action: "occupancy.delete", TargetType: "occupancy", TargetID: occID, Diff: diff})
	})
	if errors.Is(err, tenancy.ErrVersion) {
		return tenancy.VersionConflict("occupancy")
	}
	return tenancy.DBProblem(err, "delete occupancy")
}

// MyOccupancies lists the caller's own occupancies in the organization (all of
// them, or only the current ones). It needs me.manage.
func (s *Service) MyOccupancies(ctx context.Context, orgID uuid.UUID, currentOnly bool) ([]Occupancy, error) {
	if err := authz.RequireInOrg(ctx, PermMeManage, orgID); err != nil {
		return nil, err
	}
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return nil, problem.Unauthenticated("authentication is required")
	}
	out := []Occupancy{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, occSelect+` WHERE o.org_id = $1 AND p.user_id = $2 AND s.status = 'active'
			AND (NOT $3 OR (o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date)))
			ORDER BY o.valid_from DESC, o.id`, orgID, p.UserID, currentOnly)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOcc(rows)
			if err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, tenancy.DBProblem(err, "list my occupancies")
	}
	return out, nil
}

// MyPerson returns the caller's own person in the organization (national id masked). It needs me.manage.
func (s *Service) MyPerson(ctx context.Context, orgID uuid.UUID) (Person, error) {
	if err := authz.RequireInOrg(ctx, PermMeManage, orgID); err != nil {
		return Person{}, err
	}
	var out Person
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		pid, err := myPersonID(ctx, tx, orgID)
		if err != nil {
			return err
		}
		pr, err := s.loadPerson(ctx, tx, orgID, pid, false)
		if err != nil {
			return err
		}
		out = s.finish(pr)
		return nil
	})
	if err != nil {
		return Person{}, tenancy.DBProblem(err, "get my person")
	}
	return out, nil
}

func myPersonID(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (uuid.UUID, error) {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return uuid.Nil, problem.Unauthenticated("authentication is required")
	}
	var pid uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, p.UserID).Scan(&pid)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, problem.NotFound("you have no person record in this organization")
	}
	return pid, err
}

// UpdateMyPerson changes the caller's own name, contact or gate phone if the
// person is still at expectedVersion. It needs me.manage.
func (s *Service) UpdateMyPerson(ctx context.Context, orgID uuid.UUID, expectedVersion int, d Doc) (Person, error) {
	if err := authz.RequireInOrg(ctx, PermMeManage, orgID); err != nil {
		return Person{}, err
	}
	if f := d.Unknown("display_name", "email", "phone", "gate_contact_phone"); len(f) > 0 {
		return Person{}, problem.Validation(f...)
	}
	var out Person
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		pid, err := myPersonID(ctx, tx, orgID)
		if err != nil {
			return err
		}
		pr, err := s.loadPerson(ctx, tx, orgID, pid, true)
		if err != nil {
			return err
		}
		if pr.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		var country string
		if err := tx.QueryRow(ctx, `SELECT country_code FROM organization WHERE id = $1`, orgID).Scan(&country); err != nil {
			return err
		}
		in, err := s.parsePerson(d, false, country)
		if err != nil {
			return err
		}
		before := s.finish(pr)
		next := before
		if in.hasDisplayName {
			next.DisplayName = in.displayName
		}
		if in.hasEmail {
			next.Email = in.email
		}
		if in.hasPhone {
			next.Phone = in.phone
		}
		if in.hasGate {
			next.GateContactPhone = in.gatePhone
		}
		diff, err := audit.Diff("person", personAudit(before, false), personAudit(next, false))
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		upd, err := scanPersonRow(tx.QueryRow(ctx, `
			UPDATE person AS p SET display_name = $3, contact = $4, gate_contact_phone = $5, updated_at = now(), version = version + 1
			WHERE p.org_id = $1 AND p.id = $2 RETURNING `+personColumns,
			orgID, pid, next.DisplayName, contactJSON(next.Email, next.Phone), next.GateContactPhone))
		if err != nil {
			return err
		}
		out = s.finish(upd)
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: pr.SiteID, Action: "person.update", TargetType: "person", TargetID: pid, Diff: diff})
	})
	if errors.Is(err, tenancy.ErrVersion) {
		return Person{}, tenancy.VersionConflict("person")
	}
	if err != nil {
		return Person{}, tenancy.DBProblem(err, "update my person")
	}
	return out, nil
}

// GetMembership returns one role grant. It needs memberships.read for it.
func (s *Service) GetMembership(ctx context.Context, orgID, membershipID uuid.UUID) (Membership, error) {
	if err := authz.RequireAnywhere(ctx, PermMembershipRead); err != nil {
		return Membership{}, err
	}
	orgWide, sites := siteGrants(ctx, PermMembershipRead)
	var out Membership
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, err := scanMembership(tx.QueryRow(ctx, membershipSelect+` WHERE m.org_id = $1 AND m.id = $2`, orgID, membershipID))
		if err != nil {
			return err
		}
		if !orgWide {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT ($2 = 'site' AND $3::uuid = ANY($4))
				OR ($2 = 'block' AND EXISTS (SELECT 1 FROM block b WHERE b.org_id = $1 AND b.id = $3 AND b.site_id = ANY($4)))`,
				orgID, m.ScopeType, m.ScopeID, sites).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return problem.Forbidden("you do not have the " + string(PermMembershipRead) + " permission for this membership")
			}
		}
		out = m
		return nil
	})
	if err != nil {
		return Membership{}, tenancy.DBProblem(err, "get membership")
	}
	return out, nil
}

// MoveOutFilter narrows ListMoveOuts; From and To are dates (YYYY-MM-DD) bounding as_of.
type MoveOutFilter struct {
	SiteID, UnitID, PersonID *uuid.UUID
	From, To                 string
}

// ListMoveOuts lists move-out records. It needs people.move_out for the sites or blocks.
func (s *Service) ListMoveOuts(ctx context.Context, orgID uuid.UUID, f MoveOutFilter, p tenancy.PageParams) ([]MoveOut, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMoveOutRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	orgWide, sites, blocks := authz.GrantsFromContext(ctx).ScopeIDs(PermMoveOutRead)
	var out []MoveOut
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, moveOutSelect+` WHERE m.org_id = $1 AND m.id > $2 AND ($3 OR m.site_id = ANY($4) OR u.block_id = ANY($5))
			AND ($6::uuid IS NULL OR m.site_id = $6) AND ($7::uuid IS NULL OR m.unit_id = $7) AND ($8::uuid IS NULL OR m.person_id = $8)
			AND ($9 = '' OR m.as_of >= $9::date) AND ($10 = '' OR m.as_of <= $10::date) ORDER BY m.id LIMIT $11`,
			orgID, after, orgWide, sites, blocks, f.SiteID, f.UnitID, f.PersonID, f.From, f.To, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMoveOut(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list move-outs")
	}
	items, next := tenancy.Page(out, limit, func(m MoveOut) uuid.UUID { return m.ID })
	return items, next, nil
}

// GetInvitation returns one invitation as managers see it. It needs invitations.manage for its site.
func (s *Service) GetInvitation(ctx context.Context, orgID, invID uuid.UUID) (Invitation, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return Invitation{}, err
	}
	var out Invitation
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		inv, err := scanInv(tx.QueryRow(ctx, invSelect+` WHERE i.org_id = $1 AND i.id = $2`, orgID, invID))
		if err != nil {
			return err
		}
		if _, err := requireSiteScope(ctx, tx, PermInvitationManage, inv.SiteID); err != nil {
			return err
		}
		out = inv
		return nil
	})
	if err != nil {
		return Invitation{}, tenancy.DBProblem(err, "get invitation")
	}
	return out, nil
}

// GetClaim returns one unit claim. It needs claims.decide for its site.
func (s *Service) GetClaim(ctx context.Context, orgID, claimID uuid.UUID) (UnitClaim, error) {
	if err := authz.RequireAnywhere(ctx, PermClaimManage); err != nil {
		return UnitClaim{}, err
	}
	var out UnitClaim
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		c, err := scanClaim(tx.QueryRow(ctx, claimSelect+` WHERE c.org_id = $1 AND c.id = $2`, orgID, claimID))
		if err != nil {
			return err
		}
		if _, err := requireSiteScope(ctx, tx, PermClaimManage, c.SiteID); err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return UnitClaim{}, tenancy.DBProblem(err, "get claim")
	}
	return out, nil
}

// JoinCodeState is the site's join code with whether it is switched on.
type JoinCodeState struct {
	JoinCode
	Enabled bool
}

// GetJoinCodeState returns the site's latest join code and whether it works. It
// needs claims.decide for the site.
func (s *Service) GetJoinCodeState(ctx context.Context, orgID, siteID uuid.UUID) (JoinCodeState, error) {
	if err := authz.RequireAnywhere(ctx, PermClaimManage); err != nil {
		return JoinCodeState{}, err
	}
	var out JoinCodeState
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if _, err = requireSiteScope(ctx, tx, PermClaimManage, siteID); err != nil {
			return err
		}
		if _, err = tenancy.LoadSite(ctx, tx, orgID, siteID, false, true); err != nil {
			return err
		}
		out, err = s.latestJoinCode(ctx, tx, orgID, siteID)
		return err
	})
	if err != nil {
		return JoinCodeState{}, tenancy.DBProblem(err, "join code")
	}
	return out, nil
}

func (s *Service) latestJoinCode(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (JoinCodeState, error) {
	var jc JoinCodeState
	var status string
	err := tx.QueryRow(ctx, `SELECT site_id, code, uses, created_at, status FROM invitation WHERE org_id = $1 AND site_id = $2 AND kind = 'site_code'
		AND status IN ('pending', 'revoked') ORDER BY created_at DESC, id DESC LIMIT 1`, orgID, siteID).Scan(&jc.SiteID, &jc.Code, &jc.Uses, &jc.CreatedAt, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return JoinCodeState{}, problem.NotFound("this site has no join code yet")
	}
	jc.CreatedAt = jc.CreatedAt.UTC()
	jc.URL = s.joinURL(jc.Code)
	jc.Enabled = status == "pending"
	return jc, err
}

// SetJoinCodeEnabled switches the site's join code off (the code stops
// working) or on again (a new one is made when the site never had one). It
// needs claims.decide for the site.
func (s *Service) SetJoinCodeEnabled(ctx context.Context, orgID, siteID uuid.UUID, enabled bool) (JoinCodeState, error) {
	if err := authz.RequireAnywhere(ctx, PermClaimManage); err != nil {
		return JoinCodeState{}, err
	}
	var out JoinCodeState
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireSiteScope(ctx, tx, PermClaimManage, siteID); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, siteID, true, false); err != nil {
			return err
		}
		cur, err := s.latestJoinCode(ctx, tx, orgID, siteID)
		missing := false
		if err != nil {
			var pe *problem.Error
			if !errors.As(err, &pe) || pe.Status != 404 {
				return err
			}
			missing = true
		}
		switch {
		case missing && enabled:
			code, _ := newScopedToken(orgID, 16)
			if _, err := tx.Exec(ctx, `INSERT INTO invitation (id, org_id, site_id, kind, channel, code, invited_by) VALUES ($1, $2, $3, 'site_code', 'link', $4, $5)`,
				id.New(), orgID, siteID, code, principalID(ctx)); err != nil {
				return err
			}
		case missing:
			return problem.NotFound("this site has no join code yet")
		case enabled && !cur.Enabled:
			if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'pending', updated_at = now(), version = version + 1
				WHERE org_id = $1 AND site_id = $2 AND code = $3 AND kind = 'site_code'`, orgID, siteID, cur.Code); err != nil {
				return err
			}
		case !enabled && cur.Enabled:
			if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'revoked', updated_at = now(), version = version + 1
				WHERE org_id = $1 AND site_id = $2 AND kind = 'site_code' AND status = 'pending'`, orgID, siteID); err != nil {
				return err
			}
		}
		if out, err = s.latestJoinCode(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "site.set_join_code_enabled", TargetType: "site", TargetID: siteID,
			Diff: map[string]audit.Change{"enabled": {After: mustJSON(enabled)}}})
	})
	if err != nil {
		return JoinCodeState{}, tenancy.DBProblem(err, "join code")
	}
	return out, nil
}

// RotatedJoinCode is RotateJoinCode with the draft's `enabled` flag (always true after a rotation).
func (s *Service) RotatedJoinCode(ctx context.Context, orgID, siteID uuid.UUID) (JoinCodeState, error) {
	if err := authz.RequireAnywhere(ctx, PermClaimManage); err != nil {
		return JoinCodeState{}, err
	}
	jc, err := s.RotateJoinCode(ctx, orgID, siteID)
	return JoinCodeState{JoinCode: jc, Enabled: true}, err
}

var _ = slices.Contains[[]string]

// RevealNationalID returns the full national id of a person (empty when none is
// set). It needs people.national_id and a recent re-authentication, and writes
// an audit event (without the value) every time.
func (s *Service) RevealNationalID(ctx context.Context, orgID, personID uuid.UUID) (string, error) {
	if err := authz.RequireInOrg(ctx, PermNationalID, orgID); err != nil {
		return "", err
	}
	pr := authz.PrincipalFromContext(ctx)
	if pr == nil {
		return "", problem.Unauthenticated("authentication is required")
	}
	if err := pr.RequireReauth(s.now()); err != nil {
		return "", err
	}
	var out string
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		row, err := s.loadPerson(ctx, tx, orgID, personID, false)
		if err != nil {
			return err
		}
		ok, err := canSee(ctx, tx, orgID, personID, PermNationalID)
		if err != nil {
			return err
		}
		if !ok {
			return problem.Forbidden("you do not have the " + string(PermNationalID) + " permission for this person")
		}
		if len(row.sealed) > 0 && s.cipher != nil {
			pt, err := s.cipher.Open(row.sealed, row.ID[:])
			if err != nil {
				return err
			}
			out = string(pt)
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: row.SiteID, Action: "person.national_id_reveal", TargetType: "person", TargetID: personID})
	})
	if err != nil {
		return "", tenancy.DBProblem(err, "reveal national id")
	}
	return out, nil
}

// MyHouseholdMembers lists the current occupancies of the unit sides the caller
// owns or rents (the caller's own and everyone on the same side), optionally of
// one unit. It needs invitations.household.
func (s *Service) MyHouseholdMembers(ctx context.Context, orgID uuid.UUID, unitID *uuid.UUID) ([]Occupancy, error) {
	if err := authz.RequireInOrg(ctx, PermInvitationHouse, orgID); err != nil {
		return nil, err
	}
	out := []Occupancy{}
	for _, mine := range authz.GrantsFromContext(ctx).Current() {
		if mine.Kind == authz.OccHousehold || (unitID != nil && mine.UnitID != *unitID) {
			continue
		}
		err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, occSelect+` WHERE o.org_id = $1 AND o.unit_id = $2 AND o.side = $3 AND s.status = 'active'
				AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date)
				ORDER BY o.id`, orgID, mine.UnitID, string(mine.Side))
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				o, err := scanOcc(rows)
				if err != nil {
					return err
				}
				out = append(out, o)
			}
			return rows.Err()
		})
		if err != nil {
			return nil, tenancy.DBProblem(err, "list household members")
		}
	}
	return out, nil
}
