package db

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/openbekci/openbekci/server/db/migrations"
)

var errNoMigrator = errors.New("db: migrator pool not open")

func gooseProvider(pool *pgxpool.Pool) (*goose.Provider, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("create goose provider: %w", err)
	}
	return p, nil
}

func riverMigrator(pool *pgxpool.Pool) (*rivermigrate.Migrator[pgx.Tx], error) {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return nil, fmt.Errorf("create river migrator: %w", err)
	}
	return m, nil
}

// MigrateUp applies pending goose migrations, then River's own. It runs on the
// migrator pool.
func (d *DB) MigrateUp(ctx context.Context) error {
	if d.migrator == nil {
		return errNoMigrator
	}
	gp, err := gooseProvider(d.migrator)
	if err != nil {
		return err
	}
	if _, err := gp.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	rm, err := riverMigrator(d.migrator)
	if err != nil {
		return err
	}
	if _, err := rm.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back the most recent goose migration. River's schema is
// left alone; it is versioned independently.
func (d *DB) MigrateDown(ctx context.Context) error {
	if d.migrator == nil {
		return errNoMigrator
	}
	gp, err := gooseProvider(d.migrator)
	if err != nil {
		return err
	}
	if _, err := gp.Down(ctx); err != nil {
		return fmt.Errorf("goose down: %w", err)
	}
	return nil
}

// MigrateStatus writes the goose and River migration state to w.
func (d *DB) MigrateStatus(ctx context.Context, w io.Writer) error {
	if d.migrator == nil {
		return errNoMigrator
	}
	gp, err := gooseProvider(d.migrator)
	if err != nil {
		return err
	}
	st, err := gp.Status(ctx)
	if err != nil {
		return fmt.Errorf("goose status: %w", err)
	}
	for _, s := range st {
		state := "pending"
		if s.State == goose.StateApplied {
			state = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "goose  %-45s %s\n", s.Source.Path, state)
	}
	rm, err := riverMigrator(d.migrator)
	if err != nil {
		return err
	}
	res, err := rm.Migrate(ctx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{DryRun: true})
	if err != nil {
		return fmt.Errorf("river status: %w", err)
	}
	if len(res.Versions) == 0 {
		fmt.Fprintln(w, "river  up to date")
	} else {
		fmt.Fprintf(w, "river  %d migration(s) pending\n", len(res.Versions))
	}
	return nil
}

// MigrationsCurrent reports whether every goose migration and River's schema
// are applied. Used by /readyz; it runs on the app pool.
func (d *DB) MigrationsCurrent(ctx context.Context) error {
	gp, err := gooseProvider(d.app)
	if err != nil {
		return err
	}
	pending, err := gp.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("goose pending check: %w", err)
	}
	if pending {
		return errors.New("database migrations are pending; run `openbekci migrate up`")
	}
	rm, err := riverMigrator(d.app)
	if err != nil {
		return err
	}
	res, err := rm.Validate(ctx, nil)
	if err != nil {
		return fmt.Errorf("river validate: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("river migrations are not current: %v", res.Messages)
	}
	return nil
}
