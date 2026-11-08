package packs

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// GooseTable is the goose version table of a pack's migrations.
func GooseTable(packID string) string { return "pack_" + packID + "_goose_version" }

func (h *Host) packProvider(pool *pgxpool.Pool, p *Pack, locked bool) (*goose.Provider, func(), error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	opts := []goose.ProviderOption{goose.WithTableName(GooseTable(p.Manifest.ID))}
	if locked {
		l, err := lock.NewPostgresSessionLocker()
		if err != nil {
			_ = sqlDB.Close()
			return nil, nil, err
		}
		opts = append(opts, goose.WithSessionLocker(l))
	}
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, p.C.Migrations, opts...)
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("create goose provider: %w", err)
	}
	return prov, func() { _ = sqlDB.Close() }, nil
}

// tableShape is what the migration conventions look at: the column count and
// whether row-level security is enforced, per table and whether it has org_id.
type tableShape struct {
	cols   int
	hasOrg bool
	rls    bool
}

func shapes(ctx context.Context, pool *pgxpool.Pool) (map[string]tableShape, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname, (SELECT count(*) FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped),
		       EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'org_id' AND NOT a.attisdropped),
		       c.relrowsecurity AND c.relforcerowsecurity
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind = 'r'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]tableShape{}
	for rows.Next() {
		var name string
		var s tableShape
		if err := rows.Scan(&name, &s.cols, &s.hasOrg, &s.rls); err != nil {
			return nil, err
		}
		out[name] = s
	}
	return out, rows.Err()
}

// checkConventions compares the schema before and after a pack's migrations:
// new tables must be named pack_<id>_*, tenant tables (with an org_id column)
// must have forced row-level security, and no table outside the pack's
// namespace may change or disappear.
func checkConventions(packID string, before, after map[string]tableShape) error {
	prefix := "pack_" + packID + "_"
	var bad []string
	for name, a := range after {
		b, existed := before[name]
		switch {
		case strings.HasPrefix(name, prefix):
			if a.hasOrg && !a.rls {
				bad = append(bad, fmt.Sprintf("tenant table %s has no forced row-level security (call openbekci_pack_table right after CREATE TABLE)", name))
			}
		case !existed:
			bad = append(bad, fmt.Sprintf("created table %s outside the pack namespace %s*", name, prefix))
		case b.cols != a.cols:
			bad = append(bad, fmt.Sprintf("changed table %s, which is not the pack's", name))
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok && !strings.HasPrefix(name, prefix) {
			bad = append(bad, fmt.Sprintf("dropped table %s, which is not the pack's", name))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	slices.Sort(bad)
	return &RegistryError{Pack: packID, Kind: ErrMigrations, Detail: strings.Join(bad, "; ")}
}

// MigrateUp applies every pack's pending migrations, in pack id order, each
// with its own goose table pack_<id>_goose_version. It runs even for packs no
// organization has enabled: schema is global, data is per organization. It
// must be called after the core migrations, on the migrator role.
func (h *Host) MigrateUp(ctx context.Context) error {
	pool := h.cfg.DB.Migrator()
	if pool == nil {
		return fmt.Errorf("packs: migrator pool not open")
	}
	for _, p := range h.packs {
		if p.C.Migrations == nil {
			continue
		}
		before, err := shapes(ctx, pool)
		if err != nil {
			return err
		}
		prov, closeFn, err := h.packProvider(pool, p, true)
		if err != nil {
			return &RegistryError{Pack: p.Manifest.ID, Kind: ErrMigrations, Err: err}
		}
		_, err = prov.Up(ctx)
		closeFn()
		if err != nil {
			return &RegistryError{Pack: p.Manifest.ID, Kind: ErrMigrations, Err: fmt.Errorf("goose up: %w", err)}
		}
		after, err := shapes(ctx, pool)
		if err != nil {
			return err
		}
		if err := checkConventions(p.Manifest.ID, before, after); err != nil {
			return err
		}
	}
	return nil
}

// MigrateStatus writes the state of every pack's migrations to w.
func (h *Host) MigrateStatus(ctx context.Context, w io.Writer) error {
	pool := h.cfg.DB.Migrator()
	if pool == nil {
		return fmt.Errorf("packs: migrator pool not open")
	}
	for _, p := range h.packs {
		if p.C.Migrations == nil {
			continue
		}
		prov, closeFn, err := h.packProvider(pool, p, false)
		if err != nil {
			return err
		}
		st, err := prov.Status(ctx)
		closeFn()
		if err != nil {
			return err
		}
		for _, s := range st {
			state := "pending"
			if s.State == goose.StateApplied {
				state = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "pack %-8s %-40s %s\n", p.Manifest.ID, s.Source.Path, state)
		}
	}
	return nil
}

// MigrationsCurrent reports whether every pack's migrations are applied
// (the /readyz migrations check; it runs on the app pool).
func (h *Host) MigrationsCurrent(ctx context.Context) error {
	pool := h.cfg.DB.WorkerPool()
	for _, p := range h.packs {
		if p.C.Migrations == nil {
			continue
		}
		prov, closeFn, err := h.packProvider(pool, p, false)
		if err != nil {
			return err
		}
		pending, err := prov.HasPending(ctx)
		closeFn()
		if err != nil {
			return fmt.Errorf("pack %s pending check: %w", p.Manifest.ID, err)
		}
		if pending {
			return fmt.Errorf("pack %s has pending migrations; run `openbekci migrate up`", p.Manifest.ID)
		}
	}
	return nil
}
