package paymentalloc_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	pa "github.com/openbekci/openbekci/server/internal/core/money/paymentalloc"
)

func id(i int) uuid.UUID      { return uuid.UUID{15: byte(i)} }
func try(n int64) money.Money { return money.New(n, money.TRY) }
func day(n int) time.Time     { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n) }
func item(i int, due int, k pa.Kind, rem int64) pa.OpenItem {
	return pa.OpenItem{ChargeID: id(i), DueDate: day(due), Kind: k, Remaining: try(rem)}
}

func got(r pa.Result) []int64 {
	var out []int64
	for _, a := range r.Allocations {
		out = append(out, int64(a.ChargeID[15]), a.Amount.Amount)
	}
	return out
}

func same(a, b []int64) bool {
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

func TestOrders(t *testing.T) {
	open := []pa.OpenItem{
		item(3, 60, pa.Principal, 100),
		item(1, 30, pa.Fee, 20),
		item(2, 30, pa.Principal, 100),
		item(4, 10, pa.Fee, 5),
		item(5, 30, pa.Principal, 50), // same due date as charge 2: tie by id
	}
	cases := []struct {
		name    string
		order   pa.Order
		payment int64
		want    []int64 // id, amount pairs
		credit  int64
	}{
		{"default partial", pa.OldestFirstPrincipalBeforeFee, 120, []int64{2, 100, 5, 20}, 0},
		{"default all principal then fee", pa.OldestFirstPrincipalBeforeFee, 300, []int64{2, 100, 5, 50, 3, 100, 4, 5, 1, 20}, 25},
		{"fees first", pa.FeesFirst, 30, []int64{4, 5, 1, 20, 2, 5}, 0},
		{"manual only listed", pa.Manual([]uuid.UUID{id(3), id(1)}), 500, []int64{3, 100, 1, 20}, 380},
		{"manual order and dedupe", pa.Manual([]uuid.UUID{id(5), id(5), id(2)}), 120, []int64{5, 50, 2, 70}, 0},
		{"zero payment", pa.OldestFirstPrincipalBeforeFee, 0, nil, 0},
	}
	for _, c := range cases {
		r, err := pa.Allocate(try(c.payment), open, c.order, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !same(got(r), c.want) || r.Credit.Amount != c.credit {
			t.Errorf("%s: got %v credit %d, want %v credit %d", c.name, got(r), r.Credit.Amount, c.want, c.credit)
		}
	}
}

func TestManualPrincipalBeforeFeeOfSameCharge(t *testing.T) {
	open := []pa.OpenItem{item(1, 0, pa.Fee, 10), item(1, 0, pa.Principal, 100)}
	r, err := pa.Allocate(try(105), open, pa.Manual([]uuid.UUID{id(1)}), nil)
	if err != nil || r.Allocations[0].Kind != pa.Principal || r.Allocations[1].Amount.Amount != 5 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSkipsZeroRemaining(t *testing.T) {
	open := []pa.OpenItem{item(1, 0, pa.Principal, 0), item(2, 5, pa.Principal, 10)}
	r, _ := pa.Allocate(try(5), open, pa.OldestFirstPrincipalBeforeFee, nil)
	if len(r.Allocations) != 1 || r.Allocations[0].ChargeID != id(2) {
		t.Fatalf("%+v", r)
	}
}

func TestErrors(t *testing.T) {
	ok := []pa.OpenItem{item(1, 0, pa.Principal, 10)}
	if _, err := pa.Allocate(try(1), ok, nil, nil); !errors.Is(err, pa.ErrNilOrder) {
		t.Error(err)
	}
	if _, err := pa.Allocate(try(-1), ok, pa.FeesFirst, nil); !errors.Is(err, pa.ErrNegativePayment) {
		t.Error(err)
	}
	if _, err := pa.Allocate(money.New(1, "XXX"), ok, pa.FeesFirst, nil); err == nil {
		t.Error("currency")
	}
	if _, err := pa.Allocate(money.New(1, money.EUR), ok, pa.FeesFirst, nil); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Error(err)
	}
	if _, err := pa.Allocate(try(1), []pa.OpenItem{item(1, 0, pa.Principal, -1)}, pa.FeesFirst, nil); !errors.Is(err, pa.ErrNegativeItem) {
		t.Error(err)
	}
	if _, err := pa.Allocate(try(1), []pa.OpenItem{item(1, 0, "bogus", 1)}, pa.FeesFirst, nil); !errors.Is(err, pa.ErrBadKind) {
		t.Error(err)
	}
}

func TestPropNeverOverAllocates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 12).Draw(t, "n")
		open := make([]pa.OpenItem, n)
		remByKey := map[[2]string]int64{}
		for i := range open {
			k := pa.Principal
			if rapid.Bool().Draw(t, "fee") {
				k = pa.Fee
			}
			open[i] = item(i+1, rapid.IntRange(0, 200).Draw(t, "due"), k, rapid.Int64Range(0, 100000).Draw(t, "rem"))
			remByKey[[2]string{string(rune(i + 1)), string(k)}] = open[i].Remaining.Amount
		}
		payment := rapid.Int64Range(0, 2_000_000).Draw(t, "pay")
		order := pa.OldestFirstPrincipalBeforeFee
		manual := false
		switch rapid.IntRange(0, 2).Draw(t, "order") {
		case 1:
			order = pa.FeesFirst
		case 2:
			manual = true
			var ids []uuid.UUID
			for _, i := range rapid.SliceOfN(rapid.IntRange(1, 14), 0, 6).Draw(t, "ids") {
				ids = append(ids, id(i))
			}
			order = pa.Manual(ids)
		}
		r, err := pa.Allocate(try(payment), open, order, nil)
		if errors.Is(err, pa.ErrUnknownCharge) {
			return // a listed id was not open: refused, nothing allocated
		}
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		per := map[[2]string]int64{}
		for _, a := range r.Allocations {
			if a.Amount.Amount <= 0 {
				t.Fatalf("non-positive allocation %d", a.Amount.Amount)
			}
			total += a.Amount.Amount
			per[[2]string{string(rune(a.ChargeID[15])), string(a.Kind)}] += a.Amount.Amount
		}
		for k, v := range per {
			if v > remByKey[k] {
				t.Fatalf("over-allocated %v: %d > %d", k, v, remByKey[k])
			}
		}
		if total+r.Credit.Amount != payment || r.Credit.Amount < 0 {
			t.Fatalf("sum %d + credit %d != %d", total, r.Credit.Amount, payment)
		}
		again, _ := pa.Allocate(try(payment), open, order, nil)
		if !same(got(r), got(again)) {
			t.Fatal("not deterministic")
		}
		// default and fees-first orders leave credit only when everything is paid
		if !manual && r.Credit.Amount > 0 {
			var all int64
			for _, it := range open {
				all += it.Remaining.Amount
			}
			if total != all {
				t.Fatalf("credit %d while items remain open", r.Credit.Amount)
			}
		}
	})
}
