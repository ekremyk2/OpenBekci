package sdk

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is the SDK version this module implements. The server exposes it as
// sdk_version in the discovery document and refuses packs whose
// Manifest.MinSDK is newer or from another incompatible line.
const Version = "1.1.0"

// ID is an identifier (a UUID in its canonical text form) of a core record.
type ID string

// Manifest describes a pack. It is read once at startup.
type Manifest struct {
	// ID is the pack id: lower-case letters and digits, starting with a
	// letter, at most 24 characters ("tr", "example"). It prefixes everything
	// the pack contributes. The ids "enablement", "default", "goose" and
	// "core" are reserved.
	ID string
	// Version is the pack's own semantic version.
	Version string
	// MinSDK is the oldest SDK version the pack builds against; the registry
	// rejects the pack when the server's SDK is older, or (before 1.0) of a
	// different minor line.
	MinSDK string
	// Countries are ISO 3166-1 alpha-2 codes the pack is meant for. They drive
	// the "suggested packs" hint and which organizations receive the pack's
	// presets on site creation. Empty means no country restriction.
	Countries []string
}

// Pack is what a pack module exports. The server calls Manifest, then
// Register, once at startup; a failing Register aborts startup with a named
// error.
type Pack interface {
	Manifest() Manifest
	// Register hands the pack's contributions to r. Everything is registered
	// here; nothing is discovered later by reflection or name. A process may
	// call Register more than once on the same pack value (migrate with
	// auto-migration and then serve each build their own registry), so it must
	// not keep state that a second call would corrupt; keep r.Services() and
	// nothing else that is order-dependent.
	Register(ctx context.Context, r Registry) error
}

// The optional interfaces. A pack implements only what it needs; the host
// finds them by type assertion. This is the complete list.

// OnEnable is called after an organization enabled the pack and its settings
// validated. Seed pack data here. It runs in the request of the enabling
// admin; a returned error aborts the enablement.
type OnEnable interface {
	OnEnable(ctx context.Context, org ID) error
}

// OnDisable is called when an organization disables the pack. Data is retained.
type OnDisable interface {
	OnDisable(ctx context.Context, org ID) error
}

// ValidateSettings is the hook for cross-field and live checks (testing
// provider credentials) beyond the JSON Schema. values holds secret fields in
// clear text. A returned error is reported as a validation problem.
type ValidateSettings interface {
	ValidateSettings(ctx context.Context, scope Scope, values map[string]any) error
}

// StartupCheck is a configuration sanity check at boot. An error aborts
// startup with the pack's name in the message.
type StartupCheck interface {
	StartupCheck(ctx context.Context) error
}

// Ready contributes to /readyz. A failure is reported as a warning only and
// never makes the server unready.
type Ready interface {
	Ready(ctx context.Context) error
}

// ScopeKind is the level a settings document belongs to.
type ScopeKind string

// Settings scopes.
const (
	ScopeOrg  ScopeKind = "org"
	ScopeSite ScopeKind = "site"
)

// Scope selects an organization-wide or a per-site settings document.
type Scope struct {
	Kind   ScopeKind
	SiteID ID // set for ScopeSite
}

// OrgScope is the organization-wide scope.
func OrgScope() Scope { return Scope{Kind: ScopeOrg} }

// SiteScope is the scope of one site.
func SiteScope(site ID) Scope { return Scope{Kind: ScopeSite, SiteID: site} }

type orgKey struct{}

// WithOrg returns ctx carrying the organization a call acts for. The host sets
// it before every call into a pack; tests and tools may set it too.
func WithOrg(ctx context.Context, org ID) context.Context {
	return context.WithValue(ctx, orgKey{}, org)
}

// OrgFrom returns the organization of ctx.
func OrgFrom(ctx context.Context) (ID, bool) {
	o, ok := ctx.Value(orgKey{}).(ID)
	return o, ok && o != ""
}

// ErrNoOrg is returned by services called without an organization in the context.
var ErrNoOrg = errors.New("sdk: no organization in the context")

// Money is an amount in minor units (kurus, cents) of a currency.
type Money struct {
	Amount   int64
	Currency string
}

// Supports reports whether a server implementing this SDK version can host a
// pack built against minSDK: the pack's minimum must not be newer, and before
// 1.0 it must be of the same minor line (minor versions may break until 1.0).
func Supports(minSDK string) error { return supports(Version, minSDK) }

func supports(have, minSDK string) error {
	h, err := parseSemver(have)
	if err != nil {
		return err
	}
	m, err := parseSemver(minSDK)
	if err != nil {
		return fmt.Errorf("MinSDK %q: %w", minSDK, err)
	}
	switch {
	case m[0] != h[0]:
		return fmt.Errorf("the pack needs SDK %s, the server provides %s (different major version)", minSDK, have)
	case h[0] == 0 && m[1] != h[1]:
		return fmt.Errorf("the pack needs SDK %s, the server provides %s (before 1.0 minor versions may break)", minSDK, have)
	case m[1] > h[1] || (m[1] == h[1] && m[2] > h[2]):
		return fmt.Errorf("the pack needs SDK %s, the server provides only %s", minSDK, have)
	}
	return nil
}

func parseSemver(s string) ([3]int, error) {
	var out [3]int
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("%q is not a semantic version (major.minor.patch)", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("%q is not a semantic version (major.minor.patch)", s)
		}
		out[i] = n
	}
	return out, nil
}
