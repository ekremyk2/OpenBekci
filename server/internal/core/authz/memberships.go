package authz

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/membership"
)

// UserMembership is one row of the shared membership query: an active
// organization where a person is linked to the user, and one membership of
// that person valid at the query time (HasRole false when there is none).
type UserMembership struct {
	OrgID         uuid.UUID
	Slug          string
	Name          string
	DefaultLocale string
	DisplayName   string
	HasRole       bool
	Membership    Membership
	SiteOwned     bool
}

// ListUserMemberships is the single definition of a valid membership (sqlc
// query ListUserMemberships, wrapped by package membership): valid_from <= now
// < valid_to, in an active organization, of a person linked to userID. orgID
// narrows it to one organization (nil: all). Both the Resolver (enforcement)
// and GET /v1/me use it. tx is a tenant transaction for one organization or a
// platform transaction for all.
func ListUserMemberships(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time, orgID *uuid.UUID) ([]UserMembership, error) {
	rows, err := membership.List(ctx, tx, userID, now, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]UserMembership, 0, len(rows))
	for _, r := range rows {
		um := UserMembership{
			OrgID: r.OrgID, Slug: r.Slug, Name: r.Name, DefaultLocale: r.DefaultLocale,
			DisplayName: r.DisplayName, HasRole: r.HasRole, SiteOwned: r.SiteOwned,
		}
		if r.HasRole {
			um.Membership = Membership{Role: Role(r.Role), ScopeType: ScopeType(r.ScopeType), ScopeID: r.ScopeID}
		}
		out = append(out, um)
	}
	return out, nil
}
