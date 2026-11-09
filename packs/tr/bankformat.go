// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"gopkg.in/yaml.v3"
)

// Bank statement formats are data: one YAML file per bank in bankformats/
// (header fingerprint, column names, date layouts, delimiter). A new bank is
// a YAML file and a fixture, not code (BANKS.md).

//go:embed bankformats/*.yaml
var bankFormatFiles embed.FS

// bankFormat is one parsed format definition.
type bankFormat struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Formats       []string `yaml:"formats"`
	FilenameHints []string `yaml:"filename_hints"`
	Delimiter     string   `yaml:"delimiter"`
	Currency      string   `yaml:"currency"`
	Fingerprint   []string `yaml:"fingerprint"`
	DateFormats   []string `yaml:"date_formats"`
	Columns       struct {
		Date         []string `yaml:"date"`
		Description  []string `yaml:"description"`
		Amount       []string `yaml:"amount"`
		Debit        []string `yaml:"debit"`
		Credit       []string `yaml:"credit"`
		Balance      []string `yaml:"balance"`
		Reference    []string `yaml:"reference"`
		Counterparty []string `yaml:"counterparty"`
	} `yaml:"columns"`
	SkipRowPrefixes      []string `yaml:"skip_row_prefixes"`
	CounterpartyPatterns []string `yaml:"counterparty_patterns"`

	fingerprint []string // normalized
	hints       []string // normalized
	skipPrefix  []string // normalized
	cpRes       []*regexp.Regexp
}

// BankFormatIDs lists the importer ids of the formats built in (sorted).
func BankFormatIDs() []string {
	fs, err := loadBankFormats()
	if err != nil {
		return nil
	}
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	return ids
}

func loadBankFormats() ([]*bankFormat, error) {
	entries, err := bankFormatFiles.ReadDir("bankformats")
	if err != nil {
		return nil, err
	}
	var out []*bankFormat
	for _, e := range entries {
		raw, err := bankFormatFiles.ReadFile("bankformats/" + e.Name())
		if err != nil {
			return nil, err
		}
		var f bankFormat
		if err := yaml.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("bank format %s: %w", e.Name(), err)
		}
		if err := f.prepare(); err != nil {
			return nil, fmt.Errorf("bank format %s: %w", e.Name(), err)
		}
		out = append(out, &f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *bankFormat) prepare() error {
	if !strings.HasPrefix(f.ID, ID+".") {
		return fmt.Errorf("id %q must start with %q", f.ID, ID+".")
	}
	if len(f.Fingerprint) == 0 || len(f.DateFormats) == 0 || len(f.Columns.Date) == 0 {
		return fmt.Errorf("%s needs a fingerprint, date_formats and a date column", f.ID)
	}
	if len(f.Columns.Amount) == 0 && (len(f.Columns.Debit) == 0 || len(f.Columns.Credit) == 0) {
		return fmt.Errorf("%s needs an amount column or debit and credit columns", f.ID)
	}
	if f.Currency == "" {
		f.Currency = "TRY"
	}
	if len(f.Formats) == 0 {
		f.Formats = []string{"csv"}
	}
	for _, s := range f.Fingerprint {
		f.fingerprint = append(f.fingerprint, normHeader(s))
	}
	for _, s := range f.FilenameHints {
		f.hints = append(f.hints, normHeader(s))
	}
	for _, s := range f.SkipRowPrefixes {
		f.skipPrefix = append(f.skipPrefix, normHeader(s))
	}
	for _, p := range f.CounterpartyPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("counterparty pattern %q: %w", p, err)
		}
		f.cpRes = append(f.cpRes, re)
	}
	return nil
}

var turkishFold = strings.NewReplacer("İ", "i", "I", "i", "ı", "i", "ş", "s", "Ş", "s", "ğ", "g", "Ğ", "g", "ü", "u", "Ü", "u", "ö", "o", "Ö", "o", "ç", "c", "Ç", "c")

// normHeader folds a header or a file name for comparison: Turkish letters
// without diacritics, lower case, letters and digits only.
func normHeader(s string) string {
	s = strings.ToLower(turkishFold.Replace(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// decodeText returns the text of a CSV file: UTF-8 when it is valid UTF-8
// (a byte order mark is dropped), else windows-1254 (Turkish). A head cut
// inside a multi-byte character still counts as UTF-8.
func decodeText(b []byte) string {
	b = []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
	if utf8.Valid(b) {
		return string(b)
	}
	for cut := 1; cut <= 3 && cut <= len(b); cut++ {
		tail := b[len(b)-cut:]
		if tail[0] >= 0xC0 && !utf8.FullRune(tail) && utf8.Valid(b[:len(b)-cut]) {
			return string(b[:len(b)-cut])
		}
	}
	out, err := charmap.Windows1254.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}
