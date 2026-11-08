package billing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/rates"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// PreviewItem is one unit of a plan preview.
type PreviewItem struct {
	UnitID            uuid.UUID       `json:"unit_id"`
	DoorNumber        string          `json:"door_number"`
	DebtorKind        string          `json:"debtor_kind"`
	DebtorPersonID    *uuid.UUID      `json:"debtor_person_id"`
	DebtorDisplayName *string         `json:"debtor_display_name"`
	Weight            float64         `json:"weight"`
	Amount            money.Money     `json:"amount"`
	EffectiveDebtor   EffectiveDebtor `json:"effective_debtor"`
}

// Preview is what a plan would bill for one period.
type Preview struct {
	Period               string        `json:"period"`
	DueDate              string        `json:"due_date"`
	Total                money.Money   `json:"total"`
	UnitCount            int           `json:"unit_count"`
	Items                []PreviewItem `json:"lines"`
	RemainderDistributed bool          `json:"remainder_distributed"`
}

// PreviewSpec says what to preview: a stored plan or a plan not created yet,
// optionally at another amount, in a period (default: the next one due).
type PreviewSpec struct {
	PlanID *uuid.UUID
	Inline *PlanInput
	Amount *int64
	Period string
	Day    string
}

// PreviewPlan shows what a stored plan bills in the period that contains the
// given day (default: the next due period from today), without writing (money.manage).
func (s *Service) PreviewPlan(ctx context.Context, orgID, planID uuid.UUID, day string) (Preview, error) {
	return s.Preview(ctx, orgID, PreviewSpec{PlanID: &planID, Day: day})
}

// Preview resolves the effective debtor of every unit the way generation does
// and shows the amounts, without writing anything (money.manage).
func (s *Service) Preview(ctx context.Context, orgID uuid.UUID, spec PreviewSpec) (Preview, error) {
	var pv Preview
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		var p Plan
		var err error
		switch {
		case spec.PlanID != nil:
			if p, err = scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2`, orgID, *spec.PlanID)); err != nil {
				return err
			}
		case spec.Inline != nil:
			in := *spec.Inline
			if in.AllocationKeyID != nil && in.AllocationKey == nil {
				if in.AmountMode == "" {
					in.AmountMode = "total_allocated"
				}
				var code string
				if err := tx.QueryRow(ctx, `SELECT code FROM allocation_key WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, *in.AllocationKeyID).Scan(&code); err != nil {
					return bad("plan.allocation_key_id", "invalid_value")
				}
				in.AllocationKey = &code
			}
			if err := in.normalize(); err != nil {
				return err
			}
			p = Plan{ID: id.New(), OrgID: orgID, SiteID: in.SiteID, Name: in.Name, Kind: in.Kind, ChargeType: in.ChargeType, DefaultDebtor: in.DefaultDebtor, Frequency: in.Frequency,
				DueDay: in.DueDay, Start: in.Start, End: in.End, AmountMode: in.AmountMode, AllocationKey: in.AllocationKey, Scope: in.Scope}
			if spec.Amount == nil {
				spec.Amount = in.Amount
			}
		default:
			return bad("plan_id", "required")
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, p.SiteID)
		if err != nil {
			return err
		}
		tz := ledger.Location(site.Timezone)
		cur := ledger.Currency(site)
		day := spec.Day
		if day == "" {
			day = ledger.Today(site.Timezone, s.Now())
		}
		end, err := ledger.ParseDate(day)
		if err != nil {
			return bad("day", "invalid_format")
		}
		horizon := end.AddDate(1, 0, 0).Format(ledger.DateLayout) // include the next periods
		pers, err := periodsOf(p, horizon, tz)
		if err != nil {
			return bad("start", "invalid_value")
		}
		var target *rates.Period
		for i := range pers {
			if (spec.Period != "" && pers[i].Key == spec.Period) || (spec.Period == "" && pers[i].DueDate.Format(ledger.DateLayout) >= day) {
				target = &pers[i]
				break
			}
		}
		if target == nil {
			return problem.Conflict("the plan has no such period")
		}
		var sc rates.Schedule
		if spec.Amount != nil {
			st, _ := time.ParseInLocation(ledger.DateLayout, p.Start, tz)
			sc = rates.Schedule{Rates: []rates.Rate{{From: st, Amount: money.New(*spec.Amount, cur)}}}
		} else if sc, err = loadSchedule(ctx, tx, orgID, p.ID, cur, tz); err != nil {
			return err
		}
		rate, ok := rates.ForPeriod(sc, *target, tz)
		if !ok {
			return problem.Conflict("no rate is in force for that period")
		}
		due := target.DueDate.In(tz).Format(ledger.DateLayout)
		shares, err := s.planShares(ctx, tx, site, p, *target, rate.Amount, due)
		if err != nil {
			return err
		}
		pv = Preview{Period: target.Key, DueDate: due, Total: money.Zero(cur), Items: []PreviewItem{}, UnitCount: len(shares), RemainderDistributed: p.AmountMode == "total_allocated"}
		for _, sh := range shares {
			pv.Total.Amount += sh.Amount
		}
		for _, sh := range shares {
			src := "plan_default"
			if sh.Side != p.DefaultDebtor {
				src = "unit_override"
			}
			var name *string
			if sh.PersonID != nil {
				var n string
				if err := tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id=$1 AND id=$2`, orgID, *sh.PersonID).Scan(&n); err == nil {
					name = &n
				}
			}
			w := 0.0
			if pv.Total.Amount != 0 {
				w = float64(sh.Amount) / float64(pv.Total.Amount)
			}
			pv.Items = append(pv.Items, PreviewItem{UnitID: sh.UnitID, DoorNumber: sh.Door, DebtorKind: sh.Side, DebtorPersonID: sh.PersonID, DebtorDisplayName: name, Weight: w,
				Amount: money.New(sh.Amount, cur), EffectiveDebtor: EffectiveDebtor{DebtorKind: sh.Side, PersonID: sh.PersonID, DisplayName: name, Source: src}})
		}
		return nil
	})
	return pv, err
}

// DeleteRate removes a rate that no charge has been generated under (money.manage);
// the open-ended rate before it is reopened. 409 once a period in its range was billed.
func (s *Service) DeleteRate(ctx context.Context, orgID, planID, rateID uuid.UUID) error {
	return s.run(ctx, orgID, "rate", func(tx pgx.Tx) error {
		p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, planID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyManage, p.SiteID); err != nil {
			return err
		}
		var from string
		var to *string
		if err := tx.QueryRow(ctx, `SELECT from_on::text, to_on::text FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 AND id=$3`, orgID, planID, rateID).Scan(&from, &to); err != nil {
			return err
		}
		var billed int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM charge WHERE org_id=$1 AND plan_id=$2 AND due_date >= $3::date AND ($4::date IS NULL OR due_date <= $4::date)`, orgID, planID, from, to).Scan(&billed); err != nil {
			return err
		}
		if billed > 0 {
			return problem.Conflict("charges were already generated under this rate")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 AND id=$3`, orgID, planID, rateID); err != nil {
			return err
		}
		if to == nil {
			if _, err := tx.Exec(ctx, `UPDATE charge_plan_rate SET to_on = NULL WHERE org_id=$1 AND plan_id=$2 AND to_on = $3::date - 1`, orgID, planID, from); err != nil {
				return err
			}
		}
		return ledger.Audit(ctx, tx, orgID, p.SiteID, "charge_plan_rate.delete", "charge_plan_rate", rateID, nil, map[string]any{"from": from})
	})
}
