package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// RebuildChargeStatus re-derives the status of every charge of a site from
// its allocations (admin rebuild-charge-status) and returns how many changed.
func (a *Admin) RebuildChargeStatus(ctx context.Context, orgID, siteID uuid.UUID) (int64, error) {
	svc := billing.NewService(a.db, tenancy.NewService(a.db), nil)
	return svc.RebuildStatus(ledger.SystemContext(ctx, orgID), orgID, siteID)
}

// ManualLine is one line of a manual journal entry: an account code, an amount
// on one side and optionally a fund code (default: the operating fund).
type ManualLine struct {
	AccountCode string
	Debit       int64
	Credit      int64
	FundCode    string
}

// PostEntry posts a manual journal entry for the accountant (admin post-entry).
// It is audited like any posting and refused on or before the lock date.
func (a *Admin) PostEntry(ctx context.Context, orgID, siteID uuid.UUID, date, memo string, lines []ManualLine) (uuid.UUID, error) {
	ctx = ledger.SystemContext(ctx, orgID)
	var ls []ledger.Line
	err := a.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		ch, err := ledger.EnsureChart(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		for _, l := range lines {
			acc := ch.AccountCode(l.AccountCode)
			if acc == uuid.Nil {
				return fmt.Errorf("no account with code %q", l.AccountCode)
			}
			fund := ch.OperatingFund
			if l.FundCode != "" {
				if fund = ch.Funds[l.FundCode]; fund == uuid.Nil {
					return fmt.Errorf("no fund with code %q", l.FundCode)
				}
			}
			ls = append(ls, ledger.Line{AccountID: acc, FundID: fund, Debit: l.Debit, Credit: l.Credit})
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return ledger.NewService(a.db, nil).PostManual(ctx, orgID, ledger.ManualEntryInput{SiteID: siteID, EntryDate: date, Memo: memo, Lines: ls})
}
