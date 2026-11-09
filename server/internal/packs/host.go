// Package packs is the pack runtime: the registry that validates and freezes
// what packs contribute, per-organization enablement with settings and
// secrets, the services handed to packs, preset application, the pack job
// worker, routes and event delivery (packs plan sections 2 and 3, backend
// plan 10). The core business modules never import it (depguard); the HTTP
// layer and the wiring do.
package packs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Config is what the host needs from the server. Every field may be nil in
// tests that do not exercise the matching service.
type Config struct {
	DB  *db.DB
	Log *slog.Logger
	// EncryptionKey seals secret settings (ENCRYPTION_KEY); without it packs
	// with secret fields cannot be enabled.
	EncryptionKey []byte
	Payments      *payments.Service
	Files         *files.Service
	// Notifier is the notification queue.
	Notifier jobs.Notifier
	Now      func() time.Time
}

// Pack is a started pack as the core sees it.
type Pack struct {
	Manifest sdk.Manifest
	C        *Contributions
	impl     sdk.Pack
	reg      *registry
}

// SecretKeys returns the secret property names of the settings schema of a scope.
func (p *Pack) SecretKeys(scope sdk.ScopeKind) []string {
	if s := p.reg.schemas[scope]; s != nil {
		keys := make([]string, 0, len(s.secrets))
		for k := range s.secrets {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return keys
	}
	return nil
}

// Schema returns the JSON Schema of a scope, nil when there is none.
func (p *Pack) Schema(scope sdk.ScopeKind) []byte {
	if s := p.reg.schemas[scope]; s != nil {
		return s.raw
	}
	return nil
}

// Host owns the packs of one server.
type Host struct {
	cfg     Config
	log     *slog.Logger
	list    []sdk.Pack
	packs   []*Pack // by id after Start
	byID    map[string]*Pack
	frozen  atomic.Bool
	client  atomic.Pointer[jobs.Client]
	cipher  *crypto.FieldCipher
	now     func() time.Time
	changed func()
}

// New returns a Host for the compiled-in packs. Call Start before use.
func New(cfg Config, packs []sdk.Pack) (*Host, error) {
	h := &Host{cfg: cfg, list: packs, byID: map[string]*Pack{}, now: cfg.Now, log: cfg.Log}
	if h.now == nil {
		h.now = time.Now
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if len(cfg.EncryptionKey) > 0 {
		c, err := crypto.NewFieldCipher(cfg.EncryptionKey, "packs.settings")
		if err != nil {
			return nil, err
		}
		h.cipher = c
	}
	return h, nil
}

// SetJobs gives the host the River client once it exists (the workers the
// client runs need the host first).
func (h *Host) SetJobs(c *jobs.Client) { h.client.Store(c) }

var semverish = regexp.MustCompile(`^v?\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
var countryRE = regexp.MustCompile(`^[A-Z]{2}$`)

// safe runs fn, converting a panic into a *PackError.
func safe(pack, where string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PackError{Pack: pack, Where: where, Value: r}
		}
	}()
	return fn()
}

// Start validates the manifests, calls Register on every pack, runs the
// startup checks, validates namespacing and collisions across packs and
// freezes the registry. Any violation is returned (all of them, joined) and
// the server must not start.
func (h *Host) Start(ctx context.Context) error {
	var errs []error
	type cand struct {
		impl sdk.Pack
		man  sdk.Manifest
	}
	var cands []cand
	seen := map[string]bool{}
	for i, p := range h.list {
		if p == nil {
			errs = append(errs, &RegistryError{Pack: fmt.Sprintf("#%d", i), Kind: ErrManifest, Detail: "nil pack"})
			continue
		}
		var man sdk.Manifest
		if err := safe(fmt.Sprintf("#%d", i), "Manifest", func() error { man = p.Manifest(); return nil }); err != nil {
			errs = append(errs, &RegistryError{Pack: fmt.Sprintf("#%d", i), Kind: ErrManifest, Err: err})
			continue
		}
		if e := validateManifest(man); e != nil {
			errs = append(errs, e)
			continue
		}
		if seen[man.ID] {
			errs = append(errs, &RegistryError{Pack: man.ID, Kind: ErrCollision, Detail: "pack id is listed twice"})
			continue
		}
		seen[man.ID] = true
		cands = append(cands, cand{p, man})
	}
	slices.SortFunc(cands, func(a, b cand) int {
		switch {
		case a.man.ID < b.man.ID:
			return -1
		case a.man.ID > b.man.ID:
			return 1
		}
		return 0
	})
	global := map[string]string{}
	for _, c := range cands {
		pk := &Pack{Manifest: c.man, impl: c.impl}
		pk.reg = newRegistry(c.man.ID, &services{h: h, pack: c.man.ID}, &h.frozen)
		pk.C = &pk.reg.c
		err := safe(c.man.ID, "Register", func() error { return c.impl.Register(ctx, pk.reg) })
		if err != nil {
			errs = append(errs, &RegistryError{Pack: c.man.ID, Kind: ErrRegister, Err: err})
		}
		errs = append(errs, pk.reg.errs...)
		for _, id := range pk.C.Provides() {
			// ids are namespaced by pack, so a clash across packs means a namespace bug somewhere
			// (routes and the settings schema are per pack by construction)
			if strings.HasPrefix(id, "route:") || strings.HasPrefix(id, "settings_schema:") {
				continue
			}
			if other, dup := global[id]; dup && other != c.man.ID {
				errs = append(errs, &RegistryError{Pack: c.man.ID, Kind: ErrCollision, Detail: fmt.Sprintf("%q is also registered by pack %q", id, other)})
			}
			global[id] = c.man.ID
		}
		if sc, ok := c.impl.(sdk.StartupCheck); ok {
			if err := safe(c.man.ID, "StartupCheck", func() error { return sc.StartupCheck(ctx) }); err != nil {
				errs = append(errs, &RegistryError{Pack: c.man.ID, Kind: ErrStartupCheck, Err: err})
			}
		}
		h.packs = append(h.packs, pk)
		h.byID[c.man.ID] = pk
	}
	h.frozen.Store(true)
	return errors.Join(errs...)
}

func validateManifest(m sdk.Manifest) error {
	switch {
	case !packIDRE.MatchString(m.ID):
		return &RegistryError{Pack: m.ID, Kind: ErrManifest, Detail: "the id must be lower-case letters and digits, starting with a letter, at most 24 characters"}
	case reservedIDs[m.ID]:
		return &RegistryError{Pack: m.ID, Kind: ErrManifest, Detail: "the id is reserved"}
	case !semverish.MatchString(m.Version):
		return &RegistryError{Pack: m.ID, Kind: ErrManifest, Detail: fmt.Sprintf("version %q is not a semantic version", m.Version)}
	}
	for _, c := range m.Countries {
		if !countryRE.MatchString(c) {
			return &RegistryError{Pack: m.ID, Kind: ErrManifest, Detail: fmt.Sprintf("country %q is not an ISO 3166-1 alpha-2 code", c)}
		}
	}
	if err := sdk.Supports(m.MinSDK); err != nil {
		return &RegistryError{Pack: m.ID, Kind: ErrMinSDK, Detail: err.Error()}
	}
	return nil
}

// Packs returns the started packs ordered by id.
func (h *Host) Packs() []*Pack { return h.packs }

// Lookup returns a pack by id.
func (h *Host) Lookup(id string) (*Pack, bool) {
	p, ok := h.byID[id]
	return p, ok
}

// Services returns the sdk.Services of a pack, as Register hands them out
// (bound to the organization of each call's context). For tests and tools.
func (h *Host) Services(packID string) sdk.Services { return &services{h: h, pack: packID} }

// SDKVersion is the SDK version the server implements.
func (h *Host) SDKVersion() string { return sdk.Version }

// EnabledPackIDs implements instance.PackSource.
func (h *Host) EnabledPackIDs(ctx context.Context) ([]string, error) { return h.EnabledInAnyOrg(ctx) }

// PackDetails implements instance.PackSource: the compiled-in packs.
func (h *Host) PackDetails() []instance.PackDetail {
	out := make([]instance.PackDetail, 0, len(h.packs))
	for _, p := range h.packs {
		c := slices.Clone(p.Manifest.Countries)
		if c == nil {
			c = []string{}
		}
		out = append(out, instance.PackDetail{ID: p.Manifest.ID, Version: p.Manifest.Version, MinSDK: p.Manifest.MinSDK, Countries: c})
	}
	return out
}

// OnChange sets a function called after a pack was enabled or disabled (the
// instance document drops its cache).
func (h *Host) OnChange(fn func()) { h.changed = fn }

func (h *Host) notifyChanged() {
	if h.changed != nil {
		h.changed()
	}
}

// call runs fn (pack code) for an organization, recovering panics and
// mapping errors the pack returned (MapError).
func (h *Host) call(ctx context.Context, org uuid.UUID, pack, where string, fn func(ctx context.Context) error) error {
	ctx = sdk.WithOrg(ctx, sdk.ID(org.String()))
	err := safe(pack, where, func() error { return fn(ctx) })
	if err != nil {
		var pe *PackError
		if errors.As(err, &pe) {
			h.log.ErrorContext(ctx, "pack panicked", "pack", pack, "where", where, "value", fmt.Sprint(pe.Value))
		}
		return MapError(pack, err)
	}
	return nil
}

// enabledIDs returns the ids of the packs enabled for org.
func (h *Host) enabledIDs(ctx context.Context, org uuid.UUID) (map[string]bool, error) {
	out := map[string]bool{}
	err := h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT pack_id FROM pack_enablement WHERE org_id = $1 AND enabled`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	})
	return out, err
}

// Enabled reports whether the pack is enabled for org.
func (h *Host) Enabled(ctx context.Context, org uuid.UUID, id string) (bool, error) {
	if _, ok := h.byID[id]; !ok {
		return false, nil
	}
	m, err := h.enabledIDs(ctx, org)
	return m[id], err
}

// Active returns the packs enabled for org, ordered by id: the only packs
// whose implementations the core may select for it.
func (h *Host) Active(ctx context.Context, org uuid.UUID) ([]*Pack, error) {
	m, err := h.enabledIDs(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []*Pack
	for _, p := range h.packs {
		if m[p.Manifest.ID] {
			out = append(out, p)
		}
	}
	return out, nil
}

// EnabledInAnyOrg returns the pack ids enabled for at least one organization,
// for the instance discovery document. It reads across tenants on the platform role.
func (h *Host) EnabledInAnyOrg(ctx context.Context) ([]string, error) {
	out := []string{}
	err := h.cfg.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT pack_id FROM pack_enablement WHERE enabled ORDER BY pack_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if _, ok := h.byID[id]; ok { // packs removed from the build are not listed
				out = append(out, id)
			}
		}
		return rows.Err()
	})
	return out, err
}

func orgFromCtx(ctx context.Context) (uuid.UUID, error) {
	s, ok := sdk.OrgFrom(ctx)
	if !ok {
		return uuid.Nil, sdk.ErrNoOrg
	}
	id, err := uuid.Parse(string(s))
	if err != nil {
		return uuid.Nil, fmt.Errorf("sdk: bad organization id: %w", err)
	}
	return id, nil
}

func uid(id sdk.ID) (uuid.UUID, error) {
	u, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.Nil, problem.Validation(problem.Field{Field: "id", Code: "invalid_format"})
	}
	return u, nil
}

// ReadyChecks returns one warn-only readiness check per pack that implements sdk.Ready.
func (h *Host) ReadyChecks() []ReadyCheck {
	var out []ReadyCheck
	for _, p := range h.packs {
		if r, ok := p.impl.(sdk.Ready); ok {
			id := p.Manifest.ID
			out = append(out, ReadyCheck{Name: "pack:" + id, Fn: func(ctx context.Context) error {
				return safe(id, "Ready", func() error { return r.Ready(ctx) })
			}})
		}
	}
	return out
}

// ReadyCheck is a warn-only readiness probe contributed by a pack.
type ReadyCheck struct {
	Name string
	Fn   func(ctx context.Context) error
}
