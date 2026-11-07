package people

import (
	"context"
	"errors"
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

// writeInvitation adds the person (when new) and the invitation row inside tx.
// invKind is InviteManager or InviteHousehold; in.kind is what accepting creates.
func (s *Service) writeInvitation(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, invKind string, in inviteInput, unit tenancy.Unit, site tenancy.Site) (Invitation, string, error) {
	var person Person
	if in.personID != nil {
		pr, err := s.loadPerson(ctx, tx, orgID, *in.personID, false)
		if err != nil {
			return Invitation{}, "", err
		}
		person = s.finish(pr)
	} else {
		var err error
		sid := site.ID
		if person, err = s.createPersonTx(ctx, tx, orgID, &sid, nil, in.person); err != nil {
			return Invitation{}, "", err
		}
	}
	if person.UserID != nil {
		return Invitation{}, "", problem.Conflict("this person already has an account; add the occupancy directly")
	}
	channel := in.channel
	if channel == "" {
		channel = map[bool]string{true: "sms", false: "email"}[person.Phone != nil]
	}
	if err := s.channelAvailable(channel); err != nil {
		return Invitation{}, "", err
	}
	if (channel == "sms" && person.Phone == nil) || (channel == "email" && person.Email == nil) {
		return Invitation{}, "", fieldProblem(map[bool]string{true: "phone", false: "email"}[channel == "sms"], "required")
	}
	side := in.side
	switch in.kind {
	case KindOwner:
		side = SideOwner
	case KindTenant:
		side = SideTenant
	default:
		if side != SideOwner && side != SideTenant {
			var err error
			if side, err = defaultSide(ctx, tx, orgID, unit.ID, s.today()); err != nil {
				return Invitation{}, "", err
			}
		}
	}
	token, hash := newScopedToken(orgID, crypto.TokenBytes)
	iid := id.New()
	expires := s.now().Add(time.Duration(in.days) * 24 * time.Hour)
	var uid *uuid.UUID
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		uid = &u
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invitation (id, org_id, site_id, kind, unit_id, person_id, occupancy_kind, occupancy_side, share_pct, is_correspondent, resides, valid_from,
			channel, email, phone, locale, token_hash, expires_at, invited_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		iid, orgID, site.ID, invKind, unit.ID, person.ID, in.kind, side, in.sharePct, in.correspondent, in.resides, in.from,
		channel, person.Email, person.Phone, in.locale, hash, expires, uid); err != nil {
		return Invitation{}, "", err
	}
	inv, err := scanInv(tx.QueryRow(ctx, invSelect+` WHERE i.org_id = $1 AND i.id = $2`, orgID, iid))
	if err != nil {
		return Invitation{}, "", err
	}
	diff, err := audit.Diff("invitation", nil, map[string]any{"kind": invKind, "unit_id": unit.ID, "occupancy_kind": in.kind, "channel": channel, "email": person.Email, "phone": person.Phone})
	if err != nil {
		return Invitation{}, "", err
	}
	if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &site.ID, Action: "invitation.create", TargetType: "invitation", TargetID: iid, Diff: diff}); err != nil {
		return Invitation{}, "", err
	}
	return inv, token, nil
}

func (s *Service) linkFor(client, token string) string {
	c := identity.ClientWeb
	if client == "mobile" {
		c = identity.ClientMobile
	}
	return identity.LinkURL(s.cfg.BaseURL, c, identity.LinkInvite, token)
}

// afterCommit sends the invitation and returns it with the one-time token and link.
func (s *Service) afterCommit(ctx context.Context, orgID uuid.UUID, inv Invitation, token, client, siteName, locale string) Invitation {
	link := s.linkFor(client, token)
	inv.Delivery = s.deliver(ctx, orgID, inv.ID, inv.Channel, inv.Email, inv.Phone, locale, siteName, link)
	if inv.Delivery == "sent" {
		inv.SendCount++
		now := s.now().UTC()
		inv.LastSentAt = &now
	}
	inv.Token, inv.Link = &token, &link
	return inv
}

// CreateInvitation invites a person to a unit: it adds the person if needed
// and sends a one-time link by SMS or email that creates or links their
// account and the occupancy when accepted. It needs invitation.manage for the site.
func (s *Service) CreateInvitation(ctx context.Context, orgID uuid.UUID, d Doc) (Invitation, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return Invitation{}, err
	}
	in, err := s.parseInvite(d, false)
	if err != nil {
		return Invitation{}, err
	}
	if in.siteID == nil {
		return Invitation{}, fieldProblem("site_id", "required")
	}
	var inv Invitation
	var token string
	var site tenancy.Site
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, *in.siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermInvitationManage, sc); err != nil {
			return err
		}
		if site, err = tenancy.LoadSite(ctx, tx, orgID, *in.siteID, false, false); err != nil {
			return err
		}
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, *in.unitID)
		if err != nil {
			return err
		}
		if unit.SiteID != site.ID {
			return fieldProblem("unit_id", "invalid_value")
		}
		inv, token, err = s.writeInvitation(ctx, tx, orgID, InviteManager, in, unit, site)
		return err
	})
	if err != nil {
		return Invitation{}, tenancy.DBProblem(err, "invitation")
	}
	return s.afterCommit(ctx, orgID, inv, token, in.client, site.Name, in.locale), nil
}

// CreateHouseholdInvitation lets the owner or tenant of a unit invite a
// household member to it (S4); managers may too. The invitation creates a
// household occupancy on that unit only and grants no money access. It needs
// invitation.household and, for portal users, a current owner or tenant occupancy on the unit.
func (s *Service) CreateHouseholdInvitation(ctx context.Context, orgID, unitID uuid.UUID, d Doc) (Invitation, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationHouse); err != nil {
		return Invitation{}, err
	}
	in, err := s.parseInvite(d, true)
	if err != nil {
		return Invitation{}, err
	}
	var inv Invitation
	var token string
	var site tenancy.Site
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		unit, err := tenancy.GetUnitRow(ctx, tx, orgID, unitID)
		if err != nil {
			return err
		}
		sc := authz.Scope{OrgID: orgID, SiteID: &unit.SiteID, BlockID: &unit.BlockID}
		if err := authz.Require(ctx, PermInvitationHouse, sc); err != nil {
			return err
		}
		if !authz.Has(ctx, PermInvitationManage, sc) {
			own, ok := authz.CurrentOccupancy(ctx, unitID)
			if !ok || own.Kind == authz.OccHousehold {
				return problem.Forbidden("only the owner or tenant of this unit can invite its household")
			}
			if in.personID != nil {
				return problem.Forbidden("household members are invited by contact, not by existing person")
			}
			// the member joins the inviter's own side
			in.side = string(own.Side)
			if own.Kind == authz.OccOwner && in.side == "" {
				in.side = SideOwner
			}
		}
		if site, err = tenancy.LoadSite(ctx, tx, orgID, unit.SiteID, false, false); err != nil {
			return err
		}
		in.unitID = &unit.ID
		in.sharePct, in.correspondent = nil, false
		inv, token, err = s.writeInvitation(ctx, tx, orgID, InviteHousehold, in, unit, site)
		return err
	})
	if err != nil {
		return Invitation{}, tenancy.DBProblem(err, "household invitation")
	}
	return s.afterCommit(ctx, orgID, inv, token, in.client, site.Name, in.locale), nil
}

// RevokeInvitation cancels a pending invitation. It needs invitation.manage for the site.
func (s *Service) RevokeInvitation(ctx context.Context, orgID, invID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		inv, err := scanInv(tx.QueryRow(ctx, invSelect+` WHERE i.org_id = $1 AND i.id = $2 FOR UPDATE OF i`, orgID, invID))
		if err != nil {
			return err
		}
		if _, err := requireSiteScope(ctx, tx, PermInvitationManage, inv.SiteID); err != nil {
			return err
		}
		if inv.Status != "pending" {
			return problem.Conflict("only a pending invitation can be revoked")
		}
		if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'revoked', updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, invID); err != nil {
			return err
		}
		diff, err := audit.Diff("invitation", map[string]any{"status": inv.Status}, map[string]any{"status": "revoked"})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &inv.SiteID, Action: "invitation.revoke", TargetType: "invitation", TargetID: invID, Diff: diff})
	})
	return tenancy.DBProblem(err, "revoke invitation")
}

// ResendInvitation issues a fresh link for a pending (or expired) personal
// invitation, extends its validity and sends it again. The old link stops
// working. It needs invitation.manage for the site.
func (s *Service) ResendInvitation(ctx context.Context, orgID, invID uuid.UUID) (Invitation, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return Invitation{}, err
	}
	var inv Invitation
	var token, siteName, locale string
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		cur, err := scanInv(tx.QueryRow(ctx, invSelect+` WHERE i.org_id = $1 AND i.id = $2 FOR UPDATE OF i`, orgID, invID))
		if err != nil {
			return err
		}
		if _, err := requireSiteScope(ctx, tx, PermInvitationManage, cur.SiteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, cur.SiteID, false, false)
		if err != nil {
			return err
		}
		if cur.Kind == InviteSiteCode || (cur.Status != "pending" && cur.Status != "expired") {
			return problem.Conflict("only a pending personal invitation can be sent again")
		}
		if err := s.channelAvailable(cur.Channel); err != nil {
			return err
		}
		tok, hash := newScopedToken(orgID, crypto.TokenBytes)
		if _, err := tx.Exec(ctx, `UPDATE invitation SET token_hash = $3, status = 'pending', expires_at = $4, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, invID, hash, s.now().Add(DefaultInviteDays*24*time.Hour)); err != nil {
			return err
		}
		if inv, err = scanInv(tx.QueryRow(ctx, invSelect+` WHERE i.org_id = $1 AND i.id = $2`, orgID, invID)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT locale FROM invitation WHERE org_id = $1 AND id = $2`, orgID, invID).Scan(&locale); err != nil {
			return err
		}
		token, siteName = tok, site.Name
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &cur.SiteID, Action: "invitation.resend", TargetType: "invitation", TargetID: invID})
	})
	if err != nil {
		return Invitation{}, tenancy.DBProblem(err, "resend invitation")
	}
	return s.afterCommit(ctx, orgID, inv, token, "web", siteName, locale), nil
}

// JoinCode is the site's QR / join code.
type JoinCode struct {
	SiteID uuid.UUID `json:"site_id"`
	Code   string    `json:"code"`
	// URL is what the QR code encodes: {BASE_URL}/join/{code}.
	URL       string    `json:"url"`
	Uses      int       `json:"uses"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) joinURL(code string) string {
	return trimSlash(s.cfg.BaseURL) + "/join/" + code
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func (s *Service) loadJoinCode(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (JoinCode, error) {
	var jc JoinCode
	err := tx.QueryRow(ctx, `SELECT site_id, code, uses, created_at FROM invitation WHERE org_id = $1 AND site_id = $2 AND kind = 'site_code' AND status = 'pending'
		ORDER BY created_at DESC LIMIT 1`, orgID, siteID).Scan(&jc.SiteID, &jc.Code, &jc.Uses, &jc.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return JoinCode{}, problem.NotFound("this site has no join code yet")
	}
	jc.CreatedAt = jc.CreatedAt.UTC()
	jc.URL = s.joinURL(jc.Code)
	return jc, err
}

// GetJoinCode returns the site's active join code. It needs invitation.manage for the site.
func (s *Service) GetJoinCode(ctx context.Context, orgID, siteID uuid.UUID) (JoinCode, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return JoinCode{}, err
	}
	var out JoinCode
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if _, err = requireSiteScope(ctx, tx, PermInvitationManage, siteID); err != nil {
			return err
		}
		if _, err = tenancy.LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		out, err = s.loadJoinCode(ctx, tx, orgID, siteID)
		return err
	})
	if err != nil {
		return JoinCode{}, tenancy.DBProblem(err, "join code")
	}
	return out, nil
}

// RotateJoinCode creates the site's join code, or replaces it: the old code
// stops working at once. It needs invitation.manage for the site.
func (s *Service) RotateJoinCode(ctx context.Context, orgID, siteID uuid.UUID) (JoinCode, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return JoinCode{}, err
	}
	var out JoinCode
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireSiteScope(ctx, tx, PermInvitationManage, siteID); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, siteID, true, false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'revoked', updated_at = now(), version = version + 1
			WHERE org_id = $1 AND site_id = $2 AND kind = 'site_code' AND status = 'pending'`, orgID, siteID); err != nil {
			return err
		}
		code, _ := newScopedToken(orgID, 16)
		iid := id.New()
		var uid *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			uid = &u
		}
		if _, err := tx.Exec(ctx, `INSERT INTO invitation (id, org_id, site_id, kind, channel, code, invited_by) VALUES ($1, $2, $3, 'site_code', 'link', $4, $5)`,
			iid, orgID, siteID, code, uid); err != nil {
			return err
		}
		var err error
		if out, err = s.loadJoinCode(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "site.rotate_join_code", TargetType: "invitation", TargetID: iid})
	})
	if err != nil {
		return JoinCode{}, tenancy.DBProblem(err, "join code")
	}
	return out, nil
}
