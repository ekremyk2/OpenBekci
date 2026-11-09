package payments

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Provider payments (packs plan 3.8): the resident pays gross through a
// payment provider, the provider keeps a fee and settles the net to the bank
// later. Gross goes to the debtor's receivable (allocated like any payment),
// the fee to the fee expense account and the net stays in a per-provider
// clearing account until RecordSettlement moves it to the bank. Everything is
// idempotent per (organization, provider, provider_txn_id).

// ProviderInput is a paid provider transaction.
type ProviderInput struct {
	Provider      string
	ProviderTxnID string
	SiteID        uuid.UUID
	Debtor        billing.Debtor
	Gross, Fee    money.Money
	// ReceivedOn is a civil date; empty means today at the site.
	ReceivedOn string
	Reference  *string
	ChargeIDs  []uuid.UUID
}

// ProviderResult is the outcome of RecordProvider.
type ProviderResult struct {
	Payment   Payment
	Duplicate bool
}

func providerLock(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, kind, provider, ref string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 11))`, fmt.Sprintf("%s|%s|%s|%s", kind, orgID, provider, ref))
	return err
}

// RecordProvider records a paid provider transaction. A transaction recorded
// before is returned unchanged with Duplicate set. The caller must hold the
// money permissions of ledger.SystemContext.
func (s *Service) RecordProvider(ctx context.Context, orgID uuid.UUID, in ProviderInput) (ProviderResult, error) {
	var out ProviderResult
	if in.Provider == "" || in.ProviderTxnID == "" {
		return out, bad("provider_txn_id", "required")
	}
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		if err := providerLock(ctx, tx, orgID, "payment", in.Provider, in.ProviderTxnID); err != nil {
			return err
		}
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM payment WHERE org_id = $1 AND provider = $2 AND provider_txn_id = $3`, orgID, in.Provider, in.ProviderTxnID).Scan(&existing)
		switch {
		case err == nil:
			out.Duplicate = true
			out.Payment, err = loadPayment(ctx, tx, orgID, existing)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, in.SiteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, in.SiteID, true, false)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		if err := in.Debtor.Validate(); err != nil {
			return err
		}
		if in.Gross.Amount <= 0 || in.Gross.Currency != cur {
			return bad("gross", "invalid_value")
		}
		if in.Fee.Amount < 0 || in.Fee.Amount >= in.Gross.Amount || (in.Fee.Amount > 0 && in.Fee.Currency != cur) {
			return bad("fee", "invalid_value")
		}
		today := ledger.Today(site.Timezone, s.Now())
		if in.ReceivedOn == "" {
			in.ReceivedOn = today
		}
		if _, err := ledger.ParseDate(in.ReceivedOn); err != nil {
			return bad("received_on", "invalid_format")
		}
		if in.ReceivedOn > today {
			return bad("received_on", "in_the_future")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		clearing, err := ledger.EnsureAccount(ctx, tx, orgID, in.SiteID, ledger.ClearingCode(in.Provider), "Clearing: "+in.Provider, "asset", ledger.SubClearing, nil)
		if err != nil {
			return err
		}
		fee, err := ledger.EnsureAccount(ctx, tx, orgID, in.SiteID, ledger.ProviderFeeCode, "Payment provider fees", "expense", ledger.SubProviderFee, &ch.OperatingFund)
		if err != nil {
			return err
		}
		if err := checkDebtor(ctx, tx, orgID, in.SiteID, in.Debtor); err != nil {
			return err
		}
		net := money.New(in.Gross.Amount-in.Fee.Amount, cur)
		pid := id.New()
		p := Payment{ID: pid, OrgID: orgID, SiteID: in.SiteID, Method: "card:" + in.Provider, ReceivedOn: in.ReceivedOn, AssetAccountID: clearing, Reference: in.Reference,
			Gross: in.Gross, Fee: money.New(in.Fee.Amount, cur), Net: net, RecordedBy: ledger.Actor(ctx)}
		allocs, err := s.settle(ctx, tx, orgID, site, ch, &p, in.Debtor, in.ChargeIDs, fee)
		if err != nil {
			return err
		}
		if err := insertPayment(ctx, tx, p, in.Debtor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment SET provider = $3, provider_txn_id = $4 WHERE org_id = $1 AND id = $2`, orgID, pid, in.Provider, in.ProviderTxnID); err != nil {
			return err
		}
		if err := saveAllocations(ctx, tx, orgID, in.SiteID, p, allocs); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "payment.record", "payment", p.ID, nil, map[string]any{
			"amount": in.Gross.Amount, "fee": in.Fee.Amount, "method": p.Method, "provider_txn_id": in.ProviderTxnID, "receipt_no": *p.ReceiptNo, "received_on": in.ReceivedOn}); err != nil {
			return err
		}
		// the provider confirmed the money: the webhook event payment.confirmed follows the audit action
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "payment.confirm", "payment", p.ID, nil, map[string]any{"provider": in.Provider, "provider_txn_id": in.ProviderTxnID}); err != nil {
			return err
		}
		if err := s.notify(ctx, tx, NotifyPaymentConfirmed, p); err != nil {
			return err
		}
		out.Payment, err = loadPayment(ctx, tx, orgID, pid)
		if err != nil {
			return err
		}
		if s.OnRecorded != nil {
			return s.OnRecorded(ctx, tx, out.Payment)
		}
		return nil
	})
	return out, err
}

// ProviderRefundInput refunds a recorded provider payment in full.
type ProviderRefundInput struct {
	Provider      string
	ProviderTxnID string
	RefundRef     string
	Reason        string
}

// RefundProvider reverses a recorded provider payment: its entry is reversed
// (gross back out of the clearing account and the fee expense), the
// allocations are voided and the payment becomes refunded. A payment already
// refunded is returned with duplicate set.
func (s *Service) RefundProvider(ctx context.Context, orgID uuid.UUID, in ProviderRefundInput) (Payment, bool, error) {
	var pid uuid.UUID
	var status string
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		if err := providerLock(ctx, tx, orgID, "refund", in.Provider, in.ProviderTxnID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id, status FROM payment WHERE org_id = $1 AND provider = $2 AND provider_txn_id = $3`, orgID, in.Provider, in.ProviderTxnID).Scan(&pid, &status)
	})
	if err != nil {
		return Payment{}, false, err
	}
	reason := in.Reason
	if reason == "" {
		reason = "refunded by the provider"
	}
	ref := in.RefundRef
	var refp *string
	if ref != "" {
		refp = &ref
	}
	p, err := s.reverse(ctx, orgID, pid, reason, "refunded", refp)
	return p, status == "refunded", err
}
