package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/core/moneyhooks"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/permissions"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
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
func newPeople(cfg Config, log *slog.Logger, d *db.DB, ident *identity.Service, comm *community.Service) (*people.Service, error) {
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
		Hooks:         peopleHooks(comm),
	})
}

// peopleHooks subscribes the community module to the people module's events:
// when an occupancy ends, the visitor credentials its person registered end.
func peopleHooks(comm *community.Service) people.Hooks {
	return people.Hooks{
		OnOccupancyEnded: func(ctx context.Context, tx pgx.Tx, e people.OccupancyEnded) error {
			return comm.OnOccupancyEnded(ctx, tx, e.OrgID, e.UnitID, e.PersonID)
		},
	}
}

// newCommunity builds the community service. Booking fees are charged by the
// money module (bookingCharger, billing's one-off charge path); the notifier is
// the late-bound one, and the QR renderer stays unset (QR images are draft-only).
func newCommunity(cfg Config, log *slog.Logger, d *db.DB, notify jobs.Notifier) (*community.Service, error) {
	return community.NewService(d, community.Config{
		Charger:       bookingCharger{bill: billing.NewService(d, tenancy.NewService(d), nil)},
		Notifier:      notify,
		Hooks:         communityHooks(),
		EncryptionKey: cfg.EncryptionKey,
		BaseURL:       cfg.BaseURL,
		Platform:      d,
		Log:           log,
	})
}

// newFiles builds the file module: the configured blob store (local under
// DATA_DIR/files, or S3), the type allowlist and the optional virus scanner.
// Modules that attach files register their visibility resolvers on the
// returned service before the server starts.
func newFiles(ctx context.Context, cfg Config, log *slog.Logger, d *db.DB) (*files.Service, error) {
	blob, signer, err := storage.New(ctx, storage.Config{
		Driver: cfg.StorageDriver, DataDir: cfg.DataDir, SigningKey: cfg.SecretKey,
		S3: storage.S3Config{
			Bucket: cfg.StorageS3Bucket, Region: cfg.StorageS3Region, Endpoint: cfg.StorageS3Endpoint,
			AccessKey: cfg.StorageS3AccessKey, SecretKey: cfg.StorageS3SecretKey,
			PathStyle: cfg.StorageS3PathStyle, Prefix: cfg.StorageS3Prefix,
		},
	})
	if err != nil {
		return nil, err
	}
	// A crash in the middle of a write leaves ".tmp-*" files (local driver) and
	// spooled uploads in the temp directory; neither is ever picked up again.
	if local, ok := blob.(*storage.Local); ok {
		if n, serr := local.SweepTemp(time.Hour); serr != nil {
			log.Warn("sweeping leftover temporary blobs", "error", serr)
		} else if n > 0 {
			log.Info("removed temporary blobs left by an earlier crash", "count", n)
		}
	}
	if n := files.SweepSpool(time.Hour); n > 0 {
		log.Info("removed spooled uploads left by an earlier crash", "count", n)
	}
	fc := files.Config{
		MaxUploadBytes: cfg.MaxUploadBytes, Types: files.DefaultTypes(), Signer: signer, Log: log,
		MaxImagePixels: cfg.MaxImagePixels, ImageWorkers: cfg.ImageWorkers,
	}
	if len(cfg.FilesAllowedExtensions) > 0 {
		if fc.Types = files.FilterTypes(fc.Types, cfg.FilesAllowedExtensions); len(fc.Types) == 0 {
			return nil, fmt.Errorf("%sFILES_ALLOWED_EXTENSIONS lists no known file type", envPrefix)
		}
	}
	if cfg.ClamAVAddr != "" {
		fc.Scanner = files.ClamAV{Addr: cfg.ClamAVAddr, Timeout: cfg.ClamAVTimeout}
	}
	return files.NewService(d, blob, fc), nil
}

// newGovernance builds the governance service. Decision effects that need the
// money module and document filing use the module's defaults until those
// modules register their implementations (SetEffects, SetFiler); the job client
// is set as the notifier once it exists.
func newGovernance(cfg Config, log *slog.Logger, d *db.DB, fs *files.Service) *governance.Service {
	g := governance.NewService(d, d, governance.Config{BaseURL: cfg.BaseURL, InstanceName: cfg.InstanceName, Log: log})
	g.SetFileChecker(fs) // proof files of notices are files of the file store
	return g
}

// newAccess builds the access-control service: agents authenticate with their
// own tokens, the rotated token is sealed with ENCRYPTION_KEY, and visitor
// credentials come from the community module once it supplies a source (until
// then the projector handles vehicles only).
func newAccess(cfg Config, log *slog.Logger, d *db.DB, comm *community.Service) (*access.Service, error) {
	acfg := access.Config{
		Source:  communityCredentials{c: comm},
		Guards:  comm,
		BaseURL: cfg.BaseURL, InsecureDev: cfg.InsecureDev, Log: log, MaxSnapshotEntries: cfg.AccessMaxSnapshotEntries,
		SnapshotPartEntries: cfg.AccessSnapshotPartEntries, SnapshotPartBytes: cfg.AccessSnapshotPartBytes,
	}
	if len(cfg.EncryptionKey) > 0 {
		sealer, err := crypto.NewFieldCipher(cfg.EncryptionKey, "access.agent_token")
		if err != nil {
			return nil, err
		}
		acfg.Sealer = sealer
	}
	return access.NewService(d, acfg), nil
}

// apiDeps wires the HTTP API's dependencies: instance info, identity
// (sessions, sign-in methods) and the authenticator built on it.
func apiDeps(cfg Config, log *slog.Logger, d *db.DB, idem idempotency.Store, fs *files.Service, communitySvc *community.Service, mny httpapi.MoneyDeps) (httpapi.Deps, error) {
	ident, err := newIdentity(cfg, log, d)
	if err != nil {
		return httpapi.Deps{}, err
	}
	authz.SetScopeLoader(tenancy.ScopeLoader{}) // sites and blocks exist from phase B on
	peopleSvc, err := newPeople(cfg, log, d, ident, communitySvc)
	if err != nil {
		return httpapi.Deps{}, err
	}
	peopleSvc.SetMoneyHooks(moneyhooks.MoveOut{})
	reg, err := permissions.NewRegistry()
	if err != nil {
		return httpapi.Deps{}, err
	}
	methods := &instance.AuthMethodRegistry{}
	methods.Register(ident) // password always, sms_otp and magic_link when configured
	devs, err := newDevices(cfg, d)
	if err != nil {
		return httpapi.Deps{}, err
	}
	return httpapi.Deps{
		Log:         log,
		DB:          d,
		Idempotency: idem,
		Identity:    ident,
		Devices:     devs,
		Authz:       authz.NewResolver(d, reg, nil),
		Tenancy:     tenancy.NewService(d).WithArchiveKey(cfg.SecretKey),
		People:      peopleSvc,
		Community:   communitySvc,
		Money:       mny,
		Audit:       audit.NewService(d),
		Files:       fs,
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
			PushRelayIDs:         pushRelayIDs(cfg),
			UnifiedPush:          true,
		}, instance.PGIDSource{DB: d}, methods).WithChallenges(instance.PGChallenges{DB: d}),
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
