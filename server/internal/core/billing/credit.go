package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/paymentalloc"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// creditSince is the date of the latest entry that raised the debtor's prepayment credit.
func creditSince(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ch ledger.Chart, d Debtor) (string, error) {
	f := d.LedgerFilter()
	var day *string
	err := tx.QueryRow(ctx, `SELECT max(l.entry_date)::text FROM journal_line l WHERE l.org_id=$1 AND l.site_id=$2 AND l.account_id=$3 AND l.credit > 0
		AND ($4::uuid IS NULL OR l.unit_id = $4) AND ($5 = '' OR l.debtor_kind = $5) AND ($6::uuid IS NULL OR l.person_id = $6)`,
		orgID, siteID, ch.Account(ledger.SubPrepayment), f.UnitID, f.DebtorKind, f.PersonID).Scan(&day)
	if err != nil || day == nil {
		return "", err
	}
	return *day, nil
}

// ApplyCredit consumes the debtor's prepayment credit against its open charges,
// oldest first, principal before fee (rule 9): debit prepayment, credit
// receivable, and an allocation without a payment. A charge is treated as paid
// from its due date, or from the day the credit arrived when that is later, so a
// late fee accrues only for the days the credit did not yet exist. It returns
// the amount applied.
func ApplyCredit(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, ch ledger.Chart, d Debtor, today string) (int64, error) {
	credit, err := CreditOf(ctx, tx, orgID, site.ID, ch, d)
	if err != nil || credit <= 0 {
		return 0, err
	}
	tz := ledger.Location(site.Timezone)
	cur := ledger.Currency(site)
	charges, err := LoadCharges(ctx, tx, orgID, site.ID, d)
	if err != nil {
		return 0, err
	}
	items, err := OpenItems(charges, cur, tz)
	if err != nil || len(items) == 0 {
		return 0, err
	}
	res, err := paymentalloc.Allocate(money.New(credit, cur), items, paymentalloc.OldestFirstPrincipalBeforeFee, tz)
	if err != nil || len(res.Allocations) == 0 {
		return 0, err
	}
	since, err := creditSince(ctx, tx, orgID, site.ID, ch, d)
	if err != nil {
		return 0, err
	}
	dueOf := map[uuid.UUID]string{}
	fundOf := map[uuid.UUID]uuid.UUID{}
	ids := make([]uuid.UUID, 0, len(res.Allocations))
	for _, a := range res.Allocations {
		ids = append(ids, a.ChargeID)
	}
	rows, err := tx.Query(ctx, `SELECT id, due_date::text, fund_id FROM charge WHERE org_id=$1 AND id = ANY($2)`, orgID, ids)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var cid, f uuid.UUID
		var due string
		if err := rows.Scan(&cid, &due, &f); err != nil {
			rows.Close()
			return 0, err
		}
		dueOf[cid], fundOf[cid] = due, f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	perFund := map[uuid.UUID]int64{}
	var order []uuid.UUID
	var total int64
	for _, a := range res.Allocations {
		f := fundOf[a.ChargeID]
		if _, ok := perFund[f]; !ok {
			order = append(order, f)
		}
		perFund[f] += a.Amount.Amount
		total += a.Amount.Amount
	}
	var lines []ledger.Line
	for _, f := range order {
		lines = append(lines,
			d.Line(ledger.Line{AccountID: ch.Account(ledger.SubPrepayment), FundID: f, Debit: perFund[f]}, nil),
			d.Line(ledger.Line{AccountID: ch.Account(d.ReceivableSubtype()), FundID: f, Credit: perFund[f]}, nil))
	}
	sid := id.New()
	if _, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: site.ID, EntryDate: today, Memo: "Prepayment credit applied", SourceType: "credit_apply", SourceID: &sid, Lines: lines}); err != nil {
		return 0, err
	}
	for _, a := range res.Allocations {
		on := max(dueOf[a.ChargeID], since)
		if on > today {
			on = today
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_allocation (id, org_id, site_id, payment_id, charge_id, amount, applied_on) VALUES ($1,$2,$3,NULL,$4,$5,$6::date)`,
			id.New(), orgID, site.ID, a.ChargeID, a.Amount.Amount, on); err != nil {
			return 0, err
		}
	}
	if _, err := RefreshStatus(ctx, tx, orgID, site.ID, ids); err != nil {
		return 0, err
	}
	return total, nil
}

// LockOpenCharges locks the debtor's charge rows for the rest of the transaction,
// so two postings against one debtor (payments, credit, generation, move-out) run one after the other.
func LockOpenCharges(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d Debtor) error {
	where, args := d.chargeWhere(3)
	rows, err := tx.Query(ctx, `SELECT c.id FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND `+where+` AND c.cancel_entry_id IS NULL ORDER BY c.id FOR UPDATE OF c`, append([]any{orgID, siteID}, args...)...)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}
