package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
)

// apiDeps wires the HTTP API's dependencies. The sessions task sets
// Authenticator and registers its real AuthMethodSources here.
func apiDeps(cfg Config, log *slog.Logger, d *db.DB, idem idempotency.Store) httpapi.Deps {
	methods := &instance.AuthMethodRegistry{}
	methods.Register(instance.DefaultAuthMethods())
	return httpapi.Deps{
		Log:         log,
		DB:          d,
		Idempotency: idem,
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
	}
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
