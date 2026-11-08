package allocate_test

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/allocate"
)

func id(i int) uuid.UUID { return uuid.UUID{14: byte(i >> 8), 15: byte(i)} }

func try(n int64) money.Money { return money.New(n, money.TRY) }

func sum(_ interface{ Helper() }, r allocate.Result) int64 {
	var s int64
	for _, sh := range r.Shares {
		s += sh.Amount.Amount
	}
	return s
}

func amounts(r allocate.Result) []int64 {
	out := make([]int64, len(r.Shares))
	for i, s := range r.Shares {
		out[i] = s.Amount.Amount
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAllocateLargestRemainder(t *testing.T) {
	cases := []struct {
		name    string
		total   int64
		weights []allocate.Weight
		want    []int64
	}{
		{"even", 300, []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 1)}, []int64{100, 100, 100}},
		{"thirds leftover to first in order", 100, []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 1)}, []int64{34, 33, 33}},
		{"largest remainder wins", 10, []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 2), allocate.IntWeight(id(3), 4)}, []int64{1, 3, 6}},
		{"remainder .857 to heavy unit", 100, []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 6)}, []int64{14, 86}},
		{"zero weight gets nothing", 100, []allocate.Weight{allocate.IntWeight(id(1), 0), allocate.IntWeight(id(2), 3), allocate.IntWeight(id(3), 3)}, []int64{0, 50, 50}},
		{"exempt gets nothing", 100, []allocate.Weight{{UnitID: id(1), Weight: big.NewRat(5, 1), Exempt: true}, allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 2)}, []int64{0, 33, 67}},
		{"exempt with nil weight", 90, []allocate.Weight{{UnitID: id(1), Exempt: true}, allocate.IntWeight(id(2), 1)}, []int64{0, 90}},
		{"negative total", -100, []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 1)}, []int64{-34, -33, -33}},
		{"zero total", 0, []allocate.Weight{allocate.IntWeight(id(1), 1)}, []int64{0}},
		{"rational weights", 1000, []allocate.Weight{{UnitID: id(1), Weight: allocate.Frac(15, 1000)}, {UnitID: id(2), Weight: allocate.Frac(35, 1000)}}, []int64{300, 700}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := allocate.Allocate(try(c.total), c.weights, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := amounts(r); !eq(got, c.want) {
				t.Errorf("got %v want %v", got, c.want)
			}
			if sum(t, r) != c.total {
				t.Errorf("sum %d != %d", sum(t, r), c.total)
			}
		})
	}
}

func TestAllocateZeroDecimalCurrency(t *testing.T) {
	ws := []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 1)}
	r, err := allocate.Allocate(money.New(100, money.JPY), ws, nil)
	if err != nil || sum(t, r) != 100 || r.Shares[0].Amount.Currency != money.JPY {
		t.Fatalf("%v %v", r, err)
	}
	r, err = allocate.Allocate(money.New(1000, money.KWD), ws, nil)
	if err != nil || sum(t, r) != 1000 {
		t.Fatalf("%v %v", r, err)
	}
}

func TestAllocateOrderDeterminesTies(t *testing.T) {
	ws := []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 1)}
	keys := map[uuid.UUID]string{id(1): "10", id(2): "2", id(3): "3"}
	r, err := allocate.Allocate(try(100), ws, allocate.ByKeys(keys))
	if err != nil {
		t.Fatal(err)
	}
	// natural order: door "2" (id 2), "3" (id 3), "10" (id 1)
	if r.Shares[0].UnitID != id(2) || r.Shares[2].UnitID != id(1) {
		t.Fatalf("order: %v", r.Shares)
	}
	if got := amounts(r); !eq(got, []int64{34, 33, 33}) {
		t.Fatalf("got %v", got)
	}
}

func TestNaturalOrder(t *testing.T) {
	keys := map[uuid.UUID]string{
		id(1): "A-10", id(2): "A-2", id(3): "A-02b", id(4): "A-2", id(5): "A-2x", id(6): "007", id(7): "7", id(8): ""}
	ws := make([]allocate.Weight, 0)
	for i := 1; i <= 8; i++ {
		ws = append(ws, allocate.IntWeight(id(i), 1))
	}
	r, err := allocate.Allocate(try(8), ws, allocate.ByKeys(keys))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range r.Shares {
		got = append(got, keys[s.UnitID])
	}
	want := []string{"", "007", "7", "A-2", "A-2", "A-02b", "A-2x", "A-10"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// "007" and "7" tie on key, so ids decide; "A-2" duplicates too.
	if r.Shares[1].UnitID != id(6) || r.Shares[2].UnitID != id(7) {
		t.Fatalf("tie by id: %v", r.Shares[1:3])
	}
	// number-length comparison: A-9 < A-10
	k2 := map[uuid.UUID]string{id(1): "A-9", id(2): "A-10"}
	r2, _ := allocate.Allocate(try(2), []allocate.Weight{allocate.IntWeight(id(2), 1), allocate.IntWeight(id(1), 1)}, allocate.ByKeys(k2))
	if r2.Shares[0].UnitID != id(1) {
		t.Fatal("A-9 before A-10")
	}
	// A-12 vs A-13 (same length, different digits) and prefix strings
	k3 := map[uuid.UUID]string{id(1): "A-13", id(2): "A-12", id(3): "A"}
	r3, _ := allocate.Allocate(try(3), []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(2), 1), allocate.IntWeight(id(3), 1)}, allocate.ByKeys(k3))
	if r3.Shares[0].UnitID != id(3) || r3.Shares[1].UnitID != id(2) || r3.Shares[2].UnitID != id(1) {
		t.Fatalf("order %v", r3.Shares)
	}
}

func TestAllocateErrors(t *testing.T) {
	one := []allocate.Weight{allocate.IntWeight(id(1), 1)}
	cases := []struct {
		name  string
		total money.Money
		ws    []allocate.Weight
		want  error
	}{
		{"no units", try(10), nil, allocate.ErrNoUnits},
		{"all zero", try(10), []allocate.Weight{allocate.IntWeight(id(1), 0)}, allocate.ErrNoWeight},
		{"all exempt", try(10), []allocate.Weight{{UnitID: id(1), Weight: big.NewRat(1, 1), Exempt: true}}, allocate.ErrNoWeight},
		{"negative weight", try(10), []allocate.Weight{allocate.IntWeight(id(1), -1)}, allocate.ErrNegativeWeight},
		{"nil weight", try(10), []allocate.Weight{{UnitID: id(1)}}, allocate.ErrNegativeWeight},
		{"duplicate", try(10), []allocate.Weight{allocate.IntWeight(id(1), 1), allocate.IntWeight(id(1), 1)}, allocate.ErrDuplicateUnit},
		{"bad currency", money.New(10, "XXX"), one, allocate.ErrInvalidCurrency},
		{"minint", try(math.MinInt64), one, money.ErrOverflow},
	}
	for _, c := range cases {
		if _, err := allocate.Allocate(c.total, c.ws, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// zero total over nothing is fine
	if r, err := allocate.Allocate(try(0), nil, nil); err != nil || len(r.Shares) != 0 {
		t.Errorf("zero total over nothing: %v %v", r, err)
	}
}

func units(n int) []allocate.Unit {
	us := make([]allocate.Unit, n)
	for i := range us {
		us[i] = allocate.Unit{ID: id(i + 1)}
	}
	return us
}

func TestByKeyKinds(t *testing.T) {
	us := units(3)
	vals := map[uuid.UUID]*big.Rat{id(1): big.NewRat(50, 1), id(2): big.NewRat(30, 1), id(3): big.NewRat(20, 1)}

	r, err := allocate.ByKey(try(1000), "site", us, allocate.Equal(), nil)
	if err != nil || !eq(amounts(r), []int64{334, 333, 333}) || r.Basis.Key != allocate.KeyEqual || r.Basis.Scope != "site" {
		t.Errorf("equal: %v %v", r, err)
	}
	for kind, key := range map[allocate.KeyKind]allocate.Key{allocate.KeyArea: allocate.Area(vals), allocate.KeyShare: allocate.LandShare(vals), allocate.KeyConsumption: allocate.Consumption(vals)} {
		r, err := allocate.ByKey(try(1000), "block:A", us, key, nil)
		if err != nil || !eq(amounts(r), []int64{500, 300, 200}) || r.Basis.Key != kind {
			t.Errorf("%s: %v %v", kind, r, err)
		}
	}
	r, err = allocate.ByKey(try(600), "site", us, allocate.Table(map[uuid.UUID]int64{id(1): 100, id(2): 200, id(3): 300}), nil)
	if err != nil || !eq(amounts(r), []int64{100, 200, 300}) || r.Basis.Key != allocate.KeyTable {
		t.Errorf("table: %v %v", r, err)
	}
	// table mismatch, missing, negative
	tbl := allocate.Table(map[uuid.UUID]int64{id(1): 100, id(2): 200, id(3): 300})
	if _, err := allocate.ByKey(try(601), "", us, tbl, nil); !errors.Is(err, allocate.ErrTableMismatch) {
		t.Errorf("mismatch: %v", err)
	}
	if _, err := allocate.ByKey(try(300), "", us, allocate.Table(map[uuid.UUID]int64{id(1): 100, id(2): 200}), nil); !errors.Is(err, allocate.ErrMissingWeight) {
		t.Errorf("missing: %v", err)
	}
	if _, err := allocate.ByKey(try(0), "", us, allocate.Table(map[uuid.UUID]int64{id(1): -100, id(2): 100, id(3): 0}), nil); !errors.Is(err, allocate.ErrNegativeTable) {
		t.Errorf("negative amount: %v", err)
	}
	if _, err := allocate.ByKey(try(-600), "", us, tbl, nil); !errors.Is(err, allocate.ErrNegativeTable) {
		t.Errorf("negative total: %v", err)
	}
	// exempt in table: not required, not counted
	us2 := []allocate.Unit{{ID: id(1)}, {ID: id(2), Exempt: true}}
	r, err = allocate.ByKey(try(100), "", us2, allocate.Table(map[uuid.UUID]int64{id(1): 100}), nil)
	if err != nil || !eq(amounts(r), []int64{100, 0}) {
		t.Errorf("table exempt: %v %v", r, err)
	}
	// missing area for a non-exempt unit, but not for an exempt one
	if _, err := allocate.ByKey(try(10), "", us, allocate.Area(map[uuid.UUID]*big.Rat{id(1): big.NewRat(1, 1)}), nil); !errors.Is(err, allocate.ErrMissingWeight) {
		t.Errorf("missing area: %v", err)
	}
	r, err = allocate.ByKey(try(10), "", us2, allocate.Area(map[uuid.UUID]*big.Rat{id(1): big.NewRat(1, 1)}), nil)
	if err != nil || !eq(amounts(r), []int64{10, 0}) {
		t.Errorf("area exempt: %v %v", r, err)
	}
	if _, err := allocate.ByKey(try(10), "", us, allocate.Key{Kind: "bogus"}, nil); !errors.Is(err, allocate.ErrUnknownKeyKind) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestComposite(t *testing.T) {
	us := units(3)
	cons := map[uuid.UUID]*big.Rat{id(1): big.NewRat(10, 1), id(2): big.NewRat(0, 1), id(3): big.NewRat(30, 1)}
	area := map[uuid.UUID]*big.Rat{id(1): big.NewRat(60, 1), id(2): big.NewRat(60, 1), id(3): big.NewRat(80, 1)}
	key := allocate.Composite(
		allocate.Part{Fraction: big.NewRat(7, 10), Key: allocate.Consumption(cons)},
		allocate.Part{Fraction: big.NewRat(3, 10), Key: allocate.Area(area)},
	)
	r, err := allocate.ByKey(try(10000), "block:A", us, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 7000 by 10:0:30 -> 1750,0,5250 ; 3000 by 60:60:80 -> 900,900,1200
	if got := amounts(r); !eq(got, []int64{2650, 900, 6450}) {
		t.Fatalf("got %v", got)
	}
	if r.Basis.Key != allocate.KeyComposite || len(r.Basis.Parts) != 2 || r.Basis.Parts[0].Fraction != "7/10" || r.Basis.Parts[1].Total.Amount != 3000 {
		t.Fatalf("basis %+v", r.Basis)
	}
	// negative total
	rn, err := allocate.ByKey(try(-10000), "", us, key, nil)
	if err != nil || !eq(amounts(rn), []int64{-2650, -900, -6450}) {
		t.Fatalf("negative: %v %v", rn, err)
	}
	// composite with table part must match the part total
	tkey := allocate.Composite(
		allocate.Part{Fraction: big.NewRat(1, 2), Key: allocate.Table(map[uuid.UUID]int64{id(1): 1, id(2): 2, id(3): 3})},
		allocate.Part{Fraction: big.NewRat(1, 2), Key: allocate.Equal()},
	)
	rt, err := allocate.ByKey(try(12), "", us, tkey, nil)
	if err != nil || !eq(amounts(rt), []int64{3, 4, 5}) {
		t.Fatalf("composite table: %v %v", rt, err)
	}
	if _, err := allocate.ByKey(try(14), "", us, tkey, nil); !errors.Is(err, allocate.ErrTableMismatch) {
		t.Fatalf("composite table mismatch: %v", err)
	}
	// rounding across parts: 1 minor unit, 1/3+2/3 -> goes to the larger remainder part
	third := allocate.Composite(
		allocate.Part{Fraction: big.NewRat(1, 3), Key: allocate.Equal()},
		allocate.Part{Fraction: big.NewRat(2, 3), Key: allocate.Equal()},
	)
	r3, err := allocate.ByKey(try(1), "", us[:1], third, nil)
	if err != nil || sum(t, r3) != 1 {
		t.Fatalf("1 unit: %v %v", r3, err)
	}
}

func TestCompositeErrors(t *testing.T) {
	us := units(2)
	half := big.NewRat(1, 2)
	cases := map[string]allocate.Key{
		"empty":     allocate.Composite(),
		"nil frac":  allocate.Composite(allocate.Part{Key: allocate.Equal()}),
		"zero frac": allocate.Composite(allocate.Part{Fraction: new(big.Rat), Key: allocate.Equal()}, allocate.Part{Fraction: big.NewRat(1, 1), Key: allocate.Equal()}),
		"sum != 1":  allocate.Composite(allocate.Part{Fraction: half, Key: allocate.Equal()}),
	}
	want := map[string]error{"empty": allocate.ErrEmptyComposite, "nil frac": allocate.ErrBadFractions, "zero frac": allocate.ErrBadFractions, "sum != 1": allocate.ErrBadFractions}
	for n, k := range cases {
		if _, err := allocate.ByKey(try(10), "", us, k, nil); !errors.Is(err, want[n]) {
			t.Errorf("%s: %v", n, err)
		}
	}
	ok := allocate.Composite(allocate.Part{Fraction: big.NewRat(1, 1), Key: allocate.Equal()})
	if _, err := allocate.ByKey(money.New(10, "XXX"), "", us, ok, nil); !errors.Is(err, allocate.ErrInvalidCurrency) {
		t.Errorf("currency: %v", err)
	}
	if _, err := allocate.ByKey(try(math.MinInt64), "", us, ok, nil); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("minint: %v", err)
	}
	bad := allocate.Composite(allocate.Part{Fraction: big.NewRat(1, 1), Key: allocate.Area(nil)})
	if _, err := allocate.ByKey(try(10), "", us, bad, nil); !errors.Is(err, allocate.ErrMissingWeight) {
		t.Errorf("part error: %v", err)
	}
}

func TestBasisSnapshotStableJSON(t *testing.T) {
	us := units(3)
	share := map[uuid.UUID]*big.Rat{id(1): big.NewRat(15, 1000), id(2): big.NewRat(35, 1000), id(3): big.NewRat(50, 1000)}
	run := func() string {
		r, err := allocate.ByKey(try(10000), "site", us, allocate.LandShare(share), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(r.Basis)
		return string(b)
	}
	a, b := run(), run()
	if a != b {
		t.Fatal("snapshot not stable")
	}
	var back allocate.BasisSnapshot
	if err := json.Unmarshal([]byte(a), &back); err != nil || back.WeightTotal != "1/10" || back.Weights[0].Weight != "3/200" {
		t.Fatalf("decode: %+v %v", back, err)
	}
}

func TestPropAllocateSumsAndFairness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 40).Draw(t, "n")
		ws := make([]allocate.Weight, n)
		anyPos := false
		for i := range ws {
			w := rapid.Int64Range(0, 5000).Draw(t, "w")
			ex := rapid.Bool().Draw(t, "exempt")
			ws[i] = allocate.Weight{UnitID: id(i + 1), Weight: big.NewRat(w, int64(rapid.IntRange(1, 7).Draw(t, "den"))), Exempt: ex}
			if w > 0 && !ex {
				anyPos = true
			}
		}
		total := rapid.Int64Range(-10_000_000, 10_000_000).Draw(t, "total")
		cur := rapid.SampledFrom([]money.Code{money.TRY, money.JPY, money.KWD}).Draw(t, "cur")
		r, err := allocate.Allocate(money.New(total, cur), ws, nil)
		if !anyPos && total != 0 {
			if !errors.Is(err, allocate.ErrNoWeight) {
				t.Fatalf("want ErrNoWeight, got %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if sum(t, r) != total {
			t.Fatalf("sum %d != %d", sum(t, r), total)
		}
		// each share within 1 minor unit of its exact quota; exempt/zero get 0.
		wsum := new(big.Rat)
		for _, w := range ws {
			if !w.Exempt {
				wsum.Add(wsum, w.Weight)
			}
		}
		byID := map[uuid.UUID]allocate.Weight{}
		for _, w := range ws {
			byID[w.UnitID] = w
		}
		for _, s := range r.Shares {
			w := byID[s.UnitID]
			if w.Exempt || w.Weight.Sign() == 0 {
				if s.Amount.Amount != 0 {
					t.Fatalf("exempt/zero got %d", s.Amount.Amount)
				}
				continue
			}
			if wsum.Sign() == 0 {
				continue
			}
			q := new(big.Rat).Mul(big.NewRat(total, 1), w.Weight)
			q.Quo(q, wsum)
			diff := new(big.Rat).Sub(q, big.NewRat(s.Amount.Amount, 1))
			if diff.Abs(diff).Cmp(big.NewRat(1, 1)) >= 0 {
				t.Fatalf("share %d too far from quota %s", s.Amount.Amount, q.FloatString(3))
			}
		}
	})
}

func TestPropAllocateIsOrderIndependentWithSameOrderFn(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "n")
		ws := make([]allocate.Weight, n)
		for i := range ws {
			ws[i] = allocate.IntWeight(id(i+1), rapid.Int64Range(1, 100).Draw(t, "w"))
		}
		total := try(rapid.Int64Range(0, 1_000_000).Draw(t, "total"))
		a, err := allocate.Allocate(total, ws, nil)
		if err != nil {
			t.Fatal(err)
		}
		perm := rapid.Permutation(ws).Draw(t, "perm")
		b, err := allocate.Allocate(total, perm, nil)
		if err != nil {
			t.Fatal(err)
		}
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatal("result depends on input order")
		}
	})
}

func TestPropCompositeSums(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 15).Draw(t, "n")
		us := units(n)
		cons := map[uuid.UUID]*big.Rat{}
		area := map[uuid.UUID]*big.Rat{}
		for _, u := range us {
			cons[u.ID] = big.NewRat(rapid.Int64Range(0, 200).Draw(t, "c"), 1)
			area[u.ID] = big.NewRat(rapid.Int64Range(1, 200).Draw(t, "a"), 1)
		}
		f := rapid.Int64Range(1, 99).Draw(t, "f")
		key := allocate.Composite(
			allocate.Part{Fraction: big.NewRat(f, 100), Key: allocate.Consumption(cons)},
			allocate.Part{Fraction: big.NewRat(100-f, 100), Key: allocate.Area(area)},
		)
		total := rapid.Int64Range(-5_000_000, 5_000_000).Draw(t, "total")
		r, err := allocate.ByKey(try(total), "site", us, key, nil)
		if errors.Is(err, allocate.ErrNoWeight) {
			return // all-zero consumption in a part with a non-zero part total
		}
		if err != nil {
			t.Fatal(err)
		}
		if sum(t, r) != total {
			t.Fatalf("sum %d != %d", sum(t, r), total)
		}
	})
}
