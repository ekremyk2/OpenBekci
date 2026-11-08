// Package instance builds the instance discovery document (overview section
// 6.8, backend plan section 8) and its authenticated twin.
package instance

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CacheTTL is how long a built document is reused.
const CacheTTL = 60 * time.Second

// Known authentication method ids. oidc providers use "oidc:<provider>".
const (
	MethodPassword  = "password"
	MethodSMSOTP    = "sms_otp"
	MethodMagicLink = "magic_link"
)

// AuthMethod is one entry of auth_methods.
type AuthMethod struct {
	ID       string
	LabelKey string
	Icon     string
}

// AuthMethodSource reports the login methods that are currently available.
// The sessions task registers real sources (SMS gateway configured, OIDC
// providers, ...).
type AuthMethodSource interface {
	AuthMethods(ctx context.Context) []AuthMethod
}

// StaticAuthMethods is a source with a fixed list.
type StaticAuthMethods []AuthMethod

// AuthMethods implements AuthMethodSource.
func (s StaticAuthMethods) AuthMethods(context.Context) []AuthMethod { return s }

// DefaultAuthMethods are the built-in methods: password, SMS OTP, magic link.
func DefaultAuthMethods() StaticAuthMethods {
	return StaticAuthMethods{{ID: MethodPassword}, {ID: MethodSMSOTP}, {ID: MethodMagicLink}}
}

// AuthMethodRegistry collects AuthMethodSources registered at wiring time.
type AuthMethodRegistry struct {
	mu      sync.RWMutex
	sources []AuthMethodSource
}

// Register adds a source. Sources are listed in registration order.
func (r *AuthMethodRegistry) Register(s AuthMethodSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources = append(r.sources, s)
}

// Methods returns all available methods, in registration order.
func (r *AuthMethodRegistry) Methods(ctx context.Context) []AuthMethod {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []AuthMethod{}
	for _, s := range r.sources {
		out = append(out, s.AuthMethods(ctx)...)
	}
	return out
}

// Config is the instance configuration the document is built from.
type Config struct {
	Name                 string
	BaseURL              string // public URL without trailing slash
	ServerVersion        string
	MinAppVersionAndroid string
	MinAppVersionIOS     string
	LogoURL              string
	PrimaryColor         string
	// PushRelayIDs are the relay ids devices may register with
	// (OPENBEKCI_PUSH_RELAYS); empty when push through a relay is not configured.
	PushRelayIDs []string
	// UnifiedPush says the server posts to UnifiedPush endpoints directly.
	UnifiedPush bool
}

// Document is the discovery document.
type Document struct {
	InstanceID    uuid.UUID
	Name          string
	APIBaseURL    string
	APIVersions   []string
	ServerVersion string
	SDKVersion    *string
	MinAppAndroid string
	MinAppIOS     string
	AuthMethods   []AuthMethod
	PushRelayIDs  []string
	UnifiedPush   bool
	Packs         []string
	LogoURL       *string
	PrimaryColor  *string
	// RelayChallenge is the ephemeral push relay enrollment challenge, nil when
	// no enrollment is in progress.
	RelayChallenge *string
}

// Info is the authenticated twin: the document plus policies.
type Info struct {
	Document
	// Policies is empty until later phases add instance policies.
	Policies map[string]any
}

// TxRunner runs a transaction on a non-tenant connection (*db.DB.InAppTx).
type TxRunner interface {
	InAppTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// IDSource returns the stable instance id.
type IDSource interface {
	InstanceID(ctx context.Context) (uuid.UUID, error)
}

// PGIDSource reads instance_setting.
type PGIDSource struct{ DB TxRunner }

// InstanceID implements IDSource.
func (s PGIDSource) InstanceID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.DB.InAppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT instance_id FROM instance_setting`).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("read instance id: %w", err)
	}
	return id, nil
}

// Service builds and caches the document.
type Service struct {
	cfg     Config
	ids     IDSource
	methods *AuthMethodRegistry
	now     func() time.Time
	// challenges serves the pending push relay enrollment challenge; nil: none.
	challenges ChallengeSource

	mu       sync.Mutex
	id       uuid.UUID
	cached   Document
	cachedAt time.Time
}

// NewService returns a Service. methods may be nil (no auth methods listed).
func NewService(cfg Config, ids IDSource, methods *AuthMethodRegistry) *Service {
	if methods == nil {
		methods = &AuthMethodRegistry{}
	}
	return &Service{cfg: cfg, ids: ids, methods: methods, now: time.Now}
}

// clone copies the slices so callers cannot change the cached document.
func (d Document) clone() Document {
	d.APIVersions = slices.Clone(d.APIVersions)
	d.AuthMethods = slices.Clone(d.AuthMethods)
	d.PushRelayIDs = slices.Clone(d.PushRelayIDs)
	d.Packs = slices.Clone(d.Packs)
	return d
}

// Discovery returns the discovery document, cached for CacheTTL. The cache
// never holds RelayChallenge: it is ephemeral, so once enrollment exists set it
// on the returned copy per request (TODO(push-relay): compute it here, outside the cache).
func (s *Service) Discovery(ctx context.Context) (Document, error) {
	d, err := s.discovery(ctx)
	if err != nil {
		return d, err
	}
	if s.challenges != nil {
		if c, ok, cerr := s.challenges.Challenge(ctx); cerr == nil && ok {
			d.RelayChallenge = &c
		}
	}
	return d, nil
}

func (s *Service) discovery(ctx context.Context) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cachedAt.IsZero() && s.now().Sub(s.cachedAt) < CacheTTL {
		return s.cached.clone(), nil
	}
	if s.id == uuid.Nil {
		id, err := s.ids.InstanceID(ctx)
		if err != nil {
			return Document{}, err
		}
		s.id = id
	}
	doc := Document{
		InstanceID:    s.id,
		Name:          s.cfg.Name,
		APIBaseURL:    s.cfg.BaseURL + "/v1",
		APIVersions:   []string{"v1"},
		ServerVersion: s.cfg.ServerVersion,
		MinAppAndroid: s.cfg.MinAppVersionAndroid,
		MinAppIOS:     s.cfg.MinAppVersionIOS,
		AuthMethods:   s.methods.Methods(ctx),
		PushRelayIDs:  slices.Clone(s.cfg.PushRelayIDs),
		UnifiedPush:   s.cfg.UnifiedPush,
		Packs:         []string{},
	}
	if doc.PushRelayIDs == nil {
		doc.PushRelayIDs = []string{}
	}
	if s.cfg.LogoURL != "" {
		doc.LogoURL = &s.cfg.LogoURL
	}
	if s.cfg.PrimaryColor != "" {
		doc.PrimaryColor = &s.cfg.PrimaryColor
	}
	s.cached, s.cachedAt = doc, s.now()
	return doc.clone(), nil
}

// Info returns the authenticated instance information.
func (s *Service) Info(ctx context.Context) (Info, error) {
	d, err := s.Discovery(ctx)
	if err != nil {
		return Info{}, err
	}
	return Info{Document: d, Policies: map[string]any{}}, nil
}
