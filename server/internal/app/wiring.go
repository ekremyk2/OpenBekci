package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
)

// newIdentity builds the identity service from the configuration.
func newIdentity(cfg Config, log *slog.Logger, d *db.DB) (*identity.Service, error) {
	gw, err := newSMSGateway(cfg, log)
	if err != nil {
		return nil, err
	}
	mailer, err := newMailer(cfg, log)
	if err != nil {
		return nil, err
	}
	return identity.NewService(d, identity.Config{
		InstanceName: cfg.InstanceName,
		BaseURL:      cfg.BaseURL,
		SecretKey:    cfg.SecretKey,
		SMS:          gw,
		Mail:         mailer,
		Log:          log,
	}), nil
}

// apiDeps wires the HTTP API's dependencies: instance info, identity
// (sessions, sign-in methods) and the authenticator built on it.
func apiDeps(cfg Config, log *slog.Logger, d *db.DB, idem idempotency.Store) (httpapi.Deps, error) {
	ident, err := newIdentity(cfg, log, d)
	if err != nil {
		return httpapi.Deps{}, err
	}
	methods := &instance.AuthMethodRegistry{}
	methods.Register(ident) // password always, sms_otp and magic_link when configured
	return httpapi.Deps{
		Log:         log,
		DB:          d,
		Idempotency: idem,
		Identity:    ident,
		Authenticator: httpapi.SessionAuthenticator{
			Identity: ident,
			Origins:  append([]string{cfg.BaseURL}, cfg.ExtraOrigins...),
		},
		InsecureCookies: cfg.InsecureDev,
		Instance: instance.NewService(instance.Config{
			Name:                 cfg.InstanceName,
			BaseURL:              cfg.BaseURL,
			ServerVersion:        cfg.ServerVersion,
			MinAppVersionAndroid: cfg.MinAppVersionAndroid,
			MinAppVersionIOS:     cfg.MinAppVersionIOS,
			LogoURL:              cfg.BrandingLogoURL,
			PrimaryColor:         cfg.BrandingPrimaryColor,
		}, instance.PGIDSource{DB: d}, methods),
		ValidateResponses: cfg.ValidateResponses,
	}, nil
}

// purgeIdempotency removes expired idempotency keys hourly until ctx ends.
func purgeIdempotency(ctx context.Context, log *slog.Logger, s *idempotency.PG) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.Purge(ctx); err != nil {
				log.Error("purge idempotency keys", "error", err)
			} else if n > 0 {
				log.Info("purged idempotency keys", "count", n)
			}
		}
	}
}

// pruneIdentity removes expired sessions, codes and old login attempts hourly
// until ctx ends.
func pruneIdentity(ctx context.Context, log *slog.Logger, s *identity.Service) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.Prune(ctx); err != nil {
				log.Error("prune identity tables", "error", err)
			} else if n > 0 {
				log.Info("pruned identity rows", "count", n)
			}
		}
	}
}
