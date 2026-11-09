package packs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// scopeKey names a scope inside sealed-value associated data.
func scopeKey(s sdk.Scope) string {
	if s.Kind == sdk.ScopeSite {
		return "site:" + string(s.SiteID)
	}
	return "org"
}

func aad(org uuid.UUID, pack string, scope sdk.Scope, key string) []byte {
	return []byte(strings.Join([]string{org.String(), pack, scopeKey(scope), key}, "|"))
}

// sealSecrets seals secret values with the field encryption helper.
func (h *Host) sealSecrets(org uuid.UUID, pack string, scope sdk.Scope, in map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(in))
	if len(in) == 0 {
		return out, nil
	}
	if h.cipher == nil {
		return nil, problem.New(503, problem.CodeInternalError, "secret settings need OPENBEKCI_ENCRYPTION_KEY to be configured")
	}
	for k, v := range in {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		sealed, err := h.cipher.Seal(raw, aad(org, pack, scope, k))
		if err != nil {
			return nil, err
		}
		out[k] = base64.StdEncoding.EncodeToString(sealed)
	}
	return out, nil
}

func (h *Host) openSecrets(org uuid.UUID, pack string, scope sdk.Scope, sealed map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(sealed))
	for k, s := range sealed {
		if h.cipher == nil {
			return nil, errors.New("packs: stored secret settings but no encryption key is configured")
		}
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("packs: secret %q: %w", k, err)
		}
		pt, err := h.cipher.Open(raw, aad(org, pack, scope, k))
		if err != nil {
			return nil, fmt.Errorf("packs: secret %q: %w", k, err)
		}
		var v any
		if err := json.Unmarshal(pt, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// storedSettings is one settings document as stored: plain values and sealed secrets.
type storedSettings struct {
	plain  map[string]any
	sealed map[string]string
	found  bool
}

func (h *Host) loadSettings(ctx context.Context, tx pgx.Tx, org uuid.UUID, pack string, scope sdk.Scope) (storedSettings, error) {
	st := storedSettings{plain: map[string]any{}, sealed: map[string]string{}}
	var plain, sealed []byte
	var err error
	if scope.Kind == sdk.ScopeSite {
		su, uerr := uid(scope.SiteID)
		if uerr != nil {
			return st, uerr
		}
		err = tx.QueryRow(ctx, `SELECT settings, secrets FROM pack_enablement_site WHERE org_id = $1 AND site_id = $2 AND pack_id = $3`, org, su, pack).Scan(&plain, &sealed)
	} else {
		err = tx.QueryRow(ctx, `SELECT settings, secrets FROM pack_enablement WHERE org_id = $1 AND pack_id = $2`, org, pack).Scan(&plain, &sealed)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.found = true
	if err := json.Unmarshal(plain, &st.plain); err != nil {
		return st, err
	}
	if err := json.Unmarshal(sealed, &st.sealed); err != nil {
		return st, err
	}
	return st, nil
}

// settingsReader implements sdk.Settings for one pack.
type settingsReader struct {
	h    *Host
	pack string
}

func (s settingsReader) All(ctx context.Context, scope sdk.Scope) (map[string]any, error) {
	var out map[string]any
	err := s.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		st, err := s.h.loadSettings(ctx, tx, org, s.pack, scope)
		if err != nil {
			return err
		}
		secrets, err := s.h.openSecrets(org, s.pack, scope, st.sealed)
		if err != nil {
			return err
		}
		out = st.plain
		for k, v := range secrets {
			out[k] = v
		}
		return nil
	})
	return out, err
}

func (s settingsReader) Get(ctx context.Context, scope sdk.Scope, key string) (any, bool, error) {
	all, err := s.All(ctx, scope)
	if err != nil {
		return nil, false, err
	}
	v, ok := all[key]
	return v, ok, nil
}

// validateValues checks a merged settings document against the pack's schema
// of the scope, returning a validation problem with a field per violation.
func (p *Pack) validateValues(scope sdk.ScopeKind, merged map[string]any) error {
	info := p.reg.schemas[scope]
	if info == nil {
		if len(merged) > 0 {
			return problem.Validation(problem.Field{Field: "settings", Code: "unknown_field", Params: map[string]any{"scope": string(scope)}})
		}
		return nil
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return problem.BadRequest("the settings could not be read")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return problem.BadRequest("the settings could not be read")
	}
	verr := info.schema.Validate(inst)
	if verr == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return problem.Validation(problem.Field{Field: "settings", Code: "invalid_value"})
	}
	var fields []problem.Field
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			f := "settings"
			if len(e.InstanceLocation) > 0 {
				f += "." + strings.Join(e.InstanceLocation, ".")
			}
			code := "invalid_value"
			if kp := e.ErrorKind.KeywordPath(); len(kp) > 0 && kp[len(kp)-1] == "required" {
				code = "required"
			}
			fields = append(fields, problem.Field{Field: f, Code: code})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	slices.SortFunc(fields, func(a, b problem.Field) int { return strings.Compare(a.Field+a.Code, b.Field+b.Code) })
	return problem.Validation(fields...)
}
