package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/permissions"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
)

// newIdentity builds the identity service from the configuration. It also keys
// the audit hashes of PII fields (HKDF of SECRET_KEY): every process that
// records audit events builds the identity service, so the key is always set
// before the first event.
func newIdentity(cfg Config, log *slog.Logger, d *db.DB) (*identity.Service, error) {
	if err := audit.SetPIIKey(cfg.SecretKey); err != nil {
		return nil, err
	}
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

		SendDailyBudget: cfg.SendDailyBudget, SMSHourlyBudget: cfg.SMSHourlyBudget, MailHourlyBudget: cfg.MailHourlyBudget,
	}), nil
}

// newPeople builds the people service: national ids are sealed with
// ENCRYPTION_KEY and invitations go out through the SMS and mail gateways.
func newPeople(cfg Config, log *slog.Logger, d *db.DB, ident *identity.Service) (*people.Service, error) {
	gw, err := newSMSGateway(cfg, log)
	if err != nil {
		return nil, err
	}
	mailer, err := newMailer(cfg, log)
	if err != nil {
		return nil, err
	}
	return people.NewService(d, ident, people.Config{
		EncryptionKey: cfg.EncryptionKey,
		BaseURL:       cfg.BaseURL,
		InstanceName:  cfg.InstanceName,
		SMS:           gw,
		Mail:          mailer,
		Log:           log,
	})
}

// newCommunity builds the community service. The money, notifications and QR
// modules are not wired yet, so the booking charger and the notifier are the
// no-op defaults (a booking stores its fee, a notification is dropped); a later
// phase passes the real ones here.
func newCommunity(cfg Config, log *slog.Logger, d *db.DB) (*community.Service, error) {
	return community.NewService(d, community.Config{
		EncryptionKey: cfg.EncryptionKey,
		BaseURL:       cfg.BaseURL,
		Platform:      d,
		Log:           log,
	})
}

// apiDeps wires the HTTP API's dependencies: instance info, identity
// (sessions, sign-in methods) and the authenticator built on it.
func apiDeps(cfg Config, log *slog.Logger, d *db.DB, idem idempotency.Store) (httpapi.Deps, error) {
	ident, err := newIdentity(cfg, log, d)
	if err != nil {
		return httpapi.Deps{}, err
	}
	authz.SetScopeLoader(tenancy.ScopeLoader{}) // sites and blocks exist from phase B on
	peopleSvc, err := newPeople(cfg, log, d, ident)
	if err != nil {
		return httpapi.Deps{}, err
	}
	communitySvc, err := newCommunity(cfg, log, d)
	if err != nil {
		return httpapi.Deps{}, err
	}
	reg, err := permissions.NewRegistry()
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
		Authz:       authz.NewResolver(d, reg, nil),
		Tenancy:     tenancy.NewService(d),
		People:      peopleSvc,
		Community:   communitySvc,
		Audit:       audit.NewService(d),
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
