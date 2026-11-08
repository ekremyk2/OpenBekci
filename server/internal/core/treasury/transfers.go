package treasury

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Transfer is a movement of money between two cash accounts of the site (a cash hand-over
// from a collector's box to the treasurer, a deposit of cash into the bank).
type Transfer struct {
	ID             uuid.UUID   `json:"id"`
	SiteID         uuid.UUID   `json:"site_id"`
	FromAccountID  uuid.UUID   `json:"from_account_id"`
	ToAccountID    uuid.UUID   `json:"to_account_id"`
	Amount         money.Money `json:"amount"`
	TransferredOn  string      `json:"transferred_on"`
	Memo           *string     `json:"memo"`
	Kind           string      `json:"kind"`
	JournalEntryID uuid.UUID   `json:"journal_entry_id"`
	CreatedBy      uuid.UUID   `json:"created_by"`
	CreatedAt      time.Time   `json:"created_at"`
}

// TransferInput creates a transfer.
type TransferInput struct {
	SiteID        uuid.UUID   `json:"site_id"`
	FromAccountID uuid.UUID   `json:"from_account_id"`
	ToAccountID   uuid.UUID   `json:"to_account_id"`
	Amount        money.Money `json:"amount"`
	TransferredOn string      `json:"transferred_on"`
	Memo          *string     `json:"memo"`
	Kind          string      `json:"kind"`
}

const transferSel = `SELECT t.id, t.site_id, t.from_account_id, t.to_account_id, t.amount, t.transferred_on::text, t.memo, t.kind, t.journal_entry_id, COALESCE(t.created_by, '00000000-0000-0000-0000-000000000000'::uuid), t.created_at, s.currency
	FROM transfer t JOIN site s ON s.org_id = t.org_id AND s.id = t.site_id`

func scanTransfer(r pgx.Row) (t Transfer, err error) {
	var amt int64
	var cur string
	err = r.Scan(&t.ID, &t.SiteID, &t.FromAccountID, &t.ToAccountID, &amt, &t.TransferredOn, &t.Memo, &t.Kind, &t.JournalEntryID, &t.CreatedBy, &t.CreatedAt, &cur)
	t.Amount = money.New(amt, money.Code(cur))
	t.CreatedAt = t.CreatedAt.UTC()
	return
}

// CreateTransfer moves money from one bank account or cash box to another (money.post): debit the
// receiving account, credit the sending one, dated transferred_on (not in the future, after the lock
// date). Both accounts must belong to the same fund. A transfer is final; a mistake is corrected by a
// transfer the other way.
func (s *Service) CreateTransfer(ctx context.Context, orgID uuid.UUID, in TransferInput) (Transfer, error) {
	if in.FromAccountID == in.ToAccountID {
		return Transfer{}, bad("to_account_id", "must_differ")
	}
	if in.Kind != "" && in.Kind != "cash_handover" && in.Kind != "bank_deposit" && in.Kind != "other" {
		return Transfer{}, bad("kind", "invalid_value")
	}
	var out Transfer
	err := s.run(ctx, orgID, "transfer", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if err := checkAmount(in.Amount, ledger.Currency(site)); err != nil {
			return err
		}
		if err := checkDay("transferred_on", in.TransferredOn, today(site.Timezone, s.Now())); err != nil {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		from, err := cashAccount(ctx, tx, orgID, in.SiteID, in.FromAccountID, "from_account_id")
		if err != nil {
			return err
		}
		to, err := cashAccount(ctx, tx, orgID, in.SiteID, in.ToAccountID, "to_account_id")
		if err != nil {
			return err
		}
		fund := fundOfAccount(from, ch)
		if fundOfAccount(to, ch) != fund {
			return problem.Validation(problem.Field{Field: "to_account_id", Code: "funds_differ"})
		}
		kind := in.Kind
		if kind == "" {
			switch {
			case from.subtype == ledger.SubCashBox && to.subtype == ledger.SubBank:
				kind = "bank_deposit"
			case from.subtype == ledger.SubCashBox && to.subtype == ledger.SubCashBox:
				kind = "cash_handover"
			default:
				kind = "other"
			}
		}
		tid := id.New()
		memo := "Transfer"
		if in.Memo != nil && *in.Memo != "" {
			memo = *in.Memo
		}
		eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.TransferredOn, Memo: memo, SourceType: "transfer", SourceID: &tid, Lines: []ledger.Line{
			{AccountID: in.ToAccountID, FundID: fund, Debit: in.Amount.Amount},
			{AccountID: in.FromAccountID, FundID: fund, Credit: in.Amount.Amount},
		}})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO transfer (id, org_id, site_id, from_account_id, to_account_id, amount, transferred_on, memo, kind, journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10,$11)`, tid, orgID, in.SiteID, in.FromAccountID, in.ToAccountID, in.Amount.Amount, in.TransferredOn, in.Memo, kind, eid, ledger.Actor(ctx)); err != nil {
			return err
		}
		if out, err = scanTransfer(tx.QueryRow(ctx, transferSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, tid)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "transfer.create", "transfer", tid, nil, map[string]any{"from": in.FromAccountID, "to": in.ToAccountID, "amount": in.Amount.Amount, "on": in.TransferredOn})
	})
	return out, err
}

// TransferFilter filters ListTransfers.
type TransferFilter struct {
	AccountID *uuid.UUID
	From, To  string
	Kind      string
}

// ListTransfers lists a site's transfers, newest id first is not promised: by id (money.read).
func (s *Service) ListTransfers(ctx context.Context, orgID, siteID uuid.UUID, f TransferFilter, p ledger.PageParams) ([]Transfer, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Transfer
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, transferSel+` WHERE t.org_id=$1 AND t.site_id=$2 AND t.id > $3 AND ($4::uuid IS NULL OR t.from_account_id = $4 OR t.to_account_id = $4)
			AND ($5 = '' OR t.transferred_on >= $5::date) AND ($6 = '' OR t.transferred_on <= $6::date) AND ($7 = '' OR t.kind = $7) ORDER BY t.id LIMIT $8`,
			orgID, siteID, after, f.AccountID, f.From, f.To, f.Kind, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTransfer(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(t Transfer) uuid.UUID { return t.ID })
	return items, next, nil
}

// GetTransfer returns a transfer (money.read).
func (s *Service) GetTransfer(ctx context.Context, orgID, transferID uuid.UUID) (Transfer, error) {
	var t Transfer
	err := s.run(ctx, orgID, "transfer", func(tx pgx.Tx) (err error) {
		if t, err = scanTransfer(tx.QueryRow(ctx, transferSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, transferID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, t.SiteID)
	})
	return t, err
}

// Refund is money paid back to a person or unit side: prepayment credit returned, a deposit
// given back, or another payment out.
type Refund struct {
	ID              uuid.UUID   `json:"id"`
	SiteID          uuid.UUID   `json:"site_id"`
	PersonID        *uuid.UUID  `json:"person_id"`
	UnitID          *uuid.UUID  `json:"unit_id"`
	DebtorKind      *string     `json:"debtor_kind"`
	Amount          money.Money `json:"amount"`
	Reason          string      `json:"reason"`
	Kind            string      `json:"kind"`
	PaidFromAccount uuid.UUID   `json:"paid_from_account_id"`
	RefundedOn      string      `json:"refunded_on"`
	CreditID        *uuid.UUID  `json:"credit_id"`
	PaymentID       *uuid.UUID  `json:"payment_id"`
	JournalEntryID  uuid.UUID   `json:"journal_entry_id"`
	CreatedAt       time.Time   `json:"created_at"`
}

// RefundInput creates a refund.
type RefundInput struct {
	SiteID          uuid.UUID   `json:"site_id"`
	PersonID        *uuid.UUID  `json:"person_id"`
	UnitID          *uuid.UUID  `json:"unit_id"`
	DebtorKind      string      `json:"debtor_kind"`
	Amount          money.Money `json:"amount"`
	Reason          string      `json:"reason"`
	Kind            string      `json:"kind"`
	PaidFromAccount uuid.UUID   `json:"paid_from_account_id"`
	RefundedOn      string      `json:"refunded_on"`
	CreditID        *uuid.UUID  `json:"credit_id"`
	PaymentID       *uuid.UUID  `json:"payment_id"`
}

const refundSel = `SELECT r.id, r.site_id, r.person_id, r.unit_id, r.debtor_kind, r.amount, r.reason, r.kind, r.paid_from_account_id, r.refunded_on::text, r.credit_id, r.payment_id, r.journal_entry_id, r.created_at, s.currency
	FROM refund r JOIN site s ON s.org_id = r.org_id AND s.id = r.site_id`

func scanRefund(r pgx.Row) (f Refund, err error) {
	var amt int64
	var cur string
	err = r.Scan(&f.ID, &f.SiteID, &f.PersonID, &f.UnitID, &f.DebtorKind, &amt, &f.Reason, &f.Kind, &f.PaidFromAccount, &f.RefundedOn, &f.CreditID, &f.PaymentID, &f.JournalEntryID, &f.CreatedAt, &cur)
	f.Amount = money.New(amt, money.Code(cur))
	f.CreatedAt = f.CreatedAt.UTC()
	return
}

// CreateRefund pays money out of a cash account (money.post). An overpayment refund reduces the
// debtor's prepayment credit and cannot exceed it (409); a deposit return reduces the deposit
// liability of the unit; another refund is booked as an expense. The date is not in the future
// and after the lock date.
func (s *Service) CreateRefund(ctx context.Context, orgID uuid.UUID, in RefundInput) (Refund, error) {
	if in.Reason == "" {
		return Refund{}, bad("reason", "required")
	}
	if in.Kind != "overpayment" && in.Kind != "deposit_return" && in.Kind != "other" {
		return Refund{}, bad("kind", "invalid_value")
	}
	unitMode := in.UnitID != nil
	if unitMode == (in.PersonID != nil) && in.Kind != "other" {
		return Refund{}, bad("person_id", "exactly_one_of_person_or_unit")
	}
	if unitMode && in.DebtorKind != "owner" && in.DebtorKind != "tenant" {
		return Refund{}, bad("debtor_kind", "invalid_value")
	}
	var out Refund
	err := s.run(ctx, orgID, "refund", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if err := checkAmount(in.Amount, ledger.Currency(site)); err != nil {
			return err
		}
		if err := checkDay("refunded_on", in.RefundedOn, today(site.Timezone, s.Now())); err != nil {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		paid, err := cashAccount(ctx, tx, orgID, in.SiteID, in.PaidFromAccount, "paid_from_account_id")
		if err != nil {
			return err
		}
		fund := fundOfAccount(paid, ch)
		var d billing.Debtor
		switch {
		case unitMode:
			d = billing.Debtor{UnitID: in.UnitID, Kind: in.DebtorKind}
		case in.PersonID != nil:
			d = billing.Debtor{PersonID: in.PersonID}
		}
		rid := id.New()
		var debit ledger.Line
		switch in.Kind {
		case "overpayment":
			if err := billing.LockOpenCharges(ctx, tx, orgID, in.SiteID, d); err != nil {
				return err
			}
			credit, err := billing.CreditOf(ctx, tx, orgID, in.SiteID, ch, d)
			if err != nil {
				return err
			}
			if in.Amount.Amount > credit {
				return problem.Conflict("the refund is larger than the credit the debtor holds")
			}
			debit = d.Line(ledger.Line{AccountID: ch.Account(ledger.SubPrepayment), FundID: fund, Debit: in.Amount.Amount}, nil)
		case "deposit_return":
			debit = ledger.Line{AccountID: ch.Account(ledger.SubDeposit), FundID: fund, Debit: in.Amount.Amount, UnitID: in.UnitID, PersonID: in.PersonID}
			if unitMode {
				debit.DebtorKind = in.DebtorKind
			}
		default:
			debit = ledger.Line{AccountID: ch.AccountCode("5900"), FundID: fund, Debit: in.Amount.Amount}
		}
		eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.RefundedOn, Memo: "Refund: " + in.Reason, SourceType: "refund", SourceID: &rid, Lines: []ledger.Line{
			debit, {AccountID: in.PaidFromAccount, FundID: fund, Credit: in.Amount.Amount}}})
		if err != nil {
			return err
		}
		var kind *string
		if unitMode {
			kind = &in.DebtorKind
		}
		if _, err := tx.Exec(ctx, `INSERT INTO refund (id, org_id, site_id, person_id, unit_id, debtor_kind, amount, reason, kind, paid_from_account_id, refunded_on, credit_id, payment_id, journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::date,$12,$13,$14,$15)`, rid, orgID, in.SiteID, in.PersonID, in.UnitID, kind, in.Amount.Amount, in.Reason, in.Kind, in.PaidFromAccount, in.RefundedOn, in.CreditID, in.PaymentID, eid, ledger.Actor(ctx)); err != nil {
			return err
		}
		if out, err = scanRefund(tx.QueryRow(ctx, refundSel+` WHERE r.org_id=$1 AND r.id=$2`, orgID, rid)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "refund.create", "refund", rid, nil, map[string]any{"kind": in.Kind, "amount": in.Amount.Amount, "reason": in.Reason, "on": in.RefundedOn})
	})
	return out, err
}

// RefundFilter filters ListRefunds.
type RefundFilter struct {
	UnitID, PersonID *uuid.UUID
	From, To, Kind   string
}

// ListRefunds lists a site's refunds (money.read).
func (s *Service) ListRefunds(ctx context.Context, orgID, siteID uuid.UUID, f RefundFilter, p ledger.PageParams) ([]Refund, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Refund
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, refundSel+` WHERE r.org_id=$1 AND r.site_id=$2 AND r.id > $3 AND ($4::uuid IS NULL OR r.unit_id = $4) AND ($5::uuid IS NULL OR r.person_id = $5)
			AND ($6 = '' OR r.refunded_on >= $6::date) AND ($7 = '' OR r.refunded_on <= $7::date) AND ($8 = '' OR r.kind = $8) ORDER BY r.id LIMIT $9`,
			orgID, siteID, after, f.UnitID, f.PersonID, f.From, f.To, f.Kind, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRefund(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(r Refund) uuid.UUID { return r.ID })
	return items, next, nil
}

// GetRefund returns a refund (money.read).
func (s *Service) GetRefund(ctx context.Context, orgID, refundID uuid.UUID) (Refund, error) {
	var r Refund
	err := s.run(ctx, orgID, "refund", func(tx pgx.Tx) (err error) {
		if r, err = scanRefund(tx.QueryRow(ctx, refundSel+` WHERE r.org_id=$1 AND r.id=$2`, orgID, refundID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, r.SiteID)
	})
	return r, err
}
