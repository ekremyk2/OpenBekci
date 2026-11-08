package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// MoveReceivableToCredit turns paid receivable into prepayment credit: debit
// receivable, credit prepayment (a cancelled charge keeps the payments made on it).
func MoveReceivableToCredit(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ch ledger.Chart, d Debtor, fund uuid.UUID, amount int64, date, memo string) error {
	return moveCredit(ctx, tx, orgID, siteID, ch, d, fund, amount, date, memo, true)
}

// MoveCreditToReceivable is the opposite: debit prepayment, credit receivable.
func MoveCreditToReceivable(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ch ledger.Chart, d Debtor, fund uuid.UUID, amount int64, date, memo string) error {
	return moveCredit(ctx, tx, orgID, siteID, ch, d, fund, amount, date, memo, false)
}

func moveCredit(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, ch ledger.Chart, d Debtor, fund uuid.UUID, amount int64, date, memo string, toCredit bool) error {
	recv := ledger.Line{AccountID: ch.Account(d.ReceivableSubtype()), FundID: fund}
	pre := ledger.Line{AccountID: ch.Account(ledger.SubPrepayment), FundID: fund}
	if toCredit {
		recv.Debit, pre.Credit = amount, amount
	} else {
		pre.Debit, recv.Credit = amount, amount
	}
	sid := id.New()
	_, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: siteID, EntryDate: date, Memo: memo, SourceType: "credit_apply", SourceID: &sid,
		Lines: []ledger.Line{d.Line(recv, nil), d.Line(pre, nil)}})
	return err
}
