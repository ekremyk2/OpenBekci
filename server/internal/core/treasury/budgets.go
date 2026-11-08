package treasury

import (
	"bytes"
	"context"
	"encoding/csv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Budget is the plan of expenses of a fiscal year (M3 light): a line per expense category and fund.
type Budget struct {
	ID           uuid.UUID   `json:"id"`
	SiteID       uuid.UUID   `json:"site_id"`
	FiscalYearID uuid.UUID   `json:"fiscal_year_id"`
	Status       string      `json:"status"`
	DecisionID   *uuid.UUID  `json:"decision_id"`
	Total        money.Money `json:"total"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Version      int         `json:"version"`
}

// BudgetLine is one line of a budget.
type BudgetLine struct {
	ExpenseCategoryAccountID uuid.UUID   `json:"expense_category_account_id"`
	FundID                   uuid.UUID   `json:"fund_id"`
	Amount                   money.Money `json:"amount"`
}

// BudgetLines are the lines of a budget with its version (the If-Match value of the next write).
type BudgetLines struct {
	Lines   []BudgetLine `json:"lines"`
	Version int          `json:"version"`
}

const budgetSel = `SELECT b.id, b.site_id, b.fiscal_year_id, b.status, b.decision_id,
	COALESCE((SELECT SUM(l.amount) FROM budget_line l WHERE l.org_id = b.org_id AND l.budget_id = b.id), 0)::bigint, b.created_at, b.updated_at, b.version, s.currency
	FROM budget b JOIN site s ON s.org_id = b.org_id AND s.id = b.site_id`

func scanBudget(r pgx.Row) (b Budget, err error) {
	var total int64
	var cur string
	err = r.Scan(&b.ID, &b.SiteID, &b.FiscalYearID, &b.Status, &b.DecisionID, &total, &b.CreatedAt, &b.UpdatedAt, &b.Version, &cur)
	b.Total = money.New(total, money.Code(cur))
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	return
}

// CreateBudget opens the draft budget of a fiscal year (money.manage); one per year (409).
func (s *Service) CreateBudget(ctx context.Context, orgID, siteID, fiscalYearID uuid.UUID, decisionID *uuid.UUID) (Budget, error) {
	var out Budget
	err := s.run(ctx, orgID, "budget", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, siteID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM fiscal_year WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, siteID, fiscalYearID).Scan(&n); err != nil || n == 0 {
			return bad("fiscal_year_id", "invalid_value")
		}
		bid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO budget (id, org_id, site_id, fiscal_year_id, decision_id, created_by) VALUES ($1,$2,$3,$4,$5,$6)`, bid, orgID, siteID, fiscalYearID, decisionID, ledger.Actor(ctx)); err != nil {
			return err
		}
		var err error
		if out, err = scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, bid)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, siteID, "budget.create", "budget", bid, nil, map[string]any{"fiscal_year_id": fiscalYearID})
	})
	return out, err
}

// GetBudget returns a budget (money.read).
func (s *Service) GetBudget(ctx context.Context, orgID, budgetID uuid.UUID) (Budget, error) {
	var b Budget
	err := s.run(ctx, orgID, "budget", func(tx pgx.Tx) (err error) {
		if b, err = scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, budgetID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, b.SiteID)
	})
	return b, err
}

// ListBudgets lists a site's budgets (money.read).
func (s *Service) ListBudgets(ctx context.Context, orgID, siteID uuid.UUID, fiscalYear *uuid.UUID, status string, p ledger.PageParams) ([]Budget, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Budget
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, budgetSel+` WHERE b.org_id=$1 AND b.site_id=$2 AND b.id > $3 AND ($4::uuid IS NULL OR b.fiscal_year_id = $4) AND ($5 = '' OR b.status = $5) ORDER BY b.id LIMIT $6`,
			orgID, siteID, after, fiscalYear, status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBudget(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(b Budget) uuid.UUID { return b.ID })
	return items, next, nil
}

// lockDraft loads a budget for update and checks the caller may manage it, the version and that it is still a draft.
func lockBudget(ctx context.Context, tx pgx.Tx, orgID, budgetID uuid.UUID, version int, needDraft bool) (Budget, error) {
	b, err := scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2 FOR UPDATE OF b`, orgID, budgetID))
	if err != nil {
		return b, err
	}
	if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyManage, b.SiteID); err != nil {
		return b, err
	}
	if version >= 0 && b.Version != version {
		return b, tenancy.ErrVersion
	}
	if needDraft && b.Status != "draft" {
		return b, problem.Conflict("the budget is final; it can no longer be changed")
	}
	return b, nil
}

// UpdateBudget changes the decision a draft budget is linked to (money.manage; version is the If-Match value).
func (s *Service) UpdateBudget(ctx context.Context, orgID, budgetID uuid.UUID, version int, d tenancy.Doc) (Budget, error) {
	var out Budget
	err := s.run(ctx, orgID, "budget", func(tx pgx.Tx) error {
		before, err := lockBudget(ctx, tx, orgID, budgetID, version, true)
		if err != nil {
			return err
		}
		if u := d.Unknown("decision_id"); len(u) > 0 {
			return problem.Validation(u...)
		}
		dec := before.DecisionID
		if d.Has("decision_id") {
			if err := d.Get("decision_id", &dec); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE budget SET decision_id=$3, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`, orgID, budgetID, dec); err != nil {
			return err
		}
		if out, err = scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, budgetID)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, out.SiteID, "budget.update", "budget", budgetID, before, out)
	})
	return out, err
}

// DeleteBudget removes a draft budget with its lines (money.manage; version is the If-Match value).
func (s *Service) DeleteBudget(ctx context.Context, orgID, budgetID uuid.UUID, version int) error {
	return s.run(ctx, orgID, "budget", func(tx pgx.Tx) error {
		b, err := lockBudget(ctx, tx, orgID, budgetID, version, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM budget WHERE org_id=$1 AND id=$2`, orgID, budgetID); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, b.SiteID, "budget.delete", "budget", budgetID, b, nil)
	})
}

func loadLines(ctx context.Context, tx pgx.Tx, orgID, budgetID uuid.UUID, cur money.Code) ([]BudgetLine, error) {
	rows, err := tx.Query(ctx, `SELECT l.expense_category_account_id, l.fund_id, l.amount FROM budget_line l JOIN account a ON a.org_id = l.org_id AND a.id = l.expense_category_account_id
		WHERE l.org_id=$1 AND l.budget_id=$2 ORDER BY a.code, l.fund_id`, orgID, budgetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BudgetLine{}
	for rows.Next() {
		var l BudgetLine
		var amt int64
		if err := rows.Scan(&l.ExpenseCategoryAccountID, &l.FundID, &amt); err != nil {
			return nil, err
		}
		l.Amount = money.New(amt, cur)
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetBudgetLines returns the lines of a budget and its version (money.read).
func (s *Service) GetBudgetLines(ctx context.Context, orgID, budgetID uuid.UUID) (BudgetLines, error) {
	var out BudgetLines
	err := s.run(ctx, orgID, "budget", func(tx pgx.Tx) error {
		b, err := scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, budgetID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, b.SiteID); err != nil {
			return err
		}
		out.Version = b.Version
		out.Lines, err = loadLines(ctx, tx, orgID, budgetID, b.Total.Currency)
		return err
	})
	return out, err
}

// PutBudgetLines replaces the lines of a draft budget (money.manage; version is the If-Match
// value). Every line names an expense account and a fund of the site, once; amounts are not negative.
func (s *Service) PutBudgetLines(ctx context.Context, orgID, budgetID uuid.UUID, version int, lines []BudgetLine) (BudgetLines, error) {
	var out BudgetLines
	err := s.run(ctx, orgID, "budget", func(tx pgx.Tx) error {
		b, err := lockBudget(ctx, tx, orgID, budgetID, version, true)
		if err != nil {
			return err
		}
		seen := map[[2]uuid.UUID]bool{}
		for i, l := range lines {
			key := [2]uuid.UUID{l.ExpenseCategoryAccountID, l.FundID}
			if seen[key] {
				return problem.Validation(problem.Field{Field: "lines", Code: "duplicate_line", Params: map[string]any{"index": i}})
			}
			seen[key] = true
			if l.Amount.Amount < 0 || l.Amount.Currency != b.Total.Currency {
				return problem.Validation(problem.Field{Field: "lines", Code: "invalid_amount", Params: map[string]any{"index": i}})
			}
			acc, err := loadAccount(ctx, tx, orgID, b.SiteID, l.ExpenseCategoryAccountID)
			if err != nil || acc.typ != "expense" {
				return problem.Validation(problem.Field{Field: "lines", Code: "invalid_account", Params: map[string]any{"index": i}})
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM fund WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, b.SiteID, l.FundID).Scan(&n); err != nil || n == 0 {
				return problem.Validation(problem.Field{Field: "lines", Code: "invalid_fund", Params: map[string]any{"index": i}})
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM budget_line WHERE org_id=$1 AND budget_id=$2`, orgID, budgetID); err != nil {
			return err
		}
		for _, l := range lines {
			if _, err := tx.Exec(ctx, `INSERT INTO budget_line (org_id, site_id, budget_id, expense_category_account_id, fund_id, amount) VALUES ($1,$2,$3,$4,$5,$6)`,
				orgID, b.SiteID, budgetID, l.ExpenseCategoryAccountID, l.FundID, l.Amount.Amount); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE budget SET updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`, orgID, budgetID); err != nil {
			return err
		}
		out = BudgetLines{Version: b.Version + 1}
		if out.Lines, err = loadLines(ctx, tx, orgID, budgetID, b.Total.Currency); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, b.SiteID, "budget.lines", "budget", budgetID, nil, map[string]any{"lines": len(lines)})
	})
	return out, err
}

// FinalizeBudgetFor makes a draft budget final (money.manage), citing the assembly decision when given.
func (s *Service) FinalizeBudgetFor(ctx context.Context, orgID, budgetID uuid.UUID, decisionID *uuid.UUID) (Budget, error) {
	var out Budget
	err := s.run(ctx, orgID, "budget", func(tx pgx.Tx) error {
		b, err := lockBudget(ctx, tx, orgID, budgetID, -1, true)
		if err != nil {
			return err
		}
		if err := finalize(ctx, tx, orgID, b, decisionID); err != nil {
			return err
		}
		out, err = scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, budgetID))
		return err
	})
	return out, err
}

func finalize(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, b Budget, decisionID *uuid.UUID) error {
	var lines int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM budget_line WHERE org_id=$1 AND budget_id=$2`, orgID, b.ID).Scan(&lines); err != nil {
		return err
	}
	if lines == 0 {
		return problem.Conflict("a budget without lines cannot be made final")
	}
	dec := decisionID
	if dec == nil {
		dec = b.DecisionID
	}
	if _, err := tx.Exec(ctx, `UPDATE budget SET status='final', decision_id=$3, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`, orgID, b.ID, dec); err != nil {
		return err
	}
	return ledger.Audit(ctx, tx, orgID, b.SiteID, "budget.finalize", "budget", b.ID, map[string]any{"status": "draft"}, map[string]any{"status": "final", "decision_id": dec})
}

// FinalizeBudget implements money.BudgetFinalizer: the governance decision effect "adopt the
// budget" makes the draft budget final inside the transaction that records the decision.
func (s *Service) FinalizeBudget(ctx context.Context, tx pgx.Tx, d money.DecisionRef, budgetID uuid.UUID) (money.EffectRecord, error) {
	var orgID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT org_id FROM budget WHERE id=$1`, budgetID).Scan(&orgID); err != nil {
		return money.EffectRecord{}, err
	}
	b, err := scanBudget(tx.QueryRow(ctx, budgetSel+` WHERE b.org_id=$1 AND b.id=$2 FOR UPDATE OF b`, orgID, budgetID))
	if err != nil {
		return money.EffectRecord{}, err
	}
	if d.SiteID != uuid.Nil && d.SiteID != b.SiteID {
		return money.EffectRecord{}, problem.Conflict("the budget belongs to another site than the decision")
	}
	if b.Status == "final" && b.DecisionID != nil && *b.DecisionID == d.ID {
		return money.EffectRecord{Kind: "budget", ResourceID: budgetID, Summary: "already final"}, nil
	}
	if b.Status != "draft" {
		return money.EffectRecord{}, problem.Conflict("the budget is final already")
	}
	if err := finalize(ctx, tx, orgID, b, &d.ID); err != nil {
		return money.EffectRecord{}, err
	}
	return money.EffectRecord{Kind: "budget", ResourceID: budgetID, Summary: "budget made final"}, nil
}

var _ money.BudgetFinalizer = (*Service)(nil)

// BudgetActualRow compares one expense category and fund with what was spent.
type BudgetActualRow struct {
	AccountID uuid.UUID   `json:"account_id"`
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	FundID    uuid.UUID   `json:"fund_id"`
	Budget    money.Money `json:"budget"`
	Actual    money.Money `json:"actual"`
	Variance  money.Money `json:"variance"`
}

// BudgetActual is the budget-versus-actual report of a fiscal year.
type BudgetActual struct {
	FiscalYearID uuid.UUID         `json:"fiscal_year_id"`
	Rows         []BudgetActualRow `json:"rows"`
	TotalBudget  money.Money       `json:"total_budget"`
	TotalActual  money.Money       `json:"total_actual"`
}

// BudgetVsActual compares the budget of a fiscal year with the expense postings dated inside the
// year (reports.read, or reports.aggregate); variance is budget minus actual, so a positive
// variance is money left. Categories spent but not budgeted show with a zero budget.
func (s *Service) BudgetVsActual(ctx context.Context, orgID, siteID, fiscalYearID uuid.UUID) (BudgetActual, error) {
	rep := BudgetActual{FiscalYearID: fiscalYearID, Rows: []BudgetActualRow{}}
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
		var start, end string
		if err := tx.QueryRow(ctx, `SELECT start_on::text, end_on::text FROM fiscal_year WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, siteID, fiscalYearID).Scan(&start, &end); err != nil {
			return err
		}
		rep.TotalBudget, rep.TotalActual = money.Zero(cur), money.Zero(cur)
		rows, err := tx.Query(ctx, `
			WITH bud AS (SELECT l.expense_category_account_id AS acc, l.fund_id AS fund, l.amount FROM budget_line l JOIN budget b ON b.org_id = l.org_id AND b.id = l.budget_id
				WHERE l.org_id=$1 AND b.site_id=$2 AND b.fiscal_year_id=$3),
			act AS (SELECT j.account_id AS acc, j.fund_id AS fund, SUM(j.debit - j.credit)::bigint AS amount FROM journal_line j JOIN account a ON a.org_id = j.org_id AND a.id = j.account_id AND a.type='expense'
				WHERE j.org_id=$1 AND j.site_id=$2 AND j.entry_date BETWEEN $4::date AND $5::date GROUP BY 1, 2)
			SELECT a.id, a.code, a.name, COALESCE(bud.fund, act.fund), COALESCE(bud.amount, 0)::bigint, COALESCE(act.amount, 0)::bigint
			FROM bud FULL JOIN act ON act.acc = bud.acc AND act.fund = bud.fund
			JOIN account a ON a.org_id = $1 AND a.id = COALESCE(bud.acc, act.acc)
			WHERE COALESCE(bud.amount, 0) <> 0 OR COALESCE(act.amount, 0) <> 0 ORDER BY a.code, 4`, orgID, siteID, fiscalYearID, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r BudgetActualRow
			var bud, act int64
			if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.FundID, &bud, &act); err != nil {
				return err
			}
			r.Budget, r.Actual, r.Variance = money.New(bud, cur), money.New(act, cur), money.New(bud-act, cur)
			rep.Rows = append(rep.Rows, r)
			rep.TotalBudget.Amount += bud
			rep.TotalActual.Amount += act
		}
		return rows.Err()
	})
	return rep, err
}

// CSV renders the report.
func (r BudgetActual) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"code", "account", "fund_id", "budget", "actual", "variance"})
	for _, row := range r.Rows {
		_ = w.Write([]string{row.Code, reports.CSVText(row.Name), row.FundID.String(), fmtAmt(row.Budget), fmtAmt(row.Actual), fmtAmt(row.Variance)})
	}
	_ = w.Write([]string{"", "total", "", fmtAmt(r.TotalBudget), fmtAmt(r.TotalActual), fmtAmt(money.New(r.TotalBudget.Amount-r.TotalActual.Amount, r.TotalBudget.Currency))})
	w.Flush()
	return b.Bytes(), w.Error()
}

func fmtAmt(m money.Money) string { return reports.FormatAmount(m) }
