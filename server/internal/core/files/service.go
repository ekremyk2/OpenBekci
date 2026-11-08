// Package files is the file module (backend plan section 13): uploads that
// stream through the server into a blob store, content sniffing, metadata
// stripping and variants for images, visibility checks on every read, an
// optional virus scan, soft deletion and retention.
//
// Other modules attach files to their resources by registering a scope type
// with a VisibilityResolver (see RegisterResolver) and calling Attach in their
// own transaction; server-generated documents arrive through UploadGenerated.
package files

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

// TxRunner is the database access the module needs, satisfied by *db.DB.
// InPlatformTx is for the maintenance job only (it works across
// organizations); everything a request does goes through InTenantTx.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Defaults.
const (
	// DefaultMaxUploadBytes is the size limit of one uploaded file (25 MiB).
	DefaultMaxUploadBytes = 25 << 20
	// DefaultMaxImageDimension is the longest side the stored original of an
	// image is downscaled to; variants are smaller still.
	DefaultMaxImageDimension = 4096
	// DefaultUnattachedTTL is how long an upload that was never attached to a
	// resource is kept.
	DefaultUnattachedTTL = 7 * 24 * time.Hour
	// DefaultLinkTTL is how long the download links in file metadata work.
	DefaultLinkTTL = 5 * time.Minute
	// DefaultUploadsPerMinute is the per-user upload rate.
	DefaultUploadsPerMinute = 30
)

// Config configures the Service. Zero values take the defaults above.
type Config struct {
	// MaxUploadBytes limits one file.
	MaxUploadBytes int64
	// Types is the extension allowlist; nil means DefaultTypes().
	Types []TypeRule
	// MaxImagePixels refuses images with more pixels than this (default 25 million).
	MaxImagePixels int
	// ImageWorkers is how many images are decoded and re-encoded at once in the
	// process (default max(1, NumCPU/2)); others wait their turn.
	ImageWorkers int
	// UploadsPerMinute is each user's upload rate (default 30; a burst of that
	// many is allowed); beyond it Upload answers 429 rate_limited.
	UploadsPerMinute int
	// MaxImageDimension downscales stored images.
	MaxImageDimension int
	// UnattachedTTL is the lifetime of an upload without a scope.
	UnattachedTTL time.Duration
	// LinkTTL is the lifetime of the download links in file metadata.
	LinkTTL time.Duration
	// Scanner scans uploads; nil skips scanning.
	Scanner Scanner
	// Signer verifies the signed download URLs the local driver hands out.
	Signer *storage.Signer
	Log    *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service is the file module.
type Service struct {
	db   TxRunner
	blob storage.Blob
	cfg  Config

	imageSem chan struct{}
	uploads  *ratelimit.Limiter

	mu     sync.RWMutex
	scopes map[string]scopeEntry
}

type scopeEntry struct {
	resolver VisibilityResolver
	maxFiles int
}

// NewService returns a Service storing blobs in blob.
func NewService(db TxRunner, blob storage.Blob, cfg Config) *Service {
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if cfg.Types == nil {
		cfg.Types = DefaultTypes()
	}
	if cfg.MaxImagePixels <= 0 {
		cfg.MaxImagePixels = DefaultMaxImagePixels
	}
	if cfg.UploadsPerMinute <= 0 {
		cfg.UploadsPerMinute = DefaultUploadsPerMinute
	}
	if cfg.ImageWorkers <= 0 {
		cfg.ImageWorkers = max(1, runtime.NumCPU()/2)
	}
	if cfg.MaxImageDimension <= 0 {
		cfg.MaxImageDimension = DefaultMaxImageDimension
	}
	if cfg.UnattachedTTL <= 0 {
		cfg.UnattachedTTL = DefaultUnattachedTTL
	}
	if cfg.LinkTTL <= 0 {
		cfg.LinkTTL = DefaultLinkTTL
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{db: db, blob: blob, cfg: cfg, imageSem: make(chan struct{}, cfg.ImageWorkers), uploads: ratelimit.New(cfg.UploadsPerMinute, time.Minute/time.Duration(cfg.UploadsPerMinute)), scopes: map[string]scopeEntry{}}
}

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

// MaxUploadBytes is the size limit of one file.
func (s *Service) MaxUploadBytes() int64 { return s.cfg.MaxUploadBytes }

// VisibilityResolver tells the file module who may read and attach files of a
// resource. The module that owns the resource implements it (a ticket's
// photos are visible to whoever can see the ticket) and registers it under its
// scope type with RegisterResolver.
//
// Both methods run inside the request's tenant transaction (use tx for any
// query, so they see a consistent state and respect row-level security) with
// the caller's principal and grants in ctx. They answer false, not an error,
// for a resource that does not exist or is out of the caller's reach.
type VisibilityResolver interface {
	// CanRead reports whether the caller may read the files attached to ref.
	CanRead(ctx context.Context, tx pgx.Tx, ref ScopeRef) (bool, error)
	// CanAttach reports whether the caller may add files to ref.
	CanAttach(ctx context.Context, tx pgx.Tx, ref ScopeRef) (bool, error)
}

// ResolverFuncs adapts two functions to a VisibilityResolver. A nil function
// answers false.
type ResolverFuncs struct {
	Read   func(ctx context.Context, tx pgx.Tx, ref ScopeRef) (bool, error)
	Attach func(ctx context.Context, tx pgx.Tx, ref ScopeRef) (bool, error)
}

// CanRead implements VisibilityResolver.
func (r ResolverFuncs) CanRead(ctx context.Context, tx pgx.Tx, ref ScopeRef) (bool, error) {
	if r.Read == nil {
		return false, nil
	}
	return r.Read(ctx, tx, ref)
}

// CanAttach implements VisibilityResolver.
func (r ResolverFuncs) CanAttach(ctx context.Context, tx pgx.Tx, ref ScopeRef) (bool, error) {
	if r.Attach == nil {
		return false, nil
	}
	return r.Attach(ctx, tx, ref)
}

// ScopeOption tunes a registered scope type.
type ScopeOption func(*scopeEntry)

// WithMaxFiles limits how many live files one resource may have (photos per
// ticket: 10). Uploads and Attach beyond it answer 409 conflict.
func WithMaxFiles(n int) ScopeOption { return func(e *scopeEntry) { e.maxFiles = n } }

// RegisterResolver registers the visibility resolver of a scope type. Call it
// once per type while wiring, before serving requests; a second registration
// of the same type replaces the first. Scope types are short lower-case
// names such as "ticket" or "document".
func (s *Service) RegisterResolver(scopeType string, r VisibilityResolver, opts ...ScopeOption) {
	if !scopeTypeRE.MatchString(scopeType) {
		panic("files: invalid scope type " + scopeType)
	}
	e := scopeEntry{resolver: r}
	for _, o := range opts {
		o(&e)
	}
	s.mu.Lock()
	s.scopes[scopeType] = e
	s.mu.Unlock()
}

func (s *Service) scope(scopeType string) (scopeEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.scopes[scopeType]
	return e, ok
}
