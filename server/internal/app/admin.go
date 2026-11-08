package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/db"
)

// Admin bundles what the admin commands need: the identity and platform
// administration services on their own database connections.
type Admin struct {
	Identity *identity.Service
	Platform *platformadmin.Service
	Tenancy  *tenancy.Service
	db       *db.DB
	encKey   []byte
}

// OpenAdmin connects as the app and platform roles and builds the services.
// Close releases the connections.
func OpenAdmin(ctx context.Context, cfg Config, log *slog.Logger) (*Admin, error) {
	if cfg.DatabaseURL == "" {
		return nil, errors.New("OPENBEKCI_DATABASE_URL is required")
	}
	d, err := db.Open(ctx, db.Options{AppURL: cfg.DatabaseURL, PlatformURL: cfg.PlatformDatabaseURL})
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	ident, err := newIdentity(cfg, log, d)
	if err != nil {
		d.Close()
		return nil, err
	}
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	return &Admin{Identity: ident, Platform: platformadmin.NewService(d), Tenancy: tenancy.NewService(d).WithArchiveKey(cfg.SecretKey), db: d, encKey: cfg.EncryptionKey}, nil
}

// Close releases the database connections.
func (a *Admin) Close() { a.db.Close() }

// ResealNationalIDs rewrites national ids sealed in the old format under the current key id.
func (a *Admin) ResealNationalIDs(ctx context.Context) (done, failed int, err error) {
	return people.ResealNationalIDs(ctx, a.db, a.encKey)
}
