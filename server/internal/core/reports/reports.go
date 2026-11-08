package reports

import (
	"bytes"
	"context"
	"encoding/csv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// remainingSQL is the open amount of every live charge (amount less applied payments).
const remainingSQL = `SELECT c.id, c.unit_id, c.debtor_kind, c.debtor_person_id, c.kind, c.charge_type, c.due_date, c.amount - COALESCE(a.paid, 0) AS rem
	FROM charge c LEFT JOIN (SELECT charge_id, SUM(amount) AS paid FROM payment_allocation WHERE org_id = $1 AND NOT voided GROUP BY charge_id) a ON a.charge_id = c.id
	WHERE c.org_id = $1 AND c.site_id = $2 AND c.cancel_entry_id IS NULL`

// DebtorRow is a debtor with an open balance.
type DebtorRow struct {
	UnitID      *uuid.UUID  `json:"unit_id"`
	DoorNumber  *string     `json:"door_number"`
	DebtorKind  string      `json:"debtor_kind"`
	PersonID    *uuid.UUID  `json:"person_id"`
	PersonName  *string     `json:"person_display_name"`
	Principal   money.Money `json:"principal"`
	Fee         money.Money `json:"fee"`
	Total       money.Money `json:"total"`
	OldestDue   *string     `json:"oldest_due_date"`
	DaysOverdue int         `json:"days_overdue"`
}

// DebtorReport lists everyone with an open balance as of a day, largest first
// (reports.debtors; never public). Late fee accrued but not yet booked is not
// included: the hourly job books it.
func (s *Service) DebtorReport(ctx context.Context, orgID, siteID uuid.UUID, asOf string, minTotal int64) ([]DebtorRow, error) {
	out := []DebtorRow{}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermReportsDebtors, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		today := ledger.Today(site.Timezone, s.Now())
		if asOf == "" {
			asOf = today
		}
		rows, err := tx.Query(ctx, `
			WITH r AS (`+remainingSQL+`)
			SELECT CASE WHEN r.debtor_kind = 'person' THEN NULL ELSE r.unit_id END, CASE WHEN r.debtor_kind = 'person' THEN NULL ELSE u.door_number END, r.debtor_kind,
			       CASE WHEN r.debtor_kind = 'person' THEN r.debtor_person_id END, CASE WHEN r.debtor_kind = 'person' THEN p.display_name END,
			       COALESCE(SUM(r.rem) FILTER (WHERE r.kind = 'principal'), 0)::bigint, COALESCE(SUM(r.rem) FILTER (WHERE r.kind = 'fee'), 0)::bigint,
			       MIN(r.due_date) FILTER (WHERE r.rem > 0 AND r.kind = 'principal')::text,
			       COALESCE($3::date - MIN(r.due_date) FILTER (WHERE r.rem > 0 AND r.kind = 'principal'), 0)
			FROM r JOIN unit u ON u.org_id = $1 AND u.id = r.unit_id LEFT JOIN person p ON p.org_id = $1 AND p.id = r.debtor_person_id
			GROUP BY 1, 2, 3, 4, 5
			HAVING COALESCE(SUM(r.rem), 0) > $4
			ORDER BY COALESCE(SUM(r.rem), 0) DESC, 2`, orgID, siteID, asOf, minTotal)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r DebtorRow
			var pr, fe int64
			var days int32
			if err := rows.Scan(&r.UnitID, &r.DoorNumber, &r.DebtorKind, &r.PersonID, &r.PersonName, &pr, &fe, &r.OldestDue, &days); err != nil {
				return err
			}
			r.Principal, r.Fee, r.Total = money.New(pr, cur), money.New(fe, cur), money.New(pr+fe, cur)
			r.DaysOverdue = int(max(days, 0))
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// DebtorCSV renders the debtor list.
func DebtorCSV(rows []DebtorRow) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"unit", "debtor_kind", "person", "principal", "fee", "total", "oldest_due_date", "days_overdue"})
	for _, r := range rows {
		door, person, due := "", "", ""
		if r.DoorNumber != nil {
			door = *r.DoorNumber
		}
		if r.PersonName != nil {
			person = *r.PersonName
		}
		if r.OldestDue != nil {
			due = *r.OldestDue
		}
		_ = w.Write([]string{door, r.DebtorKind, person, fmtMoney(r.Principal), fmtMoney(r.Fee), fmtMoney(r.Total), due, itoa(r.DaysOverdue)})
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func itoa(n int) string { return fmtMoney(money.New(int64(n), "JPY")) }

// AgingRow is one bucket of the receivables aging.
type AgingRow struct {
	Bucket string      `json:"bucket"`
	Amount money.Money `json:"amount"`
	Count  int         `json:"count"`
}

// ReceivablesAging buckets the open amounts by days past due as of a day
// (reports.read): not yet due, 1-30, 31-60, 61-90 and over 90 days.
func (s *Service) ReceivablesAging(ctx context.Context, orgID, siteID uuid.UUID, asOf string) ([]AgingRow, error) {
	out := []AgingRow{}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermReportsRead, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		if asOf == "" {
			asOf = ledger.Today(site.Timezone, s.Now())
		}
		rows, err := tx.Query(ctx, `
			WITH r AS (`+remainingSQL+`), b AS (
			  SELECT CASE WHEN $3::date <= due_date THEN 0 WHEN $3::date - due_date <= 30 THEN 1 WHEN $3::date - due_date <= 60 THEN 2 WHEN $3::date - due_date <= 90 THEN 3 ELSE 4 END AS k, rem FROM r WHERE rem <> 0)
			SELECT k, SUM(rem)::bigint, count(*) FROM b GROUP BY k ORDER BY k`, orgID, siteID, asOf)
		if err != nil {
			return err
		}
		defer rows.Close()
		names := []string{"current", "1-30", "31-60", "61-90", "90+"}
		got := map[int]AgingRow{}
		for rows.Next() {
			var k, n int
			var amt int64
			if err := rows.Scan(&k, &amt, &n); err != nil {
				return err
			}
			got[k] = AgingRow{Bucket: names[k], Amount: money.New(amt, cur), Count: n}
		}
		for k, name := range names {
			if r, ok := got[k]; ok {
				out = append(out, r)
			} else {
				out = append(out, AgingRow{Bucket: name, Amount: money.Zero(cur)})
			}
		}
		return rows.Err()
	})
	return out, err
}

// TrialRow is one account of the trial balance.
type TrialRow struct {
	AccountID uuid.UUID   `json:"account_id"`
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Debit     money.Money `json:"debit"`
	Credit    money.Money `json:"credit"`
	Balance   money.Money `json:"balance"`
}

// TrialBalance lists every account with its debit and credit totals up to a day (reports.read).
func (s *Service) TrialBalance(ctx context.Context, orgID, siteID uuid.UUID, asOf string) ([]TrialRow, error) {
	out := []TrialRow{}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermReportsRead, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		rows, err := tx.Query(ctx, `SELECT a.id, a.code, a.name, a.type, COALESCE(SUM(l.debit), 0)::bigint, COALESCE(SUM(l.credit), 0)::bigint
			FROM account a LEFT JOIN journal_line l ON l.org_id = a.org_id AND l.account_id = a.id AND ($3 = '' OR l.entry_date <= $3::date)
			WHERE a.org_id = $1 AND a.site_id = $2 GROUP BY a.id, a.code, a.name, a.type HAVING COALESCE(SUM(l.debit), 0) <> 0 OR COALESCE(SUM(l.credit), 0) <> 0 ORDER BY a.code`, orgID, siteID, asOf)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r TrialRow
			var d, c int64
			if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.Type, &d, &c); err != nil {
				return err
			}
			r.Debit, r.Credit, r.Balance = money.New(d, cur), money.New(c, cur), money.New(d-c, cur)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// FundRow is a fund's position: what it holds in assets less what it owes.
type FundRow struct {
	FundID            uuid.UUID   `json:"fund_id"`
	Code              string      `json:"code"`
	Name              string      `json:"name"`
	Assets            money.Money `json:"assets"`
	Liabilities       money.Money `json:"liabilities"`
	Net               money.Money `json:"net"`
	IncomeLessExpense money.Money `json:"income_less_expense"`
}

// FundBalances reports every fund's assets, liabilities and result to a day (reports.read).
func (s *Service) FundBalances(ctx context.Context, orgID, siteID uuid.UUID, asOf string) ([]FundRow, error) {
	out := []FundRow{}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermReportsRead, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		if _, err := ledger.EnsureChart(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		cur := ledger.Currency(site)
		rows, err := tx.Query(ctx, `SELECT f.id, f.code, f.name,
			COALESCE(SUM(l.debit - l.credit) FILTER (WHERE a.type = 'asset'), 0)::bigint,
			COALESCE(SUM(l.credit - l.debit) FILTER (WHERE a.type = 'liability'), 0)::bigint,
			COALESCE(SUM(l.credit - l.debit) FILTER (WHERE a.type IN ('income', 'expense')), 0)::bigint
			FROM fund f LEFT JOIN journal_line l ON l.org_id = f.org_id AND l.fund_id = f.id AND ($3 = '' OR l.entry_date <= $3::date)
			LEFT JOIN account a ON a.org_id = l.org_id AND a.id = l.account_id
			WHERE f.org_id = $1 AND f.site_id = $2 GROUP BY f.id, f.code, f.name ORDER BY f.code`, orgID, siteID, asOf)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r FundRow
			var as, li, res int64
			if err := rows.Scan(&r.FundID, &r.Code, &r.Name, &as, &li, &res); err != nil {
				return err
			}
			r.Assets, r.Liabilities, r.Net, r.IncomeLessExpense = money.New(as, cur), money.New(li, cur), money.New(as-li, cur), money.New(res, cur)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// IncomeLine is a cash-basis total.
type IncomeLine struct {
	Category string      `json:"category"`
	Amount   money.Money `json:"amount"`
}

// IncomeExpense is the cash-basis gelir-gider report for a range.
type IncomeExpense struct {
	From, To     string
	Income       []IncomeLine `json:"income"`
	Expenses     []IncomeLine `json:"expenses"`
	TotalIncome  money.Money  `json:"total_income"`
	TotalExpense money.Money  `json:"total_expense"`
	Net          money.Money  `json:"net"`
}

// IncomeExpenseReport sums the money received (payments applied by charge type,
// by the day they were applied) and the expenses booked in the range
// (reports.read, or reports.aggregate for owners when the site is transparent).
func (s *Service) IncomeExpenseReport(ctx context.Context, orgID, siteID uuid.UUID, from, to string) (IncomeExpense, error) {
	rep := IncomeExpense{From: from, To: to, Income: []IncomeLine{}, Expenses: []IncomeLine{}}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if !ledger.HasSite(ctx, tx, ledger.PermReportsRead, siteID) {
			if err := ledger.RequireSite(ctx, tx, ledger.PermReportsAggregate, siteID); err != nil {
				return err
			}
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		rep.TotalIncome, rep.TotalExpense = money.Zero(cur), money.Zero(cur)
		rows, err := tx.Query(ctx, `SELECT c.charge_type, SUM(a.amount)::bigint FROM payment_allocation a JOIN charge c ON c.org_id=a.org_id AND c.id=a.charge_id
			WHERE a.org_id=$1 AND a.site_id=$2 AND NOT a.voided AND ($3 = '' OR a.applied_on >= $3::date) AND ($4 = '' OR a.applied_on <= $4::date) GROUP BY 1 ORDER BY 1`, orgID, siteID, from, to)
		if err != nil {
			return err
		}
		for rows.Next() {
			var cat string
			var amt int64
			if err := rows.Scan(&cat, &amt); err != nil {
				rows.Close()
				return err
			}
			rep.Income = append(rep.Income, IncomeLine{Category: cat, Amount: money.New(amt, cur)})
			rep.TotalIncome.Amount += amt
		}
		rows.Close()
		erows, err := tx.Query(ctx, `SELECT a.name, SUM(l.debit - l.credit)::bigint FROM journal_line l JOIN account a ON a.org_id=l.org_id AND a.id=l.account_id
			WHERE l.org_id=$1 AND l.site_id=$2 AND a.type='expense' AND ($3 = '' OR l.entry_date >= $3::date) AND ($4 = '' OR l.entry_date <= $4::date) GROUP BY 1 HAVING SUM(l.debit - l.credit) <> 0 ORDER BY 1`, orgID, siteID, from, to)
		if err != nil {
			return err
		}
		defer erows.Close()
		for erows.Next() {
			var cat string
			var amt int64
			if err := erows.Scan(&cat, &amt); err != nil {
				return err
			}
			rep.Expenses = append(rep.Expenses, IncomeLine{Category: cat, Amount: money.New(amt, cur)})
			rep.TotalExpense.Amount += amt
		}
		rep.Net = money.New(rep.TotalIncome.Amount-rep.TotalExpense.Amount, cur)
		return erows.Err()
	})
	return rep, err
}
