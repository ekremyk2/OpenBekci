package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/community"
)

// communityCredentials adapts the community module's visitor credentials to
// the access module's CredentialSource: the access projector asks for what
// changed since its last run and turns it into allow-list entries.
type communityCredentials struct{ c *community.Service }

// VisitorCredentials implements access.CredentialSource.
func (s communityCredentials) VisitorCredentials(ctx context.Context, site access.Site, since time.Time) ([]access.VisitorCredential, error) {
	list, err := s.c.VisitorCredentials(ctx, site.OrgID, site.SiteID, since)
	if err != nil {
		return nil, err
	}
	out := make([]access.VisitorCredential, 0, len(list))
	for _, v := range list {
		c := access.VisitorCredential{
			EntryID: v.SourceID, Kind: v.Kind, Value: v.Value, PointScope: v.PointScope,
			ValidFrom: v.ValidFrom, ValidTo: v.ValidTo, MaxUses: v.MaxUses, Active: v.Active,
		}
		if v.Recurring != nil {
			c.Recurring = &protocol.Recurring{Days: v.Recurring.Days, Start: v.Recurring.Start, End: v.Recurring.End}
		}
		if !v.Active {
			c.Reason = "source_inactive"
		}
		out = append(out, c)
	}
	return out, nil
}

// communityHooks makes the access projector hear about visitor changes: the
// source is queued (access_dirty, the transactional outbox) in the transaction
// of the change, so a visitor entry that commits is projected within one
// projector tick, and a rolled back one never is.
func communityHooks() community.Hooks {
	markDirty := func(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, sourceType string, sourceID uuid.UUID) error {
		return access.MarkDirty(ctx, tx, orgID, siteID, sourceType, sourceID)
	}
	return community.Hooks{
		OnVisitorDecided: func(ctx context.Context, tx pgx.Tx, d community.VisitorDecision) error {
			return markDirty(ctx, tx, d.OrgID, d.SiteID, "visitor_entry", d.EntryID)
		},
		OnVisitorChanged: func(ctx context.Context, tx pgx.Tx, siteID uuid.UUID, sourceType string, sourceID uuid.UUID) error {
			var orgID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT org_id FROM site WHERE id = $1`, siteID).Scan(&orgID); err != nil {
				return err
			}
			return markDirty(ctx, tx, orgID, siteID, sourceType, sourceID)
		},
	}
}
