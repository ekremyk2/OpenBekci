package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// ErrOpen is returned by OpenWithToken when the blob does not decrypt under
// the given token.
var ErrOpen = errors.New("crypto: cannot open sealed data")

func tokenAEAD(token string) (cipher.AEAD, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(token), nil, []byte("openbekci refresh grace v1")), key); err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// SealWithToken encrypts plaintext (AES-256-GCM) under a key derived with HKDF
// from token. Only a holder of the token can open it, and the token itself is
// never stored in clear, so a database dump (even with the server secret)
// cannot open it.
func SealWithToken(token string, plaintext []byte) ([]byte, error) {
	aead, err := tokenAEAD(token)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// OpenWithToken decrypts a blob made by SealWithToken.
func OpenWithToken(token string, blob []byte) ([]byte, error) {
	aead, err := tokenAEAD(token)
	if err != nil {
		return nil, err
	}
	if len(blob) < aead.NonceSize() {
		return nil, ErrOpen
	}
	pt, err := aead.Open(nil, blob[:aead.NonceSize()], blob[aead.NonceSize():], nil)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}
