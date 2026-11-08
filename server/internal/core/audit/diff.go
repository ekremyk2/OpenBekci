package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openbekci/openbekci/server/internal/platform/pii"
)

// Change is one changed field of Changes. Non-PII fields carry their Before
// and After values (absent for a field that did not exist on that side); PII
// fields carry only HashBefore and HashAfter.
type Change struct {
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	HashBefore string          `json:"hash_before,omitempty"`
	HashAfter  string          `json:"hash_after,omitempty"`
}

// Changes maps a field path (nested objects are flattened to dotted paths, such
// as "contact.email") to its change. Only changed fields are present.
type Changes map[string]Change

// HashValue is the audit fingerprint of a PII value of entity.field: a keyed
// HMAC-SHA256 of its canonical JSON encoding, domain-separated by entity and
// field, 128 bits, prefixed with the key id ("k1:..."); see package pii. An
// absent value, or an instance without a configured key (SetPIIKey), hashes
// to "".
func HashValue(entity, field string, raw json.RawMessage) string {
	return piiHash(entity, field, raw)
}

// Diff computes the PII-safe difference between two states of an entity.
// entity is the table or struct name in snake_case as registered in package
// pii ("person", "user", "organization"); before and after are structs
// (compared by their JSON field names) or maps, either may be nil for a
// create or a delete. Fields registered as PII (see pii.IsPII) appear only as
// keyed hashes; an entity unknown to package pii records key names only.
func Diff(entity string, before, after any) (Changes, error) {
	b, err := flatten(before)
	if err != nil {
		return nil, fmt.Errorf("audit diff: before: %w", err)
	}
	a, err := flatten(after)
	if err != nil {
		return nil, fmt.Errorf("audit diff: after: %w", err)
	}
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range a {
		keys[k] = true
	}
	d := Changes{}
	for k := range keys {
		bv, aok := b[k], a[k]
		if bytes.Equal(bv, aok) {
			continue
		}
		c := Change{}
		switch {
		case pii.IsPII(entity, k):
			c.HashBefore, c.HashAfter = HashValue(entity, k, bv), HashValue(entity, k, aok)
		case !pii.KnownEntity(entity):
			// Fail closed: nothing says which fields of this entity are
			// personal, so only the fact that the key changed is recorded.
		default:
			c.Before, c.After = bv, aok
		}
		d[k] = c
	}
	return d, nil
}

// Keys returns the changed field paths, sorted.
func (d Changes) Keys() []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// flatten marshals v to a JSON object and flattens nested objects into dotted
// paths; arrays and scalars are leaf values in canonical (sorted-key) JSON.
func flatten(v any) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if v == nil {
		return out, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("an entity must be a struct or a map: %w", err)
	}
	for k, val := range top {
		if err := flattenInto(out, k, val); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func flattenInto(out map[string]json.RawMessage, path string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return err
		}
		if len(obj) > 0 {
			for k, v := range obj {
				if err := flattenInto(out, path+"."+k, v); err != nil {
					return err
				}
			}
			return nil
		}
	}
	// Canonicalize (sorted keys inside arrays, no insignificant whitespace).
	var anyv any
	if err := json.Unmarshal(trimmed, &anyv); err != nil {
		return err
	}
	canon, err := json.Marshal(anyv)
	if err != nil {
		return err
	}
	out[path] = canon
	return nil
}
