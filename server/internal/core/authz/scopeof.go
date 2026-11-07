package authz

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// A Scope for a site or a block must come from the stored row, never from
// request input: a grant of site S is matched against Scope.SiteID, so a
// caller who can name SiteID (their own site) next to the BlockID of another
// site's block would pass. Services therefore never write a Scope literal with
// SiteID or BlockID; they call ScopeOfSite or ScopeOfBlock, which load the
// parents under RLS. TestNoScopeLiteralsOutsideAuthz fails the build for a
// literal outside this package.

// ScopeLoader reads the hierarchy of sites and blocks. Phase B (tenancy) supplies
// the implementation next to the site and block tables and the wiring injects
// it with SetScopeLoader. Both methods run inside the tenant transaction tx, so
// a row of another organization is invisible: they return ErrScopeNotFound.
type ScopeLoader interface {
	// SiteOrg returns the organization of the site.
	SiteOrg(ctx context.Context, tx pgx.Tx, siteID uuid.UUID) (orgID uuid.UUID, err error)
	// BlockParents returns the organization and the site of the block.
	BlockParents(ctx context.Context, tx pgx.Tx, blockID uuid.UUID) (orgID, siteID uuid.UUID, err error)
}

// Errors of ScopeLoader implementations and of the scope helpers.
var (
	// ErrScopeNotFound: the site or block does not exist (or is not visible
	// to the tenant transaction).
	ErrScopeNotFound = errors.New("authz: no such site or block")
	// ErrScopeUnavailable: no ScopeLoader is wired (sites and blocks do not
	// exist in this build).
	ErrScopeUnavailable = errors.New("authz: site and block scopes are not available")
)

type unavailableLoader struct{}

func (unavailableLoader) SiteOrg(context.Context, pgx.Tx, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, ErrScopeUnavailable
}

func (unavailableLoader) BlockParents(context.Context, pgx.Tx, uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	return uuid.Nil, uuid.Nil, ErrScopeUnavailable
}

var scopeLoader atomic.Pointer[ScopeLoader]

func init() {
	var l ScopeLoader = unavailableLoader{}
	scopeLoader.Store(&l)
}

// SetScopeLoader injects the loader (the wiring calls it once at start-up;
// tests may swap it). nil restores the default that answers ErrScopeUnavailable.
func SetScopeLoader(l ScopeLoader) {
	if l == nil {
		l = unavailableLoader{}
	}
	scopeLoader.Store(&l)
}

func loader() ScopeLoader { return *scopeLoader.Load() }

// ScopeOfSite returns the fully populated Scope of a site: its organization
// and itself, as stored. tx must be a tenant transaction (RLS). A missing site
// is a 404 problem; an unwired loader is an internal error.
func ScopeOfSite(ctx context.Context, tx pgx.Tx, siteID uuid.UUID) (Scope, error) {
	org, err := loader().SiteOrg(ctx, tx, siteID)
	if err != nil {
		return Scope{}, scopeErr(err, "no such site")
	}
	return Scope{OrgID: org, SiteID: &siteID}, nil
}

// ScopeOfBlock returns the fully populated Scope of a block: its organization,
// its site and itself, as stored. tx must be a tenant transaction (RLS).
func ScopeOfBlock(ctx context.Context, tx pgx.Tx, blockID uuid.UUID) (Scope, error) {
	org, site, err := loader().BlockParents(ctx, tx, blockID)
	if err != nil {
		return Scope{}, scopeErr(err, "no such block")
	}
	return Scope{OrgID: org, SiteID: &site, BlockID: &blockID}, nil
}

func scopeErr(err error, notFound string) error {
	if errors.Is(err, ErrScopeNotFound) {
		return problem.NotFound(notFound)
	}
	return problem.Internal(err)
}
