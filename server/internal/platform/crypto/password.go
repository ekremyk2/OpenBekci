// Package crypto holds the password hashing and opaque-token primitives
// (backend plan, sections 6 and 16).
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (backend plan section 6): t=3, m=64 MiB, p=4.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots bounds concurrent argon2 computations: each needs 64 MiB, so an
// unbounded burst of sign-in requests would exhaust memory.
var hashSlots = make(chan struct{}, 4)

// ErrInvalidHash is returned by VerifyPassword for a malformed encoded hash.
var ErrInvalidHash = errors.New("crypto: invalid password hash encoding")

func derive(password string, salt []byte, t, m uint32, p uint8, keyLen uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, t, m, p, keyLen)
}

// HashPassword returns the PHC-encoded argon2id hash of password:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>. The parameters travel with the
// hash so they can be raised later.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read random salt: %w", err)
	}
	key := derive(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

type params struct {
	t, m    uint32
	p       uint8
	salt    []byte
	key     []byte
	version int
}

func parseHash(encoded string) (params, error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params{}, ErrInvalidHash
	}
	var pr params
	if _, err := fmt.Sscanf(parts[2], "v=%d", &pr.version); err != nil || pr.version != argon2.Version {
		return params{}, ErrInvalidHash
	}
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return params{}, ErrInvalidHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return params{}, ErrInvalidHash
		}
		switch k {
		case "m":
			pr.m = uint32(n)
		case "t":
			pr.t = uint32(n)
		case "p":
			if n > 255 {
				return params{}, ErrInvalidHash
			}
			pr.p = uint8(n)
		default:
			return params{}, ErrInvalidHash
		}
	}
	// Refuse absurd parameters from a corrupted or hostile row before allocating.
	if pr.m == 0 || pr.t == 0 || pr.p == 0 || pr.m > 1<<20 || pr.t > 20 {
		return params{}, ErrInvalidHash
	}
	var err error
	if pr.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(pr.salt) == 0 {
		return params{}, ErrInvalidHash
	}
	if pr.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(pr.key) == 0 {
		return params{}, ErrInvalidHash
	}
	return pr, nil
}

// VerifyPassword reports whether password matches the encoded hash, comparing
// in constant time. A malformed hash returns ErrInvalidHash.
func VerifyPassword(password, encoded string) (bool, error) {
	pr, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	key := derive(password, pr.salt, pr.t, pr.m, pr.p, uint32(len(pr.key))) //nolint:gosec // length of a decoded stored key
	return subtle.ConstantTimeCompare(key, pr.key) == 1, nil
}

// NeedsRehash reports whether encoded was produced with weaker parameters than
// the current ones (a sign-in is then a chance to upgrade the stored hash).
func NeedsRehash(encoded string) bool {
	pr, err := parseHash(encoded)
	if err != nil {
		return true
	}
	return pr.m < argonMemory || pr.t < argonTime || pr.p < argonThreads
}

// DummyVerify burns the cost of a verification so that an unknown account is
// not distinguishable from a wrong password by timing.
func DummyVerify(password string) {
	_ = derive(password, make([]byte, argonSaltLen), argonTime, argonMemory, argonThreads, argonKeyLen)
}
