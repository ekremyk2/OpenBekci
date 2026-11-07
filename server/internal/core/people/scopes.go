package people

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
)

// requireBlockErr checks perm for the block's own scope (loaded under RLS).
func requireBlockErr(ctx context.Context, tx pgx.Tx, perm authz.Permission, blockID uuid.UUID) error {
	sc, err := authz.ScopeOfBlock(ctx, tx, blockID)
	if err != nil {
		return err
	}
	return authz.Require(ctx, perm, sc)
}
