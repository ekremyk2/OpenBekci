// Package moneyhooks implements the money side of the cross-module hooks: what
// happens to a leaving person's open charges at move-out (S7, M1).
package moneyhooks

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// MoveOut implements people.MoneyHooks.
type MoveOut struct{}

var _ people.MoneyHooks = MoveOut{}

type open struct {
	id        uuid.UUID
	fund      uuid.UUID
	kind      string
	amount    int64
	remaining int64 // amount minus what is applied; negative for a late fee reversal row
	net       int64 // for a fee: what is still owed after the reversals took off the oldest fees
}

// OnMoveOut handles the leaving person's open charges in the move-out transaction:
//   - carry_to_person (default): the person's unpaid principal and the unit side's unpaid late fees,
//     net of reversals and waivers, move to the person's own receivable. The fee accrued up to the
//     move-out date is booked first, and the side's fee bookings move with the charges, so nothing is
//     reversed or accrued twice afterwards;
//   - keep_on_unit: nothing moves, the debt stays with the unit side;
//   - write_off: the unpaid amounts are written off against other expenses.
//
// A move-out dated on or before the lock date is refused (409 locked_period).
// It returns the amount carried (or written off) in minor units.
func (MoveOut) OnMoveOut(ctx context.Context, tx pgx.Tx, e people.MoveOutEvent) (people.MoveOutMoney, error) {
	var cur string
	if err := tx.QueryRow(ctx, `SELECT currency FROM site WHERE org_id=$1 AND id=$2`, e.OrgID, e.SiteID).Scan(&cur); err != nil {
		return people.MoveOutMoney{}, err
	}
	res := people.MoveOutMoney{Currency: cur}
	if e.OpenCharges == "keep_on_unit" {
		return res, nil
	}
	side := billing.Debtor{UnitID: &e.UnitID, Kind: e.Side}
	if err := billing.LockOpenCharges(ctx, tx, e.OrgID, e.SiteID, side); err != nil {
		return res, err
	}
	// the leaver's fee up to the move-out date is booked while it still belongs to the unit side
	if err := billing.CloseLateFees(ctx, tx, e.OrgID, e.SiteID, side, e.AsOf); err != nil {
		return res, err
	}
	ch, err := ledger.EnsureChart(ctx, tx, e.OrgID, e.SiteID)
	if err != nil {
		return res, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id, c.fund_id, c.kind, c.amount, c.amount - COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0)
		FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND c.unit_id=$3 AND c.debtor_kind=$4 AND c.cancel_entry_id IS NULL
		  AND ((c.kind = 'principal' AND c.debtor_person_id = $5) OR c.kind = 'fee') ORDER BY c.due_date, c.id`, e.OrgID, e.SiteID, e.UnitID, e.Side, e.PersonID)
	if err != nil {
		return res, err
	}
	var all []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.fund, &o.kind, &o.amount, &o.remaining); err != nil {
			rows.Close()
			return res, err
		}
		all = append(all, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	// late fee reversal rows (negative) take off the oldest unpaid fees first, as billing.OpenItems does
	var reversed int64
	for _, o := range all {
		if o.kind == "fee" && o.amount < 0 {
			reversed += -o.amount
		}
	}
	var opens []open // what moves: unpaid principal, unpaid fee rows and the reversal rows
	for _, o := range all {
		switch {
		case o.kind == "fee" && o.amount < 0:
			opens = append(opens, o)
		case o.remaining > 0:
			if o.kind == "fee" {
				cut := min(reversed, o.remaining)
				reversed -= cut
				o.net = o.remaining - cut
			} else {
				o.net = o.remaining
			}
			opens = append(opens, o)
		}
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
		byFund[o.fund] += o.remaining // the reversal rows count with their negative remaining
	}
	var lines []ledger.Line
	for _, f := range funds {
		amt := byFund[f]
		if amt == 0 {
			continue
		}
		res.CarriedMinor += amt
		// a net amount cannot be negative here: a reversal never exceeds the unpaid fee
		if amt < 0 {
			return res, fmt.Errorf("move-out: the carried amount of fund %s is negative (%d)", f, amt)
		}
		credit := ledger.Line{AccountID: ch.Account(ledger.ReceivableSub(e.Side)), FundID: f, Credit: amt, UnitID: &unit, DebtorKind: e.Side}
		var debit ledger.Line
		if e.OpenCharges == "write_off" {
			debit = ledger.Line{AccountID: ch.AccountCode("5900"), FundID: f, Debit: amt}
		} else {
			debit = ledger.Line{AccountID: ch.Account(ledger.SubPersonReceivable), FundID: f, Debit: amt, PersonID: &person, UnitID: &unit}
		}
		lines = append(lines, debit, credit)
	}
	if len(lines) > 0 {
		// dated the move-out day; ledger.PostEntry refuses a day on or before the lock date
		if _, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: e.OrgID, SiteID: e.SiteID, EntryDate: e.AsOf, DocumentDate: e.AsOf, Memo: "Move-out: " + e.OpenCharges, SourceType: "move_out", SourceID: &e.MoveOutID, Lines: lines}); err != nil {
			return res, err
		}
	}
	for _, o := range opens {
		if e.OpenCharges == "write_off" {
			if o.net > 0 {
				if _, err := tx.Exec(ctx, `INSERT INTO payment_allocation (id, org_id, site_id, payment_id, charge_id, amount, applied_on) VALUES ($1,$2,$3,NULL,$4,$5,$6::date)`,
					id.New(), e.OrgID, e.SiteID, o.id, o.net, e.AsOf); err != nil {
					return res, err
				}
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
	} else if err := billing.MoveFeeBookings(ctx, tx, e.OrgID, e.SiteID, side, e.PersonID); err != nil {
		return res, err
	}
	return res, ledger.Audit(ctx, tx, e.OrgID, e.SiteID, "move_out.charges", "charge", e.MoveOutID, nil,
		map[string]any{"policy": e.OpenCharges, "carried_minor": res.CarriedMinor, "charges": len(opens)})
}
