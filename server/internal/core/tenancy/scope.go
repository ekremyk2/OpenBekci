package tenancy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
)

// ScopeLoader is the authz.ScopeLoader backed by the site and block tables.
// The wiring registers it with authz.SetScopeLoader.
type ScopeLoader struct{}

var _ authz.ScopeLoader = ScopeLoader{}

// SiteOrg implements authz.ScopeLoader; a site of another organization is
// invisible under row-level security and answers ErrScopeNotFound.
func (ScopeLoader) SiteOrg(ctx context.Context, tx pgx.Tx, siteID uuid.UUID) (uuid.UUID, error) {
	var org uuid.UUID
	err := tx.QueryRow(ctx, `SELECT org_id FROM site WHERE id = $1`, siteID).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, authz.ErrScopeNotFound
	}
	return org, err
}

// BlockParents implements authz.ScopeLoader.
func (ScopeLoader) BlockParents(ctx context.Context, tx pgx.Tx, blockID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var org, site uuid.UUID
	err := tx.QueryRow(ctx, `SELECT org_id, site_id FROM block WHERE id = $1`, blockID).Scan(&org, &site)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, authz.ErrScopeNotFound
	}
	return org, site, err
}
