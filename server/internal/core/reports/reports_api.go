package reports

import (
	"bytes"
	"context"
	"encoding/csv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// AgingBucketAmount is an amount in one aging bucket.
type AgingBucketAmount struct {
	Bucket string      `json:"bucket"`
	Amount money.Money `json:"amount"`
}

// AgingUnitRow is the open amount of one unit side by days past due.
type AgingUnitRow struct {
	UnitID     uuid.UUID           `json:"unit_id"`
	DoorNumber string              `json:"unit_door_number"`
	DebtorKind string              `json:"debtor_kind"`
	Buckets    []AgingBucketAmount `json:"buckets"`
	Total      money.Money         `json:"total"`
}

// AgingReport is the receivables aging of the API: buckets, a row per unit side, totals per bucket.
type AgingReport struct {
	AsOf    string              `json:"as_of"`
	Buckets []string            `json:"buckets"`
	Rows    []AgingUnitRow      `json:"rows"`
	Totals  []AgingBucketAmount `json:"totals"`
}

// AgingBucketNames are the buckets by days past due.
var AgingBucketNames = []string{"current", "1-30", "31-60", "61-90", "90+"}

// AgingByUnit buckets the open amounts of every unit side by days past due as of a day
// (reports.read); person debtors count in the totals only.
func (s *Service) AgingByUnit(ctx context.Context, orgID, siteID uuid.UUID, asOf string, block *uuid.UUID) (AgingReport, error) {
	rep := AgingReport{Buckets: AgingBucketNames, Rows: []AgingUnitRow{}, Totals: []AgingBucketAmount{}}
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
		rep.AsOf = asOf
		rows, err := tx.Query(ctx, `
			WITH r AS (`+remainingSQL+`)
			SELECT r.unit_id, u.door_number, r.debtor_kind,
			  CASE WHEN $3::date <= r.due_date THEN 0 WHEN $3::date - r.due_date <= 30 THEN 1 WHEN $3::date - r.due_date <= 60 THEN 2 WHEN $3::date - r.due_date <= 90 THEN 3 ELSE 4 END AS k,
			  SUM(r.rem)::bigint
			FROM r JOIN unit u ON u.org_id = $1 AND u.id = r.unit_id
			WHERE r.rem <> 0 AND ($4::uuid IS NULL OR u.block_id = $4)
			GROUP BY r.unit_id, u.door_number, r.debtor_kind, k ORDER BY u.door_number, r.unit_id, r.debtor_kind, k`, orgID, siteID, asOf, block)
		if err != nil {
			return err
		}
		defer rows.Close()
		totals := make([]int64, len(AgingBucketNames))
		idx := map[string]int{}
		for rows.Next() {
			var unit uuid.UUID
			var door, kind string
			var k int
			var amt int64
			if err := rows.Scan(&unit, &door, &kind, &k, &amt); err != nil {
				return err
			}
			totals[k] += amt
			if kind == "person" {
				continue
			}
			key := unit.String() + kind
			i, ok := idx[key]
			if !ok {
				row := AgingUnitRow{UnitID: unit, DoorNumber: door, DebtorKind: kind, Total: money.Zero(cur)}
				for _, n := range AgingBucketNames {
					row.Buckets = append(row.Buckets, AgingBucketAmount{Bucket: n, Amount: money.Zero(cur)})
				}
				rep.Rows = append(rep.Rows, row)
				i = len(rep.Rows) - 1
				idx[key] = i
			}
			rep.Rows[i].Buckets[k].Amount.Amount += amt
			rep.Rows[i].Total.Amount += amt
		}
		for k, n := range AgingBucketNames {
			rep.Totals = append(rep.Totals, AgingBucketAmount{Bucket: n, Amount: money.New(totals[k], cur)})
		}
		return rows.Err()
	})
	return rep, err
}

// AgingCSV renders the aging report.
func (r AgingReport) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	head := []string{"unit", "debtor_kind"}
	head = append(head, AgingBucketNames...)
	head = append(head, "total")
	_ = w.Write(head)
	for _, row := range r.Rows {
		rec := []string{CSVText(row.DoorNumber), row.DebtorKind}
		for _, bk := range row.Buckets {
			rec = append(rec, fmtMoney(bk.Amount))
		}
		_ = w.Write(append(rec, fmtMoney(row.Total)))
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

// TrialRowOut is one account of the trial balance of the API.
type TrialRowOut struct {
	AccountID uuid.UUID   `json:"account_id"`
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Debit     money.Money `json:"debit"`
	Credit    money.Money `json:"credit"`
}

// TrialReport is the trial balance of the API.
type TrialReport struct {
	AsOf        string        `json:"as_of"`
	Rows        []TrialRowOut `json:"rows"`
	TotalDebit  money.Money   `json:"total_debit"`
	TotalCredit money.Money   `json:"total_credit"`
}

// TrialBalanceFor is the trial balance as of a day, optionally of one fund (reports.read): every
// account with a balance shows its net on the debit or the credit side.
func (s *Service) TrialBalanceFor(ctx context.Context, orgID, siteID uuid.UUID, asOf string, fund *uuid.UUID) (TrialReport, error) {
	rep := TrialReport{Rows: []TrialRowOut{}}
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
		rep.AsOf = asOf
		rep.TotalDebit, rep.TotalCredit = money.Zero(cur), money.Zero(cur)
		rows, err := tx.Query(ctx, `SELECT a.id, a.code, a.name, a.type, COALESCE(SUM(l.debit - l.credit), 0)::bigint
			FROM account a JOIN journal_line l ON l.org_id = a.org_id AND l.account_id = a.id AND l.entry_date <= $3::date AND ($4::uuid IS NULL OR l.fund_id = $4)
			WHERE a.org_id = $1 AND a.site_id = $2 GROUP BY a.id, a.code, a.name, a.type HAVING SUM(l.debit - l.credit) <> 0 ORDER BY a.code`, orgID, siteID, asOf, fund)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r TrialRowOut
			var net int64
			if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.Type, &net); err != nil {
				return err
			}
			r.Debit, r.Credit = money.Zero(cur), money.Zero(cur)
			if net > 0 {
				r.Debit.Amount = net
				rep.TotalDebit.Amount += net
			} else {
				r.Credit.Amount = -net
				rep.TotalCredit.Amount += -net
			}
			rep.Rows = append(rep.Rows, r)
		}
		return rows.Err()
	})
	return rep, err
}

// CSV renders the trial balance.
func (r TrialReport) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"code", "name", "type", "debit", "credit"})
	for _, row := range r.Rows {
		_ = w.Write([]string{row.Code, CSVText(row.Name), row.Type, fmtMoney(row.Debit), fmtMoney(row.Credit)})
	}
	_ = w.Write([]string{"", "total", "", fmtMoney(r.TotalDebit), fmtMoney(r.TotalCredit)})
	w.Flush()
	return b.Bytes(), w.Error()
}

// FundBalanceOut is one fund of the fund balances report of the API.
type FundBalanceOut struct {
	FundID  uuid.UUID   `json:"fund_id"`
	Name    string      `json:"name"`
	Kind    string      `json:"kind"`
	Balance money.Money `json:"balance"`
}

// FundBalancesReport is the fund balances report of the API.
type FundBalancesReport struct {
	AsOf  string           `json:"as_of"`
	Rows  []FundBalanceOut `json:"rows"`
	Total money.Money      `json:"total"`
}

// FundBalancesFor reports the net position (assets less liabilities) of every fund as of a day (reports.read).
func (s *Service) FundBalancesFor(ctx context.Context, orgID, siteID uuid.UUID, asOf string) (FundBalancesReport, error) {
	rep := FundBalancesReport{Rows: []FundBalanceOut{}}
	rows, err := s.FundBalances(ctx, orgID, siteID, asOf)
	if err != nil {
		return rep, err
	}
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		rep.Total = money.Zero(cur)
		rep.AsOf = asOf
		if rep.AsOf == "" {
			rep.AsOf = ledger.Today(site.Timezone, s.Now())
		}
		for _, f := range rows {
			var kind string
			if err := tx.QueryRow(ctx, `SELECT kind FROM fund WHERE org_id=$1 AND id=$2`, orgID, f.FundID).Scan(&kind); err != nil {
				return err
			}
			rep.Rows = append(rep.Rows, FundBalanceOut{FundID: f.FundID, Name: f.Name, Kind: kind, Balance: f.Net})
			rep.Total.Amount += f.Net.Amount
		}
		return nil
	})
	return rep, err
}

// CSV renders the fund balances.
func (r FundBalancesReport) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"fund", "kind", "balance"})
	for _, row := range r.Rows {
		_ = w.Write([]string{CSVText(row.Name), row.Kind, fmtMoney(row.Balance)})
	}
	_ = w.Write([]string{"total", "", fmtMoney(r.Total)})
	w.Flush()
	return b.Bytes(), w.Error()
}

// AmountRow is an account total of the income and expense report.
type AmountRow struct {
	AccountID uuid.UUID   `json:"account_id"`
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Amount    money.Money `json:"amount"`
}

// IncomeExpenseOut is the cash-basis income and expense report of the API.
type IncomeExpenseOut struct {
	From         string      `json:"from"`
	To           string      `json:"to"`
	Basis        string      `json:"basis"`
	Income       []AmountRow `json:"income"`
	Expense      []AmountRow `json:"expense"`
	TotalIncome  money.Money `json:"total_income"`
	TotalExpense money.Money `json:"total_expense"`
	Net          money.Money `json:"net"`
}

// IncomeExpenseByAccount is the cash-basis gelir-gider report (reports.read, or
// reports.aggregate for owners when the site is transparent): income is what was received (payments applied
// in the range) by the income account the charge was booked to; expense is what was spent
// (expense account postings in the range, net of voids). fund restricts both.
func (s *Service) IncomeExpenseByAccount(ctx context.Context, orgID, siteID uuid.UUID, from, to string, fund *uuid.UUID) (IncomeExpenseOut, error) {
	rep := IncomeExpenseOut{From: from, To: to, Basis: "cash", Income: []AmountRow{}, Expense: []AmountRow{}}
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
		rows, err := tx.Query(ctx, `SELECT a.id, a.code, a.name, SUM(al.amount)::bigint FROM payment_allocation al
			JOIN charge c ON c.org_id = al.org_id AND c.id = al.charge_id
			JOIN journal_line jl ON jl.org_id = c.org_id AND jl.entry_id = c.journal_entry_id AND jl.credit > 0
			JOIN account a ON a.org_id = jl.org_id AND a.id = jl.account_id AND a.type = 'income'
			WHERE al.org_id=$1 AND al.site_id=$2 AND NOT al.voided AND al.applied_on BETWEEN $3::date AND $4::date AND ($5::uuid IS NULL OR c.fund_id = $5)
			GROUP BY a.id, a.code, a.name ORDER BY a.code`, orgID, siteID, from, to, fund)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r AmountRow
			var amt int64
			if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &amt); err != nil {
				rows.Close()
				return err
			}
			r.Amount = money.New(amt, cur)
			rep.Income = append(rep.Income, r)
			rep.TotalIncome.Amount += amt
		}
		rows.Close()
		erows, err := tx.Query(ctx, `SELECT a.id, a.code, a.name, SUM(l.debit - l.credit)::bigint FROM journal_line l JOIN account a ON a.org_id = l.org_id AND a.id = l.account_id AND a.type = 'expense'
			WHERE l.org_id=$1 AND l.site_id=$2 AND l.entry_date BETWEEN $3::date AND $4::date AND ($5::uuid IS NULL OR l.fund_id = $5)
			GROUP BY a.id, a.code, a.name HAVING SUM(l.debit - l.credit) <> 0 ORDER BY a.code`, orgID, siteID, from, to, fund)
		if err != nil {
			return err
		}
		defer erows.Close()
		for erows.Next() {
			var r AmountRow
			var amt int64
			if err := erows.Scan(&r.AccountID, &r.Code, &r.Name, &amt); err != nil {
				return err
			}
			r.Amount = money.New(amt, cur)
			rep.Expense = append(rep.Expense, r)
			rep.TotalExpense.Amount += amt
		}
		rep.Net = money.New(rep.TotalIncome.Amount-rep.TotalExpense.Amount, cur)
		return erows.Err()
	})
	return rep, err
}

// CSV renders the income and expense report.
func (r IncomeExpenseOut) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"section", "code", "account", "amount"})
	for _, row := range r.Income {
		_ = w.Write([]string{"income", row.Code, CSVText(row.Name), fmtMoney(row.Amount)})
	}
	for _, row := range r.Expense {
		_ = w.Write([]string{"expense", row.Code, CSVText(row.Name), fmtMoney(row.Amount)})
	}
	_ = w.Write([]string{"total_income", "", "", fmtMoney(r.TotalIncome)})
	_ = w.Write([]string{"total_expense", "", "", fmtMoney(r.TotalExpense)})
	_ = w.Write([]string{"net", "", "", fmtMoney(r.Net)})
	w.Flush()
	return b.Bytes(), w.Error()
}

// OverviewOut is the money overview of the API.
type OverviewOut struct {
	AsOf              string           `json:"as_of"`
	Funds             []FundBalanceOut `json:"funds"`
	TopDebtors        []map[string]any `json:"top_debtors"`
	ReceivableOwner   money.Money      `json:"receivable_owner"`
	ReceivableTenant  money.Money      `json:"receivable_tenant"`
	Cash              money.Money      `json:"cash"`
	Bank              money.Money      `json:"bank"`
	AccruedLateFees   money.Money      `json:"accrued_late_fees"`
	CollectionRatePct float64          `json:"collection_rate_pct"`
	Aging             AgingReport      `json:"aging"`
}

// Overview gathers the money dashboard of a site (money.read): receivables by side, cash and bank,
// the late fee accrued but not booked, the share of what fell due this year that was collected,
// the aging, every fund's balance and the five largest overdue debtors.
func (s *Service) Overview(ctx context.Context, orgID, siteID uuid.UUID) (OverviewOut, error) {
	var out OverviewOut
	var today string
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		today = ledger.Today(site.Timezone, s.Now())
		out = OverviewOut{AsOf: today, Funds: []FundBalanceOut{}, TopDebtors: []map[string]any{}}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		bal := func(sub string) (int64, error) {
			d, c, err := ledger.Balance(ctx, tx, orgID, siteID, ch.Account(sub), ledger.BalanceFilter{})
			return d - c, err
		}
		ro, err := bal(ledger.SubReceivableOwner)
		if err != nil {
			return err
		}
		rt, err := bal(ledger.SubReceivableTenant)
		if err != nil {
			return err
		}
		bank, err := bal(ledger.SubBank)
		if err != nil {
			return err
		}
		var cash int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(l.debit - l.credit), 0)::bigint FROM journal_line l JOIN account a ON a.org_id = l.org_id AND a.id = l.account_id
			WHERE l.org_id=$1 AND l.site_id=$2 AND a.subtype = 'cash_box'`, orgID, siteID).Scan(&cash); err != nil {
			return err
		}
		out.ReceivableOwner, out.ReceivableTenant, out.Cash, out.Bank = money.New(ro, cur), money.New(rt, cur), money.New(cash, cur), money.New(bank, cur)
		// the fee accrued since the last booked month, per unit side
		out.AccruedLateFees = money.Zero(cur)
		rows, err := tx.Query(ctx, `SELECT DISTINCT unit_id, debtor_kind FROM charge WHERE org_id=$1 AND site_id=$2 AND kind='principal' AND debtor_kind IN ('owner','tenant') AND cancel_entry_id IS NULL`, orgID, siteID)
		if err != nil {
			return err
		}
		var sides []billing.Debtor
		for rows.Next() {
			var u uuid.UUID
			var k string
			if err := rows.Scan(&u, &k); err != nil {
				rows.Close()
				return err
			}
			uu := u
			sides = append(sides, billing.Debtor{UnitID: &uu, Kind: k})
		}
		rows.Close()
		for _, d := range sides {
			acc, err := billing.AccrueTx(ctx, tx, orgID, site, d, today)
			if err != nil {
				return err
			}
			out.AccruedLateFees.Amount += acc.Unbooked.Amount
		}
		// collected share of the principal that fell due since the start of the year
		var due, paid int64
		year := today[:4] + "-01-01"
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(c.amount), 0)::bigint, COALESCE(SUM(LEAST(c.amount, COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0))), 0)::bigint
			FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND c.kind='principal' AND c.cancel_entry_id IS NULL AND c.charge_type <> 'opening' AND c.due_date BETWEEN $3::date AND $4::date`, orgID, siteID, year, today).Scan(&due, &paid); err != nil {
			return err
		}
		out.CollectionRatePct = 100
		if due > 0 {
			out.CollectionRatePct = float64(paid*10000/due) / 100
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if out.Aging, err = s.AgingByUnit(ctx, orgID, siteID, today, nil); err != nil {
		return out, err
	}
	funds, err := s.FundBalancesFor(ctx, orgID, siteID, today)
	if err != nil {
		return out, err
	}
	out.Funds = funds.Rows
	debtors, err := s.DebtorReport(ctx, orgID, siteID, today, 0)
	if err != nil {
		return out, err
	}
	for _, d := range debtors {
		if len(out.TopDebtors) == 5 {
			break
		}
		if d.UnitID == nil || d.DaysOverdue <= 0 {
			continue
		}
		out.TopDebtors = append(out.TopDebtors, map[string]any{"unit_id": d.UnitID, "unit_door_number": d.DoorNumber, "debtor_kind": d.DebtorKind, "debtor_person_id": nil, "debtor_display_name": nil,
			"principal": d.Principal, "late_fee": d.Fee, "total": d.Total, "months_overdue": d.DaysOverdue / 30, "in_collection": false})
	}
	return out, nil
}
