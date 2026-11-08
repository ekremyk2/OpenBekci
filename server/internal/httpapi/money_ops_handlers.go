package httpapi

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ------------------------------------------------------------ opening balances

func (s *apiServer) MoneyGetOpeningBalance(ctx context.Context, req gen.MoneyGetOpeningBalanceRequestObject) (gen.MoneyGetOpeningBalanceResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	o, err := b.GetOpeningBalance(ctx, req.OrgId, req.OpeningBalanceId)
	if err != nil {
		return nil, err
	}
	rows, err := s.openingList(ctx, req.OrgId, o.SiteID, []billing.OpeningBalance{o})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.OpeningBalance](rows[0])
	return gen.MoneyGetOpeningBalance200JSONResponse(out), err
}

func (s *apiServer) MoneyVoidOpeningBalance(ctx context.Context, req gen.MoneyVoidOpeningBalanceRequestObject) (gen.MoneyVoidOpeningBalanceResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	o, err := b.VoidOpeningBalance(ctx, req.OrgId, req.OpeningBalanceId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	rows, err := s.openingList(ctx, req.OrgId, o.SiteID, []billing.OpeningBalance{o})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.OpeningBalance](rows[0])
	return gen.MoneyVoidOpeningBalance200JSONResponse(out), err
}

func (s *apiServer) MoneyImportOpeningBalances(ctx context.Context, req gen.MoneyImportOpeningBalancesRequestObject) (gen.MoneyImportOpeningBalancesResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	up, err := readUpload(req.Body)
	if err != nil {
		return nil, err
	}
	site, err := uuid.Parse(up.fields["site_id"])
	if err != nil {
		return nil, problem.Validation(problem.Field{Field: "site_id", Code: "invalid_format"})
	}
	if len(up.data) == 0 {
		return nil, problem.Validation(problem.Field{Field: "file", Code: "required"})
	}
	var mapping map[string]string
	if m := up.fields["mapping"]; m != "" {
		if err := json.Unmarshal([]byte(m), &mapping); err != nil {
			return nil, problem.Validation(problem.Field{Field: "mapping", Code: "invalid_format"})
		}
	}
	dry := req.Params.DryRun != nil && *req.Params.DryRun
	rep, err := b.ImportOpeningCSV(ctx, req.OrgId, site, up.fields["as_of"], up.data, mapping, dry)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ImportReport](rep)
	if err != nil {
		return nil, err
	}
	if dry || rep.ErrorRows > 0 {
		return gen.MoneyImportOpeningBalances200JSONResponse(out), nil
	}
	return gen.MoneyImportOpeningBalances202JSONResponse(out), nil
}

// ------------------------------------------------------------ late fees

func (s *apiServer) MoneyListLateFeeAccruals(ctx context.Context, req gen.MoneyListLateFeeAccrualsRequestObject) (gen.MoneyListLateFeeAccrualsResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := b.ListLateFeeAccruals(ctx, req.OrgId, siteOf(q.SiteId), q.UnitId, deref(q.DebtorKind), dateStr(q.AsOf), q.Nonzero != nil && *q.Nonzero, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.LateFeeAccrualList](items, next)
	return gen.MoneyListLateFeeAccruals200JSONResponse(out), err
}

func (s *apiServer) MoneyListLateFeeBookings(ctx context.Context, req gen.MoneyListLateFeeBookingsRequestObject) (gen.MoneyListLateFeeBookingsResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := b.ListLateFeeBookings(ctx, req.OrgId, siteOf(q.SiteId), q.UnitId, deref(q.DebtorKind), deref(q.Period), pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(items))
	for i, it := range items {
		m, err := conv[map[string]any](it)
		if err != nil {
			return nil, err
		}
		m["org_id"], m["updated_at"] = req.OrgId, it.CreatedAt
		rows[i] = m
	}
	out, err := listOf[gen.LateFeeBookingList](rows, next)
	return gen.MoneyListLateFeeBookings200JSONResponse(out), err
}

func waiverOut(org uuid.UUID, w billing.WaiverRow) (map[string]any, error) {
	m, err := conv[map[string]any](w)
	if err != nil {
		return nil, err
	}
	m["org_id"], m["updated_at"] = org, w.CreatedAt
	return m, nil
}

func (s *apiServer) MoneyListLateFeeWaivers(ctx context.Context, req gen.MoneyListLateFeeWaiversRequestObject) (gen.MoneyListLateFeeWaiversResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := b.ListLateFeeWaivers(ctx, req.OrgId, siteOf(q.SiteId), q.UnitId, deref(q.DebtorKind), pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(items))
	for i, it := range items {
		if rows[i], err = waiverOut(req.OrgId, it); err != nil {
			return nil, err
		}
	}
	out, err := listOf[gen.LateFeeWaiverList](rows, next)
	return gen.MoneyListLateFeeWaivers200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateLateFeeWaiver(ctx context.Context, req gen.MoneyCreateLateFeeWaiverRequestObject) (gen.MoneyCreateLateFeeWaiverResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		SiteID uuid.UUID      `json:"site_id"`
		Amount money.Money    `json:"amount"`
		Reason string         `json:"reason"`
		Debtor billing.Debtor `json:"debtor"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	if body.Debtor.UnitID == nil || body.Debtor.IsPerson() {
		return nil, problem.Validation(problem.Field{Field: "debtor.mode", Code: "unsupported"})
	}
	w, err := b.WaiveLateFee(ctx, req.OrgId, billing.WaiveInput{SiteID: body.SiteID, UnitID: *body.Debtor.UnitID, DebtorKind: body.Debtor.Kind, Amount: body.Amount.Amount, Reason: body.Reason})
	if err != nil {
		return nil, err
	}
	row, err := b.GetLateFeeWaiver(ctx, req.OrgId, w.ID)
	if err != nil {
		return nil, err
	}
	m, err := waiverOut(req.OrgId, row)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.LateFeeWaiver](m)
	return gen.MoneyCreateLateFeeWaiver201JSONResponse(out), err
}

func freezeOut(org uuid.UUID, f billing.FreezeRow) (map[string]any, error) {
	m, err := conv[map[string]any](f)
	if err != nil {
		return nil, err
	}
	m["org_id"] = org
	return m, nil
}

func (s *apiServer) MoneyListLateFeeFreezes(ctx context.Context, req gen.MoneyListLateFeeFreezesRequestObject) (gen.MoneyListLateFeeFreezesResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := b.ListLateFeeFreezes(ctx, req.OrgId, siteOf(q.SiteId), q.UnitId, deref(q.DebtorKind), q.Active, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(items))
	for i, it := range items {
		if rows[i], err = freezeOut(req.OrgId, it); err != nil {
			return nil, err
		}
	}
	out, err := listOf[gen.LateFeeFreezeList](rows, next)
	return gen.MoneyListLateFeeFreezes200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateLateFeeFreeze(ctx context.Context, req gen.MoneyCreateLateFeeFreezeRequestObject) (gen.MoneyCreateLateFeeFreezeResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		SiteID uuid.UUID      `json:"site_id"`
		From   string         `json:"from"`
		To     *string        `json:"to"`
		Reason string         `json:"reason"`
		Debtor billing.Debtor `json:"debtor"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	if body.Debtor.UnitID == nil || body.Debtor.IsPerson() {
		return nil, problem.Validation(problem.Field{Field: "debtor.mode", Code: "unsupported"})
	}
	id, err := b.CreateLateFeeFreeze(ctx, req.OrgId, billing.FreezeInput{SiteID: body.SiteID, UnitID: *body.Debtor.UnitID, DebtorKind: body.Debtor.Kind, From: body.From, To: body.To, Reason: body.Reason})
	if err != nil {
		return nil, err
	}
	f, err := b.GetLateFeeFreeze(ctx, req.OrgId, id)
	if err != nil {
		return nil, err
	}
	m, err := freezeOut(req.OrgId, f)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.LateFeeFreeze](m)
	return gen.MoneyCreateLateFeeFreeze201JSONResponse(out), err
}

func (s *apiServer) MoneyGetLateFeeFreeze(ctx context.Context, req gen.MoneyGetLateFeeFreezeRequestObject) (gen.MoneyGetLateFeeFreezeResponseObject, error) {
	b, err := s.bl()
	if err != nil {
		return nil, err
	}
	f, err := b.GetLateFeeFreeze(ctx, req.OrgId, req.FreezeId)
	if err != nil {
		return nil, err
	}
	m, err := freezeOut(req.OrgId, f)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.LateFeeFreeze](m)
	return gen.MoneyGetLateFeeFreeze200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateLateFeeFreeze(ctx context.Context, req gen.MoneyUpdateLateFeeFreezeRequestObject) (gen.MoneyUpdateLateFeeFreezeResponseObject, error) {
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
	f, err := b.UpdateLateFeeFreeze(ctx, req.OrgId, req.FreezeId, ver, d)
	if err != nil {
		return nil, err
	}
	m, err := freezeOut(req.OrgId, f)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.LateFeeFreeze](m)
	return gen.MoneyUpdateLateFeeFreeze200JSONResponse(out), err
}

// ------------------------------------------------------------ declared payments

func (s *apiServer) MoneyDeclarePayment(ctx context.Context, req gen.MoneyDeclarePaymentRequestObject) (gen.MoneyDeclarePaymentResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		SiteID    uuid.UUID      `json:"site_id"`
		Amount    money.Money    `json:"amount"`
		PaidOn    string         `json:"paid_on"`
		Reference *string        `json:"reference"`
		Debtor    billing.Debtor `json:"debtor"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.Declare(ctx, req.OrgId, payments.DeclareInput{SiteID: body.SiteID, Debtor: body.Debtor, Amount: body.Amount, PaidOn: body.PaidOn, Reference: body.Reference})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Payment](v)
	return gen.MoneyDeclarePayment201JSONResponse(out), err
}

func (s *apiServer) MoneyConfirmPayment(ctx context.Context, req gen.MoneyConfirmPaymentRequestObject) (gen.MoneyConfirmPaymentResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		AssetAccountID uuid.UUID `json:"asset_account_id"`
		ReceivedOn     string    `json:"received_on"`
		Allocations    []struct {
			ChargeID uuid.UUID `json:"charge_id"`
		} `json:"allocations"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, a := range body.Allocations {
		ids = append(ids, a.ChargeID)
	}
	v, err := p.Confirm(ctx, req.OrgId, req.PaymentId, body.AssetAccountID, body.ReceivedOn, ids)
	if err != nil {
		return nil, err
	}
	s.fileReceipt(ctx, req.OrgId, v.ID)
	out, err := conv[gen.Payment](v)
	return gen.MoneyConfirmPayment200JSONResponse(out), err
}

func (s *apiServer) MoneyRejectPayment(ctx context.Context, req gen.MoneyRejectPaymentRequestObject) (gen.MoneyRejectPaymentResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	v, err := p.Reject(ctx, req.OrgId, req.PaymentId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Payment](v)
	return gen.MoneyRejectPayment200JSONResponse(out), err
}
