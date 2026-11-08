// Package opsx holds what the daily-operations modules (tickets,
// announcements, documents, notifications, dashboards) share: resolving the
// caller's person, any-of permission checks at a site, small SQL helpers.
package opsx

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ErrNoPerson is returned when the caller has no person in the organization
// (a platform administrator without a membership, an account that was
// removed).
var ErrNoPerson = errors.New("the caller has no person in this organization")

// PersonOf returns the person of the signed-in user in org. ok is false for a
// caller without one.
func PersonOf(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (personID uuid.UUID, ok bool, err error) {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return uuid.Nil, false, problem.Unauthenticated("authentication is required")
	}
	err = tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, p.UserID).Scan(&personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return personID, err == nil, err
}

// MustPerson is PersonOf for operations that need one (403 otherwise).
func MustPerson(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (uuid.UUID, error) {
	id, ok, err := PersonOf(ctx, tx, orgID)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, problem.Forbidden("this account has no person in the organization")
	}
	return id, nil
}

// Held returns which of perms the caller holds at scope (an AnyOf name is
// expanded to its alternatives).
func Held(ctx context.Context, scope authz.Scope, perms ...authz.Permission) []authz.Permission {
	var out []authz.Permission
	for _, p := range perms {
		for _, alt := range p.Alternatives() {
			if authz.Has(ctx, alt, scope) {
				out = append(out, alt)
			}
		}
	}
	return out
}

// RequireAny answers 403 unless the caller holds one of perms at scope, and
// returns the ones held.
func RequireAny(ctx context.Context, scope authz.Scope, perms ...authz.Permission) ([]authz.Permission, error) {
	if authz.PrincipalFromContext(ctx) == nil {
		return nil, problem.Unauthenticated("authentication is required")
	}
	held := Held(ctx, scope, perms...)
	if len(held) == 0 {
		names := make([]string, len(perms))
		for i, p := range perms {
			names[i] = string(p)
		}
		return nil, problem.Forbidden("you need one of " + strings.Join(names, ", ") + " here")
	}
	return held, nil
}

// Contains reports whether perm is among held.
func Contains(held []authz.Permission, perm authz.Permission) bool {
	for _, h := range held {
		if h == perm {
			return true
		}
	}
	return false
}

// SiteScope loads the scope of a site under RLS (404 for an unknown site).
func SiteScope(ctx context.Context, tx pgx.Tx, siteID uuid.UUID) (authz.Scope, error) {
	return authz.ScopeOfSite(ctx, tx, siteID)
}

// ActorName is the display name of the caller's person in org, for history
// entries that must survive the person leaving. Empty when unknown.
func ActorName(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) string {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return ""
	}
	var n string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id = $1 AND user_id = $2`, orgID, p.UserID).Scan(&n); err != nil {
		return ""
	}
	return n
}

// UnitsOf returns the units of the site where the caller currently owns,
// rents or lives (any occupancy kind in force today).
func UnitsOf(ctx context.Context, siteID uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, o := range authz.GrantsFromContext(ctx).Current() {
		if o.SiteID == siteID {
			out = append(out, o.UnitID)
		}
	}
	return out
}

// IsBoardSide reports whether the caller holds perm at scope through a role
// membership (managers, board, auditor, staff) rather than only as a resident.
func IsBoardSide(ctx context.Context, scope authz.Scope, perm authz.Permission) bool {
	return authz.PrincipalFromContext(ctx) != nil && authz.GrantsFromContext(ctx).HasViaRole(perm, scope)
}
