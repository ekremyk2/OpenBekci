package authz

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/openbekci/openbekci/server/internal/platform/db/dbgen"
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
// query ListUserMemberships): valid_from <= now < valid_to, in an active
// organization, of a person linked to userID. orgID narrows it to one
// organization (nil: all). Both the Resolver (enforcement) and GET /v1/me use
// it. tx is a tenant transaction for one organization or a platform
// transaction for all.
func ListUserMemberships(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time, orgID *uuid.UUID) ([]UserMembership, error) {
	p := dbgen.ListUserMembershipsParams{
		Now:    pgtype.Timestamptz{Time: now, Valid: true},
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
	}
	if orgID != nil {
		p.OrgID = pgtype.UUID{Bytes: *orgID, Valid: true}
	}
	rows, err := dbgen.New(tx).ListUserMemberships(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]UserMembership, 0, len(rows))
	for _, r := range rows {
		um := UserMembership{
			OrgID: uuid.UUID(r.OrgID.Bytes), Slug: r.Slug, Name: r.Name, DefaultLocale: r.DefaultLocale,
			DisplayName: r.DisplayName, HasRole: r.Role.Valid, SiteOwned: r.SiteOwned.Valid && r.SiteOwned.Bool,
		}
		if r.Role.Valid {
			um.Membership = Membership{Role: Role(r.Role.String), ScopeType: ScopeType(r.ScopeType.String)}
			if r.ScopeID.Valid {
				id := uuid.UUID(r.ScopeID.Bytes)
				um.Membership.ScopeID = &id
			}
		}
		out = append(out, um)
	}
	return out, nil
}
