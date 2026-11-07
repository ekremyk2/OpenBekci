package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Signer makes and checks the signatures of locally served download URLs.
// The key is derived from the instance secret, so a signature is useless on
// another instance and for any other purpose.
type Signer struct{ key []byte }

// NewSigner derives a signer from the instance secret key.
func NewSigner(secret []byte) *Signer {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("openbekci/files/signed-url/v1"))
	return &Signer{key: m.Sum(nil)}
}

// Errors from Verify.
var (
	ErrSignatureExpired = errors.New("storage: signed url expired")
	ErrSignatureInvalid = errors.New("storage: signed url invalid")
)

// Sign returns the hex signature of msg valid until exp (whole seconds).
func (s *Signer) Sign(msg string, exp time.Time) string {
	return hex.EncodeToString(s.mac(msg, exp.Unix()))
}

// Verify checks sig over msg and the expiry time given as Unix seconds.
func (s *Signer) Verify(msg string, expUnix int64, sig string, now time.Time) error {
	got, err := hex.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(got, s.mac(msg, expUnix)) != 1 {
		return ErrSignatureInvalid
	}
	if now.Unix() > expUnix {
		return ErrSignatureExpired
	}
	return nil
}

func (s *Signer) mac(msg string, expUnix int64) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(msg))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(expUnix, 10)))
	return m.Sum(nil)
}

// DownloadMessage is what a local signed URL signs: the file's organization,
// id and variant ("" for the original).
func DownloadMessage(org, id, variant string) string {
	return "GET files/" + org + "/" + id + "/" + variant
}
