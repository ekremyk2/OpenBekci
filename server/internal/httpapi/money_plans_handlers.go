package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) planOutput(ctx context.Context, p billing.Plan) (map[string]any, error) {
	m := planOut(p)
	cur, err := s.moneySiteCurrency(ctx, p.OrgID, p.SiteID)
	if err != nil {
		return nil, err
	}
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	rates, err := b.ListRates(ctx, p.OrgID, p.ID)
	if err != nil {
		return nil, err
	}
	today, _ := s.siteToday(ctx, p.OrgID, p.SiteID)
	for i := len(rates) - 1; i >= 0; i-- {
		if r := rates[i]; r.From <= today && (r.To == nil || *r.To >= today) {
			m["current_rate"] = rateOut(p.OrgID, r, cur)
			break
		}
	}
	return m, nil
}

func rateOut(org uuid.UUID, r billing.Rate, cur money.Code) map[string]any {
	return map[string]any{"id": r.ID, "org_id": org, "plan_id": r.PlanID, "from": r.From, "to": r.To, "amount": money.New(r.Amount, cur), "decision_id": r.DecisionID,
		"created_at": r.CreatedAt, "updated_at": r.CreatedAt}
}

func (s *apiServer) MoneyGetChargePlan(ctx context.Context, req gen.MoneyGetChargePlanRequestObject) (gen.MoneyGetChargePlanResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	p, err := b.GetPlan(ctx, req.OrgId, req.PlanId)
	if err != nil {
		return nil, err
	}
	m, err := s.planOutput(ctx, p)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ChargePlan](m)
	return gen.MoneyGetChargePlan200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateChargePlan(ctx context.Context, req gen.MoneyUpdateChargePlanRequestObject) (gen.MoneyUpdateChargePlanResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	p, err := b.UpdatePlan(ctx, req.OrgId, req.PlanId, ver, d)
	if err != nil {
		return nil, err
	}
	m, err := s.planOutput(ctx, p)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ChargePlan](m)
	return gen.MoneyUpdateChargePlan200JSONResponse(out), err
}

func (s *apiServer) MoneyListChargePlanRates(ctx context.Context, req gen.MoneyListChargePlanRatesRequestObject) (gen.MoneyListChargePlanRatesResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	p, err := b.GetPlan(ctx, req.OrgId, req.PlanId)
	if err != nil {
		return nil, err
	}
	rates, err := b.ListRates(ctx, req.OrgId, req.PlanId)
	if err != nil {
		return nil, err
	}
	cur, err := s.moneySiteCurrency(ctx, req.OrgId, p.SiteID)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(rates))
	for i, r := range rates {
		rows[i] = rateOut(req.OrgId, r, cur)
	}
	out, err := listOf[gen.ChargePlanRateList](rows, "")
	return gen.MoneyListChargePlanRates200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateChargePlanRate(ctx context.Context, req gen.MoneyCreateChargePlanRateRequestObject) (gen.MoneyCreateChargePlanRateResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		From       string      `json:"from"`
		To         *string     `json:"to"`
		Amount     money.Money `json:"amount"`
		DecisionID *uuid.UUID  `json:"decision_id"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	p, err := b.GetPlan(ctx, req.OrgId, req.PlanId)
	if err != nil {
		return nil, err
	}
	cur, err := s.moneySiteCurrency(ctx, req.OrgId, p.SiteID)
	if err != nil {
		return nil, err
	}
	if body.Amount.Currency != cur {
		return nil, problem.Validation(problem.Field{Field: "amount", Code: "invalid_value"})
	}
	r, err := b.AddRate(ctx, req.OrgId, req.PlanId, billing.RateInput{From: body.From, To: body.To, Amount: body.Amount.Amount, DecisionID: body.DecisionID})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ChargePlanRate](rateOut(req.OrgId, r, cur))
	return gen.MoneyCreateChargePlanRate201JSONResponse(out), err
}

func (s *apiServer) MoneyDeleteChargePlanRate(ctx context.Context, req gen.MoneyDeleteChargePlanRateRequestObject) (gen.MoneyDeleteChargePlanRateResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if err := b.DeleteRate(ctx, req.OrgId, req.PlanId, req.RateId); err != nil {
		return nil, err
	}
	return gen.MoneyDeleteChargePlanRate204Response{}, nil
}

func planInputOf(pb planBody) (billing.PlanInput, error) {
	in := billing.PlanInput{SiteID: pb.SiteID, Name: pb.Name, Kind: pb.Kind, ChargeType: pb.ChargeType, DefaultDebtor: pb.DefaultDebtor, Frequency: pb.Frequency, DueDay: pb.DueDay,
		Start: pb.Start, End: pb.End, AmountMode: pb.AmountMode, AllocationKeyID: pb.AllocationKeyID, FundID: pb.FundID, IncomeAccountID: pb.IncomeAccountID, Scope: "site"}
	switch pb.Scope.Type {
	case "", "site":
	case "block":
		if pb.Scope.ID == nil {
			return in, problem.Validation(problem.Field{Field: "scope.id", Code: "required"})
		}
		in.Scope = "block:" + pb.Scope.ID.String()
	default:
		return in, problem.Validation(problem.Field{Field: "scope.type", Code: "unsupported"})
	}
	if pb.Amount != nil {
		in.Amount = &pb.Amount.Amount
	}
	return in, nil
}

func (s *apiServer) MoneyPreviewChargePlan(ctx context.Context, req gen.MoneyPreviewChargePlanRequestObject) (gen.MoneyPreviewChargePlanResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		PlanID *uuid.UUID   `json:"plan_id"`
		Plan   *planBody    `json:"plan"`
		Amount *money.Money `json:"amount"`
		Period string       `json:"period"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	if (body.PlanID == nil) == (body.Plan == nil) {
		return nil, problem.Validation(problem.Field{Field: "plan_id", Code: "exactly_one_of_plan_id_or_plan"})
	}
	spec := billing.PreviewSpec{PlanID: body.PlanID, Period: body.Period}
	if body.Plan != nil {
		in, err := planInputOf(*body.Plan)
		if err != nil {
			return nil, err
		}
		spec.Inline = &in
	}
	if body.Amount != nil {
		spec.Amount = &body.Amount.Amount
	}
	pv, err := b.Preview(ctx, req.OrgId, spec)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ChargePlanPreview](pv)
	return gen.MoneyPreviewChargePlan200JSONResponse(out), err
}

func (s *apiServer) MoneyGetChargeGenerationStatus(ctx context.Context, req gen.MoneyGetChargeGenerationStatusRequestObject) (gen.MoneyGetChargeGenerationStatusResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	st, err := b.ChargeGenerationStatus(ctx, req.OrgId, siteOf(req.Params.SiteId))
	if err != nil {
		return nil, err
	}
	pending := []map[string]any{}
	for _, p := range st {
		for _, per := range p.Missing {
			pending = append(pending, map[string]any{"plan_id": p.PlanID, "period": per, "units": p.Units})
		}
	}
	out, err := conv[gen.ChargeGenerationStatus](map[string]any{"up_to_date": len(pending) == 0, "last_run_at": nil, "next_run_at": nil, "pending": pending})
	return gen.MoneyGetChargeGenerationStatus200JSONResponse(out), err
}

func (s *apiServer) MoneyGetCharge(ctx context.Context, req gen.MoneyGetChargeRequestObject) (gen.MoneyGetChargeResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	c, err := b.GetCharge(ctx, req.OrgId, req.ChargeId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Charge](c)
	return gen.MoneyGetCharge200JSONResponse(out), err
}

func (s *apiServer) MoneyCancelCharge(ctx context.Context, req gen.MoneyCancelChargeRequestObject) (gen.MoneyCancelChargeResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	c, err := b.CancelCharge(ctx, req.OrgId, req.ChargeId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Charge](c)
	return gen.MoneyCancelCharge200JSONResponse(out), err
}
