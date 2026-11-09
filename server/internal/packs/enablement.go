package packs

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Permissions of the pack administration endpoints. Packs are enabled per
// organization by its administrators.
const (
	PermRead   authz.Permission = "packs.read"
	PermManage authz.Permission = "packs.manage"
)

// Permissions declares the packs module's permissions for the registry.
func Permissions() []authz.PermissionDef {
	org := []authz.ScopeType{authz.ScopeOrg}
	return []authz.PermissionDef{
		{Name: PermRead, Description: "List the packs and their settings", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
		{Name: PermManage, Description: "Enable and disable packs and change their settings", Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
	}
}

// packTarget derives the audit target id of a pack (audit targets are uuids).
func packTarget(id string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("openbekci/pack/"+id))
}

// SiteSettings is the site-scope settings of a pack for one site.
type SiteSettings struct {
	SiteID     uuid.UUID      `json:"site_id"`
	Values     map[string]any `json:"values"`
	SecretsSet []string       `json:"secrets_set"`
}

// State is a pack as the administration endpoints show it.
type State struct {
	ID        string
	Version   string
	MinSDK    string
	Countries []string
	Provides  []string
	Enabled   bool
	EnabledAt *time.Time
	// Schemas are the settings schemas by scope ("org", "site").
	Schemas map[string]json.RawMessage
	// Settings are the organization-scope values, secret fields left out.
	Settings   map[string]any
	SecretsSet []string
	Sites      []SiteSettings
}

func (h *Host) requireOrg(ctx context.Context, perm authz.Permission, org uuid.UUID) error {
	return authz.Require(ctx, perm, authz.Scope{OrgID: org})
}

func secretNames(sealed map[string]string) []string {
	out := make([]string, 0, len(sealed))
	for k := range sealed {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (h *Host) stateOf(ctx context.Context, tx pgx.Tx, org uuid.UUID, p *Pack) (State, error) {
	st := State{ID: p.Manifest.ID, Version: p.Manifest.Version, MinSDK: p.Manifest.MinSDK, Countries: slices.Clone(p.Manifest.Countries), Provides: p.C.Provides(),
		Schemas: map[string]json.RawMessage{}, Settings: map[string]any{}, SecretsSet: []string{}, Sites: []SiteSettings{}}
	if st.Countries == nil {
		st.Countries = []string{}
	}
	for _, sc := range []sdk.ScopeKind{sdk.ScopeOrg, sdk.ScopeSite} {
		if raw := p.Schema(sc); raw != nil {
			st.Schemas[string(sc)] = raw
		}
	}
	var enabledAt *time.Time
	var plain, sealed []byte
	err := tx.QueryRow(ctx, `SELECT enabled, enabled_at, settings, secrets FROM pack_enablement WHERE org_id = $1 AND pack_id = $2`, org, p.Manifest.ID).Scan(&st.Enabled, &enabledAt, &plain, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if enabledAt != nil {
		t := enabledAt.UTC()
		st.EnabledAt = &t
	}
	var sm map[string]string
	if err := json.Unmarshal(plain, &st.Settings); err != nil {
		return st, err
	}
	if err := json.Unmarshal(sealed, &sm); err != nil {
		return st, err
	}
	st.SecretsSet = secretNames(sm)
	rows, err := tx.Query(ctx, `SELECT site_id, settings, secrets FROM pack_enablement_site WHERE org_id = $1 AND pack_id = $2 ORDER BY site_id`, org, p.Manifest.ID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var ss SiteSettings
		var pl, se []byte
		if err := rows.Scan(&ss.SiteID, &pl, &se); err != nil {
			return st, err
		}
		var sealedSite map[string]string
		if err := json.Unmarshal(pl, &ss.Values); err != nil {
			return st, err
		}
		if err := json.Unmarshal(se, &sealedSite); err != nil {
			return st, err
		}
		ss.SecretsSet = secretNames(sealedSite)
		st.Sites = append(st.Sites, ss)
	}
	return st, rows.Err()
}

// List returns every compiled-in pack with its manifest, settings schema and
// the organization's enablement and settings (needs packs.read). Secret values
// are never returned, only the names of the secret fields that are set.
func (h *Host) List(ctx context.Context, org uuid.UUID) ([]State, error) {
	if err := h.requireOrg(ctx, PermRead, org); err != nil {
		return nil, err
	}
	out := []State{}
	err := h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		for _, p := range h.packs {
			st, err := h.stateOf(ctx, tx, org, p)
			if err != nil {
				return err
			}
			out = append(out, st)
		}
		return nil
	})
	return out, err
}

func (h *Host) lookup(id string) (*Pack, error) {
	p, ok := h.byID[id]
	if !ok {
		return nil, problem.NotFound("no such pack")
	}
	return p, nil
}

// mergeScope computes the settings document after a write: the plain values
// replace the stored ones, secret fields keep their stored value unless the
// request sets them (a null value clears one). It returns the new plain
// document, the new sealed secrets and the cleartext merge for validation.
func (h *Host) mergeScope(org uuid.UUID, p *Pack, scope sdk.Scope, stored storedSettings, in map[string]any) (plain map[string]any, sealed map[string]string, clear map[string]any, err error) {
	secretKeys := map[string]bool{}
	if info := p.reg.schemas[scope.Kind]; info != nil {
		secretKeys = info.secrets
	}
	oldSecrets, err := h.openSecrets(org, p.Manifest.ID, scope, stored.sealed)
	if err != nil {
		return nil, nil, nil, err
	}
	plain, clear = map[string]any{}, map[string]any{}
	newSecrets := map[string]any{}
	for k, v := range oldSecrets {
		newSecrets[k] = v
	}
	for k, v := range in {
		if secretKeys[k] {
			if v == nil {
				delete(newSecrets, k)
			} else {
				newSecrets[k] = v
			}
			continue
		}
		if v != nil {
			plain[k] = v
		}
	}
	for k, v := range plain {
		clear[k] = v
	}
	for k, v := range newSecrets {
		if secretKeys[k] {
			clear[k] = v
		}
	}
	// secrets already sealed stay as they are; only changed ones are sealed again
	sealed = map[string]string{}
	toSeal := map[string]any{}
	for k, v := range newSecrets {
		if _, changed := in[k]; changed {
			toSeal[k] = v
		} else if s, ok := stored.sealed[k]; ok {
			sealed[k] = s
		}
	}
	fresh, err := h.sealSecrets(org, p.Manifest.ID, scope, toSeal)
	if err != nil {
		return nil, nil, nil, err
	}
	for k, v := range fresh {
		sealed[k] = v
	}
	return plain, sealed, clear, nil
}

// checkSettings validates a merged document against the schema and the pack's ValidateSettings hook.
func (h *Host) checkSettings(ctx context.Context, org uuid.UUID, p *Pack, scope sdk.Scope, clear map[string]any) error {
	if err := p.validateValues(scope.Kind, clear); err != nil {
		return err
	}
	if v, ok := p.impl.(sdk.ValidateSettings); ok {
		var secretKeys map[string]bool
		if info := p.reg.schemas[scope.Kind]; info != nil {
			secretKeys = info.secrets
		}
		return h.call(ctx, org, p.Manifest.ID, "ValidateSettings", func(ctx context.Context) error {
			err := v.ValidateSettings(ctx, scope, clear)
			if err == nil {
				return nil
			}
			// the text of this error reaches the admin's browser and the logs: a pack that
			// echoes a credential (a failed request URL, a quoted value) must not leak it
			msg := redactSecrets(err.Error(), clear, secretKeys)
			switch {
			case errors.Is(err, sdk.ErrCredentials):
				return &sanitizedError{msg: msg, is: sdk.ErrCredentials}
			case errors.Is(err, sdk.ErrRejected):
				return &sanitizedError{msg: msg, is: sdk.ErrRejected}
			}
			return problem.New(422, problem.CodeValidationFailed, msg)
		})
	}
	return nil
}

// sanitizedError carries a redacted message and matches one SDK sentinel; the
// pack's original error (whose text may hold a secret) is dropped.
type sanitizedError struct {
	msg string
	is  error
}

func (e *sanitizedError) Error() string { return e.msg }
func (e *sanitizedError) Unwrap() error { return e.is }

// redactSecrets replaces every occurrence of a secret value of the document in msg.
func redactSecrets(msg string, values map[string]any, secretKeys map[string]bool) string {
	for k := range secretKeys {
		v, ok := values[k]
		if !ok || v == nil {
			continue
		}
		var text string
		if s, isStr := v.(string); isStr {
			text = s
		} else if raw, err := json.Marshal(v); err == nil {
			text = string(raw)
		}
		if text != "" {
			msg = strings.ReplaceAll(msg, text, "[redacted]")
		}
	}
	return msg
}

func diffKeys(before, after map[string]any, secretsBefore, secretsAfter []string) audit.Changes {
	// settings are audited by key name only (a pack's values may be credentials)
	b, a := map[string]any{}, map[string]any{}
	for k, v := range before {
		b[k] = v
	}
	for k, v := range after {
		a[k] = v
	}
	for _, k := range secretsBefore {
		b[k] = "set"
	}
	for _, k := range secretsAfter {
		a[k] = "set"
	}
	d, _ := audit.Diff("pack_settings", b, a) // maps cannot fail to flatten
	return d
}

// Enable enables a pack for the organization with settings (needs packs.manage):
// the settings are validated against the schema and ValidateSettings, the
// enablement and the presets are written in one transaction, then OnEnable
// runs; when it fails the enablement is withdrawn again. Enabling an enabled
// pack updates its settings when given and re-applies presets to settings the
// sites have not changed.
func (h *Host) Enable(ctx context.Context, org uuid.UUID, id string, values map[string]any) (State, error) {
	if err := h.requireOrg(ctx, PermManage, org); err != nil {
		return State{}, err
	}
	p, err := h.lookup(id)
	if err != nil {
		return State{}, err
	}
	scope := sdk.OrgScope()
	var stored storedSettings
	var wasEnabled bool
	err = h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) (err error) {
		if stored, err = h.loadSettings(ctx, tx, org, id, scope); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE((SELECT enabled FROM pack_enablement WHERE org_id = $1 AND pack_id = $2), false)`, org, id).Scan(&wasEnabled)
	})
	if err != nil {
		return State{}, err
	}
	in := values
	if values == nil { // re-enabling without settings keeps the stored ones
		in = map[string]any{}
		for k, v := range stored.plain {
			in[k] = v
		}
	}
	plain, sealed, clear, err := h.mergeScope(org, p, scope, stored, in)
	if err != nil {
		return State{}, err
	}
	if err := h.checkSettings(ctx, org, p, scope, clear); err != nil {
		return State{}, err
	}
	plainJSON, _ := json.Marshal(plain)
	sealedJSON, _ := json.Marshal(sealed)
	var out State
	var undo enableUndo
	err = h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		undo = enableUndo{}
		if _, err := tx.Exec(ctx, `INSERT INTO pack_enablement (org_id, pack_id, enabled, settings, secrets, enabled_at)
			VALUES ($1, $2, true, $3, $4, now())
			ON CONFLICT (org_id, pack_id) DO UPDATE SET enabled = true, settings = EXCLUDED.settings, secrets = EXCLUDED.secrets,
			  enabled_at = CASE WHEN pack_enablement.enabled THEN pack_enablement.enabled_at ELSE now() END, disabled_at = NULL,
			  version = pack_enablement.version + 1, updated_at = now()`, org, id, plainJSON, sealedJSON); err != nil {
			return err
		}
		action := "pack.enable"
		if wasEnabled {
			action = "pack.update_settings"
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: org, Action: action, TargetType: "pack", TargetID: packTarget(id),
			Diff: diffKeys(stored.plain, plain, secretNames(stored.sealed), secretNames(sealed))}); err != nil {
			return err
		}
		u, err := h.applyToOrgSites(ctx, tx, org, p)
		if err != nil {
			return err
		}
		undo = u
		if !wasEnabled {
			if err := h.Emit(ctx, tx, org, sdk.Event{Type: sdk.EventPackEnabled}); err != nil {
				return err
			}
		}
		out, err = h.stateOf(ctx, tx, org, p)
		return err
	})
	if err != nil {
		return State{}, problemOf(err)
	}
	if oe, ok := p.impl.(sdk.OnEnable); ok && !wasEnabled {
		if err := h.call(ctx, org, id, "OnEnable", func(ctx context.Context) error { return oe.OnEnable(ctx, sdk.ID(org.String())) }); err != nil {
			// "a returned error aborts the enablement": withdraw it again, presets and chart names included
			if werr := h.withdrawEnable(context.WithoutCancel(ctx), org, id, stored, undo); werr != nil {
				h.log.ErrorContext(ctx, "could not withdraw a failed pack enablement", "pack", id, "error", werr)
			}
			return State{}, err
		}
	}
	h.notifyChanged()
	return out, nil
}

func problemOf(err error) error {
	if err == nil {
		return nil
	}
	var pe *problem.Error
	if errors.As(err, &pe) {
		return err
	}
	return problem.Internal(err)
}

// withdrawEnable takes back an enablement whose OnEnable failed: the preset
// values and chart names the enablement wrote go back to what the sites had
// (a site that edited a key meanwhile keeps its edit), and the enablement row
// returns to its earlier state: removed when there was none, otherwise
// disabled with the settings it had before.
func (h *Host) withdrawEnable(ctx context.Context, org uuid.UUID, id string, before storedSettings, undo enableUndo) error {
	return h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if err := h.undoPresets(ctx, tx, org, undo.presets); err != nil {
			return err
		}
		if err := h.undoChart(ctx, tx, org, undo.chart); err != nil {
			return err
		}
		if before.found {
			plain, _ := json.Marshal(before.plain)
			sealed, _ := json.Marshal(before.sealed)
			if _, err := tx.Exec(ctx, `UPDATE pack_enablement SET enabled = false, disabled_at = now(), settings = $3, secrets = $4, version = version + 1, updated_at = now()
				WHERE org_id = $1 AND pack_id = $2`, org, id, plain, sealed); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM pack_enablement WHERE org_id = $1 AND pack_id = $2`, org, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "pack.enable_failed", TargetType: "pack", TargetID: packTarget(id)})
	})
}

// UpdateSettings replaces the plain settings of one scope and sets or clears
// secret fields (needs packs.manage). Secret fields not mentioned keep their
// value. The pack must be enabled.
func (h *Host) UpdateSettings(ctx context.Context, org uuid.UUID, id string, scope sdk.Scope, values map[string]any) (SiteSettings, bool, error) {
	if err := h.requireOrg(ctx, PermManage, org); err != nil {
		return SiteSettings{}, false, err
	}
	p, err := h.lookup(id)
	if err != nil {
		return SiteSettings{}, false, err
	}
	if on, err := h.Enabled(ctx, org, id); err != nil {
		return SiteSettings{}, false, err
	} else if !on {
		return SiteSettings{}, false, problem.Conflict("the pack is not enabled; enable it first")
	}
	var stored storedSettings
	err = h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) (err error) {
		if scope.Kind == sdk.ScopeSite {
			su, err := uid(scope.SiteID)
			if err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM site WHERE org_id = $1 AND id = $2`, org, su).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return problem.NotFound("no such site")
			}
		}
		stored, err = h.loadSettings(ctx, tx, org, id, scope)
		return err
	})
	if err != nil {
		return SiteSettings{}, false, problemOf(err)
	}
	plain, sealed, clear, err := h.mergeScope(org, p, scope, stored, values)
	if err != nil {
		return SiteSettings{}, false, err
	}
	if err := h.checkSettings(ctx, org, p, scope, clear); err != nil {
		return SiteSettings{}, false, err
	}
	plainJSON, _ := json.Marshal(plain)
	sealedJSON, _ := json.Marshal(sealed)
	err = h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if scope.Kind == sdk.ScopeSite {
			su, _ := uid(scope.SiteID)
			if _, err := tx.Exec(ctx, `INSERT INTO pack_enablement_site (org_id, site_id, pack_id, settings, secrets) VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (org_id, site_id, pack_id) DO UPDATE SET settings = EXCLUDED.settings, secrets = EXCLUDED.secrets, updated_at = now()`, org, su, id, plainJSON, sealedJSON); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE pack_enablement SET settings = $3, secrets = $4, version = version + 1, updated_at = now() WHERE org_id = $1 AND pack_id = $2`, org, id, plainJSON, sealedJSON); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "pack.update_settings", TargetType: "pack", TargetID: packTarget(id),
			Diff: diffKeys(stored.plain, plain, secretNames(stored.sealed), secretNames(sealed))})
	})
	if err != nil {
		return SiteSettings{}, false, problemOf(err)
	}
	out := SiteSettings{Values: plain, SecretsSet: secretNames(sealed)}
	if scope.Kind == sdk.ScopeSite {
		out.SiteID, _ = uid(scope.SiteID)
	}
	return out, scope.Kind == sdk.ScopeSite, nil
}

// Disable disables a pack for the organization (needs packs.manage): OnDisable
// runs, data and settings are retained, the pack's implementations stop being
// selected, and settings the presets filled stay as the sites' own.
func (h *Host) Disable(ctx context.Context, org uuid.UUID, id string) (State, error) {
	if err := h.requireOrg(ctx, PermManage, org); err != nil {
		return State{}, err
	}
	p, err := h.lookup(id)
	if err != nil {
		return State{}, err
	}
	on, err := h.Enabled(ctx, org, id)
	if err != nil {
		return State{}, err
	}
	var out State
	err = h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		if on {
			// pack.disabled goes out while the pack is still enabled (Emit selects enabled packs);
			// its delivery is exempt from the enabled check
			if err := h.Emit(ctx, tx, org, sdk.Event{Type: sdk.EventPackDisabled}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE pack_enablement SET enabled = false, disabled_at = now(), version = version + 1, updated_at = now() WHERE org_id = $1 AND pack_id = $2`, org, id); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "pack.disable", TargetType: "pack", TargetID: packTarget(id)}); err != nil {
				return err
			}
		}
		out, err = h.stateOf(ctx, tx, org, p)
		return err
	})
	if err != nil {
		return out, problemOf(err)
	}
	h.notifyChanged()
	// OnDisable runs once the disablement is committed, so a failed commit never tells the pack it
	// is disabled; it cannot veto the disablement, a failure is logged
	if on {
		if od, ok := p.impl.(sdk.OnDisable); ok {
			if err := h.call(context.WithoutCancel(ctx), org, id, "OnDisable", func(ctx context.Context) error { return od.OnDisable(ctx, sdk.ID(org.String())) }); err != nil {
				h.log.ErrorContext(ctx, "pack OnDisable failed after the pack was disabled", "pack", id, "error", err)
			}
		}
	}
	return out, nil
}
