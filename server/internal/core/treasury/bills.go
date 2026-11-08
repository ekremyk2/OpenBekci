package treasury

import (
	"context"
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

// BillFileScope is the scope type of bill documents in the file module.
const BillFileScope = "bill"

// BillPayment is a payment of a bill.
type BillPayment struct {
	ID                uuid.UUID   `json:"id"`
	BillID            uuid.UUID   `json:"bill_id"`
	Amount            money.Money `json:"amount"`
	PaidOn            string      `json:"paid_on"`
	PaidFromAccountID uuid.UUID   `json:"paid_from_account_id"`
	Reference         *string     `json:"reference"`
	JournalEntryID    uuid.UUID   `json:"journal_entry_id"`
	CreatedAt         time.Time   `json:"created_at"`
}

// Bill is a payable booked on its issue date (the optional bill workflow, site setting bills_enabled).
type Bill struct {
	ID                uuid.UUID     `json:"id"`
	SiteID            uuid.UUID     `json:"site_id"`
	VendorID          uuid.UUID     `json:"vendor_id"`
	DocumentNo        *string       `json:"document_no"`
	IssueDate         string        `json:"issue_date"`
	DueDate           string        `json:"due_date"`
	Amount            money.Money   `json:"amount"`
	Paid              money.Money   `json:"paid"`
	Balance           money.Money   `json:"balance"`
	CategoryAccountID uuid.UUID     `json:"category_account_id"`
	FundID            uuid.UUID     `json:"fund_id"`
	FileID            *uuid.UUID    `json:"file_id"`
	Description       *string       `json:"description"`
	Status            string        `json:"status"`
	JournalEntryID    uuid.UUID     `json:"journal_entry_id"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Version           int           `json:"version"`
	Payments          []BillPayment `json:"payments"`
}

// BillInput creates a bill.
type BillInput struct {
	SiteID            uuid.UUID   `json:"site_id"`
	VendorID          uuid.UUID   `json:"vendor_id"`
	DocumentNo        *string     `json:"document_no"`
	IssueDate         string      `json:"issue_date"`
	DueDate           string      `json:"due_date"`
	Amount            money.Money `json:"amount"`
	CategoryAccountID uuid.UUID   `json:"category_account_id"`
	FundID            uuid.UUID   `json:"fund_id"`
	FileID            *uuid.UUID  `json:"file_id"`
	Description       *string     `json:"description"`
}

// BillPaymentInput pays (part of) a bill.
type BillPaymentInput struct {
	Amount            money.Money `json:"amount"`
	PaidOn            string      `json:"paid_on"`
	PaidFromAccountID uuid.UUID   `json:"paid_from_account_id"`
	Reference         *string     `json:"reference"`
}

const billSel = `SELECT b.id, b.site_id, b.vendor_id, b.document_no, b.issue_date::text, b.due_date::text, b.amount,
	COALESCE((SELECT SUM(p.amount) FROM bill_payment p WHERE p.org_id = b.org_id AND p.bill_id = b.id), 0)::bigint,
	b.category_account_id, b.fund_id, b.file_id, b.description, b.status, b.journal_entry_id, b.created_at, b.updated_at, b.version, s.currency
	FROM bill b JOIN site s ON s.org_id = b.org_id AND s.id = b.site_id`

func scanBill(r pgx.Row) (b Bill, err error) {
	var amt, paid int64
	var cur string
	err = r.Scan(&b.ID, &b.SiteID, &b.VendorID, &b.DocumentNo, &b.IssueDate, &b.DueDate, &amt, &paid, &b.CategoryAccountID, &b.FundID, &b.FileID, &b.Description, &b.Status, &b.JournalEntryID,
		&b.CreatedAt, &b.UpdatedAt, &b.Version, &cur)
	c := money.Code(cur)
	b.Amount, b.Paid, b.Balance = money.New(amt, c), money.New(paid, c), money.New(amt-paid, c)
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	b.Payments = []BillPayment{}
	return
}

func loadBillPayments(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, b *Bill) error {
	rows, err := tx.Query(ctx, `SELECT id, bill_id, amount, paid_on::text, paid_from_account_id, reference, journal_entry_id, created_at FROM bill_payment WHERE org_id=$1 AND bill_id=$2 ORDER BY paid_on, id`, orgID, b.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p BillPayment
		var amt int64
		if err := rows.Scan(&p.ID, &p.BillID, &amt, &p.PaidOn, &p.PaidFromAccountID, &p.Reference, &p.JournalEntryID, &p.CreatedAt); err != nil {
			return err
		}
		p.Amount, p.CreatedAt = money.New(amt, b.Amount.Currency), p.CreatedAt.UTC()
		b.Payments = append(b.Payments, p)
	}
	return rows.Err()
}

func billsEnabled(site tenancy.Site) error {
	if v, _ := site.Settings[SettingBillsEnabled].(bool); !v {
		return problem.Conflict("bills are not enabled for this site (site setting " + SettingBillsEnabled + ")")
	}
	return nil
}

// payableAccount is the vendor payables account and the fund it is bound to.
func payableAccount(ch ledger.Chart) uuid.UUID { return ch.Account(ledger.SubAccountsPayable) }

// CreateBill books a bill (money.post, site setting bills_enabled): debit the expense category,
// credit vendor payables, dated the issue date. The bill's fund must be the one the payables account
// is bound to.
func (s *Service) CreateBill(ctx context.Context, orgID uuid.UUID, in BillInput) (Bill, error) {
	var out Bill
	err := s.run(ctx, orgID, "bill", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if err := billsEnabled(site); err != nil {
			return err
		}
		if err := checkAmount(in.Amount, ledger.Currency(site)); err != nil {
			return err
		}
		if err := checkDay("issue_date", in.IssueDate, today(site.Timezone, s.Now())); err != nil {
			return err
		}
		if _, err := ledger.ParseDate(in.DueDate); err != nil || in.DueDate < in.IssueDate {
			return bad("due_date", "invalid_value")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		cat, err := loadAccount(ctx, tx, orgID, in.SiteID, in.CategoryAccountID)
		if err != nil || cat.typ != "expense" {
			return bad("category_account_id", "invalid_value")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM vendor WHERE org_id=$1 AND id=$2 AND (site_id IS NULL OR site_id = $3)`, orgID, in.VendorID, in.SiteID).Scan(&n); err != nil || n == 0 {
			return bad("vendor_id", "invalid_value")
		}
		bid := id.New()
		memo := "Bill"
		if in.Description != nil && *in.Description != "" {
			memo = *in.Description
		}
		eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.IssueDate, Memo: memo, SourceType: "bill", SourceID: &bid, Lines: []ledger.Line{
			{AccountID: in.CategoryAccountID, FundID: in.FundID, Debit: in.Amount.Amount, VendorID: &in.VendorID},
			{AccountID: payableAccount(ch), FundID: in.FundID, Credit: in.Amount.Amount, VendorID: &in.VendorID},
		}})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bill (id, org_id, site_id, vendor_id, document_no, issue_date, due_date, amount, category_account_id, fund_id, description, journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6::date,$7::date,$8,$9,$10,$11,$12,$13)`, bid, orgID, in.SiteID, in.VendorID, in.DocumentNo, in.IssueDate, in.DueDate, in.Amount.Amount,
			in.CategoryAccountID, in.FundID, in.Description, eid, ledger.Actor(ctx)); err != nil {
			return err
		}
		if in.FileID != nil {
			if s.Files == nil {
				return problem.Internal(errNoFiles)
			}
			if _, err := s.Files.Attach(ctx, tx, orgID, *in.FileID, files.ScopeRef{Type: BillFileScope, ID: bid}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE bill SET file_id=$3 WHERE org_id=$1 AND id=$2`, orgID, bid, *in.FileID); err != nil {
				return err
			}
		}
		if out, err = scanBill(tx.QueryRow(ctx, billSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, bid)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "bill.create", "bill", bid, nil, map[string]any{"amount": in.Amount.Amount, "vendor_id": in.VendorID, "due_date": in.DueDate})
	})
	return out, err
}

// CreateBillPayment pays part or all of a bill (money.post): debit vendor payables, credit the cash
// account, dated paid_on. The payment cannot exceed what is still owed (409).
func (s *Service) CreateBillPayment(ctx context.Context, orgID, billID uuid.UUID, in BillPaymentInput) (BillPayment, error) {
	var out BillPayment
	err := s.run(ctx, orgID, "bill", func(tx pgx.Tx) error {
		b, err := scanBill(tx.QueryRow(ctx, billSel+` WHERE b.org_id=$1 AND b.id=$2 FOR UPDATE OF b`, orgID, billID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, b.SiteID)
		if err != nil {
			return err
		}
		if err := billsEnabled(site); err != nil {
			return err
		}
		if err := checkAmount(in.Amount, ledger.Currency(site)); err != nil {
			return err
		}
		if err := checkDay("paid_on", in.PaidOn, today(site.Timezone, s.Now())); err != nil {
			return err
		}
		if b.Status == "cancelled" {
			return problem.Conflict("the bill is cancelled")
		}
		if in.Amount.Amount > b.Balance.Amount {
			return problem.Conflict("the payment is larger than what is still owed on the bill")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, b.SiteID)
		if err != nil {
			return err
		}
		if _, err := cashAccount(ctx, tx, orgID, b.SiteID, in.PaidFromAccountID, "paid_from_account_id"); err != nil {
			return err
		}
		pid := id.New()
		eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: b.SiteID, EntryDate: in.PaidOn, Memo: "Bill payment", SourceType: "bill_payment", SourceID: &pid, Lines: []ledger.Line{
			{AccountID: payableAccount(ch), FundID: b.FundID, Debit: in.Amount.Amount, VendorID: &b.VendorID},
			{AccountID: in.PaidFromAccountID, FundID: b.FundID, Credit: in.Amount.Amount, VendorID: &b.VendorID},
		}})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bill_payment (id, org_id, site_id, bill_id, amount, paid_on, paid_from_account_id, reference, journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6::date,$7,$8,$9,$10)`, pid, orgID, b.SiteID, billID, in.Amount.Amount, in.PaidOn, in.PaidFromAccountID, in.Reference, eid, ledger.Actor(ctx)); err != nil {
			return err
		}
		status := "partially_paid"
		if in.Amount.Amount == b.Balance.Amount {
			status = "paid"
		}
		if _, err := tx.Exec(ctx, `UPDATE bill SET status=$3, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`, orgID, billID, status); err != nil {
			return err
		}
		out = BillPayment{ID: pid, BillID: billID, Amount: in.Amount, PaidOn: in.PaidOn, PaidFromAccountID: in.PaidFromAccountID, Reference: in.Reference, JournalEntryID: eid, CreatedAt: s.Now().UTC()}
		return ledger.Audit(ctx, tx, orgID, b.SiteID, "bill.pay", "bill", billID, map[string]any{"status": b.Status}, map[string]any{"status": status, "paid": in.Amount.Amount})
	})
	return out, err
}

// BillFilter filters ListBills.
type BillFilter struct {
	Vendor         *uuid.UUID
	Status         string
	DueFrom, DueTo string
}

// ListBills lists a site's bills with their payments (money.read).
func (s *Service) ListBills(ctx context.Context, orgID, siteID uuid.UUID, f BillFilter, p ledger.PageParams) ([]Bill, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Bill
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, billSel+` WHERE b.org_id=$1 AND b.site_id=$2 AND b.id > $3 AND ($4::uuid IS NULL OR b.vendor_id = $4) AND ($5 = '' OR b.status = $5)
			AND ($6 = '' OR b.due_date >= $6::date) AND ($7 = '' OR b.due_date <= $7::date) ORDER BY b.id LIMIT $8`, orgID, siteID, after, f.Vendor, f.Status, f.DueFrom, f.DueTo, limit+1)
		if err != nil {
			return err
		}
		for rows.Next() {
			b, err := scanBill(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range out {
			if err := loadBillPayments(ctx, tx, orgID, &out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(b Bill) uuid.UUID { return b.ID })
	return items, next, nil
}

// GetBill returns a bill with its payments (money.read).
func (s *Service) GetBill(ctx context.Context, orgID, billID uuid.UUID) (Bill, error) {
	var b Bill
	err := s.run(ctx, orgID, "bill", func(tx pgx.Tx) (err error) {
		if b, err = scanBill(tx.QueryRow(ctx, billSel+` WHERE b.org_id=$1 AND b.id=$2`, orgID, billID)); err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, b.SiteID); err != nil {
			return err
		}
		return loadBillPayments(ctx, tx, orgID, &b)
	})
	return b, err
}

// BillFileResolver decides who may read and attach bill documents (money.read and money.post at the bill's site).
func BillFileResolver() files.VisibilityResolver {
	siteOf := func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (uuid.UUID, bool) {
		var site uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT site_id FROM bill WHERE id = $1`, ref.ID).Scan(&site); err != nil {
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
