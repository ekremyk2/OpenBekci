// Package crypto holds the password hashing and opaque-token primitives
// (backend plan, sections 6 and 16).
package crypto

import (
	"context"
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

// Concurrent argon2 computations are bounded: each needs 64 MiB, so an
// unbounded burst of sign-in requests would exhaust memory. hashSlots is the
// number running at once; hashQueue bounds running plus waiting, so a burst
// beyond it is refused at once (ErrBusy) instead of piling up goroutines and
// request time, and a waiting caller whose context ends leaves the queue.
const (
	hashWorkers  = 4
	hashMaxQueue = 64
)

var (
	hashSlots = make(chan struct{}, hashWorkers)
	hashQueue = make(chan struct{}, hashMaxQueue)
)

// ErrInvalidHash is returned by VerifyPassword for a malformed encoded hash.
var ErrInvalidHash = errors.New("crypto: invalid password hash encoding")

// ErrBusy is returned when too many password computations are already queued.
var ErrBusy = errors.New("crypto: too many password computations in progress")

func derive(ctx context.Context, password string, salt []byte, t, m uint32, p uint8, keyLen uint32) ([]byte, error) {
	select {
	case hashQueue <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	defer func() { <-hashQueue }()
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a password computation slot: %w", ctx.Err())
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, t, m, p, keyLen), nil
}

// HashPassword is HashPasswordContext with a background context.
func HashPassword(password string) (string, error) {
	return HashPasswordContext(context.Background(), password)
}

// HashPasswordContext returns the PHC-encoded argon2id hash of password:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>. The parameters travel with the
// hash so they can be raised later. It gives up when ctx ends while waiting
// for a computation slot, or with ErrBusy when the queue is full.
func HashPasswordContext(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read random salt: %w", err)
	}
	key, err := derive(ctx, password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if err != nil {
		return "", err
	}
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

// VerifyPassword is VerifyPasswordContext with a background context.
func VerifyPassword(password, encoded string) (bool, error) {
	return VerifyPasswordContext(context.Background(), password, encoded)
}

// VerifyPasswordContext reports whether password matches the encoded hash,
// comparing in constant time. A malformed hash returns ErrInvalidHash; a full
// queue ErrBusy; a context that ends while waiting its error.
func VerifyPasswordContext(ctx context.Context, password, encoded string) (bool, error) {
	pr, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	key, err := derive(ctx, password, pr.salt, pr.t, pr.m, pr.p, uint32(len(pr.key))) //nolint:gosec // length of a decoded stored key
	if err != nil {
		return false, err
	}
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
	_ = DummyVerifyContext(context.Background(), password)
}

// DummyVerifyContext is DummyVerify bounded like the real verification; it
// returns ErrBusy or the context's error when it could not run.
func DummyVerifyContext(ctx context.Context, password string) error {
	_, err := derive(ctx, password, make([]byte, argonSaltLen), argonTime, argonMemory, argonThreads, argonKeyLen)
	return err
}
