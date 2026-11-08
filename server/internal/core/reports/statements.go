// Package reports is the statements and reports module (M8, backend plan 9.12):
// statements per unit side or person with running balance and principal/fee
// split (JSON, CSV, PDF), the debtor list, trial balance, fund balances,
// receivables aging and the cash-basis income and expense report.
package reports

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/statement"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/pdf"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Service is the reports module.
type Service struct {
	db  ledger.TxRunner
	Now func() time.Time
}

// NewService returns a Service; now nil means time.Now.
func NewService(db ledger.TxRunner, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, Now: now}
}

func (s *Service) run(ctx context.Context, orgID uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.db.InTenantTx(ctx, orgID, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return ledger.DBProblem(err, what)
}

// Balance is a principal/fee split.
type Balance struct {
	Principal money.Money `json:"principal"`
	Fee       money.Money `json:"fee"`
	Total     money.Money `json:"total"`
}

// Row is one statement line with the running balance after it.
type Row struct {
	Date    string      `json:"date"`
	Kind    string      `json:"kind"`
	Part    string      `json:"part"`
	Ref     string      `json:"ref"`
	Amount  money.Money `json:"amount"`
	Balance Balance     `json:"balance"`
}

// Statement is a debtor's statement for a range.
type Statement struct {
	Debtor     billing.Debtor `json:"debtor"`
	Label      string         `json:"label"`
	From       *string        `json:"from"`
	To         *string        `json:"to"`
	AsOf       string         `json:"as_of"`
	Currency   money.Code     `json:"currency"`
	Opening    Balance        `json:"opening"`
	Closing    Balance        `json:"closing"`
	Rows       []Row          `json:"rows"`
	Credit     money.Money    `json:"credit"`
	AccruedFee money.Money    `json:"accrued_fee"`
	SiteName   string         `json:"-"`
}

func bal(b statement.Balance) Balance {
	return Balance{Principal: b.Principal, Fee: b.Fee, Total: b.Total}
}

// DebtorStatement builds the statement of a unit side or a person for a range
// (statements.read; a resident reads their own side, a former occupant their
// period, S3 and S7). The accrued but not yet booked late fee is always the last line (M4).
func (s *Service) DebtorStatement(ctx context.Context, orgID, siteID uuid.UUID, d billing.Debtor, from, to string) (Statement, error) {
	if err := d.Validate(); err != nil {
		return Statement{}, err
	}
	var out Statement
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		tz := ledger.Location(site.Timezone)
		today := ledger.Today(site.Timezone, s.Now())
		manager := ledger.HasSite(ctx, tx, ledger.PermStatementsRead, siteID)
		if !manager && !d.IsPerson() && d.UnitID != nil {
			// a resident sees only what is dated inside their own period on the unit
			if wf, wt, ok := authz.MoneyWindow(ctx, *d.UnitID, authz.UnitSide(d.Kind)); ok {
				if f := wf.Format(ledger.DateLayout); from == "" || from < f {
					from = f
				}
				if wt != nil {
					if last := wt.AddDate(0, 0, -1).Format(ledger.DateLayout); to == "" || to > last {
						to = last
					}
				}
			}
		}
		asOf := today
		if to != "" && to < today {
			asOf = to
		}
		if !manager {
			if d.IsPerson() || d.UnitID == nil {
				return problem.Forbidden("you do not have the statements.read permission here")
			}
			day, _ := ledger.ParseDate(asOf)
			if authz.CanAccessUnit(ctx, *d.UnitID, authz.UnitSide(d.Kind)) != nil && authz.CanReadUnitAt(ctx, *d.UnitID, authz.UnitSide(d.Kind), day) != nil {
				return problem.Forbidden("you do not have access to this unit's statement")
			}
		}
		cur := ledger.Currency(site)
		ch, err := ledger.EnsureChart(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		lines, err := statementLines(ctx, tx, orgID, siteID, d, cur, tz)
		if err != nil {
			return err
		}
		accrued := money.Zero(cur)
		if !d.IsPerson() {
			acc, err := billing.AccrueTx(ctx, tx, orgID, site, d, asOf)
			if err != nil {
				return err
			}
			if acc.Unbooked.Amount > 0 {
				at, _ := time.ParseInLocation(ledger.DateLayout, asOf, tz)
				lines = append(lines, statement.AccruedFeeLine(at, statement.DebtorKind(d.Kind), acc.Unbooked))
				accrued = acc.Unbooked
			}
		}
		var r statement.Range
		out = Statement{Debtor: d, AsOf: asOf, Currency: cur, From: strPtr(from), To: strPtr(to), Credit: money.Zero(cur), AccruedFee: accrued, Rows: []Row{}}
		if from != "" {
			t, err := time.ParseInLocation(ledger.DateLayout, from, tz)
			if err != nil {
				return problem.Validation(problem.Field{Field: "from", Code: "invalid_format"})
			}
			r.From = &t
		}
		if to != "" {
			t, err := time.ParseInLocation(ledger.DateLayout, to, tz)
			if err != nil {
				return problem.Validation(problem.Field{Field: "to", Code: "invalid_format"})
			}
			r.To = &t
		}
		st, err := statement.Build(cur, lines, r, tz)
		if err != nil {
			return problem.Wrap(422, problem.CodeValidationFailed, "the statement range is invalid", err)
		}
		out.Opening, out.Closing = bal(st.Opening), bal(st.Closing)
		for _, row := range st.Rows {
			out.Rows = append(out.Rows, Row{Date: row.Line.Date.In(tz).Format(ledger.DateLayout), Kind: string(row.Line.Kind), Part: string(row.Line.Part), Ref: row.Line.Ref, Amount: row.Line.Amount, Balance: bal(row.Balance)})
		}
		if cr, err := billing.CreditOf(ctx, tx, orgID, siteID, ch, d); err == nil {
			out.Credit = money.New(cr, cur)
		}
		out.SiteName = site.Name
		if d.IsPerson() {
			_ = tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id=$1 AND id=$2`, orgID, *d.PersonID).Scan(&out.Label)
		} else {
			var door string
			_ = tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id=$1 AND id=$2`, orgID, *d.UnitID).Scan(&door)
			out.Label = door + " / " + d.Kind
		}
		return nil
	})
	return out, err
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// statementLines turns the debtor's charges and applied payments into statement lines.
func statementLines(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d billing.Debtor, cur money.Code, tz *time.Location) ([]statement.Line, error) {
	where := `c.unit_id = $3 AND c.debtor_kind = $4`
	args := []any{orgID, siteID}
	if d.IsPerson() {
		where = `c.debtor_kind = 'person' AND c.debtor_person_id = $3`
		args = append(args, *d.PersonID)
	} else {
		args = append(args, *d.UnitID, d.Kind)
	}
	kind := statement.Owner
	if d.Kind == "tenant" {
		kind = statement.Tenant
	}
	rows, err := tx.Query(ctx, `SELECT c.due_date::text, c.kind, c.amount, c.period, c.charge_type, COALESCE(c.debtor_person_id, '00000000-0000-0000-0000-000000000000'::uuid), c.description
		FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND `+where+` AND c.cancel_entry_id IS NULL ORDER BY c.due_date, c.id`, args...)
	if err != nil {
		return nil, err
	}
	var lines []statement.Line
	for rows.Next() {
		var due, k, period, ctype, desc string
		var amt int64
		var person uuid.UUID
		if err := rows.Scan(&due, &k, &amt, &period, &ctype, &person, &desc); err != nil {
			rows.Close()
			return nil, err
		}
		at, _ := time.ParseInLocation(ledger.DateLayout, due, tz)
		l := statement.Line{Date: at, DebtorKind: kind, PersonID: person, Amount: money.New(amt, cur), Ref: desc}
		switch {
		case period == "opening" && k == "fee":
			l.Kind, l.Part = statement.KindOpening, statement.PartFee
		case period == "opening":
			l.Kind, l.Part = statement.KindOpening, statement.PartPrincipal
		case k == "fee" && amt < 0:
			l.Kind, l.Part = statement.KindWaiver, statement.PartFee
		case k == "fee":
			l.Kind, l.Part = statement.KindFee, statement.PartFee
		default:
			l.Kind, l.Part = statement.KindCharge, statement.PartPrincipal
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	arows, err := tx.Query(ctx, `SELECT a.applied_on::text, a.amount, c.kind, COALESCE(p.receipt_no, 'credit') FROM payment_allocation a
		JOIN charge c ON c.org_id = a.org_id AND c.id = a.charge_id LEFT JOIN payment p ON p.org_id = a.org_id AND p.id = a.payment_id
		WHERE a.org_id=$1 AND a.site_id=$2 AND `+where+` AND NOT a.voided ORDER BY a.applied_on, a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var on, k, ref string
		var amt int64
		if err := arows.Scan(&on, &amt, &k, &ref); err != nil {
			return nil, err
		}
		at, _ := time.ParseInLocation(ledger.DateLayout, on, tz)
		part := statement.PartPrincipal
		if k == "fee" {
			part = statement.PartFee
		}
		lines = append(lines, statement.Line{Date: at, Kind: statement.KindPayment, DebtorKind: kind, Amount: money.New(-amt, cur), Part: part, Ref: ref})
	}
	return lines, arows.Err()
}

func fmtMoney(m money.Money) string {
	exp, _ := m.Currency.Exponent()
	neg := m.Amount < 0
	a := m.Amount
	if neg {
		a = -a
	}
	s := strconv.FormatInt(a, 10)
	if exp > 0 {
		for len(s) <= exp {
			s = "0" + s
		}
		s = s[:len(s)-exp] + "," + s[len(s)-exp:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// CSV renders the statement as CSV (date, kind, part, ref, amount, running balance split).
func (st Statement) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"date", "kind", "part", "ref", "amount", "balance_principal", "balance_fee", "balance_total"})
	_ = w.Write([]string{"", "opening", "", "", "", fmtMoney(st.Opening.Principal), fmtMoney(st.Opening.Fee), fmtMoney(st.Opening.Total)})
	for _, r := range st.Rows {
		_ = w.Write([]string{r.Date, r.Kind, r.Part, CSVText(r.Ref), fmtMoney(r.Amount), fmtMoney(r.Balance.Principal), fmtMoney(r.Balance.Fee), fmtMoney(r.Balance.Total)})
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

// PDF renders the statement (Hesap Ekstresi) with the running balance and the principal/fee split.
func (st Statement) PDF() []byte {
	d := pdf.New("Statement " + st.Label)
	d.Line(st.SiteName, 14, true)
	d.Line("Hesap ekstresi / Account statement: "+st.Label, 11, true)
	rng := "until " + st.AsOf
	if st.From != nil {
		rng = *st.From + " - " + st.AsOf
	}
	d.Line("Period: "+rng+"   Currency: "+currencyLabel(st.Currency), 9, false)
	d.Skip(6)
	cols := []float64{50, 105, 165, 245, 400, 470, 545}
	head := func() {
		for i, h := range []string{"Date", "Kind", "Part", "Ref"} {
			d.Text(cols[i], h, 8, true)
		}
		d.Right(cols[4], "Amount", 8, true)
		d.Right(cols[5], "Fee bal.", 8, true)
		d.Right(cols[6], "Balance", 8, true)
		d.Skip(12)
		d.Rule()
		d.Skip(4)
	}
	head()
	d.Text(cols[0], "Opening balance", 8, false)
	d.Right(cols[5], fmtMoney(st.Opening.Fee), 8, false)
	d.Right(cols[6], fmtMoney(st.Opening.Total), 8, false)
	d.Skip(12)
	for _, r := range st.Rows {
		d.Text(cols[0], r.Date, 8, false)
		d.Text(cols[1], r.Kind, 8, false)
		d.Text(cols[2], r.Part, 8, false)
		ref := r.Ref
		if rs := []rune(ref); len(rs) > 28 {
			ref = string(rs[:28])
		}
		d.Text(cols[3], ref, 8, false)
		d.Right(cols[4], fmtMoney(r.Amount), 8, false)
		d.Right(cols[5], fmtMoney(r.Balance.Fee), 8, false)
		d.Right(cols[6], fmtMoney(r.Balance.Total), 8, false)
		d.Skip(12)
	}
	d.Skip(6)
	d.Rule()
	d.Skip(4)
	d.Line(fmt.Sprintf("Closing balance: %s  (principal %s, fee %s)", fmtMoney(st.Closing.Total), fmtMoney(st.Closing.Principal), fmtMoney(st.Closing.Fee)), 10, true)
	if st.AccruedFee.Amount > 0 {
		d.Line("The last line is the late fee accrued to "+st.AsOf+" and not yet booked.", 8, false)
	}
	if st.Credit.Amount > 0 {
		d.Line("Prepaid credit on account: "+fmtMoney(st.Credit), 9, false)
	}
	return d.Bytes()
}
