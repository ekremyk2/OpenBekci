package sdk

import (
	"context"
	"encoding/json"
)

// SettingsSchema describes the settings of a pack: JSON Schema 2020-12 plus the
// spec extensions "x-ui" (order, groups, widgets, help keys) and "x-secret":
// true on write-only fields, which are stored encrypted, never returned (the UI
// shows set / not set) and redacted from audit diffs.
//
// Property names must start with "<packid>_". The core validates values on
// write; ValidateSettings is the optional hook for cross-field and live checks.
//
// Core call sites: the settings module (the pack list, enable and settings
// endpoints) and the web app's schema-driven forms.
type SettingsSchema interface {
	// Schema returns the JSON Schema of the scope's settings document, or nil
	// when the pack has no settings at that scope. The top level is an object.
	Schema(scope ScopeKind) json.RawMessage
}

// Settings reads the settings of the pack for the organization in the context.
// Secret fields are returned in clear text: the pack needs its credentials.
// Settings are written only through the core's org-admin endpoints.
type Settings interface {
	// Get returns one value; ok is false when it is not set.
	Get(ctx context.Context, scope Scope, key string) (value any, ok bool, err error)
	// All returns the whole document of the scope.
	All(ctx context.Context, scope Scope) (map[string]any, error)
}

// SettingString reads a string setting, "" when unset or not a string.
func SettingString(ctx context.Context, s Settings, scope Scope, key string) (string, error) {
	v, ok, err := s.Get(ctx, scope, key)
	if err != nil || !ok {
		return "", err
	}
	str, _ := v.(string)
	return str, nil
}
