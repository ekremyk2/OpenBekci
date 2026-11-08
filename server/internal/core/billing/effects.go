package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/rates"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// The money side of the assembly decision effects (governance G5): a decision of
// kind dues_rate appends a rate period to a dues plan, a decision of kind
// special_assessment creates a one-off plan whose charges are allocated across
// the units by a key. Both run in the governance transaction (use them on a
// Service from WithTx) and carry the decision id: the rate in
// charge_plan_rate.decision_id, the assessment plan through the decision id of
// its single rate. Applying the same decision again returns the same record and
// creates nothing.

// Effect kinds in an EffectResult.
const (
	EffectRate = "charge_plan_rate"
	EffectPlan = "charge_plan"
)

// EffectResult is the record an effect created, or with a preview the changes it would make.
type EffectResult struct {
	RecordType string
	RecordID   uuid.UUID
	Preview    map[string]any
}

// DuesRateEffect is the dues-rate change a decision describes.
type DuesRateEffect struct {
	DecisionID uuid.UUID
	SiteID     uuid.UUID
	PlanID     uuid.UUID
	From       string
	Amount     money.Money
}

// SpecialAssessmentEffect is the special assessment a decision describes: the
// ChargePlanCreate of the API (kind one_off, charge_type special_assessment;
// `start` is the due date and `amount` is split by the allocation key, or is
// the per-unit amount with amount_mode per_unit).
type SpecialAssessmentEffect struct {
	DecisionID   uuid.UUID
	SiteID       uuid.UUID
	DecisionText string
	Plan         map[string]any
}

// effectContext lets a decision's effect write money records: the manager's
// decisions.manage was checked by the governance module, so money.manage and
// allocation.read are not asked again; the actor stays the caller (the audit
// trail names the person who applied the decision).
func effectContext(ctx context.Context, orgID uuid.UUID) context.Context {
	p := authz.PrincipalFromContext(ctx)
	sys := ledger.SystemContext(ctx, orgID)
	if p != nil {
		sys = authz.WithPrincipal(sys, p)
	}
	return sys
}

func conflictField(detail, field, code string, params map[string]any) error {
	return &problem.Error{Code: problem.CodeConflict, Status: http.StatusConflict, Detail: detail,
		Fields: []problem.Field{{Field: field, Code: code, Params: params}}}
}

// rateChange is a computed dues-rate change.
type rateChange struct {
	plan       Plan
	cur        money.Code
	existing   *Rate // the rate this decision already created
	closeID    *uuid.UUID
	closeFrom  string
	closeTo    string
	prevAmount int64
}

// ApplyDuesRate appends a rate period starting at the decision's effective date
// to a dues plan (the previous open-ended rate ends the day before). It refuses
// a date on or before the site's lock date (409 locked_period) and a date at or
// before a period the plan has already billed (409 conflict, field `from` code
// before_booked_period). With dryRun it computes the change and writes nothing.
func (s *Service) ApplyDuesRate(ctx context.Context, orgID uuid.UUID, e DuesRateEffect, dryRun bool) (EffectResult, error) {
	ctx = effectContext(ctx, orgID)
	var res EffectResult
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		ch, err := s.planRateChange(ctx, tx, orgID, e)
		if err != nil {
			return err
		}
		if ch.existing != nil {
			res = EffectResult{RecordType: EffectRate, RecordID: ch.existing.ID, Preview: ratePreview(e, ch)}
			return nil
		}
		res = EffectResult{RecordType: EffectRate, Preview: ratePreview(e, ch)}
		if dryRun {
			return nil
		}
		if ch.closeID != nil {
			if _, err := tx.Exec(ctx, `UPDATE charge_plan_rate SET to_on = $3::date WHERE org_id=$1 AND id=$2`, orgID, *ch.closeID, ch.closeTo); err != nil {
				return err
			}
		}
		r := Rate{ID: id.New(), PlanID: e.PlanID, From: e.From, Amount: e.Amount.Amount, DecisionID: &e.DecisionID, CreatedAt: s.Now().UTC()}
		if _, err := tx.Exec(ctx, `INSERT INTO charge_plan_rate (id, org_id, site_id, plan_id, from_on, amount, decision_id) VALUES ($1,$2,$3,$4,$5::date,$6,$7)`,
			r.ID, orgID, ch.plan.SiteID, e.PlanID, e.From, e.Amount.Amount, e.DecisionID); err != nil {
			return err
		}
		res.RecordID = r.ID
		return ledger.Audit(ctx, tx, orgID, ch.plan.SiteID, "charge_plan_rate.create", "charge_plan_rate", r.ID, nil, r)
	})
	return res, err
}

func ratePreview(e DuesRateEffect, ch rateChange) map[string]any {
	pv := map[string]any{
		"kind": "dues_rate", "plan_id": e.PlanID, "plan_name": ch.plan.Name, "from": e.From,
		"amount": e.Amount, "closes_previous_on": nil, "previous_amount": nil,
	}
	if ch.closeID != nil {
		pv["closes_previous_on"] = ch.closeTo
		pv["previous_amount"] = money.New(ch.prevAmount, ch.cur)
	}
	if ch.existing != nil {
		pv["already_applied"] = true
	}
	return pv
}

// planRateChange validates a dues-rate decision against the plan and its schedule.
func (s *Service) planRateChange(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, e DuesRateEffect) (rateChange, error) {
	var ch rateChange
	if e.PlanID == uuid.Nil {
		return ch, bad("plan_id", "required")
	}
	from, err := ledger.ParseDate(e.From)
	if err != nil {
		return ch, bad("from", "invalid_format")
	}
	if e.Amount.Amount < 0 {
		return ch, bad("amount", "invalid_value")
	}
	ch.plan, err = scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, e.PlanID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ch, bad("plan_id", "invalid_value")
	}
	if err != nil {
		return ch, err
	}
	p := ch.plan
	if p.SiteID != e.SiteID {
		return ch, problem.Validation(problem.Field{Field: "plan_id", Code: "invalid_value", Params: map[string]any{"reason": "plan_of_another_site"}})
	}
	if p.ChargeType != "dues" || p.Kind != "recurring" {
		return ch, problem.Validation(problem.Field{Field: "plan_id", Code: "invalid_value", Params: map[string]any{"reason": "not_a_recurring_dues_plan"}})
	}
	site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, p.SiteID)
	if err != nil {
		return ch, err
	}
	ch.cur = ledger.Currency(site)
	if e.Amount.Currency != ch.cur {
		return ch, problem.Validation(problem.Field{Field: "amount.currency", Code: "invalid_value", Params: map[string]any{"reason": "not_the_site_currency"}})
	}
	// already applied under this decision: the same record again
	var ex Rate
	err = tx.QueryRow(ctx, `SELECT id, plan_id, from_on::text, to_on::text, amount, decision_id, created_at FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 AND decision_id=$3`,
		orgID, e.PlanID, e.DecisionID).Scan(&ex.ID, &ex.PlanID, &ex.From, &ex.To, &ex.Amount, &ex.DecisionID, &ex.CreatedAt)
	switch {
	case err == nil:
		if ex.From != e.From || ex.Amount != e.Amount.Amount {
			return ch, problem.Conflict("this decision already set another rate on the plan")
		}
		ch.existing = &ex
		return ch, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return ch, err
	}
	var lock *string
	if err := tx.QueryRow(ctx, `SELECT lock_date::text FROM site WHERE org_id=$1 AND id=$2`, orgID, p.SiteID).Scan(&lock); err != nil {
		return ch, err
	}
	if lock != nil && e.From <= *lock {
		return ch, ledger.Locked(e.From, *lock)
	}
	var lastBooked *string
	if err := tx.QueryRow(ctx, `SELECT max(due_date)::text FROM charge WHERE org_id=$1 AND plan_id=$2 AND kind='principal' AND due_date >= $3::date`, orgID, e.PlanID, e.From).Scan(&lastBooked); err != nil {
		return ch, err
	}
	if lastBooked != nil {
		return ch, conflictField("the plan has already billed a period due on or after the new rate's date", "from", "before_booked_period", map[string]any{"booked_through": *lastBooked})
	}
	tz := ledger.Location(site.Timezone)
	sc, err := loadSchedule(ctx, tx, orgID, e.PlanID, ch.cur, tz)
	if err != nil {
		return ch, err
	}
	// the open-ended rate in force before the date ends the day before it
	if n := len(sc.Rates); n > 0 && sc.Rates[n-1].To == nil && sc.Rates[n-1].From.Before(from) {
		to := from.AddDate(0, 0, -1)
		sc.Rates[n-1].To = &to
		ch.closeTo = to.Format(ledger.DateLayout)
		ch.closeFrom = sc.Rates[n-1].From.Format(ledger.DateLayout)
		ch.prevAmount = sc.Rates[n-1].Amount.Amount
		var rid uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 AND to_on IS NULL AND from_on=$3::date`, orgID, e.PlanID, ch.closeFrom).Scan(&rid); err != nil {
			return ch, err
		}
		ch.closeID = &rid
	}
	sc.Rates = append(sc.Rates, rates.Rate{From: from, Amount: e.Amount, DecisionID: &e.DecisionID})
	if err := sc.Validate(tz, false); err != nil {
		return ch, conflictField("the rate does not fit the plan's rate schedule: "+err.Error(), "from", "rate_schedule_conflict", nil)
	}
	return ch, nil
}

// assessmentPlan is the ChargePlanCreate of a special assessment decision.
type assessmentPlan struct {
	SiteID          *uuid.UUID  `json:"site_id"`
	Name            string      `json:"name"`
	Kind            string      `json:"kind"`
	ChargeType      string      `json:"charge_type"`
	DefaultDebtor   string      `json:"default_debtor"`
	Start           string      `json:"start"`
	AmountMode      string      `json:"amount_mode"`
	AllocationKeyID *uuid.UUID  `json:"allocation_key_id"`
	Scope           *planScope  `json:"scope"`
	FundID          *uuid.UUID  `json:"fund_id"`
	IncomeAccount   *uuid.UUID  `json:"income_account_id"`
	Amount          money.Money `json:"amount"`
}

type planScope struct {
	Type string     `json:"type"`
	ID   *uuid.UUID `json:"id"`
}

func (e SpecialAssessmentEffect) input() (PlanInput, money.Money, error) {
	raw, err := json.Marshal(e.Plan)
	if err != nil {
		return PlanInput{}, money.Money{}, bad("plan", "invalid_value")
	}
	var p assessmentPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		return PlanInput{}, money.Money{}, bad("plan", "invalid_value")
	}
	var f []problem.Field
	if p.SiteID != nil && *p.SiteID != e.SiteID {
		f = append(f, problem.Field{Field: "plan.site_id", Code: "invalid_value", Params: map[string]any{"reason": "must_be_the_decisions_site"}})
	}
	if p.Kind != "" && p.Kind != "one_off" {
		f = append(f, problem.Field{Field: "plan.kind", Code: "invalid_value", Params: map[string]any{"reason": "a_special_assessment_is_one_off"}})
	}
	if p.ChargeType != "" && p.ChargeType != "special_assessment" {
		f = append(f, problem.Field{Field: "plan.charge_type", Code: "invalid_value", Params: map[string]any{"reason": "must_be_special_assessment"}})
	}
	if p.Amount.Amount <= 0 {
		f = append(f, problem.Field{Field: "plan.amount", Code: "invalid_value"})
	}
	if p.Start == "" {
		f = append(f, problem.Field{Field: "plan.start", Code: "required"})
	}
	mode := p.AmountMode
	if mode == "" {
		mode = "total_allocated"
	}
	if mode == "total_allocated" && p.AllocationKeyID == nil {
		f = append(f, problem.Field{Field: "plan.allocation_key_id", Code: "required"})
	}
	scope := "site"
	if p.Scope != nil {
		switch p.Scope.Type {
		case "", "site":
		case "block":
			if p.Scope.ID == nil {
				f = append(f, problem.Field{Field: "plan.scope.id", Code: "required"})
			} else {
				scope = "block:" + p.Scope.ID.String()
			}
		default:
			f = append(f, problem.Field{Field: "plan.scope.type", Code: "unsupported"})
		}
	}
	if len(f) > 0 {
		return PlanInput{}, money.Money{}, problem.Validation(f...)
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "Special assessment"
		if t := strings.TrimSpace(e.DecisionText); t != "" {
			name = t
		}
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	amount := p.Amount.Amount
	in := PlanInput{SiteID: e.SiteID, Name: name, Kind: "one_off", ChargeType: "special_assessment", DefaultDebtor: p.DefaultDebtor, Start: p.Start,
		AmountMode: mode, AllocationKeyID: p.AllocationKeyID, Scope: scope, FundID: p.FundID, IncomeAccountID: p.IncomeAccount, Amount: &amount}
	return in, p.Amount, nil
}

// ApplySpecialAssessment creates the one-off special assessment plan of the
// decision (its rate carries the decision id) and bills every unit its share:
// one charge per unit with charge_type special_assessment, allocated by the
// plan's key (the same allocation as plan generation), due on the plan's start
// date. It refuses a due date on or before the lock date (409 locked_period)
// and an assessment that bills nobody (422). Applying the decision again
// returns the same plan and creates nothing. With dryRun it returns the
// computed charges and writes nothing.
func (s *Service) ApplySpecialAssessment(ctx context.Context, orgID uuid.UUID, e SpecialAssessmentEffect, dryRun bool) (EffectResult, error) {
	ctx = effectContext(ctx, orgID)
	in, total, err := e.input()
	if err != nil {
		return EffectResult{}, err
	}
	var res EffectResult
	err = s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, e.SiteID)
		if err != nil {
			return err
		}
		if total.Currency != ledger.Currency(site) {
			return problem.Validation(problem.Field{Field: "plan.amount.currency", Code: "invalid_value", Params: map[string]any{"reason": "not_the_site_currency"}})
		}
		if _, err := ledger.ParseDate(in.Start); err != nil {
			return bad("plan.start", "invalid_format")
		}
		var existing uuid.UUID
		err = tx.QueryRow(ctx, `SELECT p.id FROM charge_plan p JOIN charge_plan_rate r ON r.org_id = p.org_id AND r.plan_id = p.id
			WHERE p.org_id=$1 AND p.site_id=$2 AND p.charge_type='special_assessment' AND r.decision_id=$3 LIMIT 1`, orgID, e.SiteID, e.DecisionID).Scan(&existing)
		switch {
		case err == nil:
			res = EffectResult{RecordType: EffectPlan, RecordID: existing, Preview: map[string]any{"kind": "special_assessment", "plan_id": existing, "already_applied": true}}
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		var lock *string
		if err := tx.QueryRow(ctx, `SELECT lock_date::text FROM site WHERE org_id=$1 AND id=$2`, orgID, e.SiteID).Scan(&lock); err != nil {
			return err
		}
		if lock != nil && in.Start <= *lock {
			return ledger.Locked(in.Start, *lock)
		}
		pv, err := s.Preview(ctx, orgID, PreviewSpec{Inline: &in, Period: "once", Day: in.Start})
		if err != nil {
			return err
		}
		if len(pv.Items) == 0 {
			return problem.New(http.StatusUnprocessableEntity, problem.CodeValidationFailed, "the assessment bills no unit under this allocation key")
		}
		res = EffectResult{RecordType: EffectPlan, Preview: assessmentPreview(in, pv)}
		if dryRun {
			return nil
		}
		plan, err := s.CreatePlan(ctx, orgID, in)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE charge_plan_rate SET decision_id=$3 WHERE org_id=$1 AND plan_id=$2`, orgID, plan.ID, e.DecisionID); err != nil {
			return err
		}
		// bill now, even when the due date is ahead (a one-off charge is posted
		// on its due date, as CreateCharge does); the catch-up job then finds it billed
		n, err := s.generatePlan(ctx, orgID, e.SiteID, plan.ID, in.Start)
		if err != nil {
			return err
		}
		if n == 0 {
			return problem.New(http.StatusUnprocessableEntity, problem.CodeValidationFailed, "the assessment bills no unit under this allocation key")
		}
		res.RecordID = plan.ID
		res.Preview["plan_id"] = plan.ID
		return ledger.Audit(ctx, tx, orgID, e.SiteID, "charge.special_assessment", "charge_plan", plan.ID, nil, map[string]any{"decision_id": e.DecisionID, "charges": n, "due_date": in.Start})
	})
	return res, err
}

func assessmentPreview(in PlanInput, pv Preview) map[string]any {
	lines := make([]map[string]any, 0, len(pv.Items))
	for _, it := range pv.Items {
		lines = append(lines, map[string]any{"unit_id": it.UnitID, "unit_door_number": it.DoorNumber, "debtor_kind": it.DebtorKind, "debtor_person_id": it.DebtorPersonID, "amount": it.Amount})
	}
	return map[string]any{
		"kind": "special_assessment", "name": in.Name, "charge_type": "special_assessment", "due_date": pv.DueDate,
		"amount_mode": in.AmountMode, "total": pv.Total, "unit_count": pv.UnitCount, "charges": lines,
	}
}
