// Package moneyhooks implements the money side of the cross-module hooks: what
// happens to a leaving person's open charges at move-out (S7, M1).
package moneyhooks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// MoveOut implements people.MoneyHooks.
// nowFn is the clock (tests may replace it).
var nowFn = time.Now

type MoveOut struct{}

var _ people.MoneyHooks = MoveOut{}

type open struct {
	id        uuid.UUID
	fund      uuid.UUID
	kind      string
	remaining int64
}

// OnMoveOut handles the leaving person's open charges in the move-out transaction:
//   - carry_to_person (default): the person's unpaid principal and the unit side's unpaid late fees
//     move to the person's own receivable (the charges become person debt, the journal moves the amounts);
//   - keep_on_unit: nothing moves, the debt stays with the unit side;
//   - write_off: the unpaid amounts are written off against other expenses.
//
// It returns the amount carried (or written off) in minor units.
func (MoveOut) OnMoveOut(ctx context.Context, tx pgx.Tx, e people.MoveOutEvent) (people.MoveOutMoney, error) {
	var cur string
	var tz string
	if err := tx.QueryRow(ctx, `SELECT currency, timezone FROM site WHERE org_id=$1 AND id=$2`, e.OrgID, e.SiteID).Scan(&cur, &tz); err != nil {
		return people.MoveOutMoney{}, err
	}
	res := people.MoveOutMoney{Currency: cur}
	if e.OpenCharges == "keep_on_unit" {
		return res, nil
	}
	ch, err := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
	if err != nil {
		return res, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id, c.fund_id, c.kind, c.amount - COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0)
		FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND c.unit_id=$3 AND c.debtor_kind=$4 AND c.cancel_entry_id IS NULL
		  AND ((c.kind = 'principal' AND c.debtor_person_id = $5) OR c.kind = 'fee') ORDER BY c.due_date, c.id`, e.OrgID, e.SiteID, e.UnitID, e.Side, e.PersonID)
	if err != nil {
		return res, err
	}
	var opens []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.fund, &o.kind, &o.remaining); err != nil {
			rows.Close()
			return res, err
		}
		if o.remaining > 0 {
			opens = append(opens, o)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	if len(opens) == 0 {
		return res, nil
	}
	unit := e.UnitID
	person := e.PersonID
	byFund := map[uuid.UUID]int64{}
	var funds []uuid.UUID
	for _, o := range opens {
		if _, ok := byFund[o.fund]; !ok {
			funds = append(funds, o.fund)
		}
		byFund[o.fund] += o.remaining
		res.CarriedMinor += o.remaining
	}
	var lines []ledger.Line
	for _, f := range funds {
		credit := ledger.Line{AccountID: ch.Account(ledger.ReceivableSub(e.Side)), FundID: f, Credit: byFund[f], UnitID: &unit, DebtorKind: e.Side}
		var debit ledger.Line
		if e.OpenCharges == "write_off" {
			debit = ledger.Line{AccountID: ch.AccountCode("5900"), FundID: f, Debit: byFund[f]}
		} else {
			debit = ledger.Line{AccountID: ch.Account(ledger.SubPersonReceivable), FundID: f, Debit: byFund[f], PersonID: &person, UnitID: &unit}
		}
		lines = append(lines, debit, credit)
	}
	var site struct{ lock *string }
	if err := tx.QueryRow(ctx, `SELECT lock_date::text FROM site WHERE org_id=$1 AND id=$2`, e.OrgID, e.SiteID).Scan(&site.lock); err != nil {
		return res, err
	}
	day := e.AsOf
	if site.lock != nil && day <= *site.lock {
		day = ledger.Today(tz, nowFn())
	}
	if _, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: e.OrgID, SiteID: e.SiteID, EntryDate: day, DocumentDate: e.AsOf, Memo: "Move-out: " + e.OpenCharges, SourceType: "move_out", SourceID: &e.MoveOutID, Lines: lines}); err != nil {
		return res, err
	}
	for _, o := range opens {
		if e.OpenCharges == "write_off" {
			if _, err := tx.Exec(ctx, `INSERT INTO payment_allocation (id, org_id, site_id, payment_id, charge_id, amount, applied_on) VALUES ($1,$2,$3,NULL,$4,$5,$6::date)`,
				id.New(), e.OrgID, e.SiteID, o.id, o.remaining, e.AsOf); err != nil {
				return res, err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE charge SET debtor_kind='person', debtor_person_id=$3 WHERE org_id=$1 AND id=$2`, e.OrgID, o.id, e.PersonID); err != nil {
			return res, err
		}
	}
	if e.OpenCharges == "write_off" {
		ids := make([]uuid.UUID, len(opens))
		for i, o := range opens {
			ids[i] = o.id
		}
		if _, err := billing.RefreshStatus(ctx, tx, e.OrgID, e.SiteID, ids); err != nil {
			return res, err
		}
	}
	return res, ledger.Audit(ctx, tx, e.OrgID, e.SiteID, "move_out.charges", "charge", e.MoveOutID, nil,
		map[string]any{"policy": e.OpenCharges, "carried_minor": res.CarriedMinor, "charges": len(opens)})
}
