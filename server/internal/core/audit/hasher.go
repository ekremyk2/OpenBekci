package audit

import (
	"sync/atomic"

	"github.com/openbekci/openbekci/server/internal/platform/pii"
)

var hasher atomic.Pointer[pii.Hasher]

// SetPIIKey keys the audit hashes of PII fields from the instance secret
// (SECRET_KEY; see package pii for the derivation). The wiring calls it once
// at start-up, before any event is recorded. Without it a PII field of a diff
// records that it changed but carries no hash (fail closed).
func SetPIIKey(secret []byte) error {
	h, err := pii.NewHasher(secret)
	if err != nil {
		return err
	}
	hasher.Store(h)
	return nil
}

// piiHash returns the audit hash of the canonical JSON value of entity.field,
// or "" for an absent value or when no key is configured.
func piiHash(entity, field string, canonical []byte) string {
	h := hasher.Load()
	if h == nil {
		return ""
	}
	return h.Hash(entity, field, canonical)
}
