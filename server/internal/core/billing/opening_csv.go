package billing

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// OpeningColumns are the columns of the opening balance CSV, in the order of the
// documented layout: `kind,unit,side,principal,late_fee,account_id`.
var OpeningColumns = []string{"kind", "unit", "side", "principal", "late_fee", "account_id"}

// MaxReportedRows caps the rows an import report lists.
const MaxReportedRows = 200

// CSVRowError is one problem of an import row.
type CSVRowError struct {
	Field   *string `json:"field"`
	Code    string  `json:"code"`
	Message *string `json:"message"`
	Params  any     `json:"params"`
}

// CSVRowResult is the outcome of one import row.
type CSVRowResult struct {
	Row    int            `json:"row"`
	Status string         `json:"status"`
	Errors []CSVRowError  `json:"errors"`
	Data   map[string]any `json:"data"`
}

// CSVImportReport is the answer of an opening balance import (the draft's ImportReport).
type CSVImportReport struct {
	DryRun        bool           `json:"dry_run"`
	TotalRows     int            `json:"total_rows"`
	OKRows        int            `json:"ok_rows"`
	WarningRows   int            `json:"warning_rows"`
	ErrorRows     int            `json:"error_rows"`
	Rows          []CSVRowResult `json:"rows"`
	RowsTruncated bool           `json:"rows_truncated"`
	JobID         *uuid.UUID     `json:"job_id"`
}

// ParseMinor reads a decimal amount in major units ("1.234,56", "1,234.56", "1234.5") as minor units.
func ParseMinor(s string) (int64, bool) {
	s = strings.TrimSpace(strings.NewReplacer(" ", "", " ", "", "₺", "", "TL", "", "TRY", "").Replace(s))
	if s == "" {
		return 0, false
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	dec := ""
	whole := s
	li, lc := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	cut := max(li, lc)
	if cut >= 0 {
		frac := s[cut+1:]
		// a single separator followed by three digits is a thousands separator
		thousands := (li < 0 || lc < 0) && len(frac) == 3 && strings.Count(s, s[cut:cut+1]) >= 1 && cut > 0
		if !thousands {
			whole, dec = s[:cut], frac
		}
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	if len(dec) > 2 || whole == "" && dec == "" {
		return 0, false
	}
	for len(dec) < 2 {
		dec += "0"
	}
	var v int64
	for _, c := range whole + dec {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int64(c-'0')
		if v > 1<<50 {
			return 0, false
		}
	}
	if neg {
		v = -v
	}
	return v, true
}

func sniffDelimiter(head []byte) rune {
	line := head
	if i := bytes.IndexByte(head, '\n'); i >= 0 {
		line = head[:i]
	}
	best, n := ',', bytes.Count(line, []byte(","))
	for _, d := range []rune{';', '\t'} {
		if c := bytes.Count(line, []byte(string(d))); c > n {
			best, n = d, c
		}
	}
	return best
}

// ImportOpeningCSV imports opening balances from a CSV of the layout
// `kind,unit,side,principal,late_fee,account_id` (unit is a door number; side owner or
// tenant; amounts in major units; account_id for bank and cash rows; mapping names the
// column of each field when the header differs). A dry run validates and reports; a real
// run (re-authentication) posts every row in one transaction, or nothing when a row has an
// error. The work is synchronous, so job_id is null.
func (s *Service) ImportOpeningCSV(ctx context.Context, orgID, siteID uuid.UUID, asOf string, data []byte, mapping map[string]string, dryRun bool) (CSVImportReport, error) {
	rep := CSVImportReport{DryRun: dryRun, Rows: []CSVRowResult{}}
	if _, err := ledger.ParseDate(asOf); err != nil {
		return rep, bad("as_of", "invalid_format")
	}
	if !dryRun {
		if err := authz.PrincipalFromContext(ctx).RequireReauth(s.Now()); err != nil {
			return rep, err
		}
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = sniffDelimiter(data)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return rep, bad("file", "empty_or_unreadable")
	}
	col := map[string]int{}
	for _, name := range OpeningColumns {
		want := strings.ToLower(name)
		if m := mapping[name]; m != "" {
			want = strings.ToLower(m)
		}
		col[name] = -1
		for i, h := range header {
			if strings.ToLower(strings.TrimSpace(h)) == want {
				col[name] = i
			}
		}
	}
	for _, req := range []string{"principal"} {
		if col[req] < 0 {
			return rep, problem.Validation(problem.Field{Field: "file", Code: "missing_column", Params: map[string]any{"column": req}})
		}
	}
	err = s.run(ctx, orgID, "opening balance", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, siteID); err != nil {
			return err
		}
		type parsed struct {
			row int
			in  OpeningInput
		}
		var good []parsed
		for rowNo := 2; ; rowNo++ {
			rec, err := r.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return bad("file", "unreadable_row")
			}
			get := func(name string) string {
				if i := col[name]; i >= 0 && i < len(rec) {
					return strings.TrimSpace(rec[i])
				}
				return ""
			}
			empty := true
			for _, c := range rec {
				if strings.TrimSpace(c) != "" {
					empty = false
				}
			}
			if empty {
				continue
			}
			rep.TotalRows++
			res := CSVRowResult{Row: rowNo, Status: "ok", Errors: []CSVRowError{}, Data: map[string]any{}}
			for _, name := range OpeningColumns {
				if v := get(name); v != "" {
					res.Data[name] = v
				}
			}
			fail := func(field, code string) {
				f := field
				res.Status = "error"
				res.Errors = append(res.Errors, CSVRowError{Field: &f, Code: code})
			}
			in := OpeningInput{SiteID: siteID, AsOf: asOf, Kind: strings.ToLower(get("kind"))}
			if in.Kind == "" {
				in.Kind = "unit_receivable"
			}
			switch in.Kind {
			case "unit_receivable":
				door := get("unit")
				if door == "" {
					fail("unit", "required")
				} else {
					var uid uuid.UUID
					if err := tx.QueryRow(ctx, `SELECT id FROM unit WHERE org_id=$1 AND site_id=$2 AND door_number=$3`, orgID, siteID, door).Scan(&uid); err != nil {
						fail("unit", "unknown_unit")
					} else {
						in.UnitID = &uid
					}
				}
				in.DebtorKind = strings.ToLower(get("side"))
				if in.DebtorKind == "" {
					in.DebtorKind = "owner"
				}
				if in.DebtorKind != "owner" && in.DebtorKind != "tenant" {
					fail("side", "invalid_value")
				}
			case "bank", "cash":
				a, err := uuid.Parse(get("account_id"))
				if err != nil {
					fail("account_id", "required")
				} else {
					in.AccountID = &a
				}
			default:
				fail("kind", "invalid_value")
			}
			if v, ok := ParseMinor(get("principal")); !ok {
				fail("principal", "invalid_amount")
			} else {
				in.Principal = v
			}
			if lf := get("late_fee"); lf != "" {
				if v, ok := ParseMinor(lf); !ok || v < 0 {
					fail("late_fee", "invalid_amount")
				} else {
					in.LateFee = v
				}
			}
			if res.Status == "ok" {
				if err := in.validate(); err != nil {
					fail("principal", "invalid_value")
				} else if _, err := s.postOpening(ctx, tx, orgID, in, nil, true); err != nil {
					var pe *problem.Error
					if errors.As(err, &pe) && pe.Status == 409 {
						fail("as_of", "locked_period")
					} else if errors.As(err, &pe) && pe.Status == 422 {
						fail("account_id", "invalid_value")
					} else {
						return err
					}
				}
			}
			if res.Status == "ok" {
				rep.OKRows++
				good = append(good, parsed{rowNo, in})
			} else {
				rep.ErrorRows++
			}
			if len(rep.Rows) < MaxReportedRows {
				rep.Rows = append(rep.Rows, res)
			} else {
				rep.RowsTruncated = true
			}
		}
		if dryRun || rep.ErrorRows > 0 {
			return nil
		}
		for _, g := range good {
			if _, err := s.postOpening(ctx, tx, orgID, g.in, nil, false); err != nil {
				return err
			}
		}
		return ledger.Audit(ctx, tx, orgID, siteID, "opening_balance.import", "opening_balance", siteID, nil, map[string]any{"rows": len(good), "as_of": asOf})
	})
	return rep, err
}
