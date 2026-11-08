package crypto

import (
	"bytes"
	"testing"
)

func TestFieldCipherCarriesKeyIDAndOpensLegacyValues(t *testing.T) {
	c, _ := NewFieldCipher([]byte("a-long-enough-master-key-for-tests"), "person.national_id")
	sealed, _ := c.Seal([]byte("x"), []byte("a"))
	if !bytes.HasPrefix(sealed, []byte("k1:")) || NeedsReseal(sealed) {
		t.Fatalf("sealed value has no key id: %q", sealed[:4])
	}
	legacy := sealed[3:]
	if !NeedsReseal(legacy) {
		t.Fatal("an unprefixed value needs resealing")
	}
	if pt, err := c.Open(legacy, []byte("a")); err != nil || string(pt) != "x" {
		t.Fatalf("legacy open: %q %v", pt, err)
	}
}
