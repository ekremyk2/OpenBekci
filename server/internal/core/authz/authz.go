// Package authz holds the caller identity and, later, the authorization
// matrix. Phase A only has the context accessors; the sessions task (A3)
// authenticates requests and the permission matrix arrives with the first
// business modules.
package authz

import (
	"context"

	"github.com/google/uuid"
)

// Principal is the authenticated caller.
type Principal struct {
	// ID is the person (account) id.
	ID uuid.UUID
}

type ctxKey struct{}

// WithPrincipal returns ctx carrying p. The HTTP authentication step calls it;
// tests may too.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFromContext returns the authenticated caller, or nil for an
// anonymous request. Until sessions are wired (A3) nothing sets it, so it
// always returns nil.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}
