package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// siteHooks runs several tenancy.SiteHook in order inside the site-creating
// transaction; the first error aborts the creation.
type siteHooks []tenancy.SiteHook

// SiteCreated implements tenancy.SiteHook.
func (hs siteHooks) SiteCreated(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) error {
	for _, h := range hs {
		if err := h.SiteCreated(ctx, tx, orgID, siteID); err != nil {
			return err
		}
	}
	return nil
}
