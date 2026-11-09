package httpapi

import (
	"bytes"
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/moneydocs"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) mdc() (*moneydocs.Service, error) {
	if s.mny.Docs == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Docs, nil
}

// fileReceipt files the receipt of a payment that was just recorded or confirmed. A receipt that cannot be filed is logged:
// the payment itself stands, and the receipt can be generated again on demand.
func (s *apiServer) fileReceipt(ctx context.Context, orgID, paymentID uuid.UUID) {
	d := s.mny.Docs
	if d == nil || d.Files == nil {
		return
	}
	if _, err := d.FileReceipt(ctx, orgID, paymentID); err != nil {
		slog.WarnContext(ctx, "filing the receipt of a payment", "payment_id", paymentID, "error", err)
	}
}

// fileCreditNote files the credit note of a payment that was just voided or refunded; like the receipt it is
// logged when it fails (the reversal stands) and idempotent, so a repeated void files nothing new.
func (s *apiServer) fileCreditNote(ctx context.Context, orgID, paymentID uuid.UUID) {
	d := s.mny.Docs
	if d == nil || d.Files == nil || d.Docs == nil {
		return
	}
	if _, err := d.FileCreditNote(ctx, orgID, paymentID); err != nil {
		slog.WarnContext(ctx, "filing the credit note of a payment", "payment_id", paymentID, "error", err)
	}
}

func (s *apiServer) MoneyGetPaymentReceipt(ctx context.Context, req gen.MoneyGetPaymentReceiptRequestObject) (gen.MoneyGetPaymentReceiptResponseObject, error) {
	d, err := s.mdc()
	if err != nil {
		return nil, err
	}
	b, _, err := d.ReceiptPDF(ctx, req.OrgId, req.PaymentId)
	if err != nil {
		return nil, err
	}
	return gen.MoneyGetPaymentReceipt200ApplicationpdfResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
}

func (s *apiServer) MoneySendPaymentReceipt(ctx context.Context, req gen.MoneySendPaymentReceiptRequestObject) (gen.MoneySendPaymentReceiptResponseObject, error) {
	d, err := s.mdc()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	var channels []string
	for _, c := range req.Body.Channels {
		channels = append(channels, string(c))
	}
	if err := d.SendReceipt(ctx, req.OrgId, req.PaymentId, channels); err != nil {
		return nil, err
	}
	return gen.MoneySendPaymentReceipt204Response{}, nil
}

func (s *apiServer) MoneySendStatement(ctx context.Context, req gen.MoneySendStatementRequestObject) (gen.MoneySendStatementResponseObject, error) {
	d, err := s.mdc()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	body, err := conv[struct {
		Debtor   billing.Debtor `json:"debtor"`
		From     string         `json:"from"`
		To       string         `json:"to"`
		Channels []string       `json:"channels"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := d.SendStatement(ctx, req.OrgId, body.Debtor, body.From, body.To, body.Channels)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.StatementSent](v)
	return gen.MoneySendStatement200JSONResponse(out), err
}
