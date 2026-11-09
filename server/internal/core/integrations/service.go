package integrations

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
)

// DB is the database access the module needs, satisfied by *db.DB.
type DB interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// InsertFunc inserts a River job in the caller's transaction (a closure over
// the job client: the client is built after the services whose workers it runs).
type InsertFunc func(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error

// Defaults.
const (
	// DefaultMaxAttempts is the bound on delivery attempts per (event, endpoint).
	DefaultMaxAttempts = 8
	// DefaultKeyRatePerMinute is the sustained request rate per API key.
	DefaultKeyRatePerMinute = 600
	// touchInterval throttles last_used_at writes of API keys.
	touchInterval = 5 * time.Minute
)

// Config configures the Service.
type Config struct {
	// Registry validates the scopes of API keys.
	Registry *authz.Registry
	// EncryptionKey seals webhook signing secrets (ENCRYPTION_KEY).
	EncryptionKey []byte
	// Notifier tells the organization's administrators when an endpoint keeps failing.
	Notifier jobs.Notifier
	Log      *slog.Logger
	// AllowedCIDRs are private or loopback ranges webhooks may reach
	// (WEBHOOK_ALLOWED_CIDRS); empty keeps the SSRF guard strict.
	AllowedCIDRs []netip.Prefix
	// MaxAttempts bounds delivery attempts (WEBHOOK_MAX_ATTEMPTS); 0 means DefaultMaxAttempts.
	MaxAttempts int
	// KeyRatePerMinute limits requests per API key (API_KEY_RATE_PER_MINUTE);
	// 0 means DefaultKeyRatePerMinute, a negative value removes the limit.
	KeyRatePerMinute int
	// Client sends deliveries; nil builds the guarded client (see NewGuardedClient).
	Client *http.Client
	// TLSConfig is the guarded client's TLS configuration (tests pass the receiver's roots).
	TLSConfig *tls.Config
	// Resolver resolves webhook hosts; nil means the system resolver.
	Resolver Resolver
	// Now and Backoff are for tests; nil means time.Now and RetryDelay.
	Now     func() time.Time
	Backoff func(attempt int) time.Duration
}

// Service holds API keys and webhooks.
type Service struct {
	db      DB
	cfg     Config
	cipher  *crypto.FieldCipher
	client  *http.Client
	limiter *ratelimit.Limiter
	insert  atomic.Pointer[InsertFunc]
	maxTry  int
}

// NewService builds the service. A missing encryption key is only an error for
// webhook operations (they report an internal error), so API keys keep working.
func NewService(d DB, cfg Config) (*Service, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &Service{db: d, cfg: cfg, maxTry: cfg.MaxAttempts}
	if s.maxTry <= 0 {
		s.maxTry = DefaultMaxAttempts
	}
	if len(cfg.EncryptionKey) > 0 {
		c, err := crypto.NewFieldCipher(cfg.EncryptionKey, secretCipherPurpose)
		if err != nil {
			return nil, err
		}
		s.cipher = c
	}
	if cfg.Resolver == nil {
		s.cfg.Resolver = net.DefaultResolver
	}
	s.client = cfg.Client
	if s.client == nil {
		s.client = newGuardedClient(s.cfg.Resolver, cfg.AllowedCIDRs, cfg.TLSConfig)
	}
	rate := cfg.KeyRatePerMinute
	if rate == 0 {
		rate = DefaultKeyRatePerMinute
	}
	if rate > 0 {
		s.limiter = ratelimit.New(max(rate/6, 10), time.Minute/time.Duration(rate)) // burst of ten seconds' worth
	}
	return s, nil
}

// SetInsert installs the job insert function once the River client exists.
// Events emitted before that are dropped with a warning.
func (s *Service) SetInsert(f InsertFunc) { s.insert.Store(&f) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) backoff(attempt int) time.Duration {
	if s.cfg.Backoff != nil {
		return s.cfg.Backoff(attempt)
	}
	return RetryDelay(attempt)
}

// RetryDelay is the wait after the given failed attempt (1-based): 30 seconds
// doubling per attempt, capped at four hours (30 s, 1 m, 2 m, 4 m, 8 m, 16 m,
// 32 m, ...). With the default eight attempts a delivery is abandoned about
// 64 minutes after the first try.
func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 30 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= 4*time.Hour {
			return 4 * time.Hour
		}
	}
	return d
}
