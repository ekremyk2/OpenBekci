package payments

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// ClientMoneyPayoutInput is the organization paying client money out to a site.
type ClientMoneyPayoutInput struct {
	SiteID    uuid.UUID
	Amount    money.Money
	Reference string
	// PaidOn is a civil date; empty means today at the site.
	PaidOn string
}

// PayoutClientMoney records that the organization (merchant of record) paid client money out to a site's bank
// account: Dr bank 1100, Cr client money 1195 of the site, so what the organization owes the site goes down. The
// payout cannot exceed that balance. It needs money.post at the site; the entry date follows the lock date.
func (s *Service) PayoutClientMoney(ctx context.Context, orgID uuid.UUID, in ClientMoneyPayoutInput) (ClearingBalances, error) {
	ref := strings.TrimSpace(in.Reference)
	if ref == "" {
		return ClearingBalances{}, bad("reference", "required")
	}
	if len([]rune(ref)) > 200 {
		return ClearingBalances{}, bad("reference", "too_long")
	}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyPost, in.SiteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, in.SiteID, true, false)
		if err != nil {
			return err
		}
		if in.Amount.Amount <= 0 || in.Amount.Currency != ledger.Currency(site) {
			return bad("amount", "invalid_value")
		}
		today := ledger.Today(site.Timezone, s.Now())
		paidOn := in.PaidOn
		if paidOn == "" {
			paidOn = today
		}
		if _, err := ledger.ParseDate(paidOn); err != nil {
			return bad("paid_on", "invalid_format")
		}
		if paidOn > today {
			return bad("paid_on", "in_the_future")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		// one payout at a time per site, so the balance check cannot be raced
		if err := providerLock(ctx, tx, orgID, "client_money", "", in.SiteID.String()); err != nil {
			return err
		}
		cm, err := ledger.EnsureAccount(ctx, tx, orgID, in.SiteID, ledger.ClientMoneyCode, "Client money held by the organization", "asset", ledger.SubClientMoney, nil)
		if err != nil {
			return err
		}
		d, c, err := ledger.Balance(ctx, tx, orgID, in.SiteID, cm, ledger.BalanceFilter{})
		if err != nil {
			return err
		}
		if in.Amount.Amount > d-c {
			return bad("amount", "exceeds_balance")
		}
		bank := ch.Account(ledger.SubBank)
		eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: paidOn, Memo: "Client money paid out to the site: " + ref,
			SourceType: "client_money_payout", Lines: []ledger.Line{
				{AccountID: bank, FundID: ch.OperatingFund, Debit: in.Amount.Amount},
				{AccountID: cm, FundID: ch.OperatingFund, Credit: in.Amount.Amount},
			}})
		if err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "payment.client_money_payout", "journal_entry", eid, nil, map[string]any{
			"amount": in.Amount.Amount, "reference": ref, "paid_on": paidOn})
	})
	if err != nil {
		return ClearingBalances{}, err
	}
	return s.ClearingBalancesOf(ctx, orgID, in.SiteID)
}
