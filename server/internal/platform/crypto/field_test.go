package crypto

import (
	"bytes"
	"testing"
)

func TestFieldCipherRoundTrip(t *testing.T) {
	c, err := NewFieldCipher([]byte("a-long-enough-master-key-for-tests"), "person.national_id")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal([]byte("10000000146"), []byte("person-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("10000000146")) {
		t.Fatal("the plaintext is visible in the sealed value")
	}
	pt, err := c.Open(sealed, []byte("person-1"))
	if err != nil || string(pt) != "10000000146" {
		t.Fatalf("open: %q %v", pt, err)
	}
	if _, err := c.Open(sealed, []byte("person-2")); err == nil {
		t.Error("a sealed value must not open under another associated value")
	}
	other, _ := NewFieldCipher([]byte("a-long-enough-master-key-for-tests"), "other.purpose")
	if _, err := other.Open(sealed, []byte("person-1")); err == nil {
		t.Error("a sealed value must not open under another purpose")
	}
	again, _ := c.Seal([]byte("10000000146"), []byte("person-1"))
	if bytes.Equal(sealed, again) {
		t.Error("each seal uses a fresh nonce")
	}
	if _, err := NewFieldCipher(nil, "x"); err == nil {
		t.Error("an empty key is refused")
	}
}
