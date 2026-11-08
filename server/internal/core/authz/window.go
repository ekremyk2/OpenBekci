package authz

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MoneyWindow is the period over which the caller's occupancies reach the money of
// a unit side: from the earliest start to the latest end (to is nil while one
// is still open, otherwise the first day after it). ok is false without any such occupancy.
func MoneyWindow(ctx context.Context, unitID uuid.UUID, side UnitSide) (from time.Time, to *time.Time, ok bool) {
	open := false
	for _, o := range GrantsFromContext(ctx).Occupancies {
		if o.UnitID != unitID || !o.sideMoney(side) {
			continue
		}
		if !ok || o.ValidFrom.Before(from) {
			from = o.ValidFrom
		}
		ok = true
		switch {
		case o.ValidTo == nil:
			open = true
		case to == nil || o.ValidTo.After(*to):
			t := *o.ValidTo
			to = &t
		}
	}
	if open {
		to = nil
	}
	return from, to, ok
}
