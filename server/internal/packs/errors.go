package packs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Named startup errors. A pack that violates a rule prevents startup with a
// *RegistryError that matches one of these with errors.Is.
var (
	// ErrManifest: the manifest is invalid (id syntax, reserved id, version).
	ErrManifest = errors.New("invalid manifest")
	// ErrMinSDK: the pack needs another SDK than the server provides.
	ErrMinSDK = errors.New("incompatible SDK")
	// ErrNamespace: something the pack contributes is not prefixed with its id.
	ErrNamespace = errors.New("namespace violation")
	// ErrCollision: two contributions share an identifier.
	ErrCollision = errors.New("collision")
	// ErrRegister: Register returned an error or panicked.
	ErrRegister = errors.New("register failed")
	// ErrStartupCheck: StartupCheck returned an error or panicked.
	ErrStartupCheck = errors.New("startup check failed")
	// ErrFrozen: the registry was used after startup.
	ErrFrozen = errors.New("registry is frozen")
	// ErrInvalid: a contribution is malformed (nil handler, bad schema, bad route).
	ErrInvalid = errors.New("invalid contribution")
	// ErrMigrations: the pack's migrations failed or broke a convention.
	ErrMigrations = errors.New("migrations")
)

// RegistryError is a startup problem of one pack.
type RegistryError struct {
	Pack   string
	Kind   error
	Detail string
	Err    error
}

func (e *RegistryError) Error() string {
	s := fmt.Sprintf("pack %q: %v", e.Pack, e.Kind)
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

// Is matches the named kind.
func (e *RegistryError) Is(target error) bool { return target == e.Kind }

// Unwrap returns the underlying cause, if any.
func (e *RegistryError) Unwrap() error { return e.Err }

// PackError is a panic recovered from pack code.
type PackError struct {
	Pack  string
	Where string
	Value any
}

func (e *PackError) Error() string {
	return fmt.Sprintf("pack %q panicked in %s: %v", e.Pack, e.Where, e.Value)
}

// MapError converts an error returned by a pack into a problem: the SDK's
// sentinel errors map to their statuses, everything else (and a recovered
// panic) is pack_error.
func MapError(pack string, err error) error {
	if err == nil {
		return nil
	}
	var pe *problem.Error
	if errors.As(err, &pe) {
		return err
	}
	switch {
	case errors.Is(err, sdk.ErrInvalidSignature):
		return problem.Wrap(401, problem.CodeUnauthenticated, "the signature is invalid", err)
	case errors.Is(err, sdk.ErrNotFound):
		return problem.Wrap(404, problem.CodeNotFound, "the provider does not know this transaction", err)
	case errors.Is(err, sdk.ErrRejected), errors.Is(err, sdk.ErrUnsupported), errors.Is(err, sdk.ErrCredentials):
		return problem.Wrap(422, problem.CodeValidationFailed, strings.TrimPrefix(err.Error(), "sdk: "), err)
	case errors.Is(err, sdk.ErrUnavailable):
		return problem.Wrap(503, problem.CodePackError, "the provider is temporarily unavailable", err)
	}
	return problem.Wrap(500, problem.CodePackError, "pack "+pack+" failed", err)
}
