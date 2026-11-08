// Package db owns the PostgreSQL connection pools.
//
// The pools are unexported on purpose: business code reaches the database only
// through InTenantTx and InPlatformTx. Everything else here is for wiring
// (cmd, app) and must not be used from internal/core (enforced by depguard).
package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

// Options configures Open.
type Options struct {
	// AppURL connects as openbekci_app (subject to RLS).
	AppURL string
	// PlatformURL connects as openbekci_platform (BYPASSRLS).
	PlatformURL string
	// MigratorURL connects as openbekci_migrator. Leave empty to not open the
	// migrator pool; only the migrate command needs it.
	MigratorURL string
	// Pooled switches pgx to describe-based statement caching, which works
	// behind a transaction-mode pooler such as PgBouncer.
	Pooled bool
	// SidePoolMax caps the side pool used by InIndependentTenantTx (zero means
	// DefaultSidePoolMax). It adds to the app pool's connections.
	SidePoolMax int
}

// DefaultSidePoolMax is the side pool size when Options.SidePoolMax is unset.
const DefaultSidePoolMax = 4

// DB holds the connection pools.
type DB struct {
	app *pgxpool.Pool
	// side serves InIndependentTenantTx: transactions opened while the caller
	// already holds an app connection. Nothing that runs on side waits for an
	// app connection, so those waits always end.
	side     *pgxpool.Pool
	platform *pgxpool.Pool
	migrator *pgxpool.Pool

	// appGoose answers the readiness check; built once in Open and released
	// in Close.
	appGoose      *goose.Provider
	closeAppGoose func()

	readyMu  sync.Mutex
	readyAt  time.Time
	readyErr error
}

// Open creates the pools whose URLs are set. Close must be called when done.
func Open(ctx context.Context, o Options) (*DB, error) {
	d := &DB{}
	var err error
	if o.AppURL != "" {
		if d.app, err = openPool(ctx, o.AppURL, o.Pooled, 0); err != nil {
			return nil, fmt.Errorf("open app pool: %w", err)
		}
		sideMax := o.SidePoolMax
		if sideMax < 1 {
			sideMax = DefaultSidePoolMax
		}
		if d.side, err = openPool(ctx, o.AppURL, o.Pooled, int32(sideMax)); err != nil {
			d.Close()
			return nil, fmt.Errorf("open side pool: %w", err)
		}
		if d.appGoose, d.closeAppGoose, err = gooseProvider(d.app, false); err != nil {
			d.Close()
			return nil, err
		}
	}
	if o.PlatformURL != "" {
		if d.platform, err = openPool(ctx, o.PlatformURL, o.Pooled, 0); err != nil {
			d.Close()
			return nil, fmt.Errorf("open platform pool: %w", err)
		}
	}
	if o.MigratorURL != "" {
		if d.migrator, err = openPool(ctx, o.MigratorURL, o.Pooled, 0); err != nil {
			d.Close()
			return nil, fmt.Errorf("open migrator pool: %w", err)
		}
	}
	return d, nil
}

func openPool(ctx context.Context, url string, pooled bool, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if pooled {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Close closes all pools.
func (d *DB) Close() {
	if d.closeAppGoose != nil {
		d.closeAppGoose()
		d.closeAppGoose = nil
	}
	for _, p := range []*pgxpool.Pool{d.app, d.side, d.platform, d.migrator} {
		if p != nil {
			p.Close()
		}
	}
}

// InTenantTx runs fn in a transaction on the RLS-subject pool with the tenant
// context set to orgID. It commits if fn returns nil and rolls back otherwise.
// This is the only way business code reaches a tenant connection.
func (d *DB) InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error {
	return inTx(ctx, d.app, orgID.String(), fn)
}

// InIndependentTenantTx is InTenantTx for a transaction that must commit on
// its own connection while the caller's own transaction stays open (a draft
// stored before the caller's transaction can roll it back). It uses a separate
// pool: drawing the second connection from the app pool lets N callers that
// each hold one wait forever for the connection another holds once N reaches
// the pool size.
//
// The rule: any code that may run inside another InTenantTx and needs its own
// transaction must use this, never InTenantTx; and fn must never call
// InTenantTx (or anything that does), or the guarantee is gone. The pool's
// size is OPENBEKCI_DATABASE_SIDE_POOL_MAX.
func (d *DB) InIndependentTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error {
	pool := d.side
	if pool == nil {
		pool = d.app
	}
	return inTx(ctx, pool, orgID.String(), fn)
}

// InAppTx runs fn in a transaction on the RLS-subject pool with no tenant
// context. Use it only for tables that are not tenant data (instance_setting,
// idempotency_key); tenant tables return no rows without a tenant context.
func (d *DB) InAppTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return inTx(ctx, d.app, "", fn)
}

// InPlatformTx runs fn in a transaction on the BYPASSRLS platform pool. Only
// platformadmin and the cross-organization parts of GET /v1/me may use it.
func (d *DB) InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return inTx(ctx, d.platform, "", fn)
}

func inTx(ctx context.Context, pool *pgxpool.Pool, orgID string, fn func(pgx.Tx) error) (err error) {
	if pool == nil {
		return errors.New("db: pool not open")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if orgID != "" {
		// set_config(..., is_local=true) instead of SET LOCAL: it takes bind
		// parameters and is scoped to this transaction, so it cannot leak.
		if _, err = tx.Exec(ctx, `SELECT set_config('app.org_id', $1, true)`, orgID); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Migrator returns the migrator pool. For cmd and app wiring only.
func (d *DB) Migrator() *pgxpool.Pool { return d.migrator }

// WorkerPool returns the pool River uses. For app wiring only.
func (d *DB) WorkerPool() *pgxpool.Pool { return d.app }

// PingApp checks that the app pool can run a query.
func (d *DB) PingApp(ctx context.Context) error {
	var one int
	if err := d.app.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return fmt.Errorf("app pool query: %w", err)
	}
	return nil
}

const roleBypassQuery = `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user` //nolint:gosec // G101 false positive: SQL text, not a credential

// CheckAppRole verifies the app role is subject to row-level security.
func (d *DB) CheckAppRole(ctx context.Context) error {
	var bypass bool
	if err := d.app.QueryRow(ctx, roleBypassQuery).Scan(&bypass); err != nil {
		return fmt.Errorf("query app role: %w", err)
	}
	if bypass {
		return errors.New("app database role bypasses row-level security (BYPASSRLS or superuser); tenant isolation would not hold")
	}
	return nil
}

// CheckPlatformRole verifies the platform role bypasses row-level security.
func (d *DB) CheckPlatformRole(ctx context.Context) error {
	var bypass bool
	if err := d.platform.QueryRow(ctx, roleBypassQuery).Scan(&bypass); err != nil {
		return fmt.Errorf("query platform role: %w", err)
	}
	if !bypass {
		return errors.New("platform database role lacks BYPASSRLS")
	}
	return nil
}

// WarnIfPlatformSuperuser logs a warning when the platform role is a
// superuser. That still bypasses RLS as required, but is over-privileged: it
// should be a plain BYPASSRLS role.
func (d *DB) WarnIfPlatformSuperuser(ctx context.Context, log *slog.Logger) {
	var super bool
	if err := d.platform.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		log.Warn("could not check whether the platform role is a superuser", "error", err)
		return
	}
	if super {
		log.Warn("platform database role is a superuser; use a dedicated BYPASSRLS role (openbekci_platform) instead")
	}
}
