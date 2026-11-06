package allocate

import (
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
)

// Errors returned by the allocator.
var (
	ErrNoUnits         = errors.New("allocate: no units to allocate to")
	ErrNoWeight        = errors.New("allocate: all weights are zero or exempt")
	ErrNegativeWeight  = errors.New("allocate: negative or missing weight")
	ErrDuplicateUnit   = errors.New("allocate: duplicate unit")
	ErrMissingWeight   = errors.New("allocate: no value for unit")
	ErrTableMismatch   = errors.New("allocate: table amounts do not sum to the total")
	ErrBadFractions    = errors.New("allocate: composite fractions must be positive and sum to 1")
	ErrNegativeTable   = errors.New("allocate: table keys need a non-negative total and amounts")
	ErrEmptyComposite  = errors.New("allocate: composite key without parts")
	ErrUnknownKeyKind  = errors.New("allocate: unknown key kind")
	ErrInvalidCurrency = errors.New("allocate: invalid currency")
)

// Weight is one unit's relative weight in an allocation. A nil Weight is
// invalid unless Exempt. Exempt units always get zero, whatever their weight.
type Weight struct {
	UnitID uuid.UUID
	Weight *big.Rat
	Exempt bool
}

// IntWeight is a whole-number weight (area in whole m², consumption count).
func IntWeight(id uuid.UUID, n int64) Weight {
	return Weight{UnitID: id, Weight: new(big.Rat).SetInt64(n)}
}

// Frac is the rational n/d (d must be non-zero) for share and area values.
func Frac(n, d int64) *big.Rat { return big.NewRat(n, d) }

// OrderFn is the deterministic unit order: it breaks ties when remainders are
// equal and fixes the order of the returned shares. Callers pass an order
// derived from the unit door-number sort key, then the id (see ByKeys).
type OrderFn func(a, b uuid.UUID) int

// ByID orders by unit id bytes; it is the default when order is nil.
func ByID() OrderFn {
	return func(a, b uuid.UUID) int { return bytesCmp(a[:], b[:]) }
}

func bytesCmp(a, b []byte) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ByKeys orders by a per-unit sort key (the door number, compared naturally
// so "2" < "10" and "A-2" < "A-10"), then by id. Units without a key sort
// before keyed units by their (empty) key.
func ByKeys(keys map[uuid.UUID]string) OrderFn {
	return func(a, b uuid.UUID) int {
		if c := naturalCmp(keys[a], keys[b]); c != 0 {
			return c
		}
		return bytesCmp(a[:], b[:])
	}
}

// naturalCmp compares strings treating digit runs as numbers.
func naturalCmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if isDigit(a[i]) && isDigit(b[j]) {
			si := i
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			sj := j
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na, nb := trimZeros(a[si:i]), trimZeros(b[sj:j])
			if len(na) != len(nb) {
				if len(na) < len(nb) {
					return -1
				}
				return 1
			}
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			continue
		}
		if a[i] != b[j] {
			if a[i] < b[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case len(a)-i < len(b)-j:
		return -1
	case len(a)-i > len(b)-j:
		return 1
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func trimZeros(s string) string {
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
	}
	return s
}

// Share is one unit's part of the total.
type Share struct {
	UnitID uuid.UUID   `json:"unit_id"`
	Amount money.Money `json:"amount"`
}

// WeightBasis is one unit's effective weight in a snapshot ("15/1000", "1", "0").
type WeightBasis struct {
	UnitID uuid.UUID `json:"unit_id"`
	Weight string    `json:"weight"`
	Exempt bool      `json:"exempt,omitempty"`
}

// BasisSnapshot is the JSON stored on each charge as allocation_basis: which
// key and scope produced it, the total, each unit's effective weight and the
// weight total, so a charge stays explainable after weights change later.
// A composite key nests one snapshot per part with its Fraction.
type BasisSnapshot struct {
	Key         KeyKind         `json:"key"`
	Scope       string          `json:"scope,omitempty"`
	Total       money.Money     `json:"total"`
	WeightTotal string          `json:"weight_total,omitempty"`
	Weights     []WeightBasis   `json:"weights,omitempty"`
	Fraction    string          `json:"fraction,omitempty"`
	Parts       []BasisSnapshot `json:"parts,omitempty"`
}

// Result is the outcome of an allocation. Shares are in the caller's order and
// include every unit (exempt and zero-weight units with a zero amount); the
// amounts sum exactly to the total.
type Result struct {
	Shares []Share
	Basis  BasisSnapshot
}

// Allocate splits total over weights by the largest-remainder method.
//
// Each non-exempt unit i gets floor(total * w_i / W) minor units, W being the
// sum of weights; the units left over (fewer than the number of units) go one
// each to the units with the largest fractional remainders, ties broken by
// order. Zero-weight and exempt units never receive a leftover unit. A
// negative total (a credit note) is allocated as its absolute value and
// negated. The result always sums exactly to total.
func Allocate(total money.Money, weights []Weight, order OrderFn) (Result, error) {
	return allocate(total, weights, order, KeyWeights, "")
}

func allocate(total money.Money, weights []Weight, order OrderFn, kind KeyKind, scope string) (Result, error) {
	if err := total.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidCurrency, err)
	}
	if order == nil {
		order = ByID()
	}
	ws := make([]Weight, len(weights))
	copy(ws, weights)
	sort.SliceStable(ws, func(i, j int) bool { return order(ws[i].UnitID, ws[j].UnitID) < 0 })

	seen := make(map[uuid.UUID]struct{}, len(ws))
	eff := make([]*big.Rat, len(ws))
	wsum := new(big.Rat)
	for i, w := range ws {
		if _, dup := seen[w.UnitID]; dup {
			return Result{}, fmt.Errorf("%w: %s", ErrDuplicateUnit, w.UnitID)
		}
		seen[w.UnitID] = struct{}{}
		switch {
		case w.Exempt:
			eff[i] = new(big.Rat)
		case w.Weight == nil || w.Weight.Sign() < 0:
			return Result{}, fmt.Errorf("%w: %s", ErrNegativeWeight, w.UnitID)
		default:
			eff[i] = w.Weight
		}
		wsum.Add(wsum, eff[i])
	}
	if len(ws) == 0 && total.Amount != 0 {
		return Result{}, ErrNoUnits
	}
	if wsum.Sign() == 0 && total.Amount != 0 {
		return Result{}, ErrNoWeight
	}

	neg := total.Amount < 0
	abs := total.Amount
	if neg {
		if abs == minInt64 {
			return Result{}, money.ErrOverflow
		}
		abs = -abs
	}
	rank := make([]int, len(ws))
	for i := range rank {
		rank[i] = i
	}
	parts := largestRemainder(abs, eff, rank)

	res := Result{Shares: make([]Share, len(ws))}
	snap := BasisSnapshot{Key: kind, Scope: scope, Total: total, WeightTotal: wsum.RatString(), Weights: make([]WeightBasis, len(ws))}
	for i, w := range ws {
		amt := parts[i]
		if neg {
			amt = -amt
		}
		res.Shares[i] = Share{UnitID: w.UnitID, Amount: money.New(amt, total.Currency)}
		snap.Weights[i] = WeightBasis{UnitID: w.UnitID, Weight: eff[i].RatString(), Exempt: w.Exempt}
	}
	res.Basis = snap
	return res, nil
}

const minInt64 = -1 << 63

// largestRemainder distributes total >= 0 over weights (sum > 0 unless total
// is 0) so the parts sum to total. rank breaks remainder ties (lower first).
func largestRemainder(total int64, weights []*big.Rat, rank []int) []int64 {
	out := make([]int64, len(weights))
	if total == 0 || len(weights) == 0 {
		return out
	}
	wsum := new(big.Rat)
	for _, w := range weights {
		wsum.Add(wsum, w)
	}
	bigTotal := new(big.Rat).SetInt64(total)
	rems := make([]*big.Rat, len(weights))
	var assigned int64
	for i, w := range weights {
		q := new(big.Rat).Mul(bigTotal, w)
		q.Quo(q, wsum)
		fl := new(big.Int).Quo(q.Num(), q.Denom()) // q >= 0, so Quo floors
		out[i] = fl.Int64()
		assigned += out[i]
		rems[i] = q.Sub(q, new(big.Rat).SetInt(fl))
	}
	idx := make([]int, len(weights))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if c := rems[idx[a]].Cmp(rems[idx[b]]); c != 0 {
			return c > 0
		}
		return rank[idx[a]] < rank[idx[b]]
	})
	for k := int64(0); k < total-assigned; k++ {
		out[idx[k]]++
	}
	return out
}
