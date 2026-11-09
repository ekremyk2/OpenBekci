// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/openbekci/openbekci/sdk"
)

// bankImporter is the sdk.StatementImporter of one bank format.
type bankImporter struct {
	p   *Pack
	def *bankFormat
}

var _ sdk.StatementImporter = (*bankImporter)(nil)

func (b *bankImporter) ID() string        { return b.def.ID }
func (b *bankImporter) Formats() []string { return slices.Clone(b.def.Formats) }

var xlsxMagic = []byte("PK\x03\x04")

// Detect recognizes a CSV by its header row (the fingerprint of the format,
// found in the head of the file in UTF-8 or windows-1254), an xlsx by its zip
// signature and a file name containing a hint of the format ("ziraat"): a
// workbook is compressed, its header cannot be read from the first KiB.
func (b *bankImporter) Detect(filename string, head []byte) bool {
	name := strings.ToLower(filename)
	if strings.HasSuffix(name, ".xlsx") {
		if !bytes.HasPrefix(head, xlsxMagic) {
			return false
		}
		n := normHeader(filename)
		for _, h := range b.def.hints {
			if strings.Contains(n, h) {
				return true
			}
		}
		return false
	}
	if !strings.HasSuffix(name, ".csv") && !strings.HasSuffix(name, ".txt") {
		return false
	}
	rows := csvRows(decodeText(head), b.def.Delimiter)
	_, _, ok := b.findHeader(rows)
	return ok
}

// csvRows splits text into rows of cells; a record that does not parse
// (the head may end mid-record) is read as one cell per delimiter.
func csvRows(text, delim string) [][]string {
	d := ';'
	if delim != "" {
		d = rune(delim[0])
	}
	var out [][]string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, nil)
			continue
		}
		r := csv.NewReader(strings.NewReader(line))
		r.Comma, r.LazyQuotes, r.FieldsPerRecord = d, true, -1
		rec, err := r.Read()
		if err != nil {
			rec = strings.Split(line, string(d))
		}
		out = append(out, rec)
	}
	return out
}

type columns struct {
	date, desc, amount, debit, credit, balance, ref, cp int
	header                                              []string
}

func indexOf(norm []string, aliases []string) int {
	for _, a := range aliases {
		na := normHeader(a)
		if i := slices.Index(norm, na); i >= 0 {
			return i
		}
	}
	return -1
}

// findHeader looks for the header row in the first rows: the row that holds
// every header of the fingerprint.
func (b *bankImporter) findHeader(rows [][]string) (int, columns, bool) {
	for i, row := range rows {
		if i >= 40 {
			break
		}
		norm := make([]string, len(row))
		for j, c := range row {
			norm[j] = normHeader(c)
		}
		all := len(norm) > 0
		for _, f := range b.def.fingerprint {
			if !slices.Contains(norm, f) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		c := columns{
			date: indexOf(norm, b.def.Columns.Date), desc: indexOf(norm, b.def.Columns.Description),
			amount: indexOf(norm, b.def.Columns.Amount), debit: indexOf(norm, b.def.Columns.Debit), credit: indexOf(norm, b.def.Columns.Credit),
			balance: indexOf(norm, b.def.Columns.Balance), ref: indexOf(norm, b.def.Columns.Reference), cp: indexOf(norm, b.def.Columns.Counterparty),
			header: row,
		}
		if c.date < 0 || (c.amount < 0 && (c.debit < 0 || c.credit < 0)) {
			continue
		}
		return i, c, true
	}
	return 0, columns{}, false
}

// enabled applies the organization's tr_bank_formats list (empty: all formats).
func (b *bankImporter) enabled(ctx context.Context) error {
	if b.p == nil || b.p.svc == nil {
		return nil
	}
	if _, ok := sdk.OrgFrom(ctx); !ok {
		return nil
	}
	v, ok, err := b.p.svc.Settings().Get(ctx, sdk.OrgScope(), "tr_bank_formats")
	if err != nil || !ok {
		return err
	}
	list, _ := v.([]any)
	if len(list) == 0 {
		return nil
	}
	for _, x := range list {
		if s, _ := x.(string); s == b.def.ID {
			return nil
		}
	}
	return fmt.Errorf("the format %s is not enabled in the organization's bank formats: %w", b.def.ID, sdk.ErrRejected)
}

func (b *bankImporter) Parse(ctx context.Context, filename string, data []byte) (sdk.Statement, error) {
	if err := b.enabled(ctx); err != nil {
		return sdk.Statement{}, err
	}
	var rows [][]string
	if bytes.HasPrefix(data, xlsxMagic) {
		var err error
		if rows, err = readXLSX(data, b); err != nil {
			return sdk.Statement{}, fmt.Errorf("%s: not a readable workbook: %w", filename, sdk.ErrRejected)
		}
	} else {
		rows = csvRows(decodeText(data), b.def.Delimiter)
	}
	hi, cols, ok := b.findHeader(rows)
	if !ok {
		return sdk.Statement{}, fmt.Errorf("%s: no %s header row found: %w", filename, b.def.Name, sdk.ErrRejected)
	}
	return b.parseRows(rows[hi+1:], cols), nil
}

// readXLSX returns the rows of the first sheet that holds the format's header.
func readXLSX(data []byte, b *bankImporter) ([][]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 32 << 20}) // zip-bomb caps
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var first [][]string
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = rows
		}
		if _, _, ok := b.findHeader(rows); ok {
			return rows, nil
		}
	}
	return first, nil
}

var refRe = regexp.MustCompile(`(?i)\b[A-Z0-9]+(?:-[A-Z0-9]+)*-\d{4}-(?:0[1-9]|1[0-2])\b`)

func cellAt(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func (b *bankImporter) parseRows(rows [][]string, c columns) sdk.Statement {
	st := sdk.Statement{Currency: b.def.Currency}
	n := 0
	for _, row := range rows {
		blank := true
		for _, x := range row {
			if strings.TrimSpace(x) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		if first := normHeader(cellAt(row, 0)); first != "" && b.skipRow(first) {
			continue
		}
		n++
		date, err := parseBankDate(cellAt(row, c.date), b.def.DateFormats)
		if err != nil {
			st.Errors = append(st.Errors, sdk.RowError{Row: n, Field: "date", Reason: err.Error()})
			continue
		}
		amt, err := b.amount(row, c)
		if err != nil {
			st.Errors = append(st.Errors, sdk.RowError{Row: n, Field: "amount", Reason: err.Error()})
			continue
		}
		if amt == 0 {
			st.Errors = append(st.Errors, sdk.RowError{Row: n, Field: "amount", Reason: "the amount is zero"})
			continue
		}
		l := sdk.StatementLine{LineNo: n, BookedOn: date, Amount: amt, Description: cellAt(row, c.desc), Reference: cellAt(row, c.ref), Counterparty: cellAt(row, c.cp), Raw: map[string]string{}}
		if l.Counterparty == "" {
			for _, re := range b.def.cpRes {
				if m := re.FindStringSubmatch(l.Description); m != nil {
					l.Counterparty = strings.TrimSpace(m[1])
					break
				}
			}
		}
		// the reference-code scheme (<unit>-<period>) usually travels inside the description
		if _, _, ok := ParseRefCode(l.Reference); !ok {
			if m := refRe.FindString(l.Description); m != "" {
				l.Reference = m
			}
		}
		if s := cellAt(row, c.balance); s != "" {
			if v, err := parseMoney(s); err == nil {
				l.Balance = &v
			}
		}
		for j, h := range c.header {
			if v := cellAt(row, j); v != "" && strings.TrimSpace(h) != "" {
				l.Raw[strings.TrimSpace(h)] = v
			}
		}
		st.Lines = append(st.Lines, l)
	}
	st.OpeningBalance, st.ClosingBalance = balances(st.Lines)
	return st
}

func (b *bankImporter) skipRow(first string) bool {
	for _, p := range b.def.skipPrefix {
		if strings.HasPrefix(first, p) {
			return true
		}
	}
	return false
}

func (b *bankImporter) amount(row []string, c columns) (int64, error) {
	if c.amount >= 0 {
		return parseMoney(cellAt(row, c.amount))
	}
	var debit, credit int64
	if s := cellAt(row, c.debit); s != "" {
		v, err := parseMoney(s)
		if err != nil {
			return 0, err
		}
		debit = v
	}
	if s := cellAt(row, c.credit); s != "" {
		v, err := parseMoney(s)
		if err != nil {
			return 0, err
		}
		credit = v
	}
	if debit < 0 {
		debit = -debit
	}
	return credit - debit, nil
}

// balances derives the opening and closing balance from the running balances,
// in file order (oldest first) or reverse (newest first): whichever way the
// balances chain. Without a complete, consistent chain it returns nil.
func balances(lines []sdk.StatementLine) (opening, closing *int64) {
	if len(lines) == 0 {
		return nil, nil
	}
	for _, l := range lines {
		if l.Balance == nil {
			return nil, nil
		}
	}
	asc, desc := true, true
	for i := 1; i < len(lines); i++ {
		if *lines[i-1].Balance+lines[i].Amount != *lines[i].Balance {
			asc = false
		}
		if *lines[i].Balance+lines[i-1].Amount != *lines[i-1].Balance {
			desc = false
		}
	}
	switch {
	case asc && desc && len(lines) > 1:
		// shortcut: payments that cancel out chain both ways, the order is unknowable without the dates; compare the dates if this matters
		return nil, nil
	case asc:
		o, c := *lines[0].Balance-lines[0].Amount, *lines[len(lines)-1].Balance
		return &o, &c
	case desc:
		last := lines[len(lines)-1]
		o, c := *last.Balance-last.Amount, *lines[0].Balance
		return &o, &c
	}
	return nil, nil
}

func parseBankDate(s string, layouts []string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("the date is empty")
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	// a workbook stores dates as day serials; raw cell values show them as numbers
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 20000 && f < 90000 {
		if t, err := excelize.ExcelDateToTime(f, false); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("%q is not a date", s)
}

var thousands = regexp.MustCompile(`^\d{1,3}(\.\d{3})+$`)

// parseMoney reads "1.250,00", "-30,5", "1250.5" (a workbook number), "1.250"
// (thousands), "30,00 TL" and "(30,00)" into minor units.
func parseMoney(s string) (int64, error) {
	orig := s
	s = strings.NewReplacer("TL", "", "TRY", "", "₺", "", " ", "", " ", "").Replace(strings.TrimSpace(s))
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = !neg, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasSuffix(s, "-"):
		neg, s = !neg, s[:len(s)-1]
	}
	var whole, frac string
	switch {
	case strings.Contains(s, ","):
		w, f, _ := strings.Cut(s, ",")
		whole, frac = strings.ReplaceAll(w, ".", ""), f
	case thousands.MatchString(s):
		whole = strings.ReplaceAll(s, ".", "")
	default:
		whole, frac, _ = strings.Cut(s, ".")
	}
	if len(whole) > 15 { // no account holds that much, and whole*100 must not overflow
		return 0, fmt.Errorf("%q is not an amount", orig)
	}
	if whole == "" && frac == "" {
		return 0, fmt.Errorf("%q is not an amount", orig)
	}
	if whole == "" {
		whole = "0"
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%q is not an amount", orig)
		}
	}
	if len(frac) > 2 {
		if strings.Trim(frac[2:], "0") != "" {
			return 0, fmt.Errorf("%q is not an amount in kurus", orig)
		}
		frac = frac[:2]
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an amount", orig)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("%q is not an amount", orig)
	}
	v := w*100 + f
	if neg {
		v = -v
	}
	return v, nil
}
