package paymentalloc

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/internal/civil"
)

// Errors returned by Allocate.
var (
	ErrNegativePayment = errors.New("paymentalloc: negative payment")
	ErrNegativeItem    = errors.New("paymentalloc: negative remaining amount")
	ErrBadKind         = errors.New("paymentalloc: unknown item kind")
	ErrNilOrder        = errors.New("paymentalloc: nil order")
	ErrUnknownCharge   = errors.New("paymentalloc: charge is not open")
)

// Kind is the component of a charge an open item represents.
type Kind string

// Item kinds.
const (
	Principal Kind = "principal"
	Fee       Kind = "fee"
)

// OpenItem is something that can still be paid: the remaining principal of a
// charge, or an outstanding late-fee line. A late fee booked as its own
// charge is an item of Kind Fee with that charge's id.
type OpenItem struct {
	ChargeID  uuid.UUID
	DueDate   time.Time
	Kind      Kind
	Remaining money.Money
}

// Allocation is the amount applied to one open item (payment_allocation).
type Allocation struct {
	ChargeID uuid.UUID
	Kind     Kind
	Amount   money.Money
}

// Result is the outcome of Allocate. Σ Allocations + Credit == payment.
type Result struct {
	Allocations []Allocation
	// Credit is the unallocated remainder (prepayment liability on the unit
	// side or person), zero when everything was applied.
	Credit money.Money
}

// Order decides which open items a payment is applied to, and in what order.
type Order interface {
	// arrange returns the items eligible for allocation, in allocation order.
	arrange(open []OpenItem, tz *time.Location) ([]OpenItem, error)
}

type orderFunc func(open []OpenItem, tz *time.Location) ([]OpenItem, error)

func (f orderFunc) arrange(open []OpenItem, tz *time.Location) ([]OpenItem, error) {
	return f(open, tz)
}

func rank(k Kind, principalFirst bool) int {
	if (k == Principal) == principalFirst {
		return 0
	}
	return 1
}

func sortItems(open []OpenItem, principalFirst bool, tz *time.Location) []OpenItem {
	out := make([]OpenItem, len(open))
	copy(out, open)
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i].Kind, principalFirst), rank(out[j].Kind, principalFirst); ri != rj {
			return ri < rj
		}
		if di, dj := civil.Day(out[i].DueDate, tz), civil.Day(out[j].DueDate, tz); di != dj {
			return di < dj
		}
		return bytesLess(out[i].ChargeID[:], out[j].ChargeID[:])
	})
	return out
}

func isOpen(open []OpenItem, id uuid.UUID) bool {
	for _, it := range open {
		if it.ChargeID == id && it.Remaining.Amount > 0 {
			return true
		}
	}
	return false
}

func bytesLess(a, b []byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// OldestFirstPrincipalBeforeFee is the default (decision M defaults): all open
// principal first, oldest due date first, then fees, oldest first; ties by
// charge id. Paying principal first stops fee accrual soonest.
var OldestFirstPrincipalBeforeFee Order = orderFunc(func(open []OpenItem, tz *time.Location) ([]OpenItem, error) {
	return sortItems(open, true, tz), nil
})

// FeesFirst applies a payment to fees (oldest first) before principal.
// Reserved for packs whose law requires costs and interest to be cleared first.
var FeesFirst Order = orderFunc(func(open []OpenItem, tz *time.Location) ([]OpenItem, error) {
	return sortItems(open, false, tz), nil
})

// Manual applies the payment only to the listed charges, in the listed order
// (the principal of a charge before its fee). The manager picked these, so
// nothing else is touched and what is left over becomes credit. A listed id
// that has no open amount (a typo, or a charge already settled) makes
// Allocate fail with ErrUnknownCharge instead of parking the cash as credit.
func Manual(chargeIDs []uuid.UUID) Order {
	ids := append([]uuid.UUID(nil), chargeIDs...)
	return orderFunc(func(open []OpenItem, _ *time.Location) ([]OpenItem, error) {
		var out []OpenItem
		seen := map[uuid.UUID]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if !isOpen(open, id) {
				return nil, fmt.Errorf("%w: %s", ErrUnknownCharge, id)
			}
			for _, principalFirst := range []bool{true, false} {
				for _, it := range open {
					if it.ChargeID == id && (it.Kind == Principal) == principalFirst {
						out = append(out, it)
					}
				}
			}
		}
		return out, nil
	})
}

// Allocate applies payment to the open items in the order's sequence, each
// item receiving at most its Remaining. It never allocates more than the
// payment or more than an item's remaining amount; anything left over is
// returned as Credit. Items with zero remaining are skipped; the same input
// always gives the same output. A charge id listed twice in a Manual order is
// served once. Due dates are compared as calendar days in tz (nil = UTC), so
// two charges due the same date fall back to the charge id whatever zone their
// values carry.
func Allocate(payment money.Money, open []OpenItem, order Order, tz *time.Location) (Result, error) {
	if order == nil {
		return Result{}, ErrNilOrder
	}
	if err := payment.Validate(); err != nil {
		return Result{}, err
	}
	if payment.Amount < 0 {
		return Result{}, ErrNegativePayment
	}
	for _, it := range open {
		if it.Kind != Principal && it.Kind != Fee {
			return Result{}, fmt.Errorf("%w: %q", ErrBadKind, string(it.Kind))
		}
		if it.Remaining.Currency != payment.Currency {
			return Result{}, money.ErrCurrencyMismatch
		}
		if it.Remaining.Amount < 0 {
			return Result{}, ErrNegativeItem
		}
	}
	left := payment.Amount
	res := Result{Credit: money.Zero(payment.Currency)}
	if tz == nil {
		tz = time.UTC
	}
	arranged, err := order.arrange(open, tz)
	if err != nil {
		return Result{}, err
	}
	for _, it := range arranged {
		if left == 0 {
			break
		}
		if it.Remaining.Amount == 0 {
			continue
		}
		take := it.Remaining.Amount
		if take > left {
			take = left
		}
		res.Allocations = append(res.Allocations, Allocation{ChargeID: it.ChargeID, Kind: it.Kind, Amount: money.New(take, payment.Currency)})
		left -= take
	}
	res.Credit = money.New(left, payment.Currency)
	return res, nil
}
