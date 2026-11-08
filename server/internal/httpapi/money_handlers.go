package httpapi

import (
	"bytes"
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) bl() (*billing.Service, error) {
	if s.mny.Billing == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Billing, nil
}

func (s *apiServer) py() (*payments.Service, error) {
	if s.mny.Payments == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Payments, nil
}

func (s *apiServer) rp() (*reports.Service, error) {
	if s.mny.Reports == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Reports, nil
}

// ------------------------------------------------------------ charge plans

type planBody struct {
	SiteID          uuid.UUID    `json:"site_id"`
	Name            string       `json:"name"`
	Kind            string       `json:"kind"`
	ChargeType      string       `json:"charge_type"`
	DefaultDebtor   string       `json:"default_debtor"`
	Frequency       string       `json:"frequency"`
	DueDay          int          `json:"due_day"`
	Start           string       `json:"start"`
	End             *string      `json:"end"`
	AmountMode      string       `json:"amount_mode"`
	AllocationKeyID *uuid.UUID   `json:"allocation_key_id"`
	Amount          *money.Money `json:"amount"`
	Scope           struct {
		Type string     `json:"type"`
		ID   *uuid.UUID `json:"id"`
	} `json:"scope"`
	FundID          *uuid.UUID `json:"fund_id"`
	IncomeAccountID *uuid.UUID `json:"income_account_id"`
}

func planOut(p billing.Plan) map[string]any {
	scope := map[string]any{"type": "site", "id": nil}
	if len(p.Scope) > 6 && p.Scope[:6] == "block:" {
		scope = map[string]any{"type": "block", "id": p.Scope[6:]}
	}
	return map[string]any{"id": p.ID, "org_id": p.OrgID, "site_id": p.SiteID, "name": p.Name, "kind": p.Kind, "charge_type": p.ChargeType, "default_debtor": p.DefaultDebtor,
		"frequency": p.Frequency, "due_day": p.DueDay, "start": p.Start, "end": p.End, "amount_mode": p.AmountMode, "allocation_key_id": p.AllocationKeyID, "scope": scope,
		"fund_id": p.FundID, "income_account_id": p.IncomeAccountID, "status": p.Status, "current_rate": nil, "decision_id": nil, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt, "version": p.Version}
}

func (s *apiServer) MoneyListChargePlans(ctx context.Context, req gen.MoneyListChargePlansRequestObject) (gen.MoneyListChargePlansResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	items, next, err := b.ListPlans(ctx, req.OrgId, siteOf(req.Params.SiteId), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(items))
	for i, p := range items {
		rows[i] = planOut(p)
	}
	out, err := listOf[gen.ChargePlanList](rows, next)
	return gen.MoneyListChargePlans200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateChargePlan(ctx context.Context, req gen.MoneyCreateChargePlanRequestObject) (gen.MoneyCreateChargePlanResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	pb, err := conv[planBody](req.Body)
	if err != nil {
		return nil, err
	}
	in := billing.PlanInput{SiteID: pb.SiteID, Name: pb.Name, Kind: pb.Kind, ChargeType: pb.ChargeType, DefaultDebtor: pb.DefaultDebtor, Frequency: pb.Frequency, DueDay: pb.DueDay,
		Start: pb.Start, End: pb.End, AmountMode: pb.AmountMode, AllocationKeyID: pb.AllocationKeyID, FundID: pb.FundID, IncomeAccountID: pb.IncomeAccountID, Scope: "site"}
	switch pb.Scope.Type {
	case "", "site":
	case "block":
		if pb.Scope.ID == nil {
			return nil, problem.Validation(problem.Field{Field: "scope.id", Code: "required"})
		}
		in.Scope = "block:" + pb.Scope.ID.String()
	default:
		return nil, problem.Validation(problem.Field{Field: "scope.type", Code: "unsupported"})
	}
	if pb.Amount != nil {
		in.Amount = &pb.Amount.Amount
	}
	v, err := b.CreatePlan(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ChargePlan](planOut(v))
	return gen.MoneyCreateChargePlan201JSONResponse(out), err
}

// ------------------------------------------------------------ charges

func (s *apiServer) MoneyListCharges(ctx context.Context, req gen.MoneyListChargesRequestObject) (gen.MoneyListChargesResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := billing.ChargeFilter{UnitID: p.UnitId, DebtorKind: deref(p.DebtorKind), PersonID: p.PersonId, PlanID: p.PlanId, Status: deref(p.Status), ChargeType: deref(p.ChargeType),
		From: dateStr(p.DueFrom), To: dateStr(p.DueTo)}
	if p.Overdue != nil {
		f.Overdue = *p.Overdue
	}
	if p.Side != nil && f.DebtorKind == "" {
		f.DebtorKind = *p.Side
	}
	items, next, err := b.ListCharges(ctx, req.OrgId, siteOf(p.SiteId), f, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.ChargeList](items, next)
	return gen.MoneyListCharges200JSONResponse(out), err
}

// ------------------------------------------------------------ payments

type paymentBody struct {
	SiteID         uuid.UUID      `json:"site_id"`
	Debtor         billing.Debtor `json:"debtor"`
	PayerPersonID  *uuid.UUID     `json:"payer_person_id"`
	Method         string         `json:"method"`
	Amount         money.Money    `json:"amount"`
	ReceivedOn     string         `json:"received_on"`
	AssetAccountID uuid.UUID      `json:"asset_account_id"`
	Reference      *string        `json:"reference"`
	Allocations    []struct {
		ChargeID uuid.UUID `json:"charge_id"`
	} `json:"allocations"`
}

func (s *apiServer) MoneyCreatePayment(ctx context.Context, req gen.MoneyCreatePaymentRequestObject) (gen.MoneyCreatePaymentResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	b, err := conv[paymentBody](req.Body)
	if err != nil {
		return nil, err
	}
	in := payments.Input{SiteID: b.SiteID, Debtor: b.Debtor, PayerPersonID: b.PayerPersonID, Method: b.Method, Amount: b.Amount, ReceivedOn: b.ReceivedOn, AssetAccountID: &b.AssetAccountID, Reference: b.Reference}
	for _, a := range b.Allocations {
		in.ChargeIDs = append(in.ChargeIDs, a.ChargeID)
	}
	v, err := p.Record(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Payment](v)
	return gen.MoneyCreatePayment201JSONResponse(out), err
}

func (s *apiServer) MoneyListPayments(ctx context.Context, req gen.MoneyListPaymentsRequestObject) (gen.MoneyListPaymentsResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	q := req.Params
	f := payments.Filter{UnitID: q.UnitId, DebtorKind: deref(q.DebtorKind), PersonID: q.PersonId, Status: deref(q.Status), Method: deref(q.Method), ReceiptNo: deref(q.ReceiptNo), From: dateStr(q.From), To: dateStr(q.To)}
	items, next, err := p.List(ctx, req.OrgId, siteOf(q.SiteId), f, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.PaymentList](items, next)
	return gen.MoneyListPayments200JSONResponse(out), err
}

func (s *apiServer) MoneyGetPayment(ctx context.Context, req gen.MoneyGetPaymentRequestObject) (gen.MoneyGetPaymentResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	v, err := p.Get(ctx, req.OrgId, req.PaymentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Payment](v)
	return gen.MoneyGetPayment200JSONResponse(out), err
}

func (s *apiServer) MoneyVoidPayment(ctx context.Context, req gen.MoneyVoidPaymentRequestObject) (gen.MoneyVoidPaymentResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	reason := ""
	if req.Body != nil {
		reason = req.Body.Reason
	}
	v, err := p.Void(ctx, req.OrgId, req.PaymentId, reason)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Payment](v)
	return gen.MoneyVoidPayment200JSONResponse(out), err
}

// ------------------------------------------------------------ opening balances

type openingBody struct {
	SiteID     uuid.UUID    `json:"site_id"`
	AsOf       string       `json:"as_of"`
	Kind       string       `json:"kind"`
	UnitID     *uuid.UUID   `json:"unit_id"`
	DebtorKind string       `json:"debtor_kind"`
	AccountID  *uuid.UUID   `json:"account_id"`
	Principal  money.Money  `json:"principal"`
	LateFee    *money.Money `json:"late_fee"`
}

func openingOut(o billing.OpeningBalance, cur money.Code, lockDate *string) map[string]any {
	status := "active"
	if o.Voided {
		status = "voided"
	}
	late := money.New(o.LateFee, cur)
	return map[string]any{"id": o.ID, "org_id": nil, "site_id": o.SiteID, "as_of": o.AsOf, "kind": o.Kind, "unit_id": o.UnitID, "debtor_kind": o.DebtorKind, "account_id": o.AccountID,
		"person_id": nil, "vendor_id": nil, "principal": money.New(o.Principal, cur), "late_fee": late, "journal_entry_id": o.JournalEntryID,
		"locked": lockDate != nil && o.AsOf <= *lockDate, "status": status, "void_reason": nil, "voided_at": nil, "created_at": o.CreatedAt, "updated_at": o.CreatedAt}
}

func (s *apiServer) openingList(ctx context.Context, orgID, siteID uuid.UUID, items []billing.OpeningBalance) ([]map[string]any, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	ld, err := l.GetLockDate(ctx, orgID, siteID)
	if err != nil {
		return nil, err
	}
	cur := money.Code("TRY")
	if len(items) > 0 {
		if c, err := s.moneySiteCurrency(ctx, orgID, siteID); err == nil {
			cur = c
		}
	}
	rows := make([]map[string]any, len(items))
	for i, o := range items {
		rows[i] = openingOut(o, cur, ld.LockDate)
		rows[i]["org_id"] = orgID
	}
	return rows, nil
}

func (s *apiServer) moneySiteCurrency(ctx context.Context, orgID, siteID uuid.UUID) (money.Code, error) {
	t, err := s.tn()
	if err != nil {
		return "", err
	}
	site, err := t.GetSite(ctx, orgID, siteID)
	return money.Code(site.Currency), err
}

func (s *apiServer) MoneyListOpeningBalances(ctx context.Context, req gen.MoneyListOpeningBalancesRequestObject) (gen.MoneyListOpeningBalancesResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	site := siteOf(req.Params.SiteId)
	items, next, err := b.ListOpeningBalances(ctx, req.OrgId, site, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	rows, err := s.openingList(ctx, req.OrgId, site, items)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.OpeningBalanceList](rows, next)
	return gen.MoneyListOpeningBalances200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateOpeningBalance(ctx context.Context, req gen.MoneyCreateOpeningBalanceRequestObject) (gen.MoneyCreateOpeningBalanceResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	ob, err := conv[openingBody](req.Body)
	if err != nil {
		return nil, err
	}
	in := billing.OpeningInput{SiteID: ob.SiteID, AsOf: ob.AsOf, Kind: ob.Kind, UnitID: ob.UnitID, DebtorKind: ob.DebtorKind, AccountID: ob.AccountID, Principal: ob.Principal.Amount}
	if ob.LateFee != nil {
		in.LateFee = ob.LateFee.Amount
	}
	dry := req.Params.DryRun != nil && *req.Params.DryRun
	v, err := b.CreateOpeningBalance(ctx, req.OrgId, in, dry)
	if err != nil {
		return nil, err
	}
	rows, err := s.openingList(ctx, req.OrgId, ob.SiteID, []billing.OpeningBalance{v})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.OpeningBalance](rows[0])
	return gen.MoneyCreateOpeningBalance201JSONResponse(out), err
}

// ------------------------------------------------------------ late fee policy

func (s *apiServer) lateFeePolicyOut(ctx context.Context, orgID, siteID uuid.UUID) (gen.LateFeePolicy, error) {
	b, err := s.bl()
	if err != nil {
		return gen.LateFeePolicy{}, err
	}
	rows, err := b.GetLateFeePolicy(ctx, orgID, siteID)
	if err != nil {
		return gen.LateFeePolicy{}, err
	}
	cur, err := s.moneySiteCurrency(ctx, orgID, siteID)
	if err != nil {
		return gen.LateFeePolicy{}, err
	}
	thr, err := b.WaiverThreshold(ctx, orgID, siteID)
	if err != nil {
		return gen.LateFeePolicy{}, err
	}
	rate, grace := 0.0, 0
	if n := len(rows); n > 0 {
		rate, grace = float64(rows[n-1].RateBPSPerMonth)/100, rows[n-1].GraceDays
	}
	return conv[gen.LateFeePolicy](map[string]any{"site_id": siteID, "rate_pct_per_month": rate, "max_rate_pct_per_month": 5, "grace_days": grace, "simple": true,
		"version": len(rows), "waiver_reauth_threshold": money.New(thr, cur)})
}

func (s *apiServer) MoneyGetLateFeePolicy(ctx context.Context, req gen.MoneyGetLateFeePolicyRequestObject) (gen.MoneyGetLateFeePolicyResponseObject, error) {
	out, err := s.lateFeePolicyOut(ctx, req.OrgId, siteOf(req.Params.SiteId))
	return gen.MoneyGetLateFeePolicy200JSONResponse(out), err
}

func (s *apiServer) MoneySetLateFeePolicy(ctx context.Context, req gen.MoneySetLateFeePolicyRequestObject) (gen.MoneySetLateFeePolicyResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	site := siteOf(req.Params.SiteId)
	st, err := s.siteToday(ctx, req.OrgId, site)
	if err != nil {
		return nil, err
	}
	bps := int(math.Round(float64(req.Body.RatePctPerMonth) * 100))
	if _, err := b.SetLateFeePolicy(ctx, req.OrgId, site, billing.PolicyRow{From: st, RateBPSPerMonth: bps, GraceDays: int(req.Body.GraceDays)}); err != nil {
		return nil, err
	}
	if v, err := req.Body.WaiverReauthThreshold.Get(); err == nil {
		if err := b.SetWaiverThreshold(ctx, req.OrgId, site, v.Amount); err != nil {
			return nil, err
		}
	}
	out, err := s.lateFeePolicyOut(ctx, req.OrgId, site)
	return gen.MoneySetLateFeePolicy200JSONResponse(out), err
}

func (s *apiServer) siteToday(ctx context.Context, orgID, siteID uuid.UUID) (string, error) {
	t, err := s.tn()
	if err != nil {
		return "", err
	}
	site, err := t.GetSite(ctx, orgID, siteID)
	if err != nil {
		return "", err
	}
	return ledger.Today(site.Timezone, time.Now()), nil
}

// ------------------------------------------------------------ reports and statements

func (s *apiServer) MoneyGetDebtorReport(ctx context.Context, req gen.MoneyGetDebtorReportRequestObject) (gen.MoneyGetDebtorReportResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	site := siteOf(req.Params.SiteId)
	rows, err := r.DebtorReport(ctx, req.OrgId, site, dateStr(req.Params.AsOf), 0)
	if err != nil {
		return nil, err
	}
	if f := req.Params.Format; f != nil && *f == gen.MoneyGetDebtorReportParamsFormatCsv {
		b, err := reports.DebtorCSV(rows)
		if err != nil {
			return nil, problem.Internal(err)
		}
		return gen.MoneyGetDebtorReport200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
	}
	if f := req.Params.Format; f != nil && *f != gen.MoneyGetDebtorReportParamsFormatJson {
		return nil, problem.Validation(problem.Field{Field: "format", Code: "unsupported"})
	}
	cur, err := s.moneySiteCurrency(ctx, req.OrgId, site)
	if err != nil {
		return nil, err
	}
	totalP, totalF := money.Zero(cur), money.Zero(cur)
	items := []map[string]any{}
	for _, d := range rows {
		totalP.Amount += d.Principal.Amount
		totalF.Amount += d.Fee.Amount
		if d.UnitID == nil {
			continue
		}
		items = append(items, map[string]any{"unit_id": d.UnitID, "unit_door_number": d.DoorNumber, "debtor_kind": d.DebtorKind, "debtor_person_id": d.PersonID, "debtor_display_name": d.PersonName,
			"principal": d.Principal, "late_fee": d.Fee, "total": d.Total, "months_overdue": d.DaysOverdue / 30, "in_collection": false})
	}
	asOf := dateStr(req.Params.AsOf)
	if asOf == "" {
		asOf, _ = s.siteToday(ctx, req.OrgId, site)
	}
	out, err := conv[gen.DebtorReport](map[string]any{"as_of": asOf, "rows": items, "total": money.New(totalP.Amount+totalF.Amount, cur), "total_principal": totalP, "total_late_fee": totalF})
	return gen.MoneyGetDebtorReport200JSONResponse(out), err
}

func (s *apiServer) MoneyGetUnitStatement(ctx context.Context, req gen.MoneyGetUnitStatementRequestObject) (gen.MoneyGetUnitStatementResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	var siteID uuid.UUID
	if t, err := s.tn(); err == nil {
		u, err := t.GetUnit(ctx, req.OrgId, req.UnitId)
		if err != nil {
			return nil, err
		}
		siteID = u.SiteID
	} else {
		return nil, err
	}
	unit := req.UnitId
	st, err := r.DebtorStatement(ctx, req.OrgId, siteID, billing.Debtor{UnitID: &unit, Kind: req.DebtorKind}, dateStr(req.Params.From), dateStr(req.Params.To))
	if err != nil {
		return nil, err
	}
	if f := req.Params.Format; f != nil {
		switch *f {
		case gen.MoneyGetUnitStatementParamsFormatCsv:
			b, err := st.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetUnitStatement200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetUnitStatementParamsFormatPdf:
			b := st.PDF()
			return gen.MoneyGetUnitStatement200ApplicationpdfResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetUnitStatementParamsFormatJson:
		default:
			return nil, problem.Validation(problem.Field{Field: "format", Code: "unsupported"})
		}
	}
	cur := st.Currency
	zero := money.Zero(cur)
	lines := []map[string]any{}
	for _, row := range st.Rows {
		debit, credit := zero, zero
		if row.Amount.Amount >= 0 {
			debit = row.Amount
		} else {
			credit = money.New(-row.Amount.Amount, cur)
		}
		principal, fee := zero, zero
		if row.Part == "fee" {
			fee = row.Amount
		} else {
			principal = row.Amount
		}
		lines = append(lines, map[string]any{"date": row.Date, "kind": row.Kind, "description": row.Ref, "debit": debit, "credit": credit, "principal": principal, "late_fee": fee, "balance": row.Balance.Total,
			"debtor_kind": req.DebtorKind, "debtor_display_name": nil, "ref_type": nil, "ref_id": nil})
	}
	from, to := st.AsOf, st.AsOf
	if st.From != nil {
		from = *st.From
	} else if len(st.Rows) > 0 {
		from = st.Rows[0].Date
	}
	if st.To != nil {
		to = *st.To
	}
	out, err := conv[gen.Statement](map[string]any{"debtor": map[string]any{"unit_id": req.UnitId, "debtor_kind": req.DebtorKind}, "from": from, "to": to, "opening_balance": st.Opening.Total,
		"closing_balance": st.Closing.Total, "accrued_late_fee": st.AccruedFee, "credit": st.Credit, "total_due": st.Closing.Total, "lines": lines})
	return gen.MoneyGetUnitStatement200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateCharge(ctx context.Context, req gen.MoneyCreateChargeRequestObject) (gen.MoneyCreateChargeResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[billing.ChargeInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := b.CreateCharge(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Charge](v)
	return gen.MoneyCreateCharge201JSONResponse(out), err
}
