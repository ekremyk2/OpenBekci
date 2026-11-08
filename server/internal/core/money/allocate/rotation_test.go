package allocate_test

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/allocate"
)

func equalWeights(n int) []allocate.Weight {
	ws := make([]allocate.Weight, n)
	for i := range ws {
		ws[i] = allocate.IntWeight(id(i+1), 1)
	}
	return ws
}

// F10: without rotation the same units get the extra kuruş every month; with a
// period-keyed rotation the leftover moves around, deterministically.
func TestRotationSpreadsLeftovers(t *testing.T) {
	ws := equalWeights(7) // 100 kuruş over 7 units: two units get 15
	plain, err := allocate.Allocate(try(100), ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(amounts(plain), []int64{15, 15, 14, 14, 14, 14, 14}) {
		t.Fatalf("plain %v", amounts(plain))
	}
	extra := make([]int, 7)
	for m := 1; m <= 12; m++ {
		key := fmt.Sprintf("2026-%02d", m)
		a, err := allocate.Allocate(try(100), ws, nil, allocate.WithRotation(key))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := allocate.Allocate(try(100), ws, nil, allocate.WithRotation(key))
		if !eq(amounts(a), amounts(b)) {
			t.Fatal("rotation is not deterministic")
		}
		if sum(t, a) != 100 {
			t.Fatalf("sum %d", sum(t, a))
		}
		for i, s := range a.Shares {
			if s.Amount.Amount == 15 {
				extra[i]++
			}
		}
	}
	// over a year every unit is hit at least once and no unit carries it every month
	for i, n := range extra {
		if n == 12 {
			t.Errorf("unit %d got the leftover in every period", i)
		}
	}
	hit := 0
	for _, n := range extra {
		if n > 0 {
			hit++
		}
	}
	if hit < 5 {
		t.Errorf("leftover reached only %d units in a year: %v", hit, extra)
	}
}

func TestRotationInByKeyAndComposite(t *testing.T) {
	units := make([]allocate.Unit, 7)
	for i := range units {
		units[i] = allocate.Unit{ID: id(i + 1)}
	}
	key := allocate.Composite(
		allocate.Part{Fraction: big.NewRat(7, 10), Key: allocate.Equal()},
		allocate.Part{Fraction: big.NewRat(3, 10), Key: allocate.Equal()},
	)
	seen := map[string]bool{}
	for m := 1; m <= 12; m++ {
		r, err := allocate.ByKey(try(1003), "site", units, key, nil, allocate.WithRotation(fmt.Sprintf("2026-%02d", m)))
		if err != nil || sum(t, r) != 1003 {
			t.Fatalf("%v %d", err, sum(t, r))
		}
		seen[fmt.Sprint(amounts(r))] = true
		leaf, err := allocate.ByKey(try(100), "site", units, allocate.Equal(), nil, allocate.WithRotation("k"))
		if err != nil || sum(t, leaf) != 100 {
			t.Fatal(err)
		}
	}
	if len(seen) < 2 {
		t.Error("composite rotation never changed the distribution")
	}
	// no units: rotation is a no-op
	if r, err := allocate.Allocate(try(0), nil, nil, allocate.WithRotation("x")); err != nil || len(r.Shares) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestPropRotationKeepsSumAndFloors(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(t, "n")
		total := rapid.Int64Range(-1_000_000, 1_000_000).Draw(t, "total")
		ws := equalWeights(n)
		plain, err := allocate.Allocate(try(total), ws, nil)
		if err != nil {
			t.Fatal(err)
		}
		rot, err := allocate.Allocate(try(total), ws, nil, allocate.WithRotation(rapid.String().Draw(t, "key")))
		if err != nil {
			t.Fatal(err)
		}
		if sum(t, rot) != total {
			t.Fatalf("sum %d != %d", sum(t, rot), total)
		}
		// rotation only moves leftover units: every share stays within one unit of the plain one
		for i := range rot.Shares {
			if diff := rot.Shares[i].Amount.Amount - plain.Shares[i].Amount.Amount; diff > 1 || diff < -1 {
				t.Fatalf("share %d moved by %d", i, diff)
			}
		}
	})
}

func TestTableSumOverflow(t *testing.T) {
	units := []allocate.Unit{{ID: id(1)}, {ID: id(2)}, {ID: id(3)}}
	tbl := allocate.Table(map[uuid.UUID]int64{id(1): math.MaxInt64, id(2): math.MaxInt64, id(3): 2})
	// MaxInt64 + MaxInt64 + 2 would wrap to 0 and pass a zero total
	if _, err := allocate.ByKey(money.Zero(money.TRY), "site", units, tbl, nil); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("%v", err)
	}
}
