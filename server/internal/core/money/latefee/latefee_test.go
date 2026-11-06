package latefee_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/latefee"
)

func d(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
func dp(y int, m time.Month, dd int) *time.Time {
	t := d(y, m, dd)
	return &t
}
func try(n int64) money.Money { return money.New(n, money.TRY) }

var pol = latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 500, Simple: true}

func charge(principal int64, due time.Time, pays ...latefee.Payment) latefee.OpenCharge {
	return latefee.OpenCharge{ID: uuid.New(), DueDate: due, Principal: try(principal), PaidPrincipal: pays}
}

func pay(on time.Time, n int64) latefee.Payment { return latefee.Payment{On: on, Amount: try(n)} }

func fee(t interface{ Fatal(...any) }, p latefee.Policy, c latefee.OpenCharge, asOf time.Time, fr ...latefee.Freeze) int64 {
	a, err := latefee.Accrue(p, []latefee.OpenCharge{c}, asOf, time.UTC, fr...)
	if err != nil {
		t.Fatal(err)
	}
	return a.Total.Amount
}

func TestAccrueKnownValues(t *testing.T) {
	due := d(2026, 1, 1)
	g5 := pol
	g5.GraceDays = 5
	cases := []struct {
		name string
		p    latefee.Policy
		c    latefee.OpenCharge
		asOf time.Time
		fr   []latefee.Freeze
		want int64
	}{
		{"on due date", pol, charge(100000, due), d(2026, 1, 1), nil, 0},
		{"before due date", pol, charge(100000, due), d(2025, 12, 1), nil, 0},
		{"one day", pol, charge(100000, due), d(2026, 1, 2), nil, 167}, // 166.67 half-up
		{"30 days = 5 %", pol, charge(100000, due), d(2026, 1, 31), nil, 5000},
		{"60 days = 10 % simple", pol, charge(100000, due), d(2026, 3, 2), nil, 10000},
		{"grace: last free day", g5, charge(100000, due), d(2026, 1, 6), nil, 0},
		{"grace: first charged day", g5, charge(100000, due), d(2026, 1, 7), nil, 167},
		{"partial payment on day 10", pol, charge(100000, due, pay(d(2026, 1, 11), 40000)), d(2026, 1, 31), nil, 3600},
		{"paid in full on due date", pol, charge(100000, due, pay(due, 100000)), d(2026, 6, 1), nil, 0},
		{"paid in full on grace day", g5, charge(100000, due, pay(d(2026, 1, 6), 100000)), d(2026, 6, 1), nil, 0},
		{"paid before due date", pol, charge(100000, due, pay(d(2025, 12, 20), 100000)), d(2026, 6, 1), nil, 0},
		{"payment after asOf ignored", pol, charge(100000, due, pay(d(2026, 2, 1), 100000)), d(2026, 1, 31), nil, 5000},
		{"overpayment clamps", pol, charge(100000, due, pay(d(2026, 1, 11), 90000), pay(d(2026, 1, 12), 90000)), d(2026, 1, 31), nil, 1000 + 0*0 + 0}, // placeholder replaced below
		{"freeze 10 days", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11), To: dp(2026, 1, 20)}}, 3333},
		{"open freeze from day 11", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11)}}, 1500},
		{"overlapping freezes merge", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11), To: dp(2026, 1, 15)}, {From: d(2026, 1, 13), To: dp(2026, 1, 20)}, {From: d(2026, 1, 21), To: dp(2026, 1, 22)}}, 3000},
		{"freeze after open freeze", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11)}, {From: d(2026, 1, 15), To: dp(2026, 1, 16)}}, 1500},
		{"reversed freeze ignored", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 20), To: dp(2026, 1, 10)}}, 5000},
		{"zero rate", latefee.Policy{Currency: money.TRY, Simple: true}, charge(100000, due), d(2026, 12, 1), nil, 0},
		{"zero principal", pol, charge(0, due), d(2026, 12, 1), nil, 0},
	}
	// 9 days at 100000 plus day 10 at 10000, then nothing: 910000*500/300000 = 1516.67
	cases[12].want = 1517
	for _, c := range cases {
		if got := fee(t, c.p, c.c, c.asOf, c.fr...); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestAccrueTimezoneAndMultiple(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	due := time.Date(2026, 3, 1, 0, 0, 0, 0, ist)
	asOf := time.Date(2026, 3, 31, 23, 59, 0, 0, ist) // across the DST-free Turkish zone, still day 30
	c1, c2 := charge(100000, due), charge(50000, due)
	a, err := latefee.Accrue(pol, []latefee.OpenCharge{c1, c2}, asOf, ist)
	if err != nil {
		t.Fatal(err)
	}
	if a.PerCharge[0].Fee.Amount != 5000 || a.PerCharge[1].Fee.Amount != 2500 || a.Total.Amount != 7500 || a.PerCharge[0].ChargeID != c1.ID {
		t.Fatalf("%+v", a)
	}
	// nil tz = UTC; empty charges
	e, err := latefee.Accrue(pol, nil, asOf, nil)
	if err != nil || e.Total != money.Zero(money.TRY) || len(e.PerCharge) != 0 {
		t.Fatalf("%+v %v", e, err)
	}
}

func TestPolicyValidate(t *testing.T) {
	bad := []struct {
		p    latefee.Policy
		want error
	}{
		{latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 501, Simple: true}, latefee.ErrRateTooHigh},
		{latefee.Policy{Currency: money.TRY, RateBPSPerMonth: -1, Simple: true}, latefee.ErrBadPolicy},
		{latefee.Policy{Currency: money.TRY, GraceDays: -1, Simple: true}, latefee.ErrBadPolicy},
		{latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 500}, latefee.ErrBadPolicy},
		{latefee.Policy{RateBPSPerMonth: 500, Simple: true}, latefee.ErrBadPolicy},
	}
	for i, b := range bad {
		if err := b.p.Validate(); !errors.Is(err, b.want) {
			t.Errorf("%d: %v", i, err)
		}
		if _, err := latefee.Accrue(b.p, nil, d(2026, 1, 1), nil); err == nil {
			t.Errorf("%d: Accrue accepted a bad policy", i)
		}
	}
	if err := pol.Validate(); err != nil {
		t.Error(err)
	}
}

func TestAccrueErrors(t *testing.T) {
	due := d(2026, 1, 1)
	asOf := d(2026, 2, 1)
	cases := map[string]latefee.OpenCharge{
		"negative principal": {ID: uuid.New(), DueDate: due, Principal: try(-1)},
		"principal currency": {ID: uuid.New(), DueDate: due, Principal: money.New(1, money.EUR)},
		"payment currency":   {ID: uuid.New(), DueDate: due, Principal: try(10), PaidPrincipal: []latefee.Payment{{On: due, Amount: money.New(1, money.EUR)}}},
		"negative payment":   {ID: uuid.New(), DueDate: due, Principal: try(10), PaidPrincipal: []latefee.Payment{{On: due, Amount: try(-1)}}},
	}
	for n, c := range cases {
		if _, err := latefee.Accrue(pol, []latefee.OpenCharge{c}, asOf, nil); err == nil {
			t.Errorf("%s: expected error", n)
		}
	}
	// overflow of the total
	huge := latefee.OpenCharge{ID: uuid.New(), DueDate: due, Principal: try(math.MaxInt64)}
	if _, err := latefee.Accrue(pol, []latefee.OpenCharge{huge}, d(2126, 1, 1), nil); err != nil && !errors.Is(err, money.ErrOverflow) {
		t.Errorf("huge: %v", err)
	}
	// total overflow across charges
	big := latefee.OpenCharge{ID: uuid.New(), DueDate: due, Principal: try(math.MaxInt64 / 2)}
	if _, err := latefee.Accrue(pol, []latefee.OpenCharge{big, big, big}, d(2600, 1, 1), nil); err == nil {
		t.Error("expected overflow")
	}
	// payment sums beyond int64 clamp instead of wrapping
	p := charge(10, due, pay(due, math.MaxInt64), pay(due, math.MaxInt64))
	if got := fee(t, pol, p, asOf); got != 0 {
		t.Errorf("clamped payments: %d", got)
	}
}

func TestExpectedAndReconcile(t *testing.T) {
	acc := latefee.Accrual{Total: try(5000)}
	exp, err := latefee.Expected(acc, latefee.Waiver{Amount: try(1500)}, latefee.Waiver{Amount: try(500)})
	if err != nil || exp.Amount != 3000 {
		t.Fatalf("%v %v", exp, err)
	}
	if exp, _ := latefee.Expected(acc, latefee.Waiver{Amount: try(9999)}); exp.Amount != 0 {
		t.Errorf("floored at zero: %v", exp)
	}
	if _, err := latefee.Expected(acc, latefee.Waiver{Amount: try(-1)}); !errors.Is(err, latefee.ErrNegativeAmount) {
		t.Error(err)
	}
	if _, err := latefee.Expected(acc, latefee.Waiver{Amount: money.New(1, money.EUR)}); err == nil {
		t.Error("currency mismatch")
	}
	if e, err := latefee.Expected(acc); err != nil || e.Amount != 5000 {
		t.Error("no waivers")
	}

	cases := []struct {
		booked []int64
		want   int64
		action latefee.Action
	}{
		{nil, 3000, latefee.ActionBook},
		{[]int64{1000, 500}, 1500, latefee.ActionBook},
		{[]int64{3000}, 0, latefee.ActionNone},
		{[]int64{3000, 800, -200}, -600, latefee.ActionReverse},
	}
	for _, c := range cases {
		var bs []latefee.Booking
		for i, b := range c.booked {
			bs = append(bs, latefee.Booking{Period: string(rune('a' + i)), Amount: try(b)})
		}
		dl, err := latefee.Reconcile(exp, bs)
		if err != nil || dl.Amount.Amount != c.want || dl.Action != c.action {
			t.Errorf("%v: %+v %v", c.booked, dl, err)
		}
	}
	if _, err := latefee.Reconcile(exp, []latefee.Booking{{Amount: money.New(1, money.EUR)}}); err == nil {
		t.Error("currency mismatch")
	}
	if _, err := latefee.Reconcile(try(math.MinInt64), []latefee.Booking{{Amount: try(1)}}); err == nil {
		t.Error("overflow")
	}
}

// --- properties ---

type scenario struct {
	principal int64
	due       time.Time
	pays      []latefee.Payment
	grace     int
}

func drawScenario(t *rapid.T) scenario {
	due := d(2026, 1, 1).AddDate(0, 0, rapid.IntRange(0, 365).Draw(t, "dueOff"))
	s := scenario{principal: rapid.Int64Range(1, 5_000_000).Draw(t, "principal"), due: due, grace: rapid.IntRange(0, 15).Draw(t, "grace")}
	n := rapid.IntRange(0, 4).Draw(t, "npay")
	for i := 0; i < n; i++ {
		on := due.AddDate(0, 0, rapid.IntRange(-5, 200).Draw(t, "payOff"))
		s.pays = append(s.pays, pay(on, rapid.Int64Range(0, s.principal).Draw(t, "payAmt")))
	}
	return s
}

func (s scenario) feeAt(t *rapid.T, asOf time.Time, fr ...latefee.Freeze) int64 {
	p := pol
	p.GraceDays = s.grace
	c := latefee.OpenCharge{ID: uuid.Nil, DueDate: s.due, Principal: try(s.principal), PaidPrincipal: s.pays}
	return fee(t, p, c, asOf, fr...)
}

func TestPropMonotoneInTime(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		s.pays = nil // unpaid
		prev := int64(0)
		for off := 0; off < 400; off += rapid.IntRange(1, 40).Draw(t, "step") {
			f := s.feeAt(t, s.due.AddDate(0, 0, off))
			if f < prev {
				t.Fatalf("fee decreased: %d -> %d at +%d", prev, f, off)
			}
			prev = f
		}
	})
}

func TestPropMonotoneEvenWithPayments(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		a := s.due.AddDate(0, 0, rapid.IntRange(0, 300).Draw(t, "a"))
		b := a.AddDate(0, 0, rapid.IntRange(0, 100).Draw(t, "b"))
		if s.feeAt(t, a) > s.feeAt(t, b) {
			t.Fatal("fee not monotone in asOf with payments")
		}
	})
}

func TestPropFullPaymentOnDueOrGraceDayIsZero(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		onDue := s
		onDue.pays = []latefee.Payment{pay(s.due, s.principal)}
		asOf := s.due.AddDate(0, 0, rapid.IntRange(0, 500).Draw(t, "asOf"))
		if f := onDue.feeAt(t, asOf); f != 0 {
			t.Fatalf("paid on due date but fee %d", f)
		}
		onGrace := s
		onGrace.pays = []latefee.Payment{pay(s.due.AddDate(0, 0, s.grace), s.principal)}
		if f := onGrace.feeAt(t, asOf); f != 0 {
			t.Fatalf("paid on grace day but fee %d", f)
		}
		// two partial payments summing to the principal on the due date, too
		half := s.principal / 2
		split := s
		split.pays = []latefee.Payment{pay(s.due, half), pay(s.due, s.principal-half)}
		if f := split.feeAt(t, asOf); f != 0 {
			t.Fatalf("split payment on due date but fee %d", f)
		}
	})
}

func TestPropBackdatingNeverIncreasesFee(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		if len(s.pays) == 0 {
			s.pays = []latefee.Payment{pay(s.due.AddDate(0, 0, 10), s.principal/2)}
		}
		asOf := s.due.AddDate(0, 0, rapid.IntRange(0, 400).Draw(t, "asOf"))
		before := s.feeAt(t, asOf)
		i := rapid.IntRange(0, len(s.pays)-1).Draw(t, "which")
		moved := make([]latefee.Payment, len(s.pays))
		copy(moved, s.pays)
		moved[i].On = moved[i].On.AddDate(0, 0, -rapid.IntRange(0, 100).Draw(t, "back"))
		s2 := s
		s2.pays = moved
		if after := s2.feeAt(t, asOf); after > before {
			t.Fatalf("backdating raised the fee: %d -> %d", before, after)
		}
	})
}

func TestPropFreezeNeverIncreasesFee(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		asOf := s.due.AddDate(0, 0, rapid.IntRange(0, 400).Draw(t, "asOf"))
		from := s.due.AddDate(0, 0, rapid.IntRange(0, 300).Draw(t, "ff"))
		fr := latefee.Freeze{From: from}
		if rapid.Bool().Draw(t, "closed") {
			to := from.AddDate(0, 0, rapid.IntRange(0, 100).Draw(t, "len"))
			fr.To = &to
		}
		if s.feeAt(t, asOf, fr) > s.feeAt(t, asOf) {
			t.Fatal("freeze increased the fee")
		}
	})
}

func TestPropReconcileConverges(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		expected := try(rapid.Int64Range(0, 1_000_000).Draw(t, "exp"))
		var booked []latefee.Booking
		for i := 0; i < rapid.IntRange(0, 6).Draw(t, "n"); i++ {
			booked = append(booked, latefee.Booking{Period: "p", Amount: try(rapid.Int64Range(-500_000, 500_000).Draw(t, "b"))})
		}
		dl, err := latefee.Reconcile(expected, booked)
		if err != nil {
			t.Fatal(err)
		}
		booked = append(booked, latefee.Booking{Period: "next", Amount: dl.Amount})
		again, err := latefee.Reconcile(expected, booked)
		if err != nil || again.Booked != expected || again.Action != latefee.ActionNone || !again.Amount.IsZero() {
			t.Fatalf("not converged: %+v %v", again, err)
		}
	})
}

// A payment recorded late but dated earlier lowers the expected fee, so the
// monthly job reverses part of what it booked at the previous month end.
func TestBackdatedPaymentReversesBookedFee(t *testing.T) {
	c := charge(100000, d(2026, 1, 1))
	var booked []latefee.Booking
	run := func(asOf time.Time, c latefee.OpenCharge) latefee.Delta {
		a, err := latefee.Accrue(pol, []latefee.OpenCharge{c}, asOf, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		exp, _ := latefee.Expected(a)
		dl, err := latefee.Reconcile(exp, booked)
		if err != nil {
			t.Fatal(err)
		}
		booked = append(booked, latefee.Booking{Period: asOf.Format("2006-01"), Amount: dl.Amount})
		return dl
	}
	if dl := run(d(2026, 1, 31), c); dl.Amount.Amount != 5000 || dl.Action != latefee.ActionBook {
		t.Fatalf("january: %+v", dl)
	}
	// on 20 February the manager records a payment that was received on 11 January
	c.PaidPrincipal = []latefee.Payment{pay(d(2026, 1, 11), 100000)}
	dl := run(d(2026, 2, 28), c)
	if dl.Amount.Amount != -3500 || dl.Action != latefee.ActionReverse {
		t.Fatalf("february: %+v", dl) // expected 1500 (9 days), booked 5000
	}
	if dl := run(d(2026, 3, 31), c); dl.Action != latefee.ActionNone {
		t.Fatalf("march should be idempotent: %+v", dl)
	}
}

func TestPropWaiverNeverNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		total := rapid.Int64Range(0, 1_000_000).Draw(t, "total")
		w := rapid.Int64Range(0, 2_000_000).Draw(t, "w")
		e, err := latefee.Expected(latefee.Accrual{Total: try(total)}, latefee.Waiver{Amount: try(w)})
		if err != nil || e.Amount < 0 || e.Amount > total {
			t.Fatalf("%v %v", e, err)
		}
	})
}
