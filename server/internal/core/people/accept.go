package people

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func errInviteInvalid() *problem.Error {
	return problem.NotFound("this invitation is not valid: it may have expired, been used or been revoked")
}

// InvitationPreview is what the holder of an invitation link sees before accepting.
type InvitationPreview struct {
	Kind          string     `json:"kind"`
	OrgName       string     `json:"org_name"`
	SiteName      string     `json:"site_name"`
	DoorNumber    *string    `json:"door_number"`
	OccupancyKind *string    `json:"occupancy_kind"`
	InvitedName   string     `json:"invited_name"`
	Channel       string     `json:"channel"`
	ContactHint   string     `json:"contact_hint"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

func hint(s string) string {
	if at := strings.IndexByte(s, '@'); at > 1 {
		return s[:1] + "***" + s[at:]
	}
	if len(s) > 5 {
		return s[:3] + strings.Repeat("*", len(s)-5) + s[len(s)-2:]
	}
	return "***"
}

type invRow struct {
	ID, SiteID, PersonID uuid.UUID
	UnitID               *uuid.UUID
	Kind, Channel        string
	OccKind, OccSide     *string
	SharePct             *float64
	Correspondent        bool
	Resides              bool
	ValidFrom            *time.Time
	Email, Phone         *string
	Locale               string
	ExpiresAt            *time.Time
}

func (s *Service) findInvitation(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, token string, forUpdate bool) (invRow, error) {
	var r invRow
	q := `SELECT id, site_id, person_id, unit_id, kind, channel, occupancy_kind, occupancy_side, share_pct::float8, is_correspondent, resides, valid_from, email, phone, locale, expires_at
		FROM invitation WHERE org_id = $1 AND token_hash = $2 AND status = 'pending' AND (expires_at IS NULL OR expires_at > now())`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, q, orgID, crypto.HashToken(token)).Scan(&r.ID, &r.SiteID, &r.PersonID, &r.UnitID, &r.Kind, &r.Channel, &r.OccKind, &r.OccSide, &r.SharePct,
		&r.Correspondent, &r.Resides, &r.ValidFrom, &r.Email, &r.Phone, &r.Locale, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, errInviteInvalid()
	}
	return r, err
}

// PreviewInvitation describes a pending invitation to its link's holder. It
// needs no sign-in; the token is the secret.
func (s *Service) PreviewInvitation(ctx context.Context, token string) (InvitationPreview, error) {
	orgID, ok := parseScopedToken(token)
	if !ok {
		return InvitationPreview{}, errInviteInvalid()
	}
	var out InvitationPreview
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		r, err := s.findInvitation(ctx, tx, orgID, token, false)
		if err != nil {
			return err
		}
		out = InvitationPreview{Kind: r.Kind, Channel: r.Channel, OccupancyKind: r.OccKind, ExpiresAt: r.ExpiresAt}
		if r.Phone != nil {
			out.ContactHint = hint(*r.Phone)
		} else if r.Email != nil {
			out.ContactHint = hint(*r.Email)
		}
		if err := tx.QueryRow(ctx, `SELECT o.name, s.name FROM site s JOIN organization o ON o.id = s.org_id WHERE s.org_id = $1 AND s.id = $2 AND s.status = 'active'`, orgID, r.SiteID).Scan(&out.OrgName, &out.SiteName); err != nil {
			return errInviteInvalid()
		}
		if r.UnitID != nil {
			if err := tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id = $1 AND id = $2`, orgID, *r.UnitID).Scan(&out.DoorNumber); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id = $1 AND id = $2`, orgID, r.PersonID).Scan(&out.InvitedName)
	})
	if err != nil {
		return InvitationPreview{}, tenancy.DBProblem(err, "invitation preview")
	}
	return out, nil
}

// AcceptResult says what accepting an invitation did.
type AcceptResult struct {
	OrgID       uuid.UUID  `json:"org_id"`
	SiteID      uuid.UUID  `json:"site_id"`
	PersonID    uuid.UUID  `json:"person_id"`
	UserID      uuid.UUID  `json:"user_id"`
	OccupancyID *uuid.UUID `json:"occupancy_id"`
	UserCreated bool       `json:"user_created"`
	// SignInRequired is true unless the caller was already signed in as the linked account.
	SignInRequired bool `json:"sign_in_required"`
}

// AcceptInput is the optional content of an acceptance.
type AcceptInput struct {
	// Password sets the password of a newly created account.
	Password string
	Locale   string
}

// AcceptInvitation accepts a personal invitation: it links the signed-in
// account, or finds or creates the account for the invited contact (the link
// proves the contact, like a magic link), links it to the invited person and
// creates the occupancy. The caller then signs in with any method. The
// invitation is single use.
func (s *Service) AcceptInvitation(ctx context.Context, token string, in AcceptInput) (AcceptResult, error) {
	orgID, ok := parseScopedToken(token)
	if !ok {
		return AcceptResult{}, errInviteInvalid()
	}
	var first invRow
	if err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		first, err = s.findInvitation(ctx, tx, orgID, token, false)
		return err
	}); err != nil {
		return AcceptResult{}, tenancy.DBProblem(err, "accept invitation")
	}
	res := AcceptResult{OrgID: orgID, SiteID: first.SiteID, PersonID: first.PersonID, SignInRequired: true}
	if p := authz.PrincipalFromContext(ctx); p != nil {
		res.UserID, res.SignInRequired = p.UserID, false
	} else {
		identifier := ""
		switch {
		case first.Phone != nil:
			identifier = *first.Phone
		case first.Email != nil:
			identifier = *first.Email
		}
		u, found, err := s.ident.FindUser(ctx, identifier)
		if err != nil {
			return AcceptResult{}, err
		}
		if !found {
			params := identity.CreateUserParams{Password: in.Password, Locale: first.Locale}
			if in.Locale != "" {
				params.Locale = in.Locale
			}
			if first.Phone != nil {
				params.Phone = *first.Phone
			}
			if first.Email != nil {
				params.Email = *first.Email
			}
			if u, err = s.ident.CreateUser(ctx, params); err != nil {
				return AcceptResult{}, err
			}
			res.UserCreated = true
		}
		res.UserID = u.ID
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		r, err := s.findInvitation(ctx, tx, orgID, token, true)
		if err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, r.SiteID, false, false)
		if err != nil {
			return errInviteInvalid()
		}
		personID := r.PersonID
		var existing uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, res.UserID).Scan(&existing)
		switch {
		case err == nil:
			personID = existing // the account already has a person here: the occupancy goes to it
		case errors.Is(err, pgx.ErrNoRows):
			tag, err := tx.Exec(ctx, `UPDATE person SET user_id = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2 AND user_id IS NULL`, orgID, personID, res.UserID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return problem.Conflict("the invited person is already linked to another account")
			}
		default:
			return err
		}
		res.PersonID = personID
		if r.UnitID != nil && r.OccKind != nil {
			unit, err := tenancy.GetUnitRow(ctx, tx, orgID, *r.UnitID)
			if err != nil {
				return err
			}
			from := s.today()
			if r.ValidFrom != nil {
				from = *r.ValidFrom
			}
			side := SideOwner
			if r.OccSide != nil {
				side = *r.OccSide
			}
			o, err := s.insertOccTx(ctx, tx, orgID, unit, occInput{unitID: unit.ID, personID: personID, kind: *r.OccKind, side: side, sharePct: r.SharePct,
				correspondent: r.Correspondent, resides: r.Resides, from: from})
			if err != nil {
				return err
			}
			res.OccupancyID = &o.ID
		}
		if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'accepted', accepted_by = $3, accepted_at = now(), uses = uses + 1, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, r.ID, res.UserID); err != nil {
			return err
		}
		uid := res.UserID
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &site.ID, ActorKind: audit.ActorUser, ActorUserID: &uid, Action: "invitation.accept", TargetType: "invitation", TargetID: r.ID})
	})
	if err != nil {
		return AcceptResult{}, tenancy.DBProblem(err, "accept invitation")
	}
	return res, nil
}

// JoinUnit is a unit the signed-in visitor of a join code can claim.
type JoinUnit struct {
	ID         uuid.UUID `json:"id"`
	DoorNumber string    `json:"door_number"`
	BlockName  string    `json:"block_name"`
	Type       string    `json:"type"`
}

// JoinPreview is what the site's join code shows.
type JoinPreview struct {
	OrgName  string `json:"org_name"`
	SiteName string `json:"site_name"`
	// Units are listed only to a signed-in visitor.
	Units []JoinUnit `json:"units"`
}

func (s *Service) findJoinCode(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, code string) (siteID uuid.UUID, invID uuid.UUID, err error) {
	err = tx.QueryRow(ctx, `SELECT i.site_id, i.id FROM invitation i JOIN site s ON s.org_id = i.org_id AND s.id = i.site_id AND s.status = 'active'
		WHERE i.org_id = $1 AND i.code = $2 AND i.kind = 'site_code' AND i.status = 'pending'`, orgID, code).Scan(&siteID, &invID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, problem.NotFound("this join code is not valid")
	}
	return siteID, invID, err
}

// PreviewJoin shows the site behind a join code; signed-in visitors also get
// the units they can claim.
func (s *Service) PreviewJoin(ctx context.Context, code string) (JoinPreview, error) {
	return s.previewJoin(ctx, code, authz.PrincipalFromContext(ctx) != nil)
}

// PreviewJoinUnits is PreviewJoin with the unit list for any visitor: the draft
// contract lists the units behind a join code without a sign-in (the code on
// the site's notice board is the secret).
func (s *Service) PreviewJoinUnits(ctx context.Context, code string) (JoinPreview, error) {
	return s.previewJoin(ctx, code, true)
}

func (s *Service) previewJoin(ctx context.Context, code string, withUnits bool) (JoinPreview, error) {
	orgID, ok := parseScopedToken(code)
	if !ok {
		return JoinPreview{}, problem.NotFound("this join code is not valid")
	}
	out := JoinPreview{Units: []JoinUnit{}}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		siteID, _, err := s.findJoinCode(ctx, tx, orgID, code)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT o.name, s.name FROM site s JOIN organization o ON o.id = s.org_id WHERE s.org_id = $1 AND s.id = $2`, orgID, siteID).Scan(&out.OrgName, &out.SiteName); err != nil {
			return err
		}
		if !withUnits {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT u.id, u.door_number, b.name, u.type FROM unit u JOIN block b ON b.org_id = u.org_id AND b.id = u.block_id
			WHERE u.org_id = $1 AND u.site_id = $2 AND u.status = 'active' AND u.type <> 'common' ORDER BY b.sort_order, u.door_number`, orgID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u JoinUnit
			if err := rows.Scan(&u.ID, &u.DoorNumber, &u.BlockName, &u.Type); err != nil {
				return err
			}
			out.Units = append(out.Units, u)
		}
		return rows.Err()
	})
	if err != nil {
		return JoinPreview{}, tenancy.DBProblem(err, "join preview")
	}
	return out, nil
}

// UnitClaim is a resident's request to be linked to a unit, awaiting approval.
type UnitClaim struct {
	ID           uuid.UUID  `json:"id"`
	SiteID       uuid.UUID  `json:"site_id"`
	UnitID       uuid.UUID  `json:"unit_id"`
	DoorNumber   string     `json:"door_number"`
	PersonID     uuid.UUID  `json:"person_id"`
	PersonName   string     `json:"person_name"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	Message      *string    `json:"message"`
	DecidedAt    *time.Time `json:"decided_at"`
	DecisionNote *string    `json:"decision_note"`
	OccupancyID  *uuid.UUID `json:"occupancy_id"`
	CreatedAt    time.Time  `json:"created_at"`
}

const claimSelect = `SELECT c.id, c.site_id, c.unit_id, u.door_number, c.person_id, p.display_name, c.kind, c.status, c.message, c.decided_at, c.decision_note, c.occupancy_id, c.created_at
	FROM unit_claim c JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id JOIN person p ON p.org_id = c.org_id AND p.id = c.person_id`

func scanClaim(row pgx.Row) (UnitClaim, error) {
	var c UnitClaim
	err := row.Scan(&c.ID, &c.SiteID, &c.UnitID, &c.DoorNumber, &c.PersonID, &c.PersonName, &c.Kind, &c.Status, &c.Message, &c.DecidedAt, &c.DecisionNote, &c.OccupancyID, &c.CreatedAt)
	c.CreatedAt = c.CreatedAt.UTC()
	return c, err
}

// CreateClaim files the signed-in caller's claim to a unit through a join
// code. The caller gets a person in the organization (their account's
// contact), but no rights until a manager approves.
func (s *Service) CreateClaim(ctx context.Context, code string, d Doc) (UnitClaim, error) {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return UnitClaim{}, problem.Unauthenticated("sign in to claim a unit")
	}
	orgID, ok := parseScopedToken(code)
	if !ok {
		return UnitClaim{}, problem.NotFound("this join code is not valid")
	}
	fields := d.Unknown("unit_id", "kind", "display_name", "message")
	var unitID uuid.UUID
	var kind string
	if !d.Has("unit_id") {
		fields = append(fields, problem.Field{Field: "unit_id", Code: "required"})
	} else if err := d.Get("unit_id", &unitID); err != nil {
		return UnitClaim{}, err
	}
	if err := d.Get("kind", &kind); err != nil {
		return UnitClaim{}, err
	}
	if kind != KindOwner && kind != KindTenant && kind != KindHousehold {
		fields = append(fields, problem.Field{Field: "kind", Code: map[bool]string{true: "required", false: "invalid_value"}[kind == ""]})
	}
	name, err := d.String("display_name")
	if err != nil {
		return UnitClaim{}, err
	}
	message, err := d.String("message")
	if err != nil {
		return UnitClaim{}, err
	}
	if message != nil && len(*message) > 1000 {
		fields = append(fields, problem.Field{Field: "message", Code: "too_long", Params: map[string]any{"max": 1000}})
	}
	if len(fields) > 0 {
		return UnitClaim{}, problem.Validation(fields...)
	}
	var out UnitClaim
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		siteID, invID, err := s.findJoinCode(ctx, tx, orgID, code)
		if err != nil {
			return err
		}
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		if unit.SiteID != siteID || unit.Type == "common" {
			return fieldProblem("unit_id", "invalid_value")
		}
		var personID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, p.UserID).Scan(&personID)
		if errors.Is(err, pgx.ErrNoRows) {
			var email, phone *string
			if err := tx.QueryRow(ctx, `SELECT email::text, phone FROM "user" WHERE id = $1`, p.UserID).Scan(&email, &phone); err != nil {
				return err
			}
			dn := ""
			switch {
			case name != nil && strings.TrimSpace(*name) != "":
				dn = strings.TrimSpace(*name)
			case email != nil:
				dn = *email
			case phone != nil:
				dn = *phone
			}
			person, err := s.createPersonTx(ctx, tx, orgID, &siteID, &p.UserID, personInput{displayName: dn, email: email, phone: phone})
			if err != nil {
				return err
			}
			personID = person.ID
		} else if err != nil {
			return err
		}
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND person_id = $3 AND kind = $4 AND (valid_to IS NULL OR valid_to > $5))`,
			orgID, unitID, personID, kind, s.today()).Scan(&dup); err != nil {
			return err
		}
		if dup {
			return problem.Conflict("you already have this role on this unit")
		}
		cid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO unit_claim (id, org_id, site_id, unit_id, person_id, user_id, invitation_id, kind, message) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			cid, orgID, siteID, unitID, personID, p.UserID, invID, kind, message); err != nil {
			if tenancy.IsUnique(err, "unit_claim_one_pending") {
				return problem.Conflict("you already have a pending claim for this unit")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invitation SET uses = uses + 1, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, invID); err != nil {
			return err
		}
		if out, err = scanClaim(tx.QueryRow(ctx, claimSelect+` WHERE c.org_id = $1 AND c.id = $2`, orgID, cid)); err != nil {
			return err
		}
		diff, err := audit.Diff("unit_claim", nil, map[string]any{"unit_id": unitID, "kind": kind, "message": message})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "claim.create", TargetType: "unit_claim", TargetID: cid, Diff: diff})
	})
	if err != nil {
		return UnitClaim{}, tenancy.DBProblem(err, "claim")
	}
	return out, nil
}

// ListClaims lists unit claims, optionally of one site or status (default
// pending). It needs claim.manage for the sites.
func (s *Service) ListClaims(ctx context.Context, orgID uuid.UUID, siteID *uuid.UUID, status string, p tenancy.PageParams) ([]UnitClaim, string, error) {
	if err := authz.RequireAnywhere(ctx, PermClaimManage); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "approved" && status != "rejected" && status != "all" {
		return nil, "", fieldProblem("status", "invalid_value")
	}
	orgWide, sites := siteGrants(ctx, PermClaimManage)
	var out []UnitClaim
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if siteID != nil {
			if _, err := authz.ScopeOfSite(ctx, tx, *siteID); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, claimSelect+` WHERE c.org_id = $1 AND c.id > $2 AND ($3 OR c.site_id = ANY($4)) AND ($5::uuid IS NULL OR c.site_id = $5)
			AND ($6 = 'all' OR c.status = $6) ORDER BY c.id LIMIT $7`, orgID, after, orgWide, sites, siteID, status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanClaim(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list claims")
	}
	items, next := tenancy.Page(out, limit, func(c UnitClaim) uuid.UUID { return c.ID })
	return items, next, nil
}

func (s *Service) decideClaim(ctx context.Context, orgID, claimID uuid.UUID, approve bool, d Doc) (UnitClaim, error) {
	if err := authz.RequireAnywhere(ctx, PermClaimManage); err != nil {
		return UnitClaim{}, err
	}
	allowed := []string{"note"}
	if approve {
		allowed = []string{"share_pct", "is_correspondent", "valid_from", "side", "note"}
	}
	if f := d.Unknown(allowed...); len(f) > 0 {
		return UnitClaim{}, problem.Validation(f...)
	}
	note, err := d.String("note")
	if err != nil {
		return UnitClaim{}, err
	}
	var out UnitClaim
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		c, err := scanClaim(tx.QueryRow(ctx, claimSelect+` WHERE c.org_id = $1 AND c.id = $2 FOR UPDATE OF c`, orgID, claimID))
		if err != nil {
			return err
		}
		if _, err := requireSiteScope(ctx, tx, PermClaimManage, c.SiteID); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, c.SiteID, false, false); err != nil {
			return err
		}
		if c.Status != "pending" {
			return problem.Conflict("this claim was already decided")
		}
		var uid *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			uid = &u
		}
		var occID *uuid.UUID
		status := "rejected"
		if approve {
			status = "approved"
			unit, err := tenancy.GetUnitRow(ctx, tx, orgID, c.UnitID)
			if err != nil {
				return err
			}
			od := Doc{"person_id": mustJSON(c.PersonID), "kind": mustJSON(c.Kind)}
			for _, k := range []string{"share_pct", "is_correspondent", "valid_from", "side"} {
				if d.Has(k) {
					od[k] = d[k]
				}
			}
			in, err := parseOcc(ctx, tx, orgID, unit, od, s.today())
			if err != nil {
				return err
			}
			o, err := s.insertOccTx(ctx, tx, orgID, unit, in)
			if err != nil {
				return err
			}
			occID = &o.ID
		}
		if _, err := tx.Exec(ctx, `UPDATE unit_claim SET status = $3, decided_by = $4, decided_at = now(), decision_note = $5, occupancy_id = $6, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, claimID, status, uid, note, occID); err != nil {
			return err
		}
		if out, err = scanClaim(tx.QueryRow(ctx, claimSelect+` WHERE c.org_id = $1 AND c.id = $2`, orgID, claimID)); err != nil {
			return err
		}
		diff, err := audit.Diff("unit_claim", map[string]any{"status": "pending"}, map[string]any{"status": status, "decision_note": note, "occupancy_id": occID})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &c.SiteID, Action: "claim." + map[bool]string{true: "approve", false: "reject"}[approve], TargetType: "unit_claim", TargetID: claimID, Diff: diff})
	})
	if err != nil {
		return UnitClaim{}, tenancy.DBProblem(err, "claim decision")
	}
	return out, nil
}

// ApproveClaim approves a pending claim and creates the occupancy it asked
// for. It needs claim.manage for the site.
func (s *Service) ApproveClaim(ctx context.Context, orgID, claimID uuid.UUID, d Doc) (UnitClaim, error) {
	return s.decideClaim(ctx, orgID, claimID, true, d)
}

// RejectClaim rejects a pending claim. It needs claim.manage for the site.
func (s *Service) RejectClaim(ctx context.Context, orgID, claimID uuid.UUID, d Doc) (UnitClaim, error) {
	return s.decideClaim(ctx, orgID, claimID, false, d)
}
