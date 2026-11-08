package tenancy

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ArchiveRetention is how long site_export.archive is kept after an export.
// The archive holds contact details in clear and national ids still sealed, so
// it must not live forever. Design note: the archive is deliberately outside
// person erasure (privacy_erase): it is a point-in-time hand-over document
// whose copies cannot be reached anyway, and the retention below (or an
// explicit discard) is what bounds it. The export record itself (counts,
// dates, who) stays.
const ArchiveRetention = 30 * 24 * time.Hour

// PlatformTx runs a function in a platform (cross-organization) transaction.
type PlatformTx interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// ArchivePurger deletes archives older than ArchiveRetention in every
// organization. It is run by the maintenance.expire_site_archives job.
type ArchivePurger struct{ DB PlatformTx }

// Purge clears expired archives and returns how many.
func (p ArchivePurger) Purge(ctx context.Context) (int64, error) {
	var n int64
	err := p.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE site_export SET archive = NULL
			WHERE archive IS NOT NULL AND at < now() - make_interval(secs => $1)`, ArchiveRetention.Seconds())
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// DiscardArchive deletes the stored archive of an export once the hand-over is
// confirmed (the receiving side has imported it). The export record stays. It
// needs site.export, organization-wide.
func (s *Service) DiscardArchive(ctx context.Context, orgID, exportID uuid.UUID) error {
	if err := authz.Require(ctx, PermSiteExport, authz.Scope{OrgID: orgID}); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var siteID *uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE site_export SET archive = NULL WHERE org_id = $1 AND id = $2 AND direction = 'export' AND archive IS NOT NULL RETURNING site_id`,
			orgID, exportID).Scan(&siteID)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: siteID, Action: "site.export_discard", TargetType: "site_export", TargetID: exportID})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no stored archive for this export")
		}
		return DBProblem(err, "discard archive")
	}
	return nil
}
