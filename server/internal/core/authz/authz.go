// Package authz holds the caller identity and, later, the authorization
// matrix. The HTTP authentication step (identity sessions) builds the
// Principal; the permission matrix arrives with task A4 and the first
// business modules.
package authz

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// Principal is the authenticated caller.
type Principal struct {
	// ID is the user id (the sign-in account, instance-wide). It is what
	// idempotency keys are scoped by. Per-organization rights are resolved
	// from the person linked to this user (memberships now, occupancies in
	// phase B); see GET /v1/me.
	ID uuid.UUID
	// UserID is the same value as ID, spelled out for readers of identity code.
	UserID uuid.UUID
	// SessionID is the id of the session row (token) that authenticated this
	// request.
	SessionID uuid.UUID
	// FamilyID is the user-visible session id: it is shared by a refresh token
	// and the access tokens issued from it, and survives rotation.
	FamilyID uuid.UUID
	// IsPlatformAdmin marks instance operators (platform endpoints).
	IsPlatformAdmin bool
	// ReauthUntil is when the session's re-authentication window ends; the
	// zero time means the session has not re-authenticated.
	ReauthUntil time.Time
	// ViaCookie is true for browser cookie sessions (CSRF rules apply).
	ViaCookie bool
}

// Reauthenticated reports whether the caller re-authenticated recently
// enough at time now.
func (p *Principal) Reauthenticated(now time.Time) bool {
	return p != nil && now.Before(p.ReauthUntil)
}

// RequireReauth returns a 403 reauth_required problem unless the caller
// re-authenticated within the window (POST /v1/auth/reauth). Sensitive
// operations (backend plan section 16) call it.
func (p *Principal) RequireReauth(now time.Time) error {
	if p.Reauthenticated(now) {
		return nil
	}
	return problem.New(403, problem.CodeReauthRequired, "this action requires re-authentication")
}

type ctxKey struct{}

// WithPrincipal returns ctx carrying p. The HTTP authentication step calls it;
// tests may too.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFromContext returns the authenticated caller, or nil for an
// anonymous request.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}
