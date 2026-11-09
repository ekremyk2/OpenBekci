// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"regexp"
	"strings"
)

// The reference-code scheme of bank transfers: "<unit>-<period>", for example
// "A-12-2026-03" for door A-12, period March 2026. The resident writes it in
// the description of the EFT/FAST/havale, the bank statement importers lift it
// into the statement line's Reference field, and the core's matching
// suggestions (payments/bankimport.Rank) score it above the payer's name and
// the amount. The core builds the same text (bankimport.BuildReference), so
// the two stay in step; this file is the pack's copy for the importers and
// for anything that wants to show the text.
//
// Constraints, because banks mangle descriptions: ASCII only after
// folding, no spaces inside the code, the period always last (YYYY-MM).

var refCodeRe = regexp.MustCompile(`^(.*?)[\s/-]*(\d{4})-(0[1-9]|1[0-2])\s*$`)

// RefCode returns the reference text a resident writes for a unit and period
// ("2026-03"). Spaces in the unit's door number are replaced by hyphens and
// Turkish letters are folded to ASCII, so the text survives the bank.
func RefCode(unit, period string) string {
	u := strings.Trim(strings.Join(strings.Fields(strings.ToUpper(foldASCII(unit))), "-"), "-")
	return u + "-" + period
}

// ParseRefCode splits a reference text into the unit part and the period; ok
// is false when the text does not end in a period (YYYY-MM).
func ParseRefCode(s string) (unit, period string, ok bool) {
	m := refCodeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return strings.TrimSpace(m[1]), m[2] + "-" + m[3], true
}

func foldASCII(s string) string {
	return strings.NewReplacer("İ", "I", "ı", "i", "ş", "s", "Ş", "S", "ğ", "g", "Ğ", "G", "ü", "u", "Ü", "U", "ö", "o", "Ö", "O", "ç", "c", "Ç", "C").Replace(s)
}
