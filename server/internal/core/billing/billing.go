// Package billing is the billing module (backend plan 4.5): charge plans with
// effective-dated rates, charge generation, charges, late fees, opening
// balances and the open-item view the payments and statements modules use.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/paymentalloc"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Service is the billing module.
type Service struct {
	db  ledger.TxRunner
	tn  *tenancy.Service
	Now func() time.Time
}

// NewService returns a Service; now nil means time.Now. tn supplies allocation key weights.
func NewService(db ledger.TxRunner, tn *tenancy.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, tn: tn, Now: now}
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

func bad(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// Debtor names who owes: one side of a unit, or a person (carried debt, S7).
type Debtor struct {
	UnitID   *uuid.UUID `json:"unit_id,omitempty"`
	Kind     string     `json:"debtor_kind,omitempty"`
	PersonID *uuid.UUID `json:"person_id,omitempty"`
}

// MarshalJSON writes the API's DebtorRef: a `mode` of unit_side or person and the members of that mode.
func (d Debtor) MarshalJSON() ([]byte, error) {
	if d.IsPerson() {
		return json.Marshal(struct {
			Mode     string     `json:"mode"`
			PersonID *uuid.UUID `json:"person_id"`
		}{"person", d.PersonID})
	}
	return json.Marshal(struct {
		Mode       string     `json:"mode"`
		UnitID     *uuid.UUID `json:"unit_id"`
		DebtorKind string     `json:"debtor_kind"`
	}{"unit_side", d.UnitID, d.Kind})
}

// IsPerson reports a person debtor.
func (d Debtor) IsPerson() bool { return d.PersonID != nil }

// Validate checks that exactly one mode is set.
func (d Debtor) Validate() error {
	switch {
	case d.PersonID != nil && d.UnitID == nil && d.Kind == "":
		return nil
	case d.PersonID == nil && d.UnitID != nil && (d.Kind == "owner" || d.Kind == "tenant"):
		return nil
	}
	return bad("debtor", "invalid_value")
}

// chargeWhere is the SQL condition selecting a debtor's charges (alias c) from parameter n on.
func (d Debtor) chargeWhere(n int) (string, []any) {
	if d.IsPerson() {
		return fmt.Sprintf(`c.debtor_kind = 'person' AND c.debtor_person_id = $%d`, n), []any{*d.PersonID}
	}
	return fmt.Sprintf(`c.unit_id = $%d AND c.debtor_kind = $%d`, n, n+1), []any{*d.UnitID, d.Kind}
}

// LedgerFilter is the journal filter of the debtor's receivable lines.
func (d Debtor) LedgerFilter() ledger.BalanceFilter {
	if d.IsPerson() {
		return ledger.BalanceFilter{PersonID: d.PersonID}
	}
	return ledger.BalanceFilter{UnitID: d.UnitID, DebtorKind: d.Kind}
}

// ReceivableSubtype is the account subtype holding the debtor's receivable.
func (d Debtor) ReceivableSubtype() string {
	if d.IsPerson() {
		return ledger.SubPersonReceivable
	}
	return ledger.ReceivableSub(d.Kind)
}

// Line returns a journal line dimension set for the debtor (and an optional unit for a person).
func (d Debtor) Line(l ledger.Line, personForUnit *uuid.UUID) ledger.Line {
	if d.IsPerson() {
		l.PersonID, l.UnitID = d.PersonID, nil
		return l
	}
	l.UnitID, l.DebtorKind, l.PersonID = d.UnitID, d.Kind, personForUnit
	return l
}

// Alloc is one applied part of a charge.
type Alloc struct {
	On     string
	Amount int64
}

// ChargeRow is a charge with what has been applied to it.
type ChargeRow struct {
	ID        uuid.UUID
	Kind      string
	Amount    int64
	DueDate   string
	Cancelled bool
	Allocs    []Alloc
	Paid      int64
}

// LoadCharges returns a debtor's charges ordered by due date with their (non-voided) allocations.
func LoadCharges(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d Debtor) ([]ChargeRow, error) {
	where, args := d.chargeWhere(3)
	rows, err := tx.Query(ctx, `SELECT c.id, c.kind, c.amount, c.due_date::text, c.cancel_entry_id IS NOT NULL FROM charge c
		WHERE c.org_id = $1 AND c.site_id = $2 AND `+where+` ORDER BY c.due_date, c.id`, append([]any{orgID, siteID}, args...)...)
	if err != nil {
		return nil, err
	}
	var out []ChargeRow
	idx := map[uuid.UUID]int{}
	var ids []uuid.UUID
	for rows.Next() {
		var c ChargeRow
		if err := rows.Scan(&c.ID, &c.Kind, &c.Amount, &c.DueDate, &c.Cancelled); err != nil {
			rows.Close()
			return nil, err
		}
		idx[c.ID] = len(out)
		ids = append(ids, c.ID)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return out, err
	}
	arows, err := tx.Query(ctx, `SELECT charge_id, applied_on::text, amount FROM payment_allocation WHERE org_id = $1 AND charge_id = ANY($2) AND NOT voided ORDER BY applied_on, id`, orgID, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var id uuid.UUID
		var a Alloc
		if err := arows.Scan(&id, &a.On, &a.Amount); err != nil {
			return nil, err
		}
		c := &out[idx[id]]
		c.Allocs = append(c.Allocs, a)
		c.Paid += a.Amount
	}
	return out, arows.Err()
}

// OpenItems builds the open items of a debtor for payment allocation: the
// unpaid principal of every charge and the unpaid late fees, with late-fee
// reversals (negative fee rows) taken off the oldest unpaid fees first.
func OpenItems(charges []ChargeRow, cur money.Code, tz *time.Location) ([]paymentalloc.OpenItem, error) {
	var items []paymentalloc.OpenItem
	var fees []int
	var reversed int64
	for _, c := range charges {
		if c.Cancelled {
			continue
		}
		due, err := time.ParseInLocation(ledger.DateLayout, c.DueDate, tz)
		if err != nil {
			return nil, err
		}
		switch {
		case c.Kind == "fee" && c.Amount < 0:
			reversed += -c.Amount
		case c.Kind == "fee":
			if rem := c.Amount - c.Paid; rem > 0 {
				fees = append(fees, len(items))
				items = append(items, paymentalloc.OpenItem{ChargeID: c.ID, DueDate: due, Kind: paymentalloc.Fee, Remaining: money.New(rem, cur)})
			}
		default:
			if rem := c.Amount - c.Paid; rem > 0 {
				items = append(items, paymentalloc.OpenItem{ChargeID: c.ID, DueDate: due, Kind: paymentalloc.Principal, Remaining: money.New(rem, cur)})
			}
		}
	}
	for _, i := range fees {
		if reversed == 0 {
			break
		}
		cut := min(reversed, items[i].Remaining.Amount)
		items[i].Remaining.Amount -= cut
		reversed -= cut
	}
	out := items[:0]
	for _, it := range items {
		if it.Remaining.Amount > 0 {
			out = append(out, it)
		}
	}
	return out, nil
}

// OpenTotal is Σ amount − Σ applied over the debtor's live charges (what the receivable line should show).
func OpenTotal(charges []ChargeRow) (principal, fee int64) {
	for _, c := range charges {
		if c.Cancelled {
			continue
		}
		if c.Kind == "fee" {
			fee += c.Amount - c.Paid
		} else {
			principal += c.Amount - c.Paid
		}
	}
	return
}

// CreditOf is the debtor's prepayment balance (credit − debit on the prepayment liability).
func CreditOf(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ch ledger.Chart, d Debtor) (int64, error) {
	deb, cred, err := ledger.Balance(ctx, tx, orgID, siteID, ch.Account(ledger.SubPrepayment), d.LedgerFilter())
	return cred - deb, err
}

// ReceivableOf is the debtor's receivable balance from the journal.
func ReceivableOf(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ch ledger.Chart, d Debtor) (int64, error) {
	deb, cred, err := ledger.Balance(ctx, tx, orgID, siteID, ch.Account(d.ReceivableSubtype()), d.LedgerFilter())
	return deb - cred, err
}

// RefreshStatus re-derives charge.status of the site's charges (all when ids is
// empty) from the allocations: paid, partially_paid, open; cancelled charges stay cancelled.
// Late-fee reversal rows are settled by definition.
func RefreshStatus(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ids []uuid.UUID) (int64, error) {
	ct, err := tx.Exec(ctx, `
		UPDATE charge c SET status = CASE
			WHEN c.cancel_entry_id IS NOT NULL THEN 'cancelled'
			WHEN c.amount <= 0 THEN 'paid'
			WHEN COALESCE(p.paid, 0) >= c.amount THEN 'paid'
			WHEN COALESCE(p.paid, 0) > 0 THEN 'partially_paid'
			ELSE 'open' END
		FROM charge c2 LEFT JOIN (SELECT charge_id, SUM(amount) AS paid FROM payment_allocation WHERE org_id = $1 AND NOT voided GROUP BY charge_id) p ON p.charge_id = c2.id
		WHERE c.org_id = $1 AND c.site_id = $2 AND c2.org_id = c.org_id AND c2.id = c.id AND (COALESCE(cardinality($3::uuid[]), 0) = 0 OR c.id = ANY($3))
		  AND c.status IS DISTINCT FROM CASE
			WHEN c.cancel_entry_id IS NOT NULL THEN 'cancelled'
			WHEN c.amount <= 0 THEN 'paid'
			WHEN COALESCE(p.paid, 0) >= c.amount THEN 'paid'
			WHEN COALESCE(p.paid, 0) > 0 THEN 'partially_paid'
			ELSE 'open' END`, orgID, siteID, ids)
	return ct.RowsAffected(), err
}

// RebuildStatus is the admin command: recompute every charge status of a site
// and report how many changed.
func (s *Service) RebuildStatus(ctx context.Context, orgID, siteID uuid.UUID) (changed int64, err error) {
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyManage, siteID); err != nil {
			return err
		}
		var err error
		changed, err = RefreshStatus(ctx, tx, orgID, siteID, nil)
		return err
	})
	return
}
