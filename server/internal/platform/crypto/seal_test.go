package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealWithTokenOnlyOpensWithTheSameToken(t *testing.T) {
	blob, err := SealWithToken("old-refresh-token", []byte("successor pair"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("successor")) {
		t.Fatal("plaintext visible in the blob")
	}
	got, err := OpenWithToken("old-refresh-token", blob)
	if err != nil || string(got) != "successor pair" {
		t.Fatalf("open = %q, %v", got, err)
	}
	for _, bad := range []string{"other-token", "", "old-refresh-token "} {
		if _, err := OpenWithToken(bad, blob); !errors.Is(err, ErrOpen) {
			t.Errorf("token %q opened the blob: %v", bad, err)
		}
	}
	// the server secret plays no part: nothing but the token derives the key
	blob2, _ := SealWithToken("old-refresh-token", []byte("successor pair"))
	if bytes.Equal(blob, blob2) {
		t.Error("two seals are identical (nonce reuse)")
	}
	if _, err := OpenWithToken("old-refresh-token", blob[:5]); !errors.Is(err, ErrOpen) {
		t.Errorf("truncated blob: %v", err)
	}
}
