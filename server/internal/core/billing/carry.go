package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MoveFeeBookings passes the fee bookings of a unit side to the person its debt was
// carried to (move-out), so the accrual of the carried principal is reconciled
// against the fees already booked and nothing is reversed or booked twice.
func MoveFeeBookings(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d Debtor, person uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE late_fee_booking SET debtor_kind='person', person_id=$5,
			period = CASE WHEN kind = 'fee' THEN period || '@' || left(unit_id::text, 8) ELSE period END
		WHERE org_id=$1 AND site_id=$2 AND unit_id=$3 AND debtor_kind=$4`, orgID, siteID, *d.UnitID, d.Kind, person)
	return err
}
