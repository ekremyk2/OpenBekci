package treasury

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoFiles = errors.New("treasury: no file service")

// FileScope is the scope type of expense receipts in the file module.
const FileScope = "expense"

// Expense is money the site spent (M6 light): a category account debited, a cash account credited.
type Expense struct {
	ID                 uuid.UUID   `json:"id"`
	SiteID             uuid.UUID   `json:"site_id"`
	CategoryAccountID  uuid.UUID   `json:"category_account_id"`
	FundID             uuid.UUID   `json:"fund_id"`
	Amount             money.Money `json:"amount"`
	PaidOn             string      `json:"paid_on"`
	PaidFromAccountID  uuid.UUID   `json:"paid_from_account_id"`
	VendorID           *uuid.UUID  `json:"vendor_id"`
	VendorName         *string     `json:"vendor_name"`
	Description        string      `json:"description"`
	DocumentNo         *string     `json:"document_no"`
	FileID             *uuid.UUID  `json:"file_id"`
	RecurringExpenseID *uuid.UUID  `json:"recurring_expense_id"`
	Status             string      `json:"status"`
	JournalEntryID     uuid.UUID   `json:"journal_entry_id"`
	VoidedReason       *string     `json:"voided_reason"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

// Recurrence asks for the expense to be posted again every month on a day.
type Recurrence struct {
	Frequency string `json:"frequency"`
	Day       int    `json:"day"`
}

// ExpenseInput creates an expense.
type ExpenseInput struct {
	SiteID            uuid.UUID   `json:"site_id"`
	CategoryAccountID uuid.UUID   `json:"category_account_id"`
	FundID            uuid.UUID   `json:"fund_id"`
	Amount            money.Money `json:"amount"`
	PaidOn            string      `json:"paid_on"`
	PaidFromAccountID uuid.UUID   `json:"paid_from_account_id"`
	VendorID          *uuid.UUID  `json:"vendor_id"`
	Description       string      `json:"description"`
	DocumentNo        *string     `json:"document_no"`
	FileID            *uuid.UUID  `json:"file_id"`
	Recurring         *Recurrence `json:"recurring"`
}

const expenseSel = `SELECT e.id, e.site_id, e.category_account_id, e.fund_id, e.amount, e.paid_on::text, e.paid_from_account_id, e.vendor_id, v.name, e.description, e.document_no, e.file_id,
	e.recurring_expense_id, e.status, e.journal_entry_id, e.voided_reason, e.created_at, e.updated_at, s.currency
	FROM expense e JOIN site s ON s.org_id = e.org_id AND s.id = e.site_id LEFT JOIN vendor v ON v.org_id = e.org_id AND v.id = e.vendor_id`

func scanExpense(r pgx.Row) (e Expense, err error) {
	var amt int64
	var cur string
	err = r.Scan(&e.ID, &e.SiteID, &e.CategoryAccountID, &e.FundID, &amt, &e.PaidOn, &e.PaidFromAccountID, &e.VendorID, &e.VendorName, &e.Description, &e.DocumentNo, &e.FileID,
		&e.RecurringExpenseID, &e.Status, &e.JournalEntryID, &e.VoidedReason, &e.CreatedAt, &e.UpdatedAt, &cur)
	e.Amount = money.New(amt, money.Code(cur))
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	return
}

func (in *ExpenseInput) validate() error {
	if in.Description == "" || len([]rune(in.Description)) > 500 {
		return bad("description", "invalid_value")
	}
	if r := in.Recurring; r != nil && (r.Frequency != "monthly" || r.Day < 1 || r.Day > 31) {
		return bad("recurring", "invalid_value")
	}
	return nil
}

// postExpense books an expense inside tx: debit the category (a expense account), credit the
// cash account, dated paid_on. The caller has checked permissions and the date.
func (s *Service) postExpense(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, in ExpenseInput, recurringID *uuid.UUID) (uuid.UUID, error) {
	cat, err := loadAccount(ctx, tx, orgID, in.SiteID, in.CategoryAccountID)
	if err != nil || cat.typ != "expense" {
		return uuid.Nil, bad("category_account_id", "invalid_value")
	}
	if _, err := cashAccount(ctx, tx, orgID, in.SiteID, in.PaidFromAccountID, "paid_from_account_id"); err != nil {
		return uuid.Nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM fund WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, in.FundID).Scan(&n); err != nil || n == 0 {
		return uuid.Nil, bad("fund_id", "invalid_value")
	}
	if in.VendorID != nil {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM vendor WHERE org_id=$1 AND id=$2 AND (site_id IS NULL OR site_id = $3)`, orgID, *in.VendorID, in.SiteID).Scan(&n); err != nil || n == 0 {
			return uuid.Nil, bad("vendor_id", "invalid_value")
		}
	}
	eid := id.New()
	jid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.PaidOn, Memo: in.Description, SourceType: "expense", SourceID: &eid, Lines: []ledger.Line{
		{AccountID: in.CategoryAccountID, FundID: in.FundID, Debit: in.Amount.Amount, VendorID: in.VendorID},
		{AccountID: in.PaidFromAccountID, FundID: in.FundID, Credit: in.Amount.Amount, VendorID: in.VendorID},
	}})
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO expense (id, org_id, site_id, category_account_id, fund_id, amount, paid_on, paid_from_account_id, vendor_id, description, document_no, recurring_expense_id, journal_entry_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10,$11,$12,$13,$14)`, eid, orgID, in.SiteID, in.CategoryAccountID, in.FundID, in.Amount.Amount, in.PaidOn, in.PaidFromAccountID, in.VendorID,
		in.Description, in.DocumentNo, recurringID, jid, ledger.Actor(ctx)); err != nil {
		return uuid.Nil, err
	}
	return eid, nil
}

// CreateExpense records an expense (money.post): the category is debited and the paid-from cash
// account credited, dated paid_on (not in the future, after the lock date). A receipt file
// (uploaded first) is attached to it. With recurring the same expense is posted again on that day of
// every following month by the hourly expenses.recurring job.
func (s *Service) CreateExpense(ctx context.Context, orgID uuid.UUID, in ExpenseInput) (Expense, error) {
	if err := in.validate(); err != nil {
		return Expense{}, err
	}
	var out Expense
	err := s.run(ctx, orgID, "expense", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if err := checkAmount(in.Amount, ledger.Currency(site)); err != nil {
			return err
		}
		if err := checkDay("paid_on", in.PaidOn, today(site.Timezone, s.Now())); err != nil {
			return err
		}
		if _, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID); err != nil {
			return err
		}
		var rid *uuid.UUID
		if in.Recurring != nil {
			r := id.New()
			rid = &r
			next := nextMonthDay(in.PaidOn, in.Recurring.Day)
			if _, err := tx.Exec(ctx, `INSERT INTO recurring_expense (id, org_id, site_id, category_account_id, fund_id, amount, paid_from_account_id, vendor_id, description, document_no, frequency, day_of_month, next_on, created_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'monthly',$11,$12::date,$13)`, r, orgID, in.SiteID, in.CategoryAccountID, in.FundID, in.Amount.Amount, in.PaidFromAccountID, in.VendorID,
				in.Description, in.DocumentNo, in.Recurring.Day, next, ledger.Actor(ctx)); err != nil {
				return err
			}
		}
		eid, err := s.postExpense(ctx, tx, orgID, in, rid)
		if err != nil {
			return err
		}
		if in.FileID != nil {
			if s.Files == nil {
				return problem.Internal(errNoFiles)
			}
			if _, err := s.Files.Attach(ctx, tx, orgID, *in.FileID, files.ScopeRef{Type: FileScope, ID: eid}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE expense SET file_id=$3 WHERE org_id=$1 AND id=$2`, orgID, eid, *in.FileID); err != nil {
				return err
			}
		}
		if out, err = scanExpense(tx.QueryRow(ctx, expenseSel+` WHERE e.org_id=$1 AND e.id=$2`, orgID, eid)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "expense.create", "expense", eid, nil, map[string]any{"amount": in.Amount.Amount, "paid_on": in.PaidOn, "category": in.CategoryAccountID, "recurring": in.Recurring != nil})
	})
	return out, err
}

// nextMonthDay is the day of the month after paid_on's month, the day clamped to the month's length.
func nextMonthDay(paidOn string, day int) string {
	t, _ := time.Parse(ledger.DateLayout, paidOn)
	return monthDay(t.Year(), int(t.Month())+1, day)
}

func monthDay(year, month, day int) string {
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC).Format(ledger.DateLayout)
}

// ExpenseFilter filters ListExpenses.
type ExpenseFilter struct {
	Category, Fund, Vendor *uuid.UUID
	Status, From, To, Q    string
}

// ListExpenses lists a site's expenses (money.read).
func (s *Service) ListExpenses(ctx context.Context, orgID, siteID uuid.UUID, f ExpenseFilter, p ledger.PageParams) ([]Expense, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Expense
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, expenseSel+` WHERE e.org_id=$1 AND e.site_id=$2 AND e.id > $3 AND ($4::uuid IS NULL OR e.category_account_id = $4) AND ($5::uuid IS NULL OR e.fund_id = $5)
			AND ($6::uuid IS NULL OR e.vendor_id = $6) AND ($7 = '' OR e.status = $7) AND ($8 = '' OR e.paid_on >= $8::date) AND ($9 = '' OR e.paid_on <= $9::date)
			AND ($10 = '' OR e.description ILIKE '%' || $10 || '%' OR e.document_no ILIKE '%' || $10 || '%' OR v.name ILIKE '%' || $10 || '%') ORDER BY e.id LIMIT $11`,
			orgID, siteID, after, f.Category, f.Fund, f.Vendor, f.Status, f.From, f.To, f.Q, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExpense(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(e Expense) uuid.UUID { return e.ID })
	return items, next, nil
}

// GetExpense returns an expense (money.read).
func (s *Service) GetExpense(ctx context.Context, orgID, expenseID uuid.UUID) (Expense, error) {
	var e Expense
	err := s.run(ctx, orgID, "expense", func(tx pgx.Tx) (err error) {
		if e, err = scanExpense(tx.QueryRow(ctx, expenseSel+` WHERE e.org_id=$1 AND e.id=$2`, orgID, expenseID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, e.SiteID)
	})
	return e, err
}

// VoidExpense reverses an expense (money.post): its entry is reversed dated today and the expense
// is marked voided. Voiding twice is a 409; a recurrence it came from is not stopped.
func (s *Service) VoidExpense(ctx context.Context, orgID, expenseID uuid.UUID, reason string) (Expense, error) {
	if reason == "" {
		return Expense{}, bad("reason", "required")
	}
	var out Expense
	err := s.run(ctx, orgID, "expense", func(tx pgx.Tx) error {
		e, err := scanExpense(tx.QueryRow(ctx, expenseSel+` WHERE e.org_id=$1 AND e.id=$2 FOR UPDATE OF e`, orgID, expenseID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, e.SiteID)
		if err != nil {
			return err
		}
		if e.Status == "voided" {
			return problem.Conflict("the expense is already voided")
		}
		rid, err := ledger.Reverse(ctx, tx, orgID, e.SiteID, e.JournalEntryID, today(site.Timezone, s.Now()), "Expense voided: "+reason)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE expense SET status='voided', void_entry_id=$3, voided_reason=$4, voided_at=now(), updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, expenseID, rid, reason); err != nil {
			return err
		}
		if out, err = scanExpense(tx.QueryRow(ctx, expenseSel+` WHERE e.org_id=$1 AND e.id=$2`, orgID, expenseID)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, e.SiteID, "expense.void", "expense", expenseID, map[string]any{"status": "posted"}, map[string]any{"status": "voided", "reason": reason})
	})
	return out, err
}

// FileResolver decides who may read and attach the receipt files of expenses: whoever holds
// money.read (read) or money.post (attach) at the expense's site. Register it under FileScope.
func FileResolver() files.VisibilityResolver {
	siteOf := func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (uuid.UUID, bool) {
		var site uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT site_id FROM expense WHERE id = $1`, ref.ID).Scan(&site); err != nil {
			return uuid.Nil, false
		}
		return site, true
	}
	return files.ResolverFuncs{
		Read: func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
			site, ok := siteOf(ctx, tx, ref)
			return ok && ledger.HasSite(ctx, tx, ledger.PermMoneyRead, site), nil
		},
		Attach: func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
			site, ok := siteOf(ctx, tx, ref)
			return ok && ledger.HasSite(ctx, tx, ledger.PermMoneyPost, site), nil
		},
	}
}

// RunRecurring posts the recurring expenses of a site that are due up to today (the hourly
// expenses.recurring job): one expense per due day, the next due day moves a month on, a day in a
// locked period is skipped. A recurrence whose accounts no longer fit is stopped and logged. It is
// idempotent (a unique index allows one expense per recurrence and day) and returns the number posted.
func (s *Service) RunRecurring(ctx context.Context, orgID, siteID uuid.UUID, log *slog.Logger) (int, error) {
	posted := 0
	var due []uuid.UUID
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, siteID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM recurring_expense WHERE org_id=$1 AND site_id=$2 AND active AND next_on <= $3::date ORDER BY id`, orgID, siteID, today(site.Timezone, s.Now()))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rid uuid.UUID
			if err := rows.Scan(&rid); err != nil {
				return err
			}
			due = append(due, rid)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	for _, rid := range due {
		for {
			done, n, err := s.postOneRecurring(ctx, orgID, siteID, rid, log)
			posted += n
			if err != nil {
				return posted, err
			}
			if done {
				break
			}
		}
	}
	return posted, nil
}

// postOneRecurring posts the next due day of one recurrence in its own transaction. done is true
// when nothing more is due (or the recurrence stopped).
func (s *Service) postOneRecurring(ctx context.Context, orgID, siteID, rid uuid.UUID, log *slog.Logger) (done bool, posted int, err error) {
	err = s.run(ctx, orgID, "recurring expense", func(tx pgx.Tx) error {
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false)
		if err != nil {
			return err
		}
		var in ExpenseInput
		var amt int64
		var next string
		var day int
		err = tx.QueryRow(ctx, `SELECT category_account_id, fund_id, amount, paid_from_account_id, vendor_id, description, document_no, day_of_month, next_on::text
			FROM recurring_expense WHERE org_id=$1 AND id=$2 AND active FOR UPDATE`, orgID, rid).Scan(&in.CategoryAccountID, &in.FundID, &amt, &in.PaidFromAccountID, &in.VendorID, &in.Description, &in.DocumentNo, &day, &next)
		if errors.Is(err, pgx.ErrNoRows) {
			done = true
			return nil
		}
		if err != nil {
			return err
		}
		if next > today(site.Timezone, s.Now()) {
			done = true
			return nil
		}
		in.SiteID, in.PaidOn, in.Amount = siteID, next, money.New(amt, ledger.Currency(site))
		advance := func() error {
			t, _ := time.Parse(ledger.DateLayout, next)
			_, err := tx.Exec(ctx, `UPDATE recurring_expense SET next_on=$3::date WHERE org_id=$1 AND id=$2`, orgID, rid, monthDay(t.Year(), int(t.Month())+1, day))
			return err
		}
		sp := "recurring"
		if _, err := tx.Exec(ctx, `SAVEPOINT `+sp); err != nil {
			return err
		}
		if _, perr := s.postExpense(ctx, tx, orgID, in, &rid); perr != nil {
			if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT `+sp); err != nil {
				return err
			}
			var pe *problem.Error
			if errors.As(perr, &pe) && (pe.Code == problem.CodeLockedPeriod || pe.Status == 409) {
				log.Warn("recurring expense skipped: the day is locked or closed", "recurring_expense_id", rid, "day", next)
				return advance()
			}
			log.Error("recurring expense stopped", "recurring_expense_id", rid, "error", perr)
			_, err := tx.Exec(ctx, `UPDATE recurring_expense SET active=false WHERE org_id=$1 AND id=$2`, orgID, rid)
			done = true
			return err
		}
		posted = 1
		return advance()
	})
	return done, posted, err
}
