package allocate

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
)

// KeyKind names an allocation key (backend plan §4.1, decision M2).
type KeyKind string

// Key kinds.
const (
	// KeyEqual: every non-exempt unit weighs 1 (Law 634 default for staff costs).
	KeyEqual KeyKind = "equal"
	// KeyArea: weight is the unit's area in m².
	KeyArea KeyKind = "area"
	// KeyShare: weight is the arsa payı numerator/denominator (Law 634 default for other costs).
	KeyShare KeyKind = "share"
	// KeyConsumption: weight is a metered quantity (e.g. heat-cost-allocator units).
	KeyConsumption KeyKind = "consumption"
	// KeyTable: an explicit amount per unit from an uploaded table.
	KeyTable KeyKind = "table"
	// KeyComposite: fractions of the total, each split by its own key.
	KeyComposite KeyKind = "composite"
	// KeyWeights is the kind recorded by the low-level Allocate.
	KeyWeights KeyKind = "weights"
)

// Unit is a candidate unit within the allocation scope. The caller decides the
// scope (whole site, one block) by choosing which units it passes in.
type Unit struct {
	ID     uuid.UUID
	Exempt bool
}

// Part is one fraction of a composite key.
type Part struct {
	Fraction *big.Rat
	Key      Key
}

// Key is an allocation key with its per-unit values. Build one with Equal,
// Area, LandShare, Consumption, Table or Composite.
type Key struct {
	Kind    KeyKind
	Values  map[uuid.UUID]*big.Rat // area, share, consumption
	Amounts map[uuid.UUID]int64    // table (minor units)
	Parts   []Part                 // composite
}

// Equal splits equally per unit.
func Equal() Key { return Key{Kind: KeyEqual} }

// Area splits by area (m²) per unit.
func Area(m2 map[uuid.UUID]*big.Rat) Key { return Key{Kind: KeyArea, Values: m2} }

// LandShare splits by arsa payı (any consistent numerator/denominator per unit).
func LandShare(share map[uuid.UUID]*big.Rat) Key { return Key{Kind: KeyShare, Values: share} }

// Consumption splits by a metered quantity per unit.
func Consumption(q map[uuid.UUID]*big.Rat) Key { return Key{Kind: KeyConsumption, Values: q} }

// Table uses explicit per-unit amounts in minor units; the amounts of the
// non-exempt units must sum to the total.
func Table(amounts map[uuid.UUID]int64) Key { return Key{Kind: KeyTable, Amounts: amounts} }

// Composite splits fractions of the total by different keys, for example 70 %
// by consumption and 30 % by area for central heating. Fractions must be
// positive and sum to 1; the part totals are themselves rounded by
// largest-remainder (ties to the earlier part) so the parts sum to the total.
func Composite(parts ...Part) Key { return Key{Kind: KeyComposite, Parts: parts} }

// ByKey allocates total over units by key, tagging the snapshot with scope
// (for example "site" or "block:<id>"). Units are returned in order.
func ByKey(total money.Money, scope string, units []Unit, key Key, order OrderFn) (Result, error) {
	if order == nil {
		order = ByID()
	}
	if key.Kind == KeyComposite {
		return composite(total, scope, units, key, order)
	}
	ws, err := leafWeights(total, units, key)
	if err != nil {
		return Result{}, err
	}
	return allocate(total, ws, order, key.Kind, scope)
}

func leafWeights(total money.Money, units []Unit, key Key) ([]Weight, error) {
	ws := make([]Weight, len(units))
	var tableSum int64
	for i, u := range units {
		w := Weight{UnitID: u.ID, Exempt: u.Exempt}
		switch key.Kind {
		case KeyEqual:
			w.Weight = big.NewRat(1, 1)
		case KeyArea, KeyShare, KeyConsumption:
			v, ok := key.Values[u.ID]
			switch {
			case ok && v != nil:
				w.Weight = v
			case u.Exempt:
				w.Weight = new(big.Rat)
			default:
				return nil, fmt.Errorf("%w: %s (%s)", ErrMissingWeight, u.ID, key.Kind)
			}
		case KeyTable:
			a, ok := key.Amounts[u.ID]
			switch {
			case u.Exempt:
				w.Weight = new(big.Rat)
			case !ok:
				return nil, fmt.Errorf("%w: %s (table)", ErrMissingWeight, u.ID)
			case a < 0 || total.Amount < 0:
				return nil, ErrNegativeTable
			default:
				w.Weight = new(big.Rat).SetInt64(a)
				tableSum += a
			}
		default:
			return nil, fmt.Errorf("%w: %q", ErrUnknownKeyKind, string(key.Kind))
		}
		ws[i] = w
	}
	if key.Kind == KeyTable && tableSum != total.Amount {
		return nil, fmt.Errorf("%w: table %d, total %d", ErrTableMismatch, tableSum, total.Amount)
	}
	return ws, nil
}

func composite(total money.Money, scope string, units []Unit, key Key, order OrderFn) (Result, error) {
	if len(key.Parts) == 0 {
		return Result{}, ErrEmptyComposite
	}
	fsum := new(big.Rat)
	fr := make([]*big.Rat, len(key.Parts))
	for i, p := range key.Parts {
		if p.Fraction == nil || p.Fraction.Sign() <= 0 {
			return Result{}, ErrBadFractions
		}
		fr[i] = p.Fraction
		fsum.Add(fsum, p.Fraction)
	}
	if fsum.Cmp(big.NewRat(1, 1)) != 0 {
		return Result{}, ErrBadFractions
	}
	if err := total.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidCurrency, err)
	}
	abs := total.Amount
	if abs < 0 {
		if abs == minInt64 {
			return Result{}, money.ErrOverflow
		}
		abs = -abs
	}
	rank := make([]int, len(fr))
	for i := range rank {
		rank[i] = i
	}
	totals := largestRemainder(abs, fr, rank)

	sorted := make([]Unit, len(units))
	copy(sorted, units)
	sort.SliceStable(sorted, func(i, j int) bool { return order(sorted[i].ID, sorted[j].ID) < 0 })

	sums := make([]int64, len(sorted))
	snap := BasisSnapshot{Key: KeyComposite, Scope: scope, Total: total, Parts: make([]BasisSnapshot, len(key.Parts))}
	for pi, p := range key.Parts {
		pt := totals[pi]
		if total.Amount < 0 {
			pt = -pt
		}
		r, err := ByKey(money.New(pt, total.Currency), scope, sorted, p.Key, order)
		if err != nil {
			return Result{}, fmt.Errorf("composite part %d: %w", pi, err)
		}
		for i, s := range r.Shares {
			sums[i] += s.Amount.Amount
		}
		r.Basis.Fraction = p.Fraction.RatString()
		snap.Parts[pi] = r.Basis
	}
	res := Result{Shares: make([]Share, len(sorted)), Basis: snap}
	for i := range sorted {
		res.Shares[i] = Share{UnitID: sorted[i].ID, Amount: money.New(sums[i], total.Currency)}
	}
	return res, nil
}
