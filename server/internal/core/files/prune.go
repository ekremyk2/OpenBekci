package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/audit"
)

// StalePendingAge is how long a pending upload may sit before the
// maintenance job treats it as abandoned (the request died mid-upload).
const StalePendingAge = 24 * time.Hour

// pruneBatch bounds the work of one maintenance pass.
const pruneBatch = 200

// PruneResult counts what one pass did.
type PruneResult struct {
	// Expired files reached their retention date (or were abandoned uploads)
	// and were marked deleted.
	Expired int
	// Purged files had their blobs removed.
	Purged int
}

// Prune is the maintenance.prune_files job: it marks files past their
// retention date, and uploads abandoned half-way, as deleted; removes the
// blobs of deleted files; and gives their storage back to the organization's
// usage counter. A deleted file that another table still references keeps its
// row (the foreign key refuses the delete) but loses its blobs. It works
// across organizations on the platform pool.
func (s *Service) Prune(ctx context.Context) (PruneResult, error) {
	var res PruneResult
	now := s.now()

	// 1. Expire.
	var expired []File
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE file SET status = 'deleted', deleted_at = $1
			WHERE status <> 'deleted'
			  AND ((retention_until IS NOT NULL AND retention_until < $1)
			    OR (status = 'pending' AND created_at < $2))
			RETURNING `+fileColumns, now, now.Add(-StalePendingAge))
		if err != nil {
			return err
		}
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, f := range expired {
			if err := s.recordJob(ctx, tx, "files.expire", f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("expire files: %w", err)
	}
	res.Expired = len(expired)

	// 2. Purge the blobs of deleted files, one file per transaction.
	type victim struct {
		id, org  uuid.UUID
		key      string
		size     int64
		variants map[string]Variant
		counted  bool
	}
	var victims []victim
	err = s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, org_id, key, size, variants, sha256 IS NOT NULL FROM file
			WHERE status = 'deleted' AND purged_at IS NULL
			ORDER BY created_at LIMIT $1`, pruneBatch)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v victim
			var raw []byte
			if err := rows.Scan(&v.id, &v.org, &v.key, &v.size, &raw, &v.counted); err != nil {
				return err
			}
			v.variants = map[string]Variant{}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &v.variants); err != nil {
					return err
				}
			}
			victims = append(victims, v)
		}
		return rows.Err()
	})
	if err != nil {
		return res, fmt.Errorf("find deleted files: %w", err)
	}
	var errs []error
	for _, v := range victims {
		keys := []string{v.key}
		for _, name := range []string{VariantThumb, VariantMedium} { // also variants of an upload that died before recording them
			keys = append(keys, variantKey(v.key, name))
		}
		for name := range v.variants {
			keys = append(keys, variantKey(v.key, name))
		}
		failed := false
		for _, k := range keys {
			if err := s.blob.Delete(ctx, k); err != nil {
				errs = append(errs, fmt.Errorf("delete blob %s: %w", k, err))
				failed = true
			}
		}
		if failed {
			continue // retried on the next pass
		}
		stored := v.size
		for _, vv := range v.variants {
			stored += vv.Size
		}
		err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
			if v.counted {
				if err := addUsage(ctx, tx, v.org, -stored, -1); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE file SET purged_at = $2 WHERE id = $1`, v.id, now); err != nil {
				return err
			}
			// Drop the row unless another table still points at it.
			if _, err := tx.Exec(ctx, `SAVEPOINT drop_file`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM file WHERE id = $1`, v.id); err != nil {
				var pe *pgconn.PgError
				if errors.As(err, &pe) && pe.Code == "23503" { // foreign_key_violation
					_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT drop_file`)
					return err
				}
				return err
			}
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("purge file %s: %w", v.id, err))
			continue
		}
		res.Purged++
	}
	return res, errors.Join(errs...)
}

// PruneFiles runs Prune and reports how many files had their blobs purged; it
// is what the job queue calls (platform/jobs.FilePruner).
func (s *Service) PruneFiles(ctx context.Context) (int, error) {
	r, err := s.Prune(ctx)
	return r.Purged, err
}

// recordJob records a system event for an organization from the maintenance job.
func (s *Service) recordJob(ctx context.Context, tx pgx.Tx, action string, f File) error {
	diff, err := audit.Diff("file", f.auditView(), nil)
	if err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{
		OrgID: f.OrgID, SiteID: f.SiteID, ActorKind: audit.ActorJob, Action: action, TargetType: "file", TargetID: f.ID, Diff: diff,
	})
}
