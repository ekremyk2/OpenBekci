package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// RoleGrant is one active membership of the caller's person in an organization.
type RoleGrant struct {
	Role      string
	ScopeType string // org, site or block
	ScopeID   *uuid.UUID
	SiteOwned bool
}

// OrgAccess is an organization the caller belongs to and the role grants they
// hold there.
type OrgAccess struct {
	ID   uuid.UUID
	Slug string
	Name string
	// DefaultLocale is the organization's default locale.
	DefaultLocale string
	Roles         []RoleGrant
	// Sites the person can see here through a membership or an occupancy.
	Sites []MeSite
	// PersonID is the caller's person in this organization (nil when none is linked).
	PersonID *uuid.UUID
}

// Me is the caller's identity: the account and its organizations.
type Me struct {
	User User
	// DisplayName is the name of the person linked to the user (the first
	// organization's), else the email or phone.
	DisplayName   string
	Organizations []OrgAccess
	// Occupancies are the caller's occupancies in every organization, current and former.
	Occupancies []MeOccupancy
}

// Me loads the account and, through the platform (cross-organization) pool,
// every active organization where a person is linked to the user, with the
// memberships that are valid now. Effective permissions are added per
// organization by the authorization module (task A4); occupancies by phase B.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (Me, error) {
	var me Me
	now := s.now()
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		var err error
		if me.User, err = s.loadUser(ctx, tx, userID); err != nil {
			return err
		}
		// The same query the authorization Resolver uses, so what /v1/me shows
		// and what is enforced cannot drift apart.
		rows, err := authz.ListUserMemberships(ctx, tx, userID, now, nil)
		if err != nil {
			return err
		}
		idx := map[uuid.UUID]int{}
		for _, r := range rows {
			if me.DisplayName == "" {
				me.DisplayName = r.DisplayName
			}
			i, seen := idx[r.OrgID]
			if !seen {
				me.Organizations = append(me.Organizations, OrgAccess{
					ID: r.OrgID, Slug: r.Slug, Name: r.Name, DefaultLocale: r.DefaultLocale, Roles: []RoleGrant{},
				})
				i = len(me.Organizations) - 1
				idx[r.OrgID] = i
			}
			if r.HasRole {
				me.Organizations[i].Roles = append(me.Organizations[i].Roles, RoleGrant{
					Role: string(r.Membership.Role), ScopeType: string(r.Membership.ScopeType),
					ScopeID: r.Membership.ScopeID, SiteOwned: r.SiteOwned,
				})
			}
		}
		prows, err := tx.Query(ctx, `SELECT org_id, id FROM person WHERE user_id = $1`, userID)
		if err != nil {
			return err
		}
		defer prows.Close()
		for prows.Next() {
			var org, pid uuid.UUID
			if err := prows.Scan(&org, &pid); err != nil {
				return err
			}
			if i, ok := idx[org]; ok {
				p := pid
				me.Organizations[i].PersonID = &p
			}
		}
		prows.Close()
		if err := prows.Err(); err != nil {
			return err
		}
		return s.loadSitesAndOccupancies(ctx, tx, userID, now, &me)
	})
	if err != nil {
		return Me{}, problem.Internal(fmt.Errorf("load me: %w", err))
	}
	if me.DisplayName == "" {
		switch {
		case me.User.Email != nil:
			me.DisplayName = *me.User.Email
		case me.User.Phone != nil:
			me.DisplayName = *me.User.Phone
		}
	}
	if me.Organizations == nil {
		me.Organizations = []OrgAccess{}
	}
	return me, nil
}
