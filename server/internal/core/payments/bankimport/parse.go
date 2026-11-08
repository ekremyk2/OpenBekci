// Package bankimport is the bank statement import module (backend plan 4.6, M5):
// CSV and XLSX statements parsed by an importer, a suggestion engine and manual
// matching of lines to payments through the payments service, with
// reconciliation of statement and ledger balances.
package bankimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// CoreCSV is the id of the built-in importer.
const CoreCSV = "core_csv"

// MaxFileBytes is the size cap of an uploaded statement until the files module exists.
const MaxFileBytes = 10 << 20

// Mapping is the typed column mapping: target field to source header (or a
// zero-based index as a string). Targets: booked_on, amount (both required),
// counterparty, description, reference, balance.
type Mapping struct {
	Columns    map[string]string `json:"columns"`
	DateFormat *string           `json:"date_format"`
	Delimiter  *string           `json:"delimiter"`
	Sheet      *string           `json:"sheet"`
	Remember   *bool             `json:"remember"`
}

// DefaultColumns is the layout of core_csv: the header names equal the targets.
var DefaultColumns = map[string]string{"booked_on": "booked_on", "amount": "amount", "counterparty": "counterparty", "description": "description", "reference": "reference"}

// Line is one parsed statement row.
type Line struct {
	LineNo       int
	BookedOn     string
	Amount       int64
	Counterparty string
	Description  string
	Reference    string
	// Balance is the running balance after the line when the statement has the column (minor units).
	Balance *int64
	Raw     map[string]string
}

// RowError describes a row that could not be parsed.
type RowError struct {
	Row    int    `json:"row"`
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e RowError) Error() string { return fmt.Sprintf("row %d: %s: %s", e.Row, e.Field, e.Reason) }

// ParseAmount parses "1.234,50", "-1.234,50", "1,234.50", "1234.5", "₺ 12,00" and
// "(12,00)" into minor units (kuruş). With both separators the last one is the
// decimal separator; a lone comma is decimal when 1-2 digits follow, else thousands;
// a lone dot is thousands only in the form 1.234(.567).
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("₺", "", "TL", "", "TRY", "", " ", "", " ", "").Replace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if strings.HasSuffix(s, "-") { // "12,00-"
		neg, s = true, s[:len(s)-1]
	}
	lc, ld := strings.LastIndexByte(s, ','), strings.LastIndexByte(s, '.')
	var intPart, frac string
	switch {
	case lc >= 0 && ld >= 0:
		sep := max(lc, ld)
		intPart, frac = s[:sep], s[sep+1:]
	case lc >= 0:
		if tail := s[lc+1:]; len(tail) <= 2 && strings.Count(s, ",") == 1 {
			intPart, frac = s[:lc], tail
		} else {
			intPart = s
		}
	case ld >= 0:
		if tail := s[ld+1:]; len(tail) == 3 && strings.Count(s, ".") >= 1 && s[:ld] != "" && allDigitsAndDots(s[:ld]) && (strings.Count(s, ".") > 1 || len(s[:ld]) <= 3) {
			intPart = s
		} else {
			intPart, frac = s[:ld], tail
		}
	default:
		intPart = s
	}
	intPart = strings.NewReplacer(".", "", ",", "").Replace(intPart)
	if intPart == "" {
		intPart = "0"
	}
	if len(frac) > 2 {
		return 0, errors.New("more than two decimals")
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(intPart+frac, 10, 64)
	if err != nil {
		return 0, errors.New("not a number")
	}
	if neg {
		n = -n
	}
	return n, nil
}

func allDigitsAndDots(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

var dateLayouts = []string{"2006-01-02", "02.01.2006", "2.1.2006", "02/01/2006", "2/1/2006", "02-01-2006", "02.01.2006 15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "02/01/2006 15:04:05"}

// goLayout turns a pattern such as DD.MM.YYYY into a Go layout.
func goLayout(p string) string {
	r := strings.NewReplacer("YYYY", "2006", "YY", "06", "DD", "02", "MM", "01", "HH", "15", "mm", "04", "ss", "05")
	return r.Replace(p)
}

// ParseDate parses a booking date with the mapped format, or the usual layouts.
func ParseDate(s string, format *string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("empty")
	}
	layouts := dateLayouts
	if format != nil && *format != "" {
		layouts = append([]string{goLayout(*format)}, layouts...)
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	// Excel serial dates
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 20000 && f < 80000 {
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(f))
		return t.Format("2006-01-02"), nil
	}
	return "", errors.New("unrecognized date")
}

// Parse reads a statement. It returns the parsed lines and the row errors;
// rows that fail are reported and skipped (the import becomes `partial`).
func Parse(filename string, data []byte, m Mapping) ([]Line, []RowError, error) {
	if len(data) == 0 {
		return nil, nil, errors.New("the file is empty")
	}
	var rows [][]string
	var err error
	if isXLSX(filename, data) {
		rows, err = readXLSX(data, m.Sheet)
	} else {
		rows, err = readCSV(data, m.Delimiter)
	}
	if err != nil {
		return nil, nil, err
	}
	return parseRows(rows, m)
}

func isXLSX(filename string, data []byte) bool {
	return strings.HasSuffix(strings.ToLower(filename), ".xlsx") || bytes.HasPrefix(data, []byte("PK\x03\x04"))
}

func readXLSX(data []byte, sheet *string) ([][]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("the spreadsheet cannot be read")
	}
	defer func() { _ = f.Close() }()
	name := f.GetSheetName(0)
	if sheet != nil && *sheet != "" {
		name = *sheet
	}
	rows, err := f.GetRows(name)
	if err != nil {
		return nil, fmt.Errorf("sheet %q cannot be read", name)
	}
	return rows, nil
}

func readCSV(data []byte, delim *string) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		data = []byte(latin5(data))
	}
	d := ','
	if delim != nil && *delim != "" {
		d, _ = utf8.DecodeRuneInString(*delim)
	} else {
		first, _, _ := strings.Cut(string(data), "\n")
		best := 0
		for _, c := range []rune{',', ';', '\t', '|'} {
			if n := strings.Count(first, string(c)); n > best {
				best, d = n, c
			}
		}
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = d
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var rows [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("the CSV is malformed: %w", err)
		}
		rows = append(rows, rec)
	}
}

// latin5 decodes windows-1254 (Turkish) bytes, the usual non-UTF-8 export encoding.
func latin5(b []byte) string {
	var sb strings.Builder
	m := map[byte]rune{0xD0: 'Ğ', 0xDD: 'İ', 0xDE: 'Ş', 0xF0: 'ğ', 0xFD: 'ı', 0xFE: 'ş'}
	for _, c := range b {
		if r, ok := m[c]; ok {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

func parseRows(rows [][]string, m Mapping) ([]Line, []RowError, error) {
	cols := m.Columns
	if len(cols) == 0 {
		cols = DefaultColumns
	}
	for _, req := range []string{"booked_on", "amount"} {
		if cols[req] == "" {
			return nil, nil, fmt.Errorf("the mapping needs a column for %s", req)
		}
	}
	// skip leading empty rows; the first non-empty row is the header
	start := 0
	for start < len(rows) && blank(rows[start]) {
		start++
	}
	if start >= len(rows) {
		return nil, nil, errors.New("the file has no rows")
	}
	header := rows[start]
	idx := map[string]int{}
	for target, src := range cols {
		i := -1
		if n, err := strconv.Atoi(src); err == nil && src != "" && n >= 0 && n < len(header) && !headerHas(header, src) {
			i = n
		} else {
			for j, h := range header {
				if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(src)) {
					i = j
					break
				}
			}
		}
		if i < 0 {
			if target == "booked_on" || target == "amount" {
				return nil, nil, fmt.Errorf("column %q (%s) is not in the file", src, target)
			}
			continue
		}
		idx[target] = i
	}
	get := func(row []string, target string) string {
		i, ok := idx[target]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	var lines []Line
	var errs []RowError
	for r := start + 1; r < len(rows); r++ {
		row := rows[r]
		if blank(row) {
			continue
		}
		n := r - start
		on, err := ParseDate(get(row, "booked_on"), m.DateFormat)
		if err != nil {
			errs = append(errs, RowError{Row: n, Field: "booked_on", Reason: err.Error()})
			continue
		}
		amt, err := ParseAmount(get(row, "amount"))
		if err != nil || amt == 0 {
			reason := "zero"
			if err != nil {
				reason = err.Error()
			}
			errs = append(errs, RowError{Row: n, Field: "amount", Reason: reason})
			continue
		}
		l := Line{LineNo: n, BookedOn: on, Amount: amt, Counterparty: get(row, "counterparty"), Description: get(row, "description"), Reference: get(row, "reference"), Raw: map[string]string{}}
		if b := get(row, "balance"); b != "" {
			if v, err := ParseAmount(b); err == nil {
				l.Balance = &v
			}
		}
		for j, h := range header {
			if j < len(row) {
				l.Raw[strings.TrimSpace(h)] = row[j]
			}
		}
		lines = append(lines, l)
	}
	return lines, errs, nil
}

func headerHas(header []string, s string) bool {
	for _, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), s) {
			return true
		}
	}
	return false
}

func blank(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
