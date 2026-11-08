package crypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash %q does not encode the expected parameters", h)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Fatalf("verify right password = %v, %v", ok, err)
	}
	if ok, err := VerifyPassword("wrong", h); err != nil || ok {
		t.Fatalf("verify wrong password = %v, %v", ok, err)
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of the same password are identical (salt not random)")
	}
	if NeedsRehash(h) {
		t.Error("fresh hash reported as needing rehash")
	}
}

func TestVerifyUsesParametersFromHash(t *testing.T) {
	// A hash made with lighter parameters still verifies and is flagged for rehash.
	salt := []byte("0123456789abcdef")
	key := derive("pw", salt, 1, 8, 1, 32)
	enc := "$argon2id$v=19$m=8,t=1,p=1$" + b64(salt) + "$" + b64(key)
	if ok, err := VerifyPassword("pw", enc); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if !NeedsRehash(enc) {
		t.Error("weak parameters not flagged")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{
		"", "plain", "$argon2i$v=19$m=65536,t=3,p=4$AAAA$AAAA", "$argon2id$v=18$m=65536,t=3,p=4$AAAA$AAAA",
		"$argon2id$v=19$m=65536,t=3$AAAA", "$argon2id$v=19$m=0,t=3,p=4$AAAA$AAAA",
		"$argon2id$v=19$m=999999999,t=3,p=4$AAAA$AAAA", "$argon2id$v=19$m=65536,t=3,p=4$!!$AAAA",
	} {
		if _, err := VerifyPassword("x", bad); err == nil {
			t.Errorf("hash %q accepted", bad)
		}
	}
}

func TestTokens(t *testing.T) {
	tok, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 { // 32 bytes, base64url without padding
		t.Errorf("token length = %d, want 43", len(tok))
	}
	if !bytes.Equal(hash, HashToken(tok)) {
		t.Error("stored hash differs from HashToken(token)")
	}
	tok2, _, _ := NewToken()
	if tok == tok2 {
		t.Error("tokens repeat")
	}
	if len(hash) != 32 {
		t.Errorf("hash length = %d", len(hash))
	}
}

func TestRandomDigits(t *testing.T) {
	seen := map[byte]bool{}
	for range 200 {
		d, err := RandomDigits(6)
		if err != nil || len(d) != 6 {
			t.Fatalf("RandomDigits = %q, %v", d, err)
		}
		for i := range d {
			if d[i] < '0' || d[i] > '9' {
				t.Fatalf("non-digit in %q", d)
			}
			seen[d[i]] = true
		}
	}
	if len(seen) != 10 {
		t.Errorf("only %d distinct digits in 1200 draws", len(seen))
	}
}

func TestMACBindsParts(t *testing.T) {
	k := []byte("key")
	if !Equal(MAC(k, "a", "bc"), MAC(k, "a", "bc")) {
		t.Error("MAC not deterministic")
	}
	if Equal(MAC(k, "a", "bc"), MAC(k, "ab", "c")) {
		t.Error("MAC does not separate parts")
	}
	if Equal(MAC(k, "a"), MAC([]byte("other"), "a")) {
		t.Error("MAC ignores the key")
	}
}

func b64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }
