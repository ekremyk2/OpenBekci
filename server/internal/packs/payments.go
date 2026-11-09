package packs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// AttachPayments makes the payments module deliver payment.recorded and
// payment.refunded events to the subscribers of the enabled packs, in the
// payment's own transaction.
func (h *Host) AttachPayments(p *payments.Service) {
	ids := func(pay payments.Payment) map[string]sdk.ID {
		m := map[string]sdk.ID{"payment": sdk.ID(pay.ID.String()), "site": sdk.ID(pay.SiteID.String())}
		if pay.UnitID != nil {
			m["unit"] = sdk.ID(pay.UnitID.String())
		}
		return m
	}
	p.OnRecorded = func(ctx context.Context, tx pgx.Tx, pay payments.Payment) error {
		return h.Emit(ctx, tx, pay.OrgID, sdk.Event{Type: sdk.EventPaymentRecorded, IDs: ids(pay)})
	}
	p.OnRefunded = func(ctx context.Context, tx pgx.Tx, pay payments.Payment) error {
		return h.Emit(ctx, tx, pay.OrgID, sdk.Event{Type: sdk.EventPaymentRefunded, IDs: ids(pay)})
	}
}

// CheckoutHooks is the checkout module as the webhook route sees it: a payment event whose transaction belongs to
// a checkout is completed by the checkout (which records the payment for the debtor and charges it was created
// for), anything else is recorded from the event's own target.
type CheckoutHooks interface {
	// OnPaid completes the checkout of a paid event; handled is false when the transaction is no checkout's.
	OnPaid(ctx context.Context, org uuid.UUID, provider string, ev sdk.PaymentEvent) (handled bool, err error)
	OnFailed(ctx context.Context, org uuid.UUID, provider, txn, code string) error
	OnRefunded(ctx context.Context, org uuid.UUID, provider, txn string) error
}

// AttachCheckouts makes the payment webhook route complete checkouts.
func (h *Host) AttachCheckouts(c CheckoutHooks) { h.checkouts = c }

// Providers lists the payment providers of the packs enabled for the organization, by pack id: what card
// checkout chooses from.
func (h *Host) Providers(ctx context.Context, org uuid.UUID) ([]checkout.ProviderInfo, error) {
	active, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []checkout.ProviderInfo
	for _, p := range active {
		for _, pr := range p.C.PaymentProviders {
			out = append(out, checkout.ProviderInfo{Pack: p.Manifest.ID, Provider: pr.ID()})
		}
	}
	return out, nil
}

// paymentProvider finds an enabled pack's payment provider. The provider id is
// the pack's own ("<packid>" or "<packid>.<name>"); only a pack enabled for
// the organization is selected.
func (h *Host) paymentProvider(ctx context.Context, org uuid.UUID, packID, providerID string) (sdk.PaymentProvider, error) {
	p, ok := h.byID[packID]
	if !ok {
		return nil, problem.NotFound("no such pack")
	}
	if on, err := h.Enabled(ctx, org, packID); err != nil {
		return nil, err
	} else if !on {
		return nil, problem.NotFound("the pack is not enabled for this organization")
	}
	for _, pr := range p.C.PaymentProviders {
		if pr.ID() == providerID {
			return pr, nil
		}
	}
	return nil, problem.NotFound("no such payment provider")
}

// ProviderOwner returns the pack that provides a payment provider id, "" when none does.
func (h *Host) ProviderOwner(providerID string) string {
	for _, p := range h.packs {
		for _, pr := range p.C.PaymentProviders {
			if pr.ID() == providerID {
				return p.Manifest.ID
			}
		}
	}
	return ""
}

// CreateCheckout asks an enabled pack's payment provider for a hosted checkout.
// It is the core entry point of the checkout endpoint (release 0.4).
func (h *Host) CreateCheckout(ctx context.Context, org uuid.UUID, packID, providerID string, req sdk.CheckoutRequest) (sdk.Checkout, error) {
	pr, err := h.paymentProvider(ctx, org, packID, providerID)
	if err != nil {
		return sdk.Checkout{}, err
	}
	var out sdk.Checkout
	err = h.call(ctx, org, packID, "CreateCheckout", func(ctx context.Context) (err error) { out, err = pr.CreateCheckout(ctx, req); return err })
	return out, err
}

// PaymentStatus asks the provider for the state of a transaction.
func (h *Host) PaymentStatus(ctx context.Context, org uuid.UUID, packID, providerID, txn string) (sdk.PaymentStatus, error) {
	pr, err := h.paymentProvider(ctx, org, packID, providerID)
	if err != nil {
		return sdk.PaymentStatus{}, err
	}
	var out sdk.PaymentStatus
	err = h.call(ctx, org, packID, "Status", func(ctx context.Context) (err error) {
		out, err = pr.Status(ctx, sdk.ProviderRef{ProviderTxnID: txn})
		return err
	})
	return out, err
}

// RefundPayment refunds a recorded provider payment in full: the provider
// refunds first (RefundRef is its idempotency key), then the core posts the
// reversal. A replay of the same refund is acknowledged without a second posting.
func (h *Host) RefundPayment(ctx context.Context, org uuid.UUID, packID, providerID string, req sdk.RefundRequest) (sdk.RefundOutcome, error) {
	pr, err := h.paymentProvider(ctx, org, packID, providerID)
	if err != nil {
		return sdk.RefundOutcome{}, err
	}
	// the core reverses whole payments; a partial refund must not reach the ledger as a full one
	var gross int64
	err = h.tenantTx(sdk.WithOrg(ctx, sdk.ID(org.String())), func(org uuid.UUID, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT gross FROM payment WHERE org_id = $1 AND provider = $2 AND provider_txn_id = $3`, org, providerID, req.ProviderTxnID).Scan(&gross)
	})
	if err != nil {
		return sdk.RefundOutcome{}, err
	}
	if req.Amount.Amount != 0 && req.Amount.Amount != gross {
		return sdk.RefundOutcome{}, MapError(packID, fmt.Errorf("partial refunds: %w", sdk.ErrUnsupported))
	}
	var res sdk.RefundResult
	if err := h.call(ctx, org, packID, "Refund", func(ctx context.Context) (err error) { res, err = pr.Refund(ctx, req); return err }); err != nil {
		return sdk.RefundOutcome{}, err
	}
	rec := recorder{h: h, pack: packID}
	out, err := rec.RefundProviderPayment(sdk.WithOrg(ctx, sdk.ID(org.String())), sdk.ProviderRefund{Provider: providerID, ProviderTxnID: req.ProviderTxnID, RefundRef: orElse(res.ProviderRefundID, req.RefundRef), Reason: req.Reason})
	return out, err
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// HandleWebhook runs a provider's webhook: the pack verifies and parses the
// delivery, then the core records the events idempotently per (organization,
// provider, provider_txn_id): a replay is acknowledged and ignored. It is the
// logic behind POST /v1/orgs/{org}/packs/{pack}/payments/{provider}/webhook.
func (h *Host) HandleWebhook(ctx context.Context, org uuid.UUID, packID, providerID string, req sdk.WebhookRequest) (sdk.WebhookResult, error) {
	pr, err := h.paymentProvider(ctx, org, packID, providerID)
	if err != nil {
		return sdk.WebhookResult{}, err
	}
	var res sdk.WebhookResult
	if err := h.call(ctx, org, packID, "HandleWebhook", func(ctx context.Context) (err error) { res, err = pr.HandleWebhook(ctx, req); return err }); err != nil {
		return sdk.WebhookResult{}, err
	}
	rec := recorder{h: h, pack: packID}
	octx := sdk.WithOrg(ctx, sdk.ID(org.String()))
	for i, ev := range res.Events {
		switch ev.Kind {
		case sdk.EventKindPaid:
			if ev.ProviderTxnID == "" {
				return res, problem.Validation(problem.Field{Field: fmt.Sprintf("events[%d]", i), Code: "invalid_value"})
			}
			// a transaction of a checkout is recorded for the debtor and the charges the checkout was created for
			// (never from what the event says); anything else from the event's own target
			handled := false
			if h.checkouts != nil {
				handled, err = h.checkouts.OnPaid(octx, org, providerID, ev)
			}
			if err == nil && !handled {
				if ev.Target.UnitID == "" || ev.Target.SiteID == "" {
					return res, problem.Validation(problem.Field{Field: fmt.Sprintf("events[%d]", i), Code: "invalid_value"})
				}
				_, err = rec.RecordProviderPayment(octx, sdk.ProviderPayment{Provider: providerID, ProviderTxnID: ev.ProviderTxnID, Target: ev.Target,
					Gross: ev.Gross, Fee: ev.Fee, Reference: ev.Reference, ReceivedOn: dateOf(ev)})
			}
		case sdk.EventKindRefunded:
			_, err = rec.RefundProviderPayment(octx, sdk.ProviderRefund{Provider: providerID, ProviderTxnID: ev.ProviderTxnID, RefundRef: orElse(ev.RefundID, ev.ProviderTxnID), Reason: "refunded by the provider"})
			if err == nil && h.checkouts != nil {
				err = h.checkouts.OnRefunded(octx, org, providerID, ev.ProviderTxnID)
			}
		case sdk.EventKindSettled:
			if ev.Settlement == nil || ev.Target.SiteID == "" {
				return res, problem.Validation(problem.Field{Field: fmt.Sprintf("events[%d]", i), Code: "invalid_value"})
			}
			_, err = rec.RecordSettlement(octx, sdk.ProviderSettlement{Provider: providerID, Site: ev.Target.SiteID, Ref: ev.Settlement.Ref, SettledOn: ev.Settlement.SettledOn, Net: ev.Settlement.Net,
				ProviderTxnIDs: ev.Settlement.ProviderTxnIDs})
		case sdk.EventKindFailed:
			if h.checkouts != nil && ev.ProviderTxnID != "" {
				err = h.checkouts.OnFailed(octx, org, providerID, ev.ProviderTxnID, "provider_declined")
			}
		default:
			err = fmt.Errorf("unknown payment event kind %q", ev.Kind)
		}
		if err != nil {
			return res, MapError(packID, err)
		}
	}
	return res, nil
}

func dateOf(ev sdk.PaymentEvent) string {
	if ev.OccurredAt.IsZero() {
		return ""
	}
	return ev.OccurredAt.UTC().Format("2006-01-02")
}

// SyncSettlements asks a provider for its settlements of a period and posts
// each (idempotent per settlement ref). site is the site whose bank account
// receives the net.
func (h *Host) SyncSettlements(ctx context.Context, org uuid.UUID, packID, providerID string, site sdk.ID, req sdk.SettlementRequest) (posted int, err error) {
	pr, err := h.paymentProvider(ctx, org, packID, providerID)
	if err != nil {
		return 0, err
	}
	var list []sdk.Settlement
	if err := h.call(ctx, org, packID, "Settlements", func(ctx context.Context) (err error) { list, err = pr.Settlements(ctx, req); return err }); err != nil {
		return 0, err
	}
	rec := recorder{h: h, pack: packID}
	octx := sdk.WithOrg(ctx, sdk.ID(org.String()))
	for _, s := range list {
		out, err := rec.RecordSettlement(octx, sdk.ProviderSettlement{Provider: providerID, Site: site, Ref: s.Ref, SettledOn: s.SettledOn, Net: s.Net, ProviderTxnIDs: s.ProviderTxnIDs})
		if err != nil {
			return posted, MapError(packID, err)
		}
		if !out.Duplicate {
			posted++
		}
	}
	return posted, nil
}

// writeResponse sends a provider response (nil: 200 with an empty body).
func writeResponse(w http.ResponseWriter, r *sdk.HTTPResponse) {
	if r == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(r.Body) // the caller went away; nothing useful to do
}

var errNotPack = errors.New("not a pack route")

// splitProviderPath parses "payments/{provider}/webhook".
func splitProviderPath(rest string) (provider string, err error) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 3 && parts[0] == "payments" && parts[2] == "webhook" && parts[1] != "" {
		return parts[1], nil
	}
	return "", errNotPack
}
