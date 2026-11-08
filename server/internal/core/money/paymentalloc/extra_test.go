package paymentalloc_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	pa "github.com/openbekci/openbekci/server/internal/core/money/paymentalloc"
)

// F7: a manual selection with an id that is not open is an error, not credit.
func TestManualUnknownOrSettledChargeIsAnError(t *testing.T) {
	open := []pa.OpenItem{item(1, 0, pa.Principal, 100), item(2, 1, pa.Principal, 0)}
	for _, ids := range [][]uuid.UUID{{id(99)}, {id(1), id(99)}, {id(2)}} {
		if _, err := pa.Allocate(try(10), open, pa.Manual(ids), nil); !errors.Is(err, pa.ErrUnknownCharge) {
			t.Errorf("%v: %v", ids, err)
		}
	}
	if r, err := pa.Allocate(try(10), open, pa.Manual([]uuid.UUID{id(1)}), nil); err != nil || r.Credit.Amount != 0 {
		t.Errorf("%+v %v", r, err)
	}
}

// F10: due dates are compared as calendar days in the site zone, so two charges
// due on the same date fall back to the id tie-break whatever zone built them.
func TestDueDatesCompareAsCalendarDays(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// both are 10 March in Istanbul; as instants the second is earlier
	a := pa.OpenItem{ChargeID: id(1), DueDate: time.Date(2026, 3, 10, 0, 0, 0, 0, ist), Kind: pa.Principal, Remaining: try(10)}
	b := pa.OpenItem{ChargeID: id(2), DueDate: time.Date(2026, 3, 9, 21, 0, 0, 0, time.UTC), Kind: pa.Principal, Remaining: try(10)}
	r, err := pa.Allocate(try(10), []pa.OpenItem{b, a}, pa.OldestFirstPrincipalBeforeFee, ist)
	if err != nil || r.Allocations[0].ChargeID != id(1) {
		t.Fatalf("same calendar day must tie-break by id: %+v %v", r, err)
	}
	// a genuinely older day still wins
	c := pa.OpenItem{ChargeID: id(3), DueDate: time.Date(2026, 3, 9, 0, 0, 0, 0, ist), Kind: pa.Principal, Remaining: try(10)}
	r, _ = pa.Allocate(try(10), []pa.OpenItem{a, c}, pa.OldestFirstPrincipalBeforeFee, ist)
	if r.Allocations[0].ChargeID != id(3) {
		t.Fatalf("older day first: %+v", r)
	}
}
