package access

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var turkishLetters = strings.NewReplacer(
	"İ", "I", "ı", "I", "Ş", "S", "ş", "S", "Ç", "C", "ç", "C",
	"Ğ", "G", "ğ", "G", "Ö", "O", "ö", "O", "Ü", "U", "ü", "U",
)

// NormalizePlate is the single plate normalization function shared with the
// agent (design 7.4, pinned by testdata/access-protocol/plate-normalization.json):
// Unicode NFKC, Turkish letters to their Latin base, ASCII upper case, then
// everything except A-Z and 0-9 is removed. The empty result means "invalid":
// such a plate projects no credential.
func NormalizePlate(s string) string {
	s = turkishLetters.Replace(norm.NFKC.String(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// QRPrefix is the prefix of a visitor QR string.
const QRPrefix = "obk1."

// QRValue is the credential value of a visitor QR token: hex(sha256(token)).
func QRValue(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
