package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/dashboards"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/reports"
)

// govMoney implements governance.DecisionEffects with the money module: adopting
// a budget finalizes it under the decision (treasury.Service.FinalizeBudget, in
// the apply transaction). The dues-rate and special-assessment effects stay
// unavailable (409 with the module's reason) until billing offers transaction
// scoped plan operations.
type govMoney struct {
	governance.DecisionEffectsUnavailable
	budgets money.BudgetFinalizer
}

// FinalizeBudget implements governance.DecisionEffects.
func (g govMoney) FinalizeBudget(ctx context.Context, tx pgx.Tx, d governance.DecisionRef, budgetID uuid.UUID) (governance.EffectRecord, error) {
	rec, err := g.budgets.FinalizeBudget(ctx, tx, money.DecisionRef{ID: d.DecisionID, SiteID: d.SiteID}, budgetID)
	if err != nil {
		return governance.EffectRecord{}, err
	}
	return governance.EffectRecord{RecordType: rec.Kind, RecordID: rec.ResourceID}, nil
}

// moneyFigures implements dashboards.MoneyFigures on the money tables: the
// site's receivables, overdue amounts, cash and bank and collection rate (the
// money overview), and what one person owes per unit side.
type moneyFigures struct{ reports *reports.Service }

var _ dashboards.MoneyFigures = moneyFigures{}

const remainingSQL = `c.amount - COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0)`

// SiteFigures implements dashboards.MoneyFigures.
func (m moneyFigures) SiteFigures(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, currency string, asOf time.Time) (dashboards.SiteMoney, error) {
	zero := dashboards.Money{Currency: currency}
	out := dashboards.SiteMoney{ReceivableOwner: zero, ReceivableTenant: zero, OverdueTotal: zero, Cash: zero, Bank: zero, AccruedLateFees: zero}
	ov, err := m.reports.Overview(ctx, orgID, siteID)
	if err != nil {
		return out, err
	}
	out.ReceivableOwner = dashboards.Money{Amount: ov.ReceivableOwner.Amount, Currency: currency}
	out.ReceivableTenant = dashboards.Money{Amount: ov.ReceivableTenant.Amount, Currency: currency}
	out.Cash = dashboards.Money{Amount: ov.Cash.Amount, Currency: currency}
	out.Bank = dashboards.Money{Amount: ov.Bank.Amount, Currency: currency}
	out.AccruedLateFees = dashboards.Money{Amount: ov.AccruedLateFees.Amount, Currency: currency}
	out.CollectionRatePct = ov.CollectionRatePct
	var overdue int64
	var units int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(r), 0), COUNT(DISTINCT unit_id) FROM (
			SELECT c.unit_id, `+remainingSQL+` AS r FROM charge c
			WHERE c.org_id = $1 AND c.site_id = $2 AND c.cancel_entry_id IS NULL AND c.due_date < $3::date) x WHERE r > 0`,
		orgID, siteID, asOf.Format("2006-01-02")).Scan(&overdue, &units); err != nil {
		return out, err
	}
	out.OverdueTotal, out.OverdueUnits = dashboards.Money{Amount: overdue, Currency: currency}, units
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM payment WHERE org_id = $1 AND site_id = $2 AND status = 'pending_declared'`, orgID, siteID).Scan(&out.PendingDeclaredPayments); err != nil {
		return out, err
	}
	return out, nil
}

// MySides implements dashboards.MoneyFigures: one entry per unit side the
// person lives on today (a household member only with money access).
func (m moneyFigures) MySides(ctx context.Context, tx pgx.Tx, orgID, personID uuid.UUID, siteID *uuid.UUID) ([]dashboards.MySide, error) {
	rows, err := tx.Query(ctx, `SELECT o.unit_id, u.door_number, o.side, s.currency, s.bank_details, o.site_id
		FROM occupancy o JOIN unit u ON u.org_id = o.org_id AND u.id = o.unit_id JOIN site s ON s.org_id = o.org_id AND s.id = o.site_id
		WHERE o.org_id = $1 AND o.person_id = $2 AND o.valid_from <= CURRENT_DATE AND (o.valid_to IS NULL OR o.valid_to > CURRENT_DATE)
		  AND (o.kind <> 'household' OR o.may_see_money) AND ($3::uuid IS NULL OR o.site_id = $3)
		ORDER BY u.door_number, o.side`, orgID, personID, siteID)
	if err != nil {
		return nil, err
	}
	type ref struct {
		unit       uuid.UUID
		door, side string
		cur        string
		bank       []byte
		site       uuid.UUID
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.unit, &r.door, &r.side, &r.cur, &r.bank, &r.site); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []dashboards.MySide
	for _, r := range refs {
		var principal, fee, credit int64
		var next *time.Time
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(r) FILTER (WHERE kind = 'principal'), 0), COALESCE(SUM(r) FILTER (WHERE kind = 'fee'), 0), MIN(due_date) FILTER (WHERE r > 0)
			FROM (SELECT c.kind, c.due_date, `+remainingSQL+` AS r FROM charge c
			WHERE c.org_id = $1 AND c.unit_id = $2 AND c.debtor_kind = $3 AND c.cancel_entry_id IS NULL) x WHERE r > 0`,
			orgID, r.unit, r.side).Scan(&principal, &fee, &next); err != nil {
			return nil, err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(p.net - COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = p.org_id AND a.payment_id = p.id AND NOT a.voided), 0)), 0)
			FROM payment p WHERE p.org_id = $1 AND p.unit_id = $2 AND p.debtor_kind = $3 AND p.status = 'confirmed'`, orgID, r.unit, r.side).Scan(&credit); err != nil {
			return nil, err
		}
		side := dashboards.MySide{
			UnitID: r.unit, UnitDoorNumber: r.door, DebtorKind: r.side,
			Principal:      dashboards.Money{Amount: principal, Currency: r.cur},
			AccruedLateFee: dashboards.Money{Amount: fee, Currency: r.cur},
			Total:          dashboards.Money{Amount: principal + fee, Currency: r.cur},
			Credit:         dashboards.Money{Amount: max(credit, 0), Currency: r.cur},
		}
		if next != nil {
			d := next.Format("2006-01-02")
			side.NextDueDate = &d
		}
		var bd map[string]any
		if json.Unmarshal(r.bank, &bd) == nil {
			side.BankDetails = bd
		}
		out = append(out, side)
	}
	return out, nil
}
