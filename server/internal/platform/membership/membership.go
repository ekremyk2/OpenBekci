// Package membership wraps the sqlc query ListUserMemberships, the single
// definition of "a membership that is valid now" (valid_from <= now < valid_to,
// in an active organization, of a person linked to the user). The authorization
// Resolver (enforcement) and GET /v1/me both read through it, so what /v1/me
// shows and what is enforced cannot drift apart. It lives outside the core
// packages because they may not import the generated database code.
package membership

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/openbekci/openbekci/server/internal/platform/db/dbgen"
)

// Row is one organization of the user, with one valid membership of their
// person there (HasRole false when the person has none valid now).
type Row struct {
	OrgID         uuid.UUID
	Slug          string
	Name          string
	DefaultLocale string
	DisplayName   string
	HasRole       bool
	Role          string
	ScopeType     string
	ScopeID       *uuid.UUID
	SiteOwned     bool
}

// List returns the rows for userID at time now; orgID narrows it to one
// organization (nil: all). tx is a tenant transaction for one organization or a
// platform transaction for all.
func List(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time, orgID *uuid.UUID) ([]Row, error) {
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
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		row := Row{
			OrgID: uuid.UUID(r.OrgID.Bytes), Slug: r.Slug, Name: r.Name, DefaultLocale: r.DefaultLocale,
			DisplayName: r.DisplayName, HasRole: r.Role.Valid, Role: r.Role.String, ScopeType: r.ScopeType.String,
			SiteOwned: r.SiteOwned.Valid && r.SiteOwned.Bool,
		}
		if r.ScopeID.Valid {
			id := uuid.UUID(r.ScopeID.Bytes)
			row.ScopeID = &id
		}
		out = append(out, row)
	}
	return out, nil
}
