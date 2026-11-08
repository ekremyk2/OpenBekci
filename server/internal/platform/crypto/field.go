package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// CurrentKeyID names the key a sealed value is written with. Sealed values start
// with "<id>:" so a rotated key can be told from the old one (plan 16); values
// without a prefix are the pre-key-id format and open with the same key.
const CurrentKeyID = "k1"

// FieldCipher seals single database fields (national ids) with AES-256-GCM.
// The key is derived from OPENBEKCI_ENCRYPTION_KEY and a purpose label, so a
// ciphertext of one field kind cannot be replayed as another. The sealed value
// is nonce || ciphertext || tag.
type FieldCipher struct{ aead cipher.AEAD }

// NewFieldCipher derives the cipher for purpose from the master key. It
// fails when the key is empty.
func NewFieldCipher(masterKey []byte, purpose string) (*FieldCipher, error) {
	if len(masterKey) == 0 {
		return nil, errors.New("crypto: no encryption key configured")
	}
	// HKDF-style single block: SHA-256(master || 0x00 || purpose). The master key
	// is already high-entropy (checked at start-up), so no stretching is needed.
	h := sha256.New()
	h.Write(masterKey)
	h.Write([]byte{0})
	h.Write([]byte("openbekci/field/" + purpose))
	block, err := aes.NewCipher(h.Sum(nil))
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &FieldCipher{aead: aead}, nil
}

// Seal encrypts plaintext. aad (for example the row's org id and person id)
// is authenticated but not stored; Open must pass the same value.
func (c *FieldCipher) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize(), c.aead.NonceSize()+len(plaintext)+c.aead.Overhead())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: read nonce: %w", err)
	}
	out := append([]byte(CurrentKeyID+":"), c.aead.Seal(nonce, nonce, plaintext, aad)...)
	return out, nil
}

// Open decrypts a value sealed by Seal.
func (c *FieldCipher) Open(sealed, aad []byte) ([]byte, error) {
	if rest, ok := bytes.CutPrefix(sealed, []byte(CurrentKeyID+":")); ok {
		if pt, err := c.open(rest, aad); err == nil {
			return pt, nil
		}
	}
	return c.open(sealed, aad)
}

// NeedsReseal reports whether sealed is in the old format or under another key id.
func NeedsReseal(sealed []byte) bool { return !bytes.HasPrefix(sealed, []byte(CurrentKeyID+":")) }

func (c *FieldCipher) open(sealed, aad []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n+c.aead.Overhead() {
		return nil, errors.New("crypto: sealed value is too short")
	}
	pt, err := c.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	return pt, nil
}
