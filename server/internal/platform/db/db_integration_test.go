package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/openbekci/openbekci/server/db/migrations"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

const defaultSuperuserURL = "postgres://postgres:postgres@localhost:5432/postgres"

// rlsExempt are tables in public that are intentionally not tenant tables.
// idempotency_key is keyed by org_id and principal_id explicitly and written by the HTTP layer before authorization (see its migration).
// user, session, otp_code, login_attempt and delivery_log are instance-wide identity tables (backend plan 4.2, section 5):
// a user belongs to no organization; organizations reach it through person.user_id.
var rlsExempt = []string{"instance_setting", "goose_db_version", "idempotency_key", "user", "session", "otp_code", "login_attempt", "delivery_log"}

func isRLSExempt(table string) bool {
	return slices.Contains(rlsExempt, table) || strings.HasPrefix(table, "river_")
}

// testDB is a throwaway migrated database plus a DB opened on it as the three
// application roles.
type testDB struct {
	*DB
	superURL string
	name     string
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test skipped in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	superURL := os.Getenv("OPENBEKCI_TEST_SUPERUSER_URL")
	if superURL == "" {
		superURL = defaultSuperuserURL
	}
	super, err := pgx.Connect(ctx, superURL)
	if err != nil {
		t.Skipf("PostgreSQL superuser unreachable (set OPENBEKCI_TEST_SUPERUSER_URL): %v", err)
	}
	defer super.Close(ctx)

	// Roles are cluster-wide; create them once if missing (same attributes as
	// deploy/compose/init/01-roles.sql).
	for _, r := range []struct{ name, attrs string }{
		{"openbekci_migrator", "BYPASSRLS"},
		{"openbekci_app", "NOBYPASSRLS"},
		{"openbekci_platform", "BYPASSRLS"},
	} {
		var exists bool
		if err := super.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)`, r.name).Scan(&exists); err != nil {
			t.Fatalf("check role %s: %v", r.name, err)
		}
		if !exists {
			if _, err := super.Exec(ctx, fmt.Sprintf(`CREATE ROLE %s LOGIN %s PASSWORD 'openbekci'`, r.name, r.attrs)); err != nil {
				t.Fatalf("create role %s: %v", r.name, err)
			}
		}
	}

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "openbekci_test_" + hex.EncodeToString(suffix[:])
	if _, err := super.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s OWNER openbekci_migrator`, name)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), superURL)
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})

	// Default privileges are per database (see 01-roles.sql).
	su := withDatabase(t, superURL, name, "", "")
	dbSuper, err := pgx.Connect(ctx, su)
	if err != nil {
		t.Fatalf("connect superuser to %s: %v", name, err)
	}
	defer dbSuper.Close(ctx)
	for _, stmt := range []string{
		`ALTER DEFAULT PRIVILEGES FOR ROLE openbekci_migrator IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO openbekci_app, openbekci_platform`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE openbekci_migrator IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO openbekci_app, openbekci_platform`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE openbekci_migrator IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO openbekci_app, openbekci_platform`,
		`REVOKE ALL ON SCHEMA public FROM PUBLIC`,
		`GRANT ALL ON SCHEMA public TO openbekci_migrator`,
		`GRANT USAGE ON SCHEMA public TO openbekci_app, openbekci_platform`,
	} {
		if _, err := dbSuper.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	d, err := Open(ctx, Options{
		AppURL:      withDatabase(t, superURL, name, "openbekci_app", "openbekci"),
		PlatformURL: withDatabase(t, superURL, name, "openbekci_platform", "openbekci"),
		MigratorURL: withDatabase(t, superURL, name, "openbekci_migrator", "openbekci"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(d.Close)
	if err := d.MigrateUp(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return &testDB{DB: d, superURL: superURL, name: name}
}

func withDatabase(t *testing.T, base, dbName, user, password string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %q: %v", base, err)
	}
	u.Path = "/" + dbName
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	return u.String()
}

func insertOrg(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, slug string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, $2, $2)`, orgID, slug)
	return err
}

func countOrgs(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM organization`).Scan(&n)
	return n, err
}

// canonicalPolicyExpr is how PostgreSQL deparses the expression emitted by
// openbekci_enable_rls, for both USING and WITH CHECK.
const canonicalPolicyExpr = `(org_id = ( SELECT (NULLIF(current_setting('app.org_id'::text, true), ''::text))::uuid AS "nullif"))`

func TestIntegrationEveryTenantTableHasRLSPolicy(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	rows, err := d.platform.Query(ctx, `
		SELECT c.relname::text, c.relrowsecurity, c.relforcerowsecurity,
		       EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid = c.oid),
		       (SELECT count(*) FROM pg_policies pp WHERE pp.schemaname = n.nspname AND pp.tablename = c.relname),
		       (SELECT count(*) FROM pg_policies pp WHERE pp.schemaname = n.nspname AND pp.tablename = c.relname
		          AND pp.cmd = 'ALL' AND pp.permissive = 'PERMISSIVE'
		          AND pp.qual = $1 AND pp.with_check = $1)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY 1`, canonicalPolicyExpr)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var table string
		var enabled, forced, hasPolicy bool
		var policies, canonical int
		if err := rows.Scan(&table, &enabled, &forced, &hasPolicy, &policies, &canonical); err != nil {
			t.Fatal(err)
		}
		seen++
		if isRLSExempt(table) {
			continue
		}
		if !enabled || !forced || !hasPolicy {
			t.Errorf("table %s: rls enabled=%v forced=%v has_policy=%v; tenant tables must call openbekci_enable_rls", table, enabled, forced, hasPolicy)
			continue
		}
		// Exactly one policy, with the canonical USING and WITH CHECK text.
		if policies != 1 || canonical != 1 {
			t.Errorf("table %s: %d policies, %d with the canonical USING/WITH CHECK expression; want exactly 1 and 1", table, policies, canonical)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no tables found in public; migrations did not run")
	}
}

func TestIntegrationTenantIsolation(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	orgA, orgB := id.New(), id.New()

	for _, o := range []struct {
		id   uuid.UUID
		slug string
	}{{orgA, "alpha"}, {orgB, "beta"}} {
		err := d.InTenantTx(ctx, o.id, func(tx pgx.Tx) error { return insertOrg(ctx, tx, o.id, o.slug) })
		if err != nil {
			t.Fatalf("insert %s: %v", o.slug, err)
		}
	}

	for _, o := range []uuid.UUID{orgA, orgB} {
		err := d.InTenantTx(ctx, o, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id FROM organization`)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			if len(ids) != 1 || ids[0] != o {
				return fmt.Errorf("tenant %s sees %v, want only itself", o, ids)
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}

	err := d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		n, err := countOrgs(ctx, tx)
		if err != nil {
			return err
		}
		if n != 2 {
			return fmt.Errorf("platform sees %d organizations, want 2", n)
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// WITH CHECK: tenant A cannot write a row that belongs to B.
	err = d.InTenantTx(ctx, orgA, func(tx pgx.Tx) error { return insertOrg(ctx, tx, orgB, "forged") })
	if err == nil {
		t.Error("tenant A inserted a row for tenant B")
	}
	// USING: tenant A cannot touch B's row.
	err = d.InTenantTx(ctx, orgA, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE organization SET name = 'hijacked' WHERE id = $1`, orgB)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 0 {
			return fmt.Errorf("tenant A updated %d of B's rows", tag.RowsAffected())
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

func TestIntegrationTenantContextDoesNotLeak(t *testing.T) {
	base := newTestDB(t)
	ctx := context.Background()

	// A single-connection app pool guarantees the second query reuses the
	// connection that carried the tenant context.
	cfg, err := pgxpool.ParseConfig(withDatabase(t, base.superURL, base.name, "openbekci_app", "openbekci"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	d := &DB{app: pool, platform: base.platform}

	org := id.New()
	if err := d.InTenantTx(ctx, org, func(tx pgx.Tx) error { return insertOrg(ctx, tx, org, "alpha") }); err != nil {
		t.Fatal(err)
	}

	// After set_config the GUC reads '' (not NULL) on this connection. The
	// NULLIF in the policy must turn that into "no tenant": zero rows, no error.
	n, err := countOrgs(ctx, pool)
	if err != nil {
		t.Fatalf("query without tenant context errored (NULLIF missing?): %v", err)
	}
	if n != 0 {
		t.Fatalf("query without tenant context saw %d rows, want 0", n)
	}

	// And the tenant context itself did not stick to the connection.
	var guc string
	if err := pool.QueryRow(ctx, `SELECT current_setting('app.org_id', true)`).Scan(&guc); err != nil {
		t.Fatal(err)
	}
	if guc != "" {
		t.Fatalf("app.org_id = %q after transaction, want empty", guc)
	}
}

func TestIntegrationForeignKeysIncludeOrgID(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	// Convention: a foreign key to a table that has org_id must include org_id,
	// so the database refuses cross-organization links (FK checks bypass RLS).
	// Passes vacuously until a second tenant table exists; keep it.
	rows, err := d.platform.Query(ctx, `
		SELECT con.conname::text, child.relname::text
		FROM pg_constraint con
		JOIN pg_class child ON child.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = child.relnamespace AND n.nspname = 'public'
		WHERE con.contype = 'f'
		  AND EXISTS (SELECT 1 FROM pg_attribute a
		              WHERE a.attrelid = con.confrelid AND a.attname = 'org_id' AND NOT a.attisdropped)
		  AND NOT EXISTS (SELECT 1 FROM pg_attribute a
		              WHERE a.attrelid = con.conrelid AND a.attname = 'org_id' AND a.attnum = ANY (con.conkey))`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var con, table string
		if err := rows.Scan(&con, &table); err != nil {
			t.Fatal(err)
		}
		t.Errorf("foreign key %s on %s references a tenant table without org_id; use (org_id, x_id) REFERENCES parent (org_id, id)", con, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRolesAndMigrations(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	if err := d.CheckAppRole(ctx); err != nil {
		t.Errorf("CheckAppRole: %v", err)
	}
	if err := d.CheckPlatformRole(ctx); err != nil {
		t.Errorf("CheckPlatformRole: %v", err)
	}
	if err := d.MigrationsCurrent(ctx); err != nil {
		t.Errorf("MigrationsCurrent: %v", err)
	}
	if err := d.PingApp(ctx); err != nil {
		t.Errorf("PingApp: %v", err)
	}

	// instance_setting is populated at migrate time, exactly once.
	var n int
	if err := d.platform.QueryRow(ctx, `SELECT count(*) FROM instance_setting`).Scan(&n); err != nil || n != 1 {
		t.Errorf("instance_setting rows = %d, err = %v; want 1", n, err)
	}
	// Migrating again is a no-op.
	if err := d.MigrateUp(ctx); err != nil {
		t.Errorf("second migrate up: %v", err)
	}
	// The root table's org_id must equal its id.
	err := d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $2, 'bad', 'bad')`, id.New(), id.New())
		return err
	})
	if err == nil {
		t.Error("organization with org_id != id was accepted")
	}
}

func TestIntegrationDetectPoolerDirectConnection(t *testing.T) {
	d := newTestDB(t)
	info, err := DetectPooler(context.Background(), withDatabase(t, d.superURL, d.name, "openbekci_app", "openbekci"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Detected {
		t.Errorf("direct PostgreSQL connection reported as pooled: %s", info.Reason)
	}
	if info.ServerVersion == "" {
		t.Error("server version empty")
	}
}

// Regression: /readyz used to build a *sql.DB per call and never close it,
// leaking one goroutine each time.
func TestIntegrationReadinessDoesNotLeakGoroutines(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	if err := d.MigrationsCurrent(ctx); err != nil { // warm up
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	for range 200 {
		if err := d.MigrationsCurrent(ctx); err != nil {
			t.Fatal(err)
		}
		// Bypass the cache so the underlying check is exercised every time.
		if err := d.checkMigrations(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Errorf("goroutines grew by %d over 200 readiness checks", grew)
	}
}

// A fresh database must apply the whole chain in version order, without goose's
// allow-missing mode: a migration older than one that is already applied would
// fail on every database that has the newer one. The test checks the applied
// order and that the files are strictly ordered by their version.
func TestIntegrationMigrationsApplyInVersionOrder(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	rows, err := d.migrator.Query(ctx, `SELECT version_id FROM goose_db_version WHERE version_id > 0 ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var applied []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		applied = append(applied, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("no migrations were applied")
	}
	if !slices.IsSorted(applied) {
		t.Errorf("migrations were applied out of version order: %v", applied)
	}
	files, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []int64
	for _, f := range files {
		var v int64
		if _, err := fmt.Sscanf(f.Name(), "%d_", &v); err == nil {
			onDisk = append(onDisk, v)
		}
	}
	slices.Sort(onDisk)
	if !slices.Equal(applied, onDisk) {
		t.Errorf("applied versions %v differ from the files %v", applied, onDisk)
	}
	// The identity chain follows the API pipeline's idempotency migrations
	// (A2 shipped 20261108100000 before identity existed).
	for _, f := range files {
		var v int64
		_, err := fmt.Sscanf(f.Name(), "%d_", &v)
		identity := strings.Contains(f.Name(), "_identity") || strings.Contains(f.Name(), "_people_minimal")
		if err == nil && identity && v <= 20261108100000 {
			t.Errorf("%s is not newer than 20261108100000_idempotency_scope", f.Name())
		}
	}
}
