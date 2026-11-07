package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
		rows, err := tx.Query(ctx, `
			SELECT o.id, o.slug, o.name, o.default_locale, p.display_name, m.role, m.scope_type, m.scope_id, m.site_owned
			FROM person p
			JOIN organization o ON o.id = p.org_id AND o.status = 'active'
			LEFT JOIN membership m ON m.org_id = p.org_id AND m.person_id = p.id
			     AND m.valid_from <= $2 AND (m.valid_to IS NULL OR m.valid_to > $2)
			WHERE p.user_id = $1
			ORDER BY o.name, o.id, p.created_at, m.role, m.scope_type, m.scope_id NULLS FIRST`, userID, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		idx := map[uuid.UUID]int{}
		for rows.Next() {
			var oa OrgAccess
			var person string
			var role, scope *string
			var scopeID *uuid.UUID
			var siteOwned *bool
			if err := rows.Scan(&oa.ID, &oa.Slug, &oa.Name, &oa.DefaultLocale, &person, &role, &scope, &scopeID, &siteOwned); err != nil {
				return err
			}
			if me.DisplayName == "" {
				me.DisplayName = person
			}
			i, seen := idx[oa.ID]
			if !seen {
				oa.Roles = []RoleGrant{}
				me.Organizations = append(me.Organizations, oa)
				i = len(me.Organizations) - 1
				idx[oa.ID] = i
			}
			if role != nil {
				g := RoleGrant{Role: *role, ScopeID: scopeID}
				if scope != nil {
					g.ScopeType = *scope
				}
				g.SiteOwned = siteOwned != nil && *siteOwned
				me.Organizations[i].Roles = append(me.Organizations[i].Roles, g)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
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
