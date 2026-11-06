// Package dbtest creates throwaway migrated databases for integration tests.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/db"
)

//nolint:gosec // local development default, overridden by OPENBEKCI_TEST_SUPERUSER_URL
const defaultSuperuserURL ="postgres://postgres:postgres@localhost:5432/postgres"

// New creates a database, creates the three roles if missing, migrates it and
// returns a DB opened on it as the application roles. The database is dropped
// when the test ends. The test is skipped in -short mode or when PostgreSQL
// (OPENBEKCI_TEST_SUPERUSER_URL) is unreachable.
func New(t testing.TB) *db.DB {
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

	dbSuper, err := pgx.Connect(ctx, withDatabase(t, superURL, name, "", ""))
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

	d, err := db.Open(ctx, db.Options{
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
	return d
}

func withDatabase(t testing.TB, base, dbName, user, password string) string {
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
