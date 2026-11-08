package sdk

import (
	"context"
	"time"
)

// AllocationPolicy splits a charge over units. The core ships share (arsa
// payi), equal, area and table; a pack adds a national method.
//
// Core call sites: charge generation in billing, when a charge plan names the
// policy id ("<packid>.<name>") as its allocation method for a site whose
// organization has the pack enabled.
type AllocationPolicy interface {
	ID() string
	// Allocate splits req.Total over req.Units. The amounts must sum to Total
	// exactly (the core rejects the result otherwise) and are in the same currency.
	Allocate(ctx context.Context, req AllocationRequest) ([]UnitAmount, error)
}

// AllocationRequest is the input of an AllocationPolicy.
type AllocationRequest struct {
	Total Money
	Units []AllocationUnit
}

// AllocationUnit is a unit taking part in an allocation.
type AllocationUnit struct {
	UnitID ID
	AreaM2 float64
	// Share is the land share (arsa payi) as a fraction of 1; zero when unknown.
	Share float64
}

// UnitAmount is one unit's part.
type UnitAmount struct {
	UnitID ID
	Amount int64 // minor units
}

// PaymentAllocationOrder decides in which order a payment settles the debtor's
// open items. The core default is oldest first, principal before fee; a manual
// override by the person recording the payment always wins.
//
// Core call sites: payments, when a payment is recorded for a site whose
// payment_allocation_order setting names the order's id.
type PaymentAllocationOrder interface {
	ID() string
	// Order returns the ids of items in the order they are settled. Every id
	// must appear exactly once.
	Order(ctx context.Context, items []OpenItem) ([]ID, error)
}

// OpenItem is something a debtor still owes.
type OpenItem struct {
	ID     ID
	DueOn  time.Time
	Kind   string // "principal" or "fee"
	Amount int64  // minor units still open
}
