package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
)

// TokenBytes is the entropy of an opaque token.
const TokenBytes = 32

// NewToken returns a new opaque token (32 random bytes, base64url without
// padding) and the SHA-256 hash that is stored instead of it. Tokens carry
// 256 bits of entropy, so a fast hash is the right tool (no salt, no stretching).
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("read random token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// DeriveToken returns an opaque token that is a keyed function of parts
// (HMAC-SHA256 of key, base64url without padding, 256 bits like NewToken) and
// the SHA-256 that is stored. The same inputs always give the same token, which
// is what lets a lost refresh rotation be answered again; it is as unguessable
// as key is secret, so key must be a real secret.
func DeriveToken(key []byte, parts ...string) (token string, hash []byte) {
	token = base64.RawURLEncoding.EncodeToString(MAC(key, parts...))
	return token, HashToken(token)
}

// HashToken returns the SHA-256 of a presented token, the value looked up in
// the database.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// RandomDigits returns n uniformly random decimal digits (leading zeros kept).
func RandomDigits(n int) (string, error) {
	ten := big.NewInt(10)
	out := make([]byte, n)
	for i := range out {
		d, err := rand.Int(rand.Reader, ten)
		if err != nil {
			return "", fmt.Errorf("read random digit: %w", err)
		}
		out[i] = "0123456789"[d.Int64()]
	}
	return string(out), nil
}

// MAC returns HMAC-SHA256(key, parts joined by 0x00). It hashes short codes
// (a 6-digit code has too little entropy for a bare hash) bound to their
// purpose and target.
func MAC(key []byte, parts ...string) []byte {
	m := hmac.New(sha256.New, key)
	for i, p := range parts {
		if i > 0 {
			m.Write([]byte{0})
		}
		m.Write([]byte(p))
	}
	return m.Sum(nil)
}

// Equal compares two byte slices in constant time.
func Equal(a, b []byte) bool { return hmac.Equal(a, b) }
