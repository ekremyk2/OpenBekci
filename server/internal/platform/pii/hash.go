package pii

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Audit hashes (backend plan 16).
//
// A personal value in an audit diff is replaced by a fingerprint so that an
// auditor can see THAT a field changed, and whether two events carry the same
// value, without the trail holding the value. A plain SHA-256 of a phone
// number, plate or national id is enumerable offline, which would defeat
// erasure (the trail is kept when a person is erased), so the fingerprint is
// keyed:
//
//	key  = HKDF-SHA256(SECRET_KEY, salt: none, info: "openbekci/audit-pii/v1")
//	tag  = HMAC-SHA256(key, entity "." field NUL canonical-JSON-of-value)
//	hash = "k1:" + hex(tag[:16])            // 128 bits, prefixed with a key id
//
// The entity and field are part of the message (domain separation): the same
// phone number hashes differently in a person's contact and in a visitor row,
// and a hash cannot be matched across fields. The key is per instance, so
// hashes cannot be matched across instances either. Organizations of one
// instance share the key; the hash is still not comparable across entities.
//
// The key id ("k1") names the derivation (info string and secret) so the key
// can rotate: a new Hasher keeps decoding old ids (Verify) while Hash writes
// the current one. SECRET_KEY is therefore part of the backup set: a restore
// with a different SECRET_KEY keeps the old hashes valid as history but they
// no longer match new values.
//
// Erasure (privacy_erase) never touches the hashes: they hold no value and
// cannot be reversed without SECRET_KEY plus a guess of the value, and a
// guess must also name the entity and field.

const (
	hashInfo     = "openbekci/audit-pii/v1"
	currentKeyID = "k1"
	hashBytes    = 16 // 128 bits
	minSecret    = 16
)

// Hasher computes audit hashes. It is safe for concurrent use.
type Hasher struct {
	keys map[string][]byte // key id -> derived key
}

// NewHasher derives the audit key from the instance secret (SECRET_KEY).
func NewHasher(secret []byte) (*Hasher, error) {
	if len(secret) < minSecret {
		return nil, errors.New("pii: the instance secret is too short to key audit hashes")
	}
	k, err := hkdf.Key(sha256.New, secret, nil, hashInfo, sha256.Size)
	if err != nil {
		return nil, err
	}
	return &Hasher{keys: map[string][]byte{currentKeyID: k}}, nil
}

// Hash returns the fingerprint of the canonical JSON encoding of a value of
// entity.field, or "" for an absent value (empty canonical).
func (h *Hasher) Hash(entity, field string, canonical []byte) string {
	if len(canonical) == 0 {
		return ""
	}
	return currentKeyID + ":" + hex.EncodeToString(h.tag(currentKeyID, entity, field, canonical))
}

// Verify reports whether hash is the fingerprint of the value under the key
// named by its prefix. Auditors and tests use it to check a candidate value.
func (h *Hasher) Verify(hash, entity, field string, canonical []byte) bool {
	id, hexTag, ok := strings.Cut(hash, ":")
	if !ok || h.keys[id] == nil {
		return false
	}
	want := h.tag(id, entity, field, canonical)
	got, err := hex.DecodeString(hexTag)
	return err == nil && hmac.Equal(got, want)
}

func (h *Hasher) tag(keyID, entity, field string, canonical []byte) []byte {
	m := hmac.New(sha256.New, h.keys[keyID])
	m.Write([]byte(strings.ToLower(entity) + "." + strings.ToLower(field)))
	m.Write([]byte{0})
	m.Write(canonical)
	return m.Sum(nil)[:hashBytes]
}
