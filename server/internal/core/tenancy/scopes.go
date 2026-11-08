package tenancy

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
)

func requireSiteErr(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) error {
	_, err := requireSite(ctx, tx, perm, siteID)
	return err
}

func requireBlockErr(ctx context.Context, tx pgx.Tx, perm authz.Permission, blockID uuid.UUID) error {
	_, err := requireBlock(ctx, tx, perm, blockID)
	return err
}
