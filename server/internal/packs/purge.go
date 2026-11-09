package packs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// PurgeResult says what a purge deleted.
type PurgeResult struct {
	// Tables maps each pack table to the rows deleted for the organization.
	Tables map[string]int64
	// Settings is true when the pack's enablement and settings were removed.
	Settings bool
}

// PurgeTables lists the pack's tenant tables (pack_<id>_* with an org_id
// column, the goose table excluded), children before parents.
func (h *Host) PurgeTables(ctx context.Context, tx pgx.Tx, packID string) ([]string, error) {
	if reservedIDs[packID] {
		return nil, fmt.Errorf("packs: %q is a reserved id, not a pack", packID)
	}
	prefix := "pack_" + packID + "_"
	rows, err := tx.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname LIKE $1 ESCAPE '\' AND c.relname <> $2
		  AND EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'org_id' AND NOT a.attisdropped)`,
		strings.NewReplacer(`\`, `\\`, `_`, `\_`, `%`, `\%`).Replace(prefix)+"%", GooseTable(packID))
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// foreign keys between the pack's tables: referencing (child) -> referenced (parent)
	deps := map[string][]string{}
	frows, err := tx.Query(ctx, `SELECT c.conrelid::regclass::text, c.confrelid::regclass::text FROM pg_constraint c WHERE c.contype = 'f' AND c.conrelid::regclass::text = ANY($1) AND c.confrelid::regclass::text = ANY($1)`, tables)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var child, parent string
		if err := frows.Scan(&child, &parent); err != nil {
			frows.Close()
			return nil, err
		}
		if child != parent {
			deps[parent] = append(deps[parent], child)
		}
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return nil, err
	}
	// order: a table is deleted after every table that references it
	slices.Sort(tables)
	var ordered []string
	done := map[string]bool{}
	for len(ordered) < len(tables) {
		progressed := false
		for _, t := range tables {
			if done[t] {
				continue
			}
			ready := true
			for _, child := range deps[t] {
				if !done[child] {
					ready = false
				}
			}
			if ready {
				ordered = append(ordered, t)
				done[t] = true
				progressed = true
			}
		}
		if !progressed {
			return nil, fmt.Errorf("packs: circular foreign keys between the tables of pack %s", packID)
		}
	}
	return ordered, nil
}

// Purge deletes a pack's rows for an organization: every row of its
// pack_<id>_* tables with that org_id, and its enablement and settings. The
// pack must be disabled for the organization. It runs on the platform role and
// is never automatic; the CLI asks for confirmation. Settings that presets
// wrote into sites stay (they are the sites' settings now); only the "pack
// default" markers go.
func (h *Host) Purge(ctx context.Context, org uuid.UUID, packID string) (PurgeResult, error) {
	res := PurgeResult{Tables: map[string]int64{}}
	if !packIDRE.MatchString(packID) {
		return res, problem.Validation(problem.Field{Field: "pack", Code: "invalid_format"})
	}
	// a reserved id is not a pack: "enablement" would match the core's own
	// pack_enablement_site table by prefix and wipe every pack's site settings
	if reservedIDs[packID] {
		return res, problem.Validation(problem.Field{Field: "pack", Code: "reserved"})
	}
	err := h.cfg.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT enabled FROM pack_enablement WHERE org_id = $1 AND pack_id = $2), false)`, org, packID).Scan(&enabled)
		if err != nil {
			return err
		}
		if enabled {
			return problem.Conflict("the pack is enabled for this organization; disable it first")
		}
		tables, err := h.PurgeTables(ctx, tx, packID)
		if err != nil {
			return err
		}
		for _, t := range tables {
			tag, err := tx.Exec(ctx, `DELETE FROM `+pgx.Identifier{t}.Sanitize()+` WHERE org_id = $1`, org)
			if err != nil {
				return fmt.Errorf("purge %s: %w", t, err)
			}
			res.Tables[t] = tag.RowsAffected()
		}
		if _, err := tx.Exec(ctx, `DELETE FROM site_pack_default WHERE org_id = $1 AND pack_id = $2`, org, packID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM pack_enablement_site WHERE org_id = $1 AND pack_id = $2`, org, packID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM pack_enablement WHERE org_id = $1 AND pack_id = $2`, org, packID)
		if err != nil {
			return err
		}
		res.Settings = tag.RowsAffected() > 0
		return audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "pack.purge", TargetType: "pack", TargetID: packTarget(packID)})
	})
	return res, problemOf(err)
}
