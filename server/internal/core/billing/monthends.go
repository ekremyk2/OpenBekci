package billing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
)

// monthEndsToBook lists the month ends to reconcile for a debtor, oldest first, ending with
// closed (the last closed month end, always included so reversals after backdated payments are
// posted): every month after the last month a fee was booked for, from the month of the debtor's
// oldest principal charge. One fee line per debtor per closed month (M4).
func monthEndsToBook(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d Debtor, closed string) ([]string, error) {
	where, args := d.chargeWhere(3)
	var oldest *string
	if err := tx.QueryRow(ctx, `SELECT min(c.due_date)::text FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND c.kind='principal' AND c.cancel_entry_id IS NULL AND `+where,
		append([]any{orgID, siteID}, args...)...).Scan(&oldest); err != nil {
		return nil, err
	}
	if oldest == nil || *oldest > closed {
		return []string{closed}, nil
	}
	bwhere, bargs := d.bookingWhere(3)
	var last *string
	if err := tx.QueryRow(ctx, `SELECT max(left(period, 7)) FROM late_fee_booking WHERE org_id=$1 AND site_id=$2 AND kind='fee' AND `+bwhere,
		append([]any{orgID, siteID}, bargs...)...).Scan(&last); err != nil {
		return nil, err
	}
	start := (*oldest)[:7]
	if last != nil && *last >= start {
		t, _ := time.Parse("2006-01", *last)
		start = t.AddDate(0, 1, 0).Format("2006-01")
	}
	var out []string
	for m, _ := time.Parse("2006-01", start); ; m = m.AddDate(0, 1, 0) {
		end := m.AddDate(0, 1, -1).Format(ledger.DateLayout)
		if end >= closed {
			break
		}
		out = append(out, end)
	}
	return append(out, closed), nil
}
