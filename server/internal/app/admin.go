package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	chatcore "github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
	"github.com/openbekci/openbekci/server/internal/core/privacy"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/packs"
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
	cfg      Config
	packs    *packs.Host
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
	host, err := packs.New(packs.Config{DB: d, Log: log}, cfg.Packs)
	if err == nil {
		err = host.Start(ctx)
	}
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("packs: %w", err)
	}
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	return &Admin{Identity: ident, Platform: platformadmin.NewService(d), Tenancy: tenancy.NewService(d).WithArchiveKey(cfg.SecretKey), db: d, encKey: cfg.EncryptionKey, packs: host, cfg: cfg}, nil
}

// PurgePack deletes a pack's rows for an organization (see packs.Host.Purge).
func (a *Admin) PurgePack(ctx context.Context, org uuid.UUID, pack string) (packs.PurgeResult, error) {
	return a.packs.Purge(ctx, org, pack)
}

// PackTables lists the pack's tenant tables, for the purge confirmation.
func (a *Admin) PackTables(ctx context.Context, pack string) ([]string, error) {
	var out []string
	err := a.db.InPlatformTx(ctx, func(tx pgx.Tx) (err error) {
		out, err = a.packs.PurgeTables(ctx, tx, pack)
		return err
	})
	return out, err
}

// HasPack reports whether the binary contains the pack.
func (a *Admin) HasPack(id string) bool {
	_, ok := a.packs.Lookup(id)
	return ok
}

// ErasePerson runs the privacy erase as the operator (see privacy.Service.EraseAsOperator).
func (a *Admin) ErasePerson(ctx context.Context, org, person uuid.UUID, reason string, force bool) (privacy.EraseResult, error) {
	return privacy.NewService(a.db, privacy.Config{}).EraseAsOperator(ctx, org, person, reason, force)
}

// ChatSetWebhook registers an organization's chat bot webhook with the provider.
func (a *Admin) ChatSetWebhook(ctx context.Context, org uuid.UUID, provider, url string) (string, error) {
	svc, err := chatcore.NewService(a.db, chatcore.Config{EncryptionKey: a.encKey, Packs: nil, BaseURL: a.cfg.BaseURL})
	if err != nil {
		return "", err
	}
	return svc.RegisterWebhook(ctx, org, provider, url)
}

// Close releases the database connections.
func (a *Admin) Close() { a.db.Close() }

// ResealNationalIDs rewrites national ids sealed in the old format under the current key id.
func (a *Admin) ResealNationalIDs(ctx context.Context) (done, failed int, err error) {
	return people.ResealNationalIDs(ctx, a.db, a.encKey)
}
