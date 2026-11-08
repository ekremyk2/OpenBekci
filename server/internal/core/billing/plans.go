package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/allocate"
	"github.com/openbekci/openbekci/server/internal/core/money/rates"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Plan is a charge plan.
type Plan struct {
	ID              uuid.UUID  `json:"id"`
	OrgID           uuid.UUID  `json:"org_id"`
	SiteID          uuid.UUID  `json:"site_id"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	ChargeType      string     `json:"charge_type"`
	DefaultDebtor   string     `json:"default_debtor"`
	Frequency       string     `json:"frequency"`
	DueDay          int        `json:"due_day"`
	Start           string     `json:"start"`
	End             *string    `json:"end"`
	AmountMode      string     `json:"amount_mode"`
	AllocationKey   *string    `json:"allocation_key"`
	AllocationKeyID *uuid.UUID `json:"allocation_key_id"`
	Scope           string     `json:"scope_ref"`
	FundID          uuid.UUID  `json:"fund_id"`
	IncomeAccountID *uuid.UUID `json:"income_account_id"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Version         int        `json:"version"`
}

const planCols = `id, org_id, site_id, name, kind, charge_type, default_debtor, frequency, due_day, start_on::text, end_on::text, amount_mode, allocation_key, (SELECT k.id FROM allocation_key k WHERE k.org_id = charge_plan.org_id AND k.site_id = charge_plan.site_id AND k.code = charge_plan.allocation_key) AS allocation_key_id, scope, fund_id, income_account_id, status, created_at, updated_at, version`

func scanPlan(r pgx.Row) (p Plan, err error) {
	err = r.Scan(&p.ID, &p.OrgID, &p.SiteID, &p.Name, &p.Kind, &p.ChargeType, &p.DefaultDebtor, &p.Frequency, &p.DueDay, &p.Start, &p.End, &p.AmountMode,
		&p.AllocationKey, &p.AllocationKeyID, &p.Scope, &p.FundID, &p.IncomeAccountID, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return
}

// PlanInput creates a plan. Rates may be given inline (the first rate starts at Start).
type PlanInput struct {
	SiteID          uuid.UUID  `json:"site_id"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	ChargeType      string     `json:"charge_type"`
	DefaultDebtor   string     `json:"default_debtor"`
	Frequency       string     `json:"frequency"`
	DueDay          int        `json:"due_day"`
	Start           string     `json:"start"`
	End             *string    `json:"end"`
	AmountMode      string     `json:"amount_mode"`
	AllocationKey   *string    `json:"allocation_key"`
	AllocationKeyID *uuid.UUID `json:"allocation_key_id"`
	Scope           string     `json:"scope"`
	FundID          *uuid.UUID `json:"fund_id"`
	IncomeAccountID *uuid.UUID `json:"income_account_id"`
	Amount          *int64     `json:"amount"`
}

func Ptr[T any](v T) *T { return &v }

func in(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

func (p *PlanInput) normalize() error {
	if p.Kind == "" {
		p.Kind = "recurring"
	}
	if p.ChargeType == "" {
		p.ChargeType = "dues"
	}
	if p.DefaultDebtor == "" {
		p.DefaultDebtor = "owner"
	}
	if p.Frequency == "" {
		p.Frequency = "monthly"
	}
	if p.DueDay == 0 {
		p.DueDay = 1
	}
	if p.AmountMode == "" {
		p.AmountMode = "per_unit"
	}
	if p.Scope == "" {
		p.Scope = "site"
	}
	var f []problem.Field
	chk := func(ok bool, field string) {
		if !ok {
			f = append(f, problem.Field{Field: field, Code: "invalid_value"})
		}
	}
	chk(len([]rune(p.Name)) >= 1 && len([]rune(p.Name)) <= 200, "name")
	chk(in(p.Kind, "recurring", "one_off"), "kind")
	chk(in(p.ChargeType, "dues", "special_assessment", "reserve_contribution", "utility", "booking_fee", "legal_cost", "deposit", "adjustment", "settlement"), "charge_type")
	chk(in(p.DefaultDebtor, "owner", "tenant"), "default_debtor")
	chk(in(p.Frequency, "monthly", "quarterly", "yearly"), "frequency")
	chk(p.DueDay >= 1 && p.DueDay <= 31, "due_day")
	chk(in(p.AmountMode, "per_unit", "total_allocated"), "amount_mode")
	chk(p.AmountMode == "per_unit" || (p.AllocationKey != nil && *p.AllocationKey != ""), "allocation_key")
	chk(p.Scope == "site" || strings.HasPrefix(p.Scope, "block:") || strings.HasPrefix(p.Scope, "tag:"), "scope")
	if _, err := ledger.ParseDate(p.Start); err != nil {
		f = append(f, problem.Field{Field: "start", Code: "invalid_format"})
	}
	if p.End != nil {
		if _, err := ledger.ParseDate(*p.End); err != nil || *p.End < p.Start {
			f = append(f, problem.Field{Field: "end", Code: "invalid_value"})
		}
	}
	if p.Amount != nil && *p.Amount < 0 {
		f = append(f, problem.Field{Field: "amount", Code: "invalid_value"})
	}
	if len(f) > 0 {
		return problem.Validation(f...)
	}
	return nil
}

// CreatePlan adds a plan (money.manage), with a first rate when Amount is given.
func (s *Service) CreatePlan(ctx context.Context, orgID uuid.UUID, in PlanInput) (Plan, error) {
	if in.AllocationKeyID != nil && in.AllocationKey == nil && in.AmountMode == "" {
		in.AmountMode = "total_allocated"
	}
	if in.AllocationKeyID != nil && in.AllocationKey == nil {
		in.AllocationKey = Ptr("pending")
	}
	if err := in.normalize(); err != nil {
		return Plan{}, err
	}
	var out Plan
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, in.SiteID); err != nil {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if in.AllocationKeyID != nil {
			var code string
			if err := tx.QueryRow(ctx, `SELECT code FROM allocation_key WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, *in.AllocationKeyID).Scan(&code); err != nil {
				return bad("allocation_key_id", "invalid_value")
			}
			in.AllocationKey = &code
		}
		fund := ch.OperatingFund
		if in.FundID != nil {
			fund = *in.FundID
		} else if in.ChargeType == "reserve_contribution" {
			fund = ch.Funds[ledger.FundReserve]
		}
		if out, err = scanPlan(tx.QueryRow(ctx, `INSERT INTO charge_plan (id, org_id, site_id, name, kind, charge_type, default_debtor, frequency, due_day, start_on, end_on, amount_mode, allocation_key, scope, fund_id, income_account_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11::date,$12,$13,$14,$15,$16) RETURNING `+planCols,
			id.New(), orgID, in.SiteID, in.Name, in.Kind, in.ChargeType, in.DefaultDebtor, in.Frequency, in.DueDay, in.Start, in.End, in.AmountMode, in.AllocationKey, in.Scope, fund, in.IncomeAccountID)); err != nil {
			return err
		}
		if in.Amount != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO charge_plan_rate (id, org_id, site_id, plan_id, from_on, amount) VALUES ($1,$2,$3,$4,$5::date,$6)`, id.New(), orgID, in.SiteID, out.ID, in.Start, *in.Amount); err != nil {
				return err
			}
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "charge_plan.create", "charge_plan", out.ID, nil, out)
	})
	return out, err
}

// ListPlans lists a site's plans (money.read).
func (s *Service) ListPlans(ctx context.Context, orgID, siteID uuid.UUID, p ledger.PageParams) ([]Plan, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Plan
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND site_id=$2 AND id > $3 ORDER BY id LIMIT $4`, orgID, siteID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			pl, err := scanPlan(rows)
			if err != nil {
				return err
			}
			out = append(out, pl)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(p Plan) uuid.UUID { return p.ID })
	return items, next, nil
}

// GetPlan returns a plan (money.read).
func (s *Service) GetPlan(ctx context.Context, orgID, planID uuid.UUID) (Plan, error) {
	var p Plan
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) (err error) {
		if p, err = scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2`, orgID, planID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, p.SiteID)
	})
	return p, err
}

// UpdatePlan changes a plan's name, end date or status (money.manage). Billing
// terms (key, scope, mode, frequency) are fixed once created: change them with a new plan.
func (s *Service) UpdatePlan(ctx context.Context, orgID, planID uuid.UUID, version int, d tenancy.Doc) (Plan, error) {
	var out Plan
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		before, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, planID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != version {
			return tenancy.ErrVersion
		}
		if u := d.Unknown("name", "end", "status"); len(u) > 0 {
			return problem.Validation(u...)
		}
		next := before
		for k, dst := range map[string]any{"name": &next.Name, "end": &next.End, "status": &next.Status} {
			if d.Has(k) {
				if err := d.Get(k, dst); err != nil {
					return err
				}
			}
		}
		if !in(next.Status, "active", "paused", "archived") || (next.End != nil && *next.End < next.Start) {
			return bad("status", "invalid_value")
		}
		if out, err = scanPlan(tx.QueryRow(ctx, `UPDATE charge_plan SET name=$3, end_on=$4::date, status=$5, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2 RETURNING `+planCols,
			orgID, planID, next.Name, next.End, next.Status)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, out.SiteID, "charge_plan.update", "charge_plan", planID, before, out)
	})
	return out, err
}

// Rate is an effective-dated amount of a plan.
type Rate struct {
	ID         uuid.UUID  `json:"id"`
	PlanID     uuid.UUID  `json:"plan_id"`
	From       string     `json:"from"`
	To         *string    `json:"to"`
	Amount     int64      `json:"amount"`
	DecisionID *uuid.UUID `json:"decision_id"`
}

// RateInput adds a rate; the previous open-ended rate is closed the day before From.
type RateInput struct {
	From       string     `json:"from"`
	To         *string    `json:"to"`
	Amount     int64      `json:"amount"`
	DecisionID *uuid.UUID `json:"decision_id"`
}

// AddRate adds a rate to a plan (money.manage). Rates may not overlap (409).
func (s *Service) AddRate(ctx context.Context, orgID, planID uuid.UUID, in RateInput) (Rate, error) {
	if _, err := ledger.ParseDate(in.From); err != nil || in.Amount < 0 || (in.To != nil && *in.To < in.From) {
		return Rate{}, bad("from", "invalid_value")
	}
	var r Rate
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, planID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyManage, p.SiteID); err != nil {
			return err
		}
		// close an open-ended rate that started before the new one
		if _, err := tx.Exec(ctx, `UPDATE charge_plan_rate SET to_on = $3::date - 1 WHERE org_id=$1 AND plan_id=$2 AND to_on IS NULL AND from_on < $3::date`, orgID, planID, in.From); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 AND from_on <= COALESCE($4::date, 'infinity') AND COALESCE(to_on, 'infinity') >= $3::date`, orgID, planID, in.From, in.To).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problem.Conflict("the rate overlaps another rate of the plan")
		}
		r = Rate{ID: id.New(), PlanID: planID, From: in.From, To: in.To, Amount: in.Amount, DecisionID: in.DecisionID}
		if _, err := tx.Exec(ctx, `INSERT INTO charge_plan_rate (id, org_id, site_id, plan_id, from_on, to_on, amount, decision_id) VALUES ($1,$2,$3,$4,$5::date,$6::date,$7,$8)`,
			r.ID, orgID, p.SiteID, planID, in.From, in.To, in.Amount, in.DecisionID); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, p.SiteID, "charge_plan_rate.create", "charge_plan_rate", r.ID, nil, r)
	})
	return r, err
}

// ListRates lists a plan's rates by start date (money.read).
func (s *Service) ListRates(ctx context.Context, orgID, planID uuid.UUID) ([]Rate, error) {
	out := []Rate{}
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2`, orgID, planID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, p.SiteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, plan_id, from_on::text, to_on::text, amount, decision_id FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 ORDER BY from_on`, orgID, planID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Rate
			if err := rows.Scan(&r.ID, &r.PlanID, &r.From, &r.To, &r.Amount, &r.DecisionID); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func loadSchedule(ctx context.Context, tx pgx.Tx, orgID, planID uuid.UUID, cur money.Code, tz *time.Location) (rates.Schedule, error) {
	rows, err := tx.Query(ctx, `SELECT from_on::text, to_on::text, amount, decision_id FROM charge_plan_rate WHERE org_id=$1 AND plan_id=$2 ORDER BY from_on`, orgID, planID)
	if err != nil {
		return rates.Schedule{}, err
	}
	defer rows.Close()
	var sc rates.Schedule
	for rows.Next() {
		var from string
		var to *string
		var amt int64
		var dec *uuid.UUID
		if err := rows.Scan(&from, &to, &amt, &dec); err != nil {
			return sc, err
		}
		r := rates.Rate{Amount: money.New(amt, cur), DecisionID: dec}
		r.From, _ = time.ParseInLocation(ledger.DateLayout, from, tz)
		if to != nil {
			t, _ := time.ParseInLocation(ledger.DateLayout, *to, tz)
			r.To = &t
		}
		sc.Rates = append(sc.Rates, r)
	}
	return sc, rows.Err()
}

// unitCharge is what a plan bills one unit for one period.
type unitCharge struct {
	UnitID   uuid.UUID
	Door     string
	Amount   int64
	Side     string
	PersonID *uuid.UUID
	Basis    json.RawMessage
}

// occupantOn returns the person of a unit side on a day: the correspondent,
// else the largest share, else the earliest occupancy.
func occupantOn(ctx context.Context, tx pgx.Tx, orgID, siteID, unitID uuid.UUID, side, day string) (*uuid.UUID, error) {
	var p uuid.UUID
	err := tx.QueryRow(ctx, `SELECT person_id FROM occupancy WHERE org_id=$1 AND site_id=$2 AND unit_id=$3 AND kind=$4 AND valid_from <= $5::date AND (valid_to IS NULL OR valid_to > $5::date)
		ORDER BY is_correspondent DESC, share_pct DESC NULLS LAST, valid_from, id LIMIT 1`, orgID, siteID, unitID, side, day).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &p, err
}

// planShares computes who is billed what for one period of a plan at a rate amount.
func (s *Service) planShares(ctx context.Context, tx pgx.Tx, site tenancy.Site, p Plan, per rates.Period, amount money.Money, dueDay string) ([]unitCharge, error) {
	q := `SELECT u.id, u.door_number, u.payer_overrides FROM unit u WHERE u.org_id=$1 AND u.site_id=$2 AND u.status='active' AND u.type <> 'common'`
	var onlyBlock *uuid.UUID
	var onlyTag string
	qa := []any{p.OrgID, p.SiteID}
	switch {
	case strings.HasPrefix(p.Scope, "block:"):
		b, err := uuid.Parse(strings.TrimPrefix(p.Scope, "block:"))
		if err != nil {
			return nil, bad("scope", "invalid_value")
		}
		onlyBlock = &b
		q += ` AND u.block_id = $3`
		qa = append(qa, b)
	case strings.HasPrefix(p.Scope, "tag:"):
		onlyTag = strings.TrimPrefix(p.Scope, "tag:")
		q += ` AND EXISTS (SELECT 1 FROM unit_tag t WHERE t.org_id = u.org_id AND t.unit_id = u.id AND t.tag = $3)`
		qa = append(qa, onlyTag)
	}
	rows, err := tx.Query(ctx, q+` ORDER BY u.id`, qa...)
	if err != nil {
		return nil, err
	}
	type unitRow struct {
		id   uuid.UUID
		door string
		po   map[string]string
	}
	var us []unitRow
	for rows.Next() {
		var u unitRow
		var raw []byte
		if err := rows.Scan(&u.id, &u.door, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(raw, &u.po)
		us = append(us, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	amounts := map[uuid.UUID]int64{}
	basis := map[uuid.UUID]json.RawMessage{}
	if p.AmountMode == "per_unit" {
		for _, u := range us {
			amounts[u.id] = amount.Amount
		}
	} else {
		w, err := s.tn.KeyWeights(ctx, p.OrgID, p.SiteID, *p.AllocationKey, onlyTag, onlyBlock)
		if err != nil {
			return nil, problem.Wrap(422, problem.CodeValidationFailed, "the allocation key is not usable for this plan", err)
		}
		var ws []allocate.Weight
		for _, u := range us {
			if r, ok := w[u.id]; ok {
				ws = append(ws, allocate.Weight{UnitID: u.id, Weight: r})
			} else {
				ws = append(ws, allocate.Weight{UnitID: u.id, Exempt: true})
			}
		}
		res, err := allocate.Allocate(amount, ws, allocate.ByID(), allocate.WithRotation(p.Name+"/"+per.Key))
		if err != nil {
			return nil, problem.Wrap(422, problem.CodeValidationFailed, "the plan cannot be allocated", err)
		}
		bj, _ := json.Marshal(res.Basis)
		for _, sh := range res.Shares {
			amounts[sh.UnitID] = sh.Amount.Amount
			basis[sh.UnitID] = bj
		}
	}
	var out []unitCharge
	for _, u := range us {
		a := amounts[u.id]
		if a == 0 {
			continue
		}
		side := tenancy.EffectivePayer(tenancy.Unit{PayerOverrides: u.po}, site, p.ChargeType, p.DefaultDebtor)
		person, err := occupantOn(ctx, tx, p.OrgID, p.SiteID, u.id, side, dueDay)
		if err != nil {
			return nil, err
		}
		if side == "tenant" && person == nil {
			side = "owner"
			if person, err = occupantOn(ctx, tx, p.OrgID, p.SiteID, u.id, side, dueDay); err != nil {
				return nil, err
			}
		}
		out = append(out, unitCharge{UnitID: u.id, Door: u.door, Amount: a, Side: side, PersonID: person, Basis: basis[u.id]})
	}
	return out, nil
}

// periodsOf enumerates the plan's periods due up to the given day.
func periodsOf(p Plan, upTo string, tz *time.Location) ([]rates.Period, error) {
	start, err := time.ParseInLocation(ledger.DateLayout, p.Start, tz)
	if err != nil {
		return nil, err
	}
	to, err := time.ParseInLocation(ledger.DateLayout, upTo, tz)
	if err != nil {
		return nil, err
	}
	if p.Kind == "one_off" {
		if start.After(to) {
			return nil, nil
		}
		return []rates.Period{{Key: "once", DueDate: start, Start: start, End: start}}, nil
	}
	rp := rates.Plan{Frequency: rates.Frequency(p.Frequency), Start: start, DueDay: p.DueDay}
	if p.End != nil {
		e, _ := time.ParseInLocation(ledger.DateLayout, *p.End, tz)
		rp.End = &e
	}
	ps, err := rates.Periods(rp, start, to, tz)
	if err != nil {
		return nil, err
	}
	var out []rates.Period
	for _, per := range ps {
		if !per.DueDate.After(to) {
			out = append(out, per)
		}
	}
	return out, nil
}

// PreviewItem is one unit of a plan preview.
type PreviewItem struct {
	UnitID          uuid.UUID   `json:"unit_id"`
	DoorNumber      string      `json:"door_number"`
	Amount          money.Money `json:"amount"`
	EffectiveDebtor Debtor      `json:"effective_debtor"`
}

// Preview is what a plan would bill for one period.
type Preview struct {
	Period  string        `json:"period"`
	DueDate string        `json:"due_date"`
	Total   money.Money   `json:"total"`
	Items   []PreviewItem `json:"items"`
}

// PreviewPlan shows, without writing, what the plan bills in the period that
// contains the given day (default: the next due period from today); the
// effective debtor of every unit is resolved the same way generation does (money.read).
func (s *Service) PreviewPlan(ctx context.Context, orgID, planID uuid.UUID, day string) (Preview, error) {
	var pv Preview
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2`, orgID, planID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, p.SiteID)
		if err != nil {
			return err
		}
		tz := ledger.Location(site.Timezone)
		if day == "" {
			day = ledger.Today(site.Timezone, s.Now())
		}
		end, _ := ledger.ParseDate(day)
		horizon := end.AddDate(0, 1, 0).Format(ledger.DateLayout) // include the next period
		pers, err := periodsOf(p, horizon, tz)
		if err != nil {
			return bad("start", "invalid_value")
		}
		var target *rates.Period
		for i := range pers {
			if pers[i].DueDate.Format(ledger.DateLayout) >= day {
				target = &pers[i]
				break
			}
		}
		if target == nil {
			return problem.Conflict("the plan has no period due on or after that day")
		}
		sc, err := loadSchedule(ctx, tx, orgID, planID, ledger.Currency(site), tz)
		if err != nil {
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
		pv = Preview{Period: target.Key, DueDate: due, Total: money.Zero(ledger.Currency(site)), Items: []PreviewItem{}}
		for _, sh := range shares {
			pv.Total.Amount += sh.Amount
			uid, side := sh.UnitID, sh.Side
			pv.Items = append(pv.Items, PreviewItem{UnitID: sh.UnitID, DoorNumber: sh.Door, Amount: money.New(sh.Amount, ledger.Currency(site)), EffectiveDebtor: Debtor{UnitID: &uid, Kind: side, PersonID: sh.PersonID}})
		}
		return nil
	})
	return pv, err
}
