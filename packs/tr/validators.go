// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"fmt"
	"math/big"
	"strings"
)

// Validator codes: stable identifiers the catalog labels in tr and en.
const (
	CodeTaxIDFormat   = "tr.validator.tax_id.format"
	CodeTaxIDChecksum = "tr.validator.tax_id.checksum"
	CodeIBANFormat    = "tr.validator.iban.format"
	CodeIBANChecksum  = "tr.validator.iban.checksum"
	CodeIBANReserved  = "tr.validator.iban.reserved"
	CodeIBANBank      = "tr.validator.iban.bank_code"
	CodePlateFormat   = "tr.validator.plate.format"
	CodePlateProvince = "tr.validator.plate.province"
)

// ValidationError is the error the validators return. Code is one of the
// Code constants; the core can use it as the field error code.
type ValidationError struct {
	code string
	msg  string
}

func (e *ValidationError) Error() string { return e.msg }

// Code returns the stable catalog code.
func (e *ValidationError) Code() string { return e.code }

func bad(code, format string, args ...any) error {
	return &ValidationError{code: code, msg: fmt.Sprintf(format, args...)}
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---- vergi numarası

// taxID validates a Turkish tax number: the 10-digit vergi kimlik numarası
// (VKN) of companies and other taxpayers, with its check digit, or the
// 11-digit TCKN individuals use as their tax number (the same checksum as the
// core's national id validator, repeated here because a pack cannot import the
// core).
type taxID struct{}

func (taxID) ID() string      { return ID + ".tax_id" }
func (taxID) Kind() string    { return "tax_id" }
func (taxID) Country() string { return "TR" }

func (taxID) Validate(value string) error { return ValidateTaxID(value) }

// ValidateTaxID checks a vergi numarası (VKN, 10 digits) or a TCKN (11 digits).
func ValidateTaxID(value string) error {
	v := strings.TrimSpace(value)
	if !digitsOnly(v) || (len(v) != 10 && len(v) != 11) {
		return bad(CodeTaxIDFormat, "a vergi numarası has 10 digits (a TCKN has 11): %q", value)
	}
	if len(v) == 11 {
		if !validTCKN(v) {
			return bad(CodeTaxIDChecksum, "the TCKN %q fails its checksum", value)
		}
		return nil
	}
	if !validVKN(v) {
		return bad(CodeTaxIDChecksum, "the vergi numarası %q fails its check digit", value)
	}
	return nil
}

// validVKN is the GİB algorithm: for the first nine digits d[i], t = (d[i] +
// 9 - i) mod 10; p = t * 2^(9-i) mod 9, or 9 when t is not zero and p is
// zero; the check digit is (10 - sum(p) mod 10) mod 10.
func validVKN(v string) bool {
	sum := 0
	for i := 0; i < 9; i++ {
		d := int(v[i] - '0')
		t := (d + 9 - i) % 10
		p := (t * (1 << uint(9-i))) % 9
		if t != 0 && p == 0 {
			p = 9
		}
		sum += p
	}
	return (10-sum%10)%10 == int(v[9]-'0')
}

// validTCKN: 11 digits, the first not zero, digit 10 = ((d1+d3+d5+d7+d9)*7 -
// (d2+d4+d6+d8)) mod 10, digit 11 = sum of the first ten mod 10.
func validTCKN(v string) bool {
	if v[0] == '0' {
		return false
	}
	var d [11]int
	for i := range d {
		d[i] = int(v[i] - '0')
	}
	odd := d[0] + d[2] + d[4] + d[6] + d[8]
	even := d[1] + d[3] + d[5] + d[7]
	if ((odd*7-even)%10+10)%10 != d[9] {
		return false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		sum += d[i]
	}
	return sum%10 == d[10]
}

// ---- IBAN

// iban validates a Turkish IBAN beyond the generic mod-97 check: 26
// characters, "TR", two check digits, the five-digit bank code (EFT kodu, not
// all zeros), the reserved sixth BBAN digit which is always 0, and sixteen
// digits of account number.
type iban struct{}

func (iban) ID() string      { return ID + ".iban" }
func (iban) Kind() string    { return "iban" }
func (iban) Country() string { return "TR" }

func (iban) Validate(value string) error { return ValidateIBAN(value) }

// ValidateIBAN checks a TR IBAN; spaces are ignored and case does not matter.
func ValidateIBAN(value string) error {
	v := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), " ", ""))
	if len(v) != 26 || !strings.HasPrefix(v, "TR") || !digitsOnly(v[2:]) {
		return bad(CodeIBANFormat, "a Turkish IBAN is TR plus 24 digits (26 characters): %q", value)
	}
	if v[9] != '0' {
		return bad(CodeIBANReserved, "the reserved digit (10th character) of a Turkish IBAN is 0: %q", value)
	}
	if v[4:9] == "00000" {
		return bad(CodeIBANBank, "the bank code of %q is 00000", value)
	}
	if !mod97(v) {
		return bad(CodeIBANChecksum, "the IBAN %q fails its mod 97 check", value)
	}
	return nil
}

func mod97(iban string) bool {
	rearranged := iban[4:] + iban[:4]
	var b strings.Builder
	for _, r := range rearranged {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			fmt.Fprint(&b, int(r-'A')+10)
		default:
			return false
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	return ok && new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

// ---- plate

// plate validates a vehicle registration plate: the two-digit province code
// 01 to 81, one to three letters from the plate alphabet (no Q, W, X, O, I or
// Turkish-only letters) and digits: one letter takes four digits, two letters
// three or four, three letters two or three. Diplomatic and special plates are
// not covered.
type plate struct{}

func (plate) ID() string      { return ID + ".plate" }
func (plate) Kind() string    { return "plate" }
func (plate) Country() string { return "TR" }

func (plate) Validate(value string) error { return ValidatePlate(value) }

const plateLetters = "ABCDEFGHJKLMNPRSTUVYZ"

// ValidatePlate checks a Turkish plate such as "34 ABC 123" or "06AB1234".
func ValidatePlate(value string) error {
	v := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", " ", "").Replace(value))
	if len(v) < 5 || len(v) > 9 {
		return bad(CodePlateFormat, "%q is not a Turkish plate (for example 34 ABC 123)", value)
	}
	prov := v[:2]
	if !digitsOnly(prov) {
		return bad(CodePlateFormat, "%q does not start with a two-digit province code", value)
	}
	if prov < "01" || prov > "81" {
		return bad(CodePlateProvince, "the province code %s of %q is not 01 to 81", prov, value)
	}
	rest := v[2:]
	letters := 0
	for letters < len(rest) && rest[letters] >= 'A' && rest[letters] <= 'Z' {
		letters++
	}
	digits := rest[letters:]
	if letters < 1 || letters > 3 || !digitsOnly(digits) {
		return bad(CodePlateFormat, "%q is not a Turkish plate (province code, 1 to 3 letters, then digits)", value)
	}
	for _, r := range rest[:letters] {
		if !strings.ContainsRune(plateLetters, r) {
			return bad(CodePlateFormat, "the letter %q is not used on Turkish plates", r)
		}
	}
	ok := false
	switch letters {
	case 1:
		ok = len(digits) == 4
	case 2:
		ok = len(digits) == 3 || len(digits) == 4
	case 3:
		ok = len(digits) == 2 || len(digits) == 3
	}
	if !ok {
		return bad(CodePlateFormat, "%q has the wrong number of digits for %d letter(s)", value, letters)
	}
	return nil
}
