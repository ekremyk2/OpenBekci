package httpapi

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Card checkout, refunds of provider payments and provider settlements (release 0.4).

func (s *apiServer) chk() (*checkout.Service, error) {
	if s.mny.Checkout == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Checkout, nil
}

func (s *apiServer) MoneyCreateCheckout(ctx context.Context, req gen.MoneyCreateCheckoutRequestObject) (gen.MoneyCreateCheckoutResponseObject, error) {
	c, err := s.chk()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	b, err := conv[struct {
		UnitID     uuid.UUID    `json:"unit_id"`
		DebtorKind string       `json:"debtor_kind"`
		ChargeIDs  []uuid.UUID  `json:"charge_ids"`
		Amount     *money.Money `json:"amount"`
		Client     string       `json:"client"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.Create(ctx, req.OrgId, checkout.CreateInput{UnitID: b.UnitID, DebtorKind: b.DebtorKind, ChargeIDs: b.ChargeIDs, Amount: b.Amount, Client: b.Client})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Checkout](v)
	return gen.MoneyCreateCheckout201JSONResponse(out), err
}

func (s *apiServer) MoneyGetCheckout(ctx context.Context, req gen.MoneyGetCheckoutRequestObject) (gen.MoneyGetCheckoutResponseObject, error) {
	c, err := s.chk()
	if err != nil {
		return nil, err
	}
	v, err := c.Get(ctx, req.OrgId, req.CheckoutId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Checkout](v)
	return gen.MoneyGetCheckout200JSONResponse(out), err
}

func (s *apiServer) MoneyListMyCheckouts(ctx context.Context, req gen.MoneyListMyCheckoutsRequestObject) (gen.MoneyListMyCheckoutsResponseObject, error) {
	c, err := s.chk()
	if err != nil {
		return nil, err
	}
	items, next, err := c.ListMine(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.CheckoutList](items, next)
	return gen.MoneyListMyCheckouts200JSONResponse(out), err
}

func (s *apiServer) MoneyListCheckouts(ctx context.Context, req gen.MoneyListCheckoutsRequestObject) (gen.MoneyListCheckoutsResponseObject, error) {
	c, err := s.chk()
	if err != nil {
		return nil, err
	}
	items, next, err := c.ListSite(ctx, req.OrgId, siteOf(req.Params.SiteId), deref(req.Params.Status), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.CheckoutList](items, next)
	return gen.MoneyListCheckouts200JSONResponse(out), err
}

func (s *apiServer) MoneyRefundPayment(ctx context.Context, req gen.MoneyRefundPaymentRequestObject) (gen.MoneyRefundPaymentResponseObject, error) {
	c, err := s.chk()
	if err != nil {
		return nil, err
	}
	version, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	reason := ""
	if req.Body != nil && req.Body.Reason != nil {
		reason = *req.Body.Reason
	}
	p, err := c.Refund(ctx, req.OrgId, req.PaymentId, version, reason)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Payment](p)
	return gen.MoneyRefundPayment200JSONResponse(out), err
}

// settlementCursor is the opaque cursor of the settlement list: the last row's (settled_on, provider, ref).
func settlementCursor(r payments.SettlementRow) string {
	return base64.RawURLEncoding.EncodeToString([]byte(r.SettledOn + "\x1f" + r.Provider + "\x1f" + r.Ref))
}

func decodeSettlementCursor(c string) (*payments.SettlementRow, error) {
	if c == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	parts := strings.Split(string(b), "\x1f")
	if err != nil || len(parts) != 3 {
		return nil, problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
	}
	if _, err := ledger.ParseDate(parts[0]); err != nil {
		return nil, problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
	}
	return &payments.SettlementRow{SettledOn: parts[0], Provider: parts[1], Ref: parts[2]}, nil
}

func (s *apiServer) MoneyListPaymentSettlements(ctx context.Context, req gen.MoneyListPaymentSettlementsRequestObject) (gen.MoneyListPaymentSettlementsResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	limit := 50
	if req.Params.Limit != nil && *req.Params.Limit > 0 {
		limit = min(*req.Params.Limit, 200)
	}
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	after, err := decodeSettlementCursor(cursor)
	if err != nil {
		return nil, err
	}
	rows, err := p.ListSettlements(ctx, req.OrgId, siteOf(req.Params.SiteId), deref(req.Params.Provider), limit+1, after)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = settlementCursor(rows[len(rows)-1])
	}
	if rows == nil {
		rows = []payments.SettlementRow{}
	}
	out, err := listOf[gen.ProviderSettlementList](rows, next)
	return gen.MoneyListPaymentSettlements200JSONResponse(out), err
}

func (s *apiServer) MoneyGetPaymentClearing(ctx context.Context, req gen.MoneyGetPaymentClearingRequestObject) (gen.MoneyGetPaymentClearingResponseObject, error) {
	p, err := s.py()
	if err != nil {
		return nil, err
	}
	v, err := p.ClearingBalancesOf(ctx, req.OrgId, siteOf(req.Params.SiteId))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.PaymentClearing](v)
	return gen.MoneyGetPaymentClearing200JSONResponse(out), err
}
