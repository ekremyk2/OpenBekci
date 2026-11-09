// SPDX-License-Identifier: AGPL-3.0-or-later

package example

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

// importer reads the tiny "example bank" CSV export:
//
//	example-bank-v1
//	date;amount;counterparty;description;reference;balance
//	03.02.2026;1.250,00;Ayse Yilmaz;Dues February;A-12-202602;10.250,00
//
// Dates are DD.MM.YYYY, amounts use the Turkish decimal comma, the balance
// column is optional. The first line identifies the format.
type importer struct{}

var _ sdk.StatementImporter = importer{}

const importerMagic = "example-bank-v1"

func (importer) ID() string        { return ID }
func (importer) Formats() []string { return []string{"csv"} }

func (importer) Detect(filename string, head []byte) bool {
	first, _, _ := bytes.Cut(head, []byte("\n"))
	return strings.HasSuffix(strings.ToLower(filename), ".csv") && strings.TrimSpace(string(first)) == importerMagic
}

func (importer) Parse(_ context.Context, _ string, data []byte) (sdk.Statement, error) {
	text := strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})) /* UTF-8 BOM */, "\r\n", "\n")
	rows := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(rows) < 2 || strings.TrimSpace(rows[0]) != importerMagic {
		return sdk.Statement{}, fmt.Errorf("not an example-bank statement: %w", sdk.ErrRejected)
	}
	head := strings.Split(rows[1], ";")
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{"date", "amount"} {
		if _, ok := col[need]; !ok {
			return sdk.Statement{}, fmt.Errorf("column %q missing: %w", need, sdk.ErrRejected)
		}
	}
	st := sdk.Statement{Currency: "TRY"}
	for i, row := range rows[2:] {
		lineNo := i + 1
		if strings.TrimSpace(row) == "" {
			continue
		}
		cells := strings.Split(row, ";")
		get := func(name string) string {
			if j, ok := col[name]; ok && j < len(cells) {
				return strings.TrimSpace(cells[j])
			}
			return ""
		}
		date, err := parseDate(get("date"))
		if err != nil {
			st.Errors = append(st.Errors, sdk.RowError{Row: lineNo, Field: "date", Reason: err.Error()})
			continue
		}
		amount, err := parseAmount(get("amount"))
		if err != nil {
			st.Errors = append(st.Errors, sdk.RowError{Row: lineNo, Field: "amount", Reason: err.Error()})
			continue
		}
		l := sdk.StatementLine{LineNo: lineNo, BookedOn: date, Amount: amount, Counterparty: get("counterparty"), Description: get("description"), Reference: get("reference"),
			Raw: map[string]string{}}
		for name, j := range col {
			if j < len(cells) {
				l.Raw[name] = strings.TrimSpace(cells[j])
			}
		}
		if b := get("balance"); b != "" {
			if v, err := parseAmount(b); err == nil {
				l.Balance = &v
			}
		}
		st.Lines = append(st.Lines, l)
	}
	if n := len(st.Lines); n > 0 && st.Lines[0].Balance != nil && st.Lines[n-1].Balance != nil {
		open := *st.Lines[0].Balance - st.Lines[0].Amount
		closing := *st.Lines[n-1].Balance
		st.OpeningBalance, st.ClosingBalance = &open, &closing
	}
	return st, nil
}

func parseDate(s string) (string, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || len(parts[0]) != 2 || len(parts[1]) != 2 || len(parts[2]) != 4 {
		return "", fmt.Errorf("%q is not DD.MM.YYYY", s)
	}
	return parts[2] + "-" + parts[1] + "-" + parts[0], nil
}

// parseAmount reads "1.250,00" and "-30,5" into minor units.
func parseAmount(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ".", "")
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ",")
	if len(frac) > 2 || whole == "" {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an amount", s)
	}
	v := w*100 + f
	if neg {
		v = -v
	}
	return v, nil
}
