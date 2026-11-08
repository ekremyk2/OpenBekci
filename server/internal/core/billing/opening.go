package billing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// OpeningBalance is an opening balance (M7): a unit side's carried debt (or
// credit), or the opening balance of a bank or cash account.
type OpeningBalance struct {
	ID             uuid.UUID  `json:"id"`
	SiteID         uuid.UUID  `json:"site_id"`
	AsOf           string     `json:"as_of"`
	Kind           string     `json:"kind"`
	UnitID         *uuid.UUID `json:"unit_id"`
	DebtorKind     *string    `json:"debtor_kind"`
	AccountID      *uuid.UUID `json:"account_id"`
	Principal      int64      `json:"principal"`
	LateFee        int64      `json:"late_fee"`
	JournalEntryID uuid.UUID  `json:"journal_entry_id"`
	Voided         bool       `json:"voided"`
	VoidedAt       *time.Time `json:"voided_at"`
	VoidReason     *string    `json:"void_reason"`
	CreatedAt      time.Time  `json:"created_at"`
}

// OpeningInput is one opening balance. Principal may be negative for a credit
// balance of a unit side (a prepayment).
type OpeningInput struct {
	SiteID     uuid.UUID  `json:"site_id"`
	AsOf       string     `json:"as_of"`
	Kind       string     `json:"kind"`
	UnitID     *uuid.UUID `json:"unit_id"`
	DebtorKind string     `json:"debtor_kind"`
	AccountID  *uuid.UUID `json:"account_id"`
	Principal  int64      `json:"principal"`
	LateFee    int64      `json:"late_fee"`
}

const obCols = `id, site_id, as_of::text, kind, unit_id, debtor_kind, account_id, principal, late_fee, journal_entry_id, voided_at IS NOT NULL, voided_at, void_reason, created_at`

func scanOB(r pgx.Row) (o OpeningBalance, err error) {
	err = r.Scan(&o.ID, &o.SiteID, &o.AsOf, &o.Kind, &o.UnitID, &o.DebtorKind, &o.AccountID, &o.Principal, &o.LateFee, &o.JournalEntryID, &o.Voided, &o.VoidedAt, &o.VoidReason, &o.CreatedAt)
	o.CreatedAt = o.CreatedAt.UTC()
	return
}

func (in OpeningInput) validate() error {
	if _, err := ledger.ParseDate(in.AsOf); err != nil {
		return bad("as_of", "invalid_format")
	}
	switch in.Kind {
	case "unit_receivable":
		if in.UnitID == nil || (in.DebtorKind != "owner" && in.DebtorKind != "tenant") {
			return bad("unit_id", "required")
		}
		if in.Principal == 0 && in.LateFee == 0 {
			return bad("principal", "invalid_value")
		}
		if in.LateFee < 0 || (in.Principal < 0 && in.LateFee > 0) {
			return bad("late_fee", "invalid_value")
		}
	case "bank", "cash":
		if in.AccountID == nil || in.Principal == 0 || in.LateFee != 0 {
			return bad("account_id", "required")
		}
	default:
		return bad("kind", "invalid_value")
	}
	return nil
}

// CreateOpeningBalance posts an opening balance against opening_equity
// (money.post). Real posting needs re-authentication; dryRun only validates
// and reports what would be posted. A date on or before the lock date is 409 locked_period.
func (s *Service) CreateOpeningBalance(ctx context.Context, orgID uuid.UUID, in OpeningInput, dryRun bool) (OpeningBalance, error) {
	if err := in.validate(); err != nil {
		return OpeningBalance{}, err
	}
	if !dryRun {
		if err := authz.PrincipalFromContext(ctx).RequireReauth(s.Now()); err != nil {
			return OpeningBalance{}, err
		}
	}
	var out OpeningBalance
	err := s.run(ctx, orgID, "opening balance", func(tx pgx.Tx) error {
		var err error
		out, err = s.postOpening(ctx, tx, orgID, in, nil, dryRun)
		return err
	})
	return out, err
}

func (s *Service) postOpening(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, in OpeningInput, importRow *uuid.UUID, dryRun bool) (OpeningBalance, error) {
	site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
	if err != nil {
		return OpeningBalance{}, err
	}
	if site.LockDate != nil && in.AsOf <= *site.LockDate {
		return OpeningBalance{}, ledger.Locked(in.AsOf, *site.LockDate)
	}
	ob := OpeningBalance{ID: id.New(), SiteID: in.SiteID, AsOf: in.AsOf, Kind: in.Kind, UnitID: in.UnitID, AccountID: in.AccountID, Principal: in.Principal, LateFee: in.LateFee, CreatedAt: s.Now().UTC()}
	if in.DebtorKind != "" {
		ob.DebtorKind = &in.DebtorKind
	}
	var entries []uuid.UUID
	if in.Kind == "unit_receivable" {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM unit WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, *in.UnitID).Scan(&n); err != nil {
			return ob, err
		}
		if n == 0 {
			return ob, bad("unit_id", "invalid_value")
		}
	} else {
		var typ, sub string
		if err := tx.QueryRow(ctx, `SELECT type, COALESCE(subtype, '') FROM account WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, *in.AccountID).Scan(&typ, &sub); err != nil {
			return ob, bad("account_id", "invalid_value")
		}
		if typ != "asset" || (in.Kind == "bank" && sub != ledger.SubBank) || (in.Kind == "cash" && sub != ledger.SubCashBox) {
			return ob, bad("account_id", "invalid_value")
		}
	}
	if dryRun {
		ob.JournalEntryID = uuid.Nil
		return ob, nil
	}
	ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
	if err != nil {
		return ob, err
	}
	equity := ch.Account(ledger.SubOpeningEquity)
	today := ledger.Today(site.Timezone, s.Now())
	switch in.Kind {
	case "unit_receivable":
		person, err := occupantOn(ctx, tx, orgID, in.SiteID, *in.UnitID, in.DebtorKind, in.AsOf)
		if err != nil {
			return ob, err
		}
		if in.Principal > 0 {
			cid, err := postCharge(ctx, tx, ch, chargeIn{OrgID: orgID, SiteID: in.SiteID, UnitID: *in.UnitID, FundID: ch.OperatingFund, Period: "opening", Kind: "principal", ChargeType: "opening",
				DebtorKind: in.DebtorKind, PersonID: person, Amount: in.Principal, DueDate: in.AsOf, EntryDate: in.AsOf, Description: "Opening balance", CreditAccount: equity, SourceType: "opening_balance"})
			if err != nil {
				return ob, err
			}
			var eid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT journal_entry_id FROM charge WHERE org_id=$1 AND id=$2`, orgID, cid).Scan(&eid); err != nil {
				return ob, err
			}
			entries = append(entries, eid)
		}
		if in.Principal < 0 {
			eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.AsOf, Memo: "Opening balance (credit)", SourceType: "opening_balance", Lines: []ledger.Line{
				{AccountID: equity, FundID: ch.OperatingFund, Debit: -in.Principal},
				{AccountID: ch.Account(ledger.SubPrepayment), FundID: ch.OperatingFund, Credit: -in.Principal, UnitID: in.UnitID, DebtorKind: in.DebtorKind, PersonID: person},
			}})
			if err != nil {
				return ob, err
			}
			entries = append(entries, eid)
		}
		if in.LateFee > 0 {
			cid, err := postCharge(ctx, tx, ch, chargeIn{OrgID: orgID, SiteID: in.SiteID, UnitID: *in.UnitID, FundID: ch.OperatingFund, Period: "opening", Kind: "fee", ChargeType: "late_fee",
				DebtorKind: in.DebtorKind, PersonID: person, Amount: in.LateFee, DueDate: in.AsOf, EntryDate: in.AsOf, Description: "Opening late fee", CreditAccount: equity, SourceType: "opening_balance"})
			if err != nil {
				return ob, err
			}
			var eid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT journal_entry_id FROM charge WHERE org_id=$1 AND id=$2`, orgID, cid).Scan(&eid); err != nil {
				return ob, err
			}
			entries = append(entries, eid)
		}
	default:
		lines := []ledger.Line{{AccountID: *in.AccountID, FundID: ch.OperatingFund}, {AccountID: equity, FundID: ch.OperatingFund}}
		if in.Principal > 0 {
			lines[0].Debit, lines[1].Credit = in.Principal, in.Principal
		} else {
			lines[0].Credit, lines[1].Debit = -in.Principal, -in.Principal
		}
		eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.AsOf, Memo: "Opening balance", SourceType: "opening_balance", Lines: lines})
		if err != nil {
			return ob, err
		}
		entries = append(entries, eid)
	}
	_ = today
	ob.JournalEntryID = entries[0]
	if _, err := tx.Exec(ctx, `INSERT INTO opening_balance (id, org_id, site_id, as_of, kind, unit_id, debtor_kind, account_id, principal, late_fee, journal_entry_id, entry_ids, import_row_id, created_by)
		VALUES ($1,$2,$3,$4::date,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		ob.ID, orgID, in.SiteID, in.AsOf, in.Kind, in.UnitID, ob.DebtorKind, in.AccountID, in.Principal, in.LateFee, ob.JournalEntryID, entries, importRow, ledger.Actor(ctx)); err != nil {
		return ob, err
	}
	return ob, ledger.Audit(ctx, tx, orgID, in.SiteID, "opening_balance.create", "opening_balance", ob.ID, nil, ob)
}

// VoidOpeningBalance reverses an opening balance (money.post, re-auth). It is
// idempotent: voiding a voided balance returns it unchanged. Locked periods and
// payments already applied to the opening charges refuse it (409).
func (s *Service) VoidOpeningBalance(ctx context.Context, orgID, obID uuid.UUID, reason string) (OpeningBalance, error) {
	if err := authz.PrincipalFromContext(ctx).RequireReauth(s.Now()); err != nil {
		return OpeningBalance{}, err
	}
	var out OpeningBalance
	err := s.run(ctx, orgID, "opening balance", func(tx pgx.Tx) error {
		o, err := scanOB(tx.QueryRow(ctx, `SELECT `+obCols+` FROM opening_balance WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, obID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, o.SiteID)
		if err != nil {
			return err
		}
		if o.Voided {
			out = o
			return nil
		}
		if site.LockDate != nil && o.AsOf <= *site.LockDate {
			return ledger.Locked(o.AsOf, *site.LockDate)
		}
		var entries []uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT entry_ids FROM opening_balance WHERE org_id=$1 AND id=$2`, orgID, obID).Scan(&entries); err != nil {
			return err
		}
		today := ledger.Today(site.Timezone, s.Now())
		var first uuid.UUID
		for _, e := range entries {
			var paid int64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(a.amount),0) FROM payment_allocation a JOIN charge c ON c.org_id=a.org_id AND c.id=a.charge_id WHERE a.org_id=$1 AND c.journal_entry_id=$2 AND NOT a.voided`, orgID, e).Scan(&paid); err != nil {
				return err
			}
			if paid > 0 {
				return problem.Conflict("payments are applied to this opening balance; void them first")
			}
			rid, err := ledger.Reverse(ctx, tx, orgID, o.SiteID, e, today, "Opening balance voided")
			if err != nil {
				return err
			}
			if first == uuid.Nil {
				first = rid
			}
			if _, err := tx.Exec(ctx, `UPDATE charge SET cancel_entry_id=$3, status='cancelled' WHERE org_id=$1 AND journal_entry_id=$2`, orgID, e, rid); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE opening_balance SET voided_at=now(), void_entry_id=$3, void_reason=$4 WHERE org_id=$1 AND id=$2`, orgID, obID, first, reason); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, o.SiteID, "opening_balance.void", "opening_balance", obID, map[string]any{"voided": false}, map[string]any{"voided": true}); err != nil {
			return err
		}
		out, err = scanOB(tx.QueryRow(ctx, `SELECT `+obCols+` FROM opening_balance WHERE org_id=$1 AND id=$2`, orgID, obID))
		return err
	})
	return out, err
}

// ListOpeningBalances lists a site's opening balances (money.read).
func (s *Service) ListOpeningBalances(ctx context.Context, orgID, siteID uuid.UUID, p ledger.PageParams) ([]OpeningBalance, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []OpeningBalance
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+obCols+` FROM opening_balance WHERE org_id=$1 AND site_id=$2 AND id > $3 ORDER BY id LIMIT $4`, orgID, siteID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOB(rows)
			if err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(o OpeningBalance) uuid.UUID { return o.ID })
	return items, next, nil
}

// GetOpeningBalance returns one (money.read).
func (s *Service) GetOpeningBalance(ctx context.Context, orgID, obID uuid.UUID) (OpeningBalance, error) {
	var o OpeningBalance
	err := s.run(ctx, orgID, "opening balance", func(tx pgx.Tx) (err error) {
		if o, err = scanOB(tx.QueryRow(ctx, `SELECT `+obCols+` FROM opening_balance WHERE org_id=$1 AND id=$2`, orgID, obID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, o.SiteID)
	})
	return o, err
}

// ImportReport summarizes an opening balance import.
type ImportReport struct {
	DryRun    bool     `json:"dry_run"`
	Rows      int      `json:"rows"`
	Posted    int      `json:"posted"`
	Skipped   int      `json:"skipped"`
	Errors    []string `json:"errors"`
	Principal int64    `json:"principal_total"`
}

// ImportOpeningBalances posts the opening columns that the spreadsheet import
// (phase B) validated into import_row.opening of an applied batch (money.post;
// the real run needs re-auth, a dry run does not). Rows already posted are skipped,
// so repeating the call is safe. Positive amounts are debts of the named side.
func (s *Service) ImportOpeningBalances(ctx context.Context, orgID, siteID, batchID uuid.UUID, dryRun bool) (ImportReport, error) {
	rep := ImportReport{DryRun: dryRun, Errors: []string{}}
	if !dryRun {
		if err := authz.PrincipalFromContext(ctx).RequireReauth(s.Now()); err != nil {
			return rep, err
		}
	}
	err := s.run(ctx, orgID, "import batch", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyPost, siteID); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM import_batch WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, siteID, batchID).Scan(&status); err != nil {
			return err
		}
		if status != "applied" {
			return problem.Conflict("only an applied import batch carries opening balances")
		}
		rows, err := tx.Query(ctx, `SELECT id, unit_id, opening FROM import_row WHERE org_id=$1 AND batch_id=$2 AND opening IS NOT NULL AND unit_id IS NOT NULL ORDER BY row_no`, orgID, batchID)
		if err != nil {
			return err
		}
		type irow struct {
			id, unit uuid.UUID
			op       map[string]any
		}
		var rs []irow
		for rows.Next() {
			var r irow
			var raw []byte
			if err := rows.Scan(&r.id, &r.unit, &raw); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal(raw, &r.op)
			rs = append(rs, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range rs {
			rep.Rows++
			var have int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM opening_balance WHERE org_id=$1 AND import_row_id=$2 AND voided_at IS NULL`, orgID, r.id).Scan(&have); err != nil {
				return err
			}
			if have > 0 {
				rep.Skipped++
				continue
			}
			amt, _ := r.op["amount_minor"].(float64)
			side, _ := r.op["side"].(string)
			asOf, _ := r.op["as_of"].(string)
			if side == "" || asOf == "" || amt == 0 {
				rep.Errors = append(rep.Errors, "row without side, date or amount: "+r.id.String())
				continue
			}
			u := r.unit
			rid := r.id
			if _, err := s.postOpening(ctx, tx, orgID, OpeningInput{SiteID: siteID, AsOf: asOf, Kind: "unit_receivable", UnitID: &u, DebtorKind: side, Principal: int64(amt)}, &rid, dryRun); err != nil {
				return err
			}
			rep.Posted++
			rep.Principal += int64(amt)
		}
		return nil
	})
	return rep, err
}
