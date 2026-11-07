package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// ScopeOfSite builds the Scope of a site from the row itself, read inside the
// tenant transaction tx (row-level security applies), never from request
// input. An unknown site is 404. Same name and shape as the constructor of
// the authorization fix on feat/a4-authz-audit; the merge keeps one.
func ScopeOfSite(ctx context.Context, tx pgx.Tx, siteID uuid.UUID) (Scope, error) {
	var org uuid.UUID
	err := tx.QueryRow(ctx, `SELECT org_id FROM site WHERE id = $1`, siteID).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scope{}, problem.NotFound("no such site")
	}
	if err != nil {
		return Scope{}, problem.Internal(err)
	}
	return Scope{OrgID: org, SiteID: &siteID}, nil
}

// ScopeOfBlock builds the Scope of a block (with its site) from the row,
// read under row-level security. An unknown block is 404.
func ScopeOfBlock(ctx context.Context, tx pgx.Tx, blockID uuid.UUID) (Scope, error) {
	var org, site uuid.UUID
	err := tx.QueryRow(ctx, `SELECT org_id, site_id FROM block WHERE id = $1`, blockID).Scan(&org, &site)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scope{}, problem.NotFound("no such block")
	}
	if err != nil {
		return Scope{}, problem.Internal(err)
	}
	return Scope{OrgID: org, SiteID: &site, BlockID: &blockID}, nil
}
