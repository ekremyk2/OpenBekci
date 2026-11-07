package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// orgPathPrefix marks the operations that run inside an organization.
const orgPathPrefix = "/v1/orgs/{org_id}"

// tenantRunner is satisfied by *db.DB.
type tenantRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

type orgScopeKey struct{}

type orgScope struct {
	id uuid.UUID
	db tenantRunner
}

// OrgTx runs fn in a tenant transaction (RLS context set) for the
// organization of the current /v1/orgs/{org_id}/... request. It is the only
// database path handlers of those operations use; services do the same
// through their own InTenantTx interface. It fails with an internal error
// outside an organization route.
func OrgTx(ctx context.Context, fn func(pgx.Tx) error) error {
	o, _ := ctx.Value(orgScopeKey{}).(orgScope)
	if o.db == nil {
		return problem.Internal(errors.New("httpapi.OrgTx called outside an organization route"))
	}
	return o.db.InTenantTx(ctx, o.id, fn)
}

// OrgID returns the organization of the current /v1/orgs/{org_id}/... request.
func OrgID(ctx context.Context) (uuid.UUID, bool) {
	o, ok := ctx.Value(orgScopeKey{}).(orgScope)
	return o.id, ok
}

func isOrgPath(pattern string) bool {
	return pattern == orgPathPrefix || strings.HasPrefix(pattern, orgPathPrefix+"/")
}

// authzContext runs after authentication. It puts the instance-level grants
// in the context and, for operations under /v1/orgs/{org_id}, resolves the
// caller's memberships in that organization once: without an active
// membership (or for an unknown or malformed organization id) the answer is
// 404 not_found, so the existence of an organization does not leak. The
// operation's x-openbekci-roles permissions are then checked against the
// grants (403 insufficient_permission), so a stored idempotent response is
// never replayed to a caller who lost the permission. The handlers call
// authz.Require through their service for the concrete scope.
func (p *pipeline) authzContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr := authz.PrincipalFromContext(r.Context())
		if p.resolver == nil || pr == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := authz.WithGrants(r.Context(), p.resolver.InstanceGrants(pr))
		if p.op(r).orgScoped {
			orgID, err := uuid.Parse(chi.URLParam(r, "org_id"))
			if err != nil {
				problem.Write(w, r, problem.NotFound("no such organization"))
				return
			}
			g, member, err := p.resolver.ResolveOrg(ctx, pr, orgID)
			if err != nil {
				problem.Write(w, r, err)
				return
			}
			if !member {
				problem.Write(w, r, problem.NotFound("no such organization"))
				return
			}
			// The operation's permissions (x-openbekci-roles) are checked here, before
			// validation, the idempotency store (a stored response is replayed
			// without reaching the service) and the handler. The services check
			// again with the concrete scope (defence in depth).
			for _, perm := range p.op(r).perms {
				if !g.HasInOrg(perm, orgID) {
					problem.Write(w, r, problem.Forbidden("you do not have the "+string(perm)+" permission here"))
					return
				}
			}
			ctx = authz.WithGrants(ctx, g)
			ctx = context.WithValue(ctx, orgScopeKey{}, orgScope{id: orgID, db: p.tenantDB})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
