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

func sch(p latefee.Policy) latefee.PolicySchedule { return latefee.Constant(p, d(2000, 1, 1)) }

func charge(principal int64, due time.Time, pays ...latefee.Payment) latefee.OpenCharge {
	return latefee.OpenCharge{ID: uuid.New(), DueDate: due, Principal: try(principal), PaidPrincipal: pays}
}

func pay(on time.Time, n int64) latefee.Payment { return latefee.Payment{On: on, Amount: try(n)} }

func feeS(t interface{ Fatal(...any) }, s latefee.PolicySchedule, c latefee.OpenCharge, asOf time.Time, fr ...latefee.Freeze) int64 {
	a, err := latefee.Accrue(s, []latefee.OpenCharge{c}, asOf, time.UTC, fr...)
	if err != nil {
		t.Fatal(err)
	}
	return a.Total.Amount
}

func fee(t interface{ Fatal(...any) }, p latefee.Policy, c latefee.OpenCharge, asOf time.Time, fr ...latefee.Freeze) int64 {
	return feeS(t, sch(p), c, asOf, fr...)
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
		{"one day", pol, charge(100000, due), d(2026, 1, 2), nil, 161}, // 161.29: 31-day month
		{"full 31-day month = exactly 5 %", pol, charge(100000, due), d(2026, 2, 1), nil, 5000},
		{"30 of 31 days", pol, charge(100000, due), d(2026, 1, 31), nil, 4839},
		{"28-day month = exactly 5 %", pol, charge(100000, d(2026, 2, 1)), d(2026, 3, 1), nil, 5000},
		{"two months = 10 % simple", pol, charge(100000, due), d(2026, 3, 1), nil, 10000},
		{"grace: last free day", g5, charge(100000, due), d(2026, 1, 6), nil, 0},
		{"grace: first charged day", g5, charge(100000, due), d(2026, 1, 7), nil, 161},
		{"partial payment on day 11", pol, charge(100000, due, pay(d(2026, 1, 11), 40000)), d(2026, 1, 31), nil, 3548},
		{"paid in full on due date", pol, charge(100000, due, pay(due, 100000)), d(2026, 6, 1), nil, 0},
		{"paid on due+1 costs one day", pol, charge(100000, due, pay(d(2026, 1, 2), 100000)), d(2026, 6, 1), nil, 161},
		{"paid in full on grace day", g5, charge(100000, due, pay(d(2026, 1, 6), 100000)), d(2026, 6, 1), nil, 0},
		{"paid on grace day +1 costs one day", g5, charge(100000, due, pay(d(2026, 1, 7), 100000)), d(2026, 6, 1), nil, 161},
		{"paid before due date", pol, charge(100000, due, pay(d(2025, 12, 20), 100000)), d(2026, 6, 1), nil, 0},
		{"payment after asOf ignored", pol, charge(100000, due, pay(d(2026, 2, 1), 100000)), d(2026, 1, 31), nil, 4839},
		// 10 days at 100000 plus day 11 at 10000, then nothing: 1010000*500/310000 = 1629.03
		{"overpayment clamps", pol, charge(100000, due, pay(d(2026, 1, 11), 90000), pay(d(2026, 1, 12), 90000)), d(2026, 1, 31), nil, 1629},
		{"freeze 10 days", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11), To: dp(2026, 1, 20)}}, 3226},
		{"open freeze from day 11", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11)}}, 1613},
		{"overlapping freezes merge", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11), To: dp(2026, 1, 15)}, {From: d(2026, 1, 13), To: dp(2026, 1, 20)}, {From: d(2026, 1, 21), To: dp(2026, 1, 22)}}, 2903},
		{"freeze after open freeze", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 11)}, {From: d(2026, 1, 15), To: dp(2026, 1, 16)}}, 1613},
		{"reversed freeze ignored", pol, charge(100000, due), d(2026, 1, 31), []latefee.Freeze{{From: d(2026, 1, 20), To: dp(2026, 1, 10)}}, 4839},
		{"zero rate", latefee.Policy{Currency: money.TRY, Simple: true}, charge(100000, due), d(2026, 12, 1), nil, 0},
		{"zero principal", pol, charge(0, due), d(2026, 12, 1), nil, 0},
	}
	for _, c := range cases {
		if got := fee(t, c.p, c.c, c.asOf, c.fr...); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

// F6: a payment made on day D stops accrual for D, so the payoff quote on D
// equals the fee after paying on D; and the free window is exactly GraceDays.
func TestPayoffSemantics(t *testing.T) {
	due := d(2026, 1, 1)
	for _, grace := range []int{0, 3, 10} {
		p := pol
		p.GraceDays = grace
		for off := 0; off <= 60; off++ {
			day := due.AddDate(0, 0, off)
			quote := fee(t, p, charge(100000, due), day)
			paid := fee(t, p, charge(100000, due, pay(day, 100000)), day.AddDate(0, 0, 90))
			if quote != paid {
				t.Fatalf("grace %d, day +%d: quote %d != fee after paying that day %d", grace, off, quote, paid)
			}
			switch {
			case off <= grace && paid != 0:
				t.Fatalf("grace %d: paying on due+%d must be free, got %d", grace, off, paid)
			case off == grace+1 && paid != 161:
				t.Fatalf("grace %d: paying on due+grace+1 costs exactly one day, got %d", grace, paid)
			}
		}
	}
}

func TestAccrueTimezoneAndMultiple(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	due := time.Date(2026, 3, 1, 0, 0, 0, 0, ist)
	asOf := time.Date(2026, 3, 31, 23, 59, 0, 0, ist)
	c1, c2 := charge(100000, due), charge(50000, due)
	a, err := latefee.Accrue(sch(pol), []latefee.OpenCharge{c1, c2}, asOf, ist)
	if err != nil {
		t.Fatal(err)
	}
	if a.PerCharge[0].Fee.Amount != 4839 || a.PerCharge[1].Fee.Amount != 2419 || a.Total.Amount != 7258 || a.PerCharge[0].ChargeID != c1.ID {
		t.Fatalf("%+v", a)
	}
	// empty charges
	e, err := latefee.Accrue(sch(pol), nil, asOf, nil)
	if err != nil || e.Total != money.Zero(money.TRY) || len(e.PerCharge) != 0 {
		t.Fatalf("%+v %v", e, err)
	}
}

func TestPolicyValidate(t *testing.T) {
	bad := []struct {
		p    latefee.Policy
		want error
	}{
		{latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 501, CeilingBPS: 500, Simple: true}, latefee.ErrRateTooHigh},
		{latefee.Policy{Currency: money.TRY, RateBPSPerMonth: -1, Simple: true}, latefee.ErrBadPolicy},
		{latefee.Policy{Currency: money.TRY, GraceDays: -1, Simple: true}, latefee.ErrBadPolicy},
		{latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 500}, latefee.ErrBadPolicy},
		{latefee.Policy{RateBPSPerMonth: 500, Simple: true}, latefee.ErrBadPolicy},
	}
	for i, b := range bad {
		if err := b.p.Validate(); !errors.Is(err, b.want) {
			t.Errorf("%d: %v", i, err)
		}
		if _, err := latefee.Accrue(latefee.Constant(b.p, d(2020, 1, 1)), nil, d(2026, 1, 1), nil); err == nil {
			t.Errorf("%d: Accrue accepted a bad policy", i)
		}
	}
	if err := pol.Validate(); err != nil {
		t.Error(err)
	}
}

func TestScheduleValidateAndAt(t *testing.T) {
	low := pol
	low.RateBPSPerMonth = 200
	eur := pol
	eur.Currency = money.EUR
	bad := map[string]latefee.PolicySchedule{
		"empty":         {},
		"bad policy":    {Policies: []latefee.PolicyPeriod{{From: d(2026, 1, 1), Policy: latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 900, CeilingBPS: 500, Simple: true}}}},
		"mixed":         {Policies: []latefee.PolicyPeriod{{From: d(2026, 1, 1), To: dp(2026, 1, 31), Policy: pol}, {From: d(2026, 2, 1), Policy: eur}}},
		"to<from":       {Policies: []latefee.PolicyPeriod{{From: d(2026, 2, 1), To: dp(2026, 1, 1), Policy: pol}}},
		"overlap":       {Policies: []latefee.PolicyPeriod{{From: d(2026, 1, 1), To: dp(2026, 2, 1), Policy: pol}, {From: d(2026, 2, 1), Policy: low}}},
		"open not last": {Policies: []latefee.PolicyPeriod{{From: d(2026, 1, 1), Policy: pol}, {From: d(2026, 2, 1), Policy: low}}},
	}
	for n, s := range bad {
		if err := s.Validate(nil); err == nil {
			t.Errorf("%s: accepted", n)
		}
		if _, err := latefee.Accrue(s, nil, d(2026, 3, 1), nil); err == nil {
			t.Errorf("%s: Accrue accepted", n)
		}
	}
	good := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2026, 2, 1), Policy: low},
		{From: d(2026, 1, 1), To: dp(2026, 1, 31), Policy: pol},
	}}
	if err := good.Validate(time.UTC); err != nil {
		t.Fatal(err)
	}
	if p, ok := good.At(d(2026, 1, 31), time.UTC); !ok || p.RateBPSPerMonth != 500 {
		t.Fatalf("%v %v", p, ok)
	}
	if p, ok := good.At(d(2026, 2, 1), nil); !ok || p.RateBPSPerMonth != 200 {
		t.Fatalf("%v %v", p, ok)
	}
	if _, ok := good.At(d(2025, 12, 31), time.UTC); ok {
		t.Fatal("before the first policy")
	}
}

// F5: the calendar date of a schedule bound is read in the site time zone, not
// in the value's own location. 1 July 00:00 Istanbul read back in UTC is
// 30 June 21:00, which must still be 1 July.
func TestScheduleUsesSiteTimezone(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	low := pol
	low.RateBPSPerMonth = 200
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, ist).UTC() // 2026-06-30T21:00Z
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, ist).UTC()  // 2026-06-29T21:00Z
	s := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2026, 1, 1), To: &to, Policy: pol},
		{From: from, Policy: low},
	}}
	if err := s.Validate(ist); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.At(time.Date(2026, 6, 30, 12, 0, 0, 0, ist), ist); p.RateBPSPerMonth != 500 {
		t.Fatalf("30 June in Istanbul must use the old policy, got %d", p.RateBPSPerMonth)
	}
	if p, _ := s.At(time.Date(2026, 7, 1, 0, 0, 0, 0, ist).UTC(), ist); p.RateBPSPerMonth != 200 {
		t.Fatalf("1 July in Istanbul must use the new policy, got %d", p.RateBPSPerMonth)
	}
}

// F2: changing a future rate never changes past accruals; each day uses the
// policy in force that day.
func TestPolicyChangeIsProspective(t *testing.T) {
	low := pol
	low.RateBPSPerMonth = 250
	due := d(2026, 1, 1)
	c := charge(1_000_000, due)
	constant := latefee.Constant(pol, d(2000, 1, 1))
	changed := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2000, 1, 1), To: dp(2026, 1, 31), Policy: pol},
		{From: d(2026, 2, 1), Policy: low},
	}}
	// asOf 1 Feb: only January (old policy) has accrued
	if a, b := feeS(t, constant, c, d(2026, 2, 1)), feeS(t, changed, c, d(2026, 2, 1)); a != b || a != 50000 {
		t.Fatalf("past accrual changed: %d vs %d", a, b)
	}
	// February (28 days) at 250 bps adds exactly 25,000
	if got := feeS(t, changed, c, d(2026, 3, 1)); got != 75000 {
		t.Fatalf("got %d", got)
	}
	// raising the rate for the future does not touch the past either
	raised := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2000, 1, 1), To: dp(2026, 1, 31), Policy: low},
		{From: d(2026, 2, 1), Policy: pol},
	}}
	if got := feeS(t, raised, c, d(2026, 2, 1)); got != 25000 {
		t.Fatalf("raised: %d", got)
	}
	// a change in the middle of a month splits that month
	mid := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2000, 1, 1), To: dp(2026, 1, 10), Policy: pol},
		{From: d(2026, 1, 11), Policy: low},
	}}
	// 10 days at 500/31 + 21 days at 250/31 of 1,000,000 = 16129.03 + 16935.48
	if got := feeS(t, mid, c, d(2026, 2, 1)); got != 33065 {
		t.Fatalf("mid-month: %d", got)
	}
	// a gap without a policy accrues nothing; grace comes from the due date's policy
	gap := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2000, 1, 1), To: dp(2026, 1, 15), Policy: pol},
		{From: d(2026, 1, 21), Policy: pol},
	}}
	if got := feeS(t, gap, c, d(2026, 2, 1)); got != 41935 { // 26 of 31 days
		t.Fatalf("gap: %d", got)
	}
	graced := pol
	graced.GraceDays = 3
	gs := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
		{From: d(2000, 1, 1), To: dp(2025, 12, 31), Policy: graced},
		{From: d(2026, 1, 1), Policy: pol},
	}}
	// due date 2026-01-01 is under the no-grace policy
	if got := feeS(t, gs, c, d(2026, 2, 1)); got != 50000 {
		t.Fatalf("grace from due-date policy: %d", got)
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
		"fee line":           {ID: uuid.New(), Kind: latefee.KindFee, DueDate: due, Principal: try(10)},
		"unknown kind":       {ID: uuid.New(), Kind: "x", DueDate: due, Principal: try(10)},
	}
	for n, c := range cases {
		if _, err := latefee.Accrue(sch(pol), []latefee.OpenCharge{c}, asOf, nil); err == nil {
			t.Errorf("%s: expected error", n)
		}
	}
	if _, err := latefee.Accrue(sch(pol), []latefee.OpenCharge{{ID: uuid.New(), Kind: latefee.KindFee, DueDate: due, Principal: try(10)}}, asOf, nil); !errors.Is(err, latefee.ErrNotPrincipal) {
		t.Errorf("fee line: %v", err)
	}
	if got := fee(t, pol, latefee.OpenCharge{ID: uuid.New(), Kind: latefee.KindPrincipal, DueDate: due, Principal: try(100000)}, asOf); got != 5000 {
		t.Errorf("explicit principal kind: %d", got)
	}
	// overflow of the total
	huge := latefee.OpenCharge{ID: uuid.New(), DueDate: due, Principal: try(math.MaxInt64)}
	if _, err := latefee.Accrue(sch(pol), []latefee.OpenCharge{huge}, d(2126, 1, 1), nil); err != nil && !errors.Is(err, money.ErrOverflow) {
		t.Errorf("huge: %v", err)
	}
	// total overflow across charges
	big := latefee.OpenCharge{ID: uuid.New(), DueDate: due, Principal: try(math.MaxInt64 / 2)}
	if _, err := latefee.Accrue(sch(pol), []latefee.OpenCharge{big, big, big}, d(2600, 1, 1), nil); err == nil {
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
	bk := func(k latefee.BookingKind, n int64) latefee.Booking {
		return latefee.Booking{Period: "p", Kind: k, Amount: try(n)}
	}
	e, err := latefee.Expected(acc, []latefee.Booking{bk(latefee.BookingWaiver, 1500), bk(latefee.BookingWaiver, 500), bk(latefee.BookingFee, 100)})
	if err != nil || e.Fee.Amount != 3000 || e.Waived.Amount != 2000 || !e.Surplus.IsZero() {
		t.Fatalf("%+v %v", e, err)
	}
	over, err := latefee.Expected(acc, []latefee.Booking{bk(latefee.BookingWaiver, 9999)})
	if err != nil || over.Fee.Amount != 0 || over.Waived.Amount != 5000 || over.Surplus.Amount != 4999 {
		t.Fatalf("waiver above accrued: %+v %v", over, err)
	}
	for _, b := range []latefee.Booking{bk(latefee.BookingWaiver, -1), bk(latefee.BookingFee, -1), bk(latefee.BookingReversal, 1), bk("x", 1)} {
		if _, err := latefee.Expected(acc, []latefee.Booking{b}); !errors.Is(err, latefee.ErrBadBooking) {
			t.Errorf("%v: %v", b, err)
		}
		if _, err := latefee.Reconcile(try(0), []latefee.Booking{b}); !errors.Is(err, latefee.ErrBadBooking) {
			t.Errorf("reconcile %v: %v", b, err)
		}
	}
	if _, err := latefee.Expected(acc, []latefee.Booking{{Kind: latefee.BookingWaiver, Amount: money.New(1, money.EUR)}}); err == nil {
		t.Error("currency mismatch")
	}
	if e, err := latefee.Expected(acc, nil); err != nil || e.Fee.Amount != 5000 {
		t.Error("no bookings")
	}
	if _, err := latefee.Expected(acc, []latefee.Booking{bk(latefee.BookingWaiver, math.MaxInt64), bk(latefee.BookingWaiver, 1)}); err == nil {
		t.Error("overflow")
	}

	if _, err := latefee.Expected(latefee.Accrual{Total: try(math.MinInt64)}, []latefee.Booking{bk(latefee.BookingWaiver, math.MaxInt64)}); err == nil {
		t.Error("surplus overflow")
	}

	cases := []struct {
		booked []latefee.Booking
		want   int64
		action latefee.Action
	}{
		{nil, 3000, latefee.ActionBook},
		{[]latefee.Booking{bk(latefee.BookingFee, 1000), bk(latefee.BookingFee, 500)}, 1500, latefee.ActionBook},
		{[]latefee.Booking{bk(latefee.BookingFee, 3000)}, 0, latefee.ActionNone},
		{[]latefee.Booking{bk(latefee.BookingFee, 3000), bk(latefee.BookingFee, 800), bk(latefee.BookingReversal, -200)}, -600, latefee.ActionReverse},
	}
	for i, c := range cases {
		dl, err := latefee.Reconcile(e.Fee, append(c.booked, bk(latefee.BookingWaiver, 2000)))
		if err != nil || dl.Amount.Amount != c.want || dl.Action != c.action {
			t.Errorf("%d: %+v %v", i, dl, err)
		}
	}
	if _, err := latefee.Reconcile(e.Fee, []latefee.Booking{{Kind: latefee.BookingFee, Amount: money.New(1, money.EUR)}}); err == nil {
		t.Error("currency mismatch")
	}
	if _, err := latefee.Reconcile(try(math.MinInt64), []latefee.Booking{bk(latefee.BookingFee, 1)}); err == nil {
		t.Error("overflow")
	}
}

// F3: the three misuses of the old double-entry waiver API are impossible now.
func TestWaiverIsSingleEntry(t *testing.T) {
	acc := latefee.Accrual{Total: try(1000)}
	waiver := latefee.Booking{Period: "w", Kind: latefee.BookingWaiver, Amount: try(400)}
	fee1000 := latefee.Booking{Period: "m", Kind: latefee.BookingFee, Amount: try(1000)}

	// recording the waiver once reverses 400 of the booked fee, labelled as such by the delta
	e, _ := latefee.Expected(acc, []latefee.Booking{waiver, fee1000})
	dl, _ := latefee.Reconcile(e.Fee, []latefee.Booking{waiver, fee1000})
	if dl.Amount.Amount != -400 || dl.Action != latefee.ActionReverse {
		t.Fatalf("%+v", dl)
	}
	// the waiver booking is never re-charged: with the reversal posted, nothing is due
	rev := latefee.Booking{Period: "m2", Kind: latefee.BookingReversal, Amount: try(-400)}
	dl, _ = latefee.Reconcile(e.Fee, []latefee.Booking{waiver, fee1000, rev})
	if dl.Action != latefee.ActionNone {
		t.Fatalf("%+v", dl)
	}
	// a waiver above the accrued fee never produces a positive fee booking
	small := latefee.Accrual{Total: try(100)}
	big := latefee.Booking{Period: "w", Kind: latefee.BookingWaiver, Amount: try(300)}
	e, _ = latefee.Expected(small, []latefee.Booking{big})
	if e.Fee.Amount != 0 || e.Surplus.Amount != 200 {
		t.Fatalf("%+v", e)
	}
	dl, _ = latefee.Reconcile(e.Fee, []latefee.Booking{big})
	if dl.Action != latefee.ActionNone {
		t.Fatalf("surplus waiver must not book a fee: %+v", dl)
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

// F1: whatever the principal, due date, grace, payments and month, the fee that
// accrues inside one calendar month never exceeds principal x 5 %, and over any
// horizon it never exceeds 5 % per started month.
func TestPropMonthlyCap(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		principal := rapid.Int64Range(1, 50_000_000).Draw(t, "principal")
		year := rapid.IntRange(2020, 2030).Draw(t, "y")
		month := time.Month(rapid.IntRange(1, 12).Draw(t, "m"))
		first := d(year, month, 1)
		next := first.AddDate(0, 1, 0)
		due := first.AddDate(0, 0, -rapid.IntRange(0, 40).Draw(t, "before"))
		c := charge(principal, due)
		// fee accrued by the end of the month minus the fee accrued by its start
		inMonth := feeS(t, sch(pol), c, next) - feeS(t, sch(pol), c, first)
		limit := principal * latefee.DefaultRateBPS / 10000
		if inMonth > limit+1 { // +1: the two roundings of the difference
			t.Fatalf("fee in %s %d = %d exceeds 5 %% of %d (%d)", month, year, inMonth, principal, limit)
		}
		// a charge fully late for exactly this month costs exactly the rate
		if full := feeS(t, sch(pol), charge(principal, first), next); full > limit {
			t.Fatalf("full month %d > cap %d", full, limit)
		}
		// over a long horizon: at most one cap per touched month
		asOf := due.AddDate(0, 0, rapid.IntRange(1, 800).Draw(t, "h"))
		months := int64((asOf.Year()-due.Year())*12+int(asOf.Month())-int(due.Month())) + 1
		if total := feeS(t, sch(pol), c, asOf); total > months*limit {
			t.Fatalf("total %d exceeds %d months x %d", total, months, limit)
		}
	})
}

func TestPropFullPaymentOnDueOrGraceDayIsZero(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		asOf := s.due.AddDate(0, 0, rapid.IntRange(0, 500).Draw(t, "asOf"))
		// the free window is exactly GraceDays days after the due date
		for off := 0; off <= s.grace; off++ {
			free := s
			free.pays = []latefee.Payment{pay(s.due.AddDate(0, 0, off), s.principal)}
			if f := free.feeAt(t, asOf); f != 0 {
				t.Fatalf("paid on due+%d (grace %d) but fee %d", off, s.grace, f)
			}
		}
		// ... and one day later costs one day of fee
		late := s
		late.pays = []latefee.Payment{pay(s.due.AddDate(0, 0, s.grace+1), s.principal)}
		onePlus := s.due.AddDate(0, 0, s.grace+3+400)
		oneDay := s
		oneDay.pays = nil
		if f := late.feeAt(t, onePlus); f != oneDay.feeAt(t, s.due.AddDate(0, 0, s.grace+1)) {
			t.Fatalf("paid on due+grace+1: fee %d", f)
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

// A payoff quote on day D equals the fee after paying on D, for any scenario.
func TestPropQuoteEqualsPaidFee(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		day := s.due.AddDate(0, 0, rapid.IntRange(0, 300).Draw(t, "day"))
		quote := s.feeAt(t, day)
		// pay what is still unpaid on that day
		var paid int64
		for _, p := range s.pays {
			if !p.On.After(day) {
				paid += p.Amount.Amount
			}
		}
		rest := s.principal - paid
		if rest < 0 {
			rest = 0
		}
		after := s
		after.pays = append(append([]latefee.Payment(nil), s.pays...), pay(day, rest))
		if f := after.feeAt(t, day.AddDate(0, 0, rapid.IntRange(0, 200).Draw(t, "later"))); f != quote {
			t.Fatalf("quote %d != fee after paying %d", quote, f)
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

// F2 property: appending a policy that starts after asOf never changes the fee.
func TestPropFuturePolicyDoesNotChangePast(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := drawScenario(t)
		asOf := s.due.AddDate(0, 0, rapid.IntRange(0, 300).Draw(t, "asOf"))
		p := pol
		p.GraceDays = s.grace
		c := latefee.OpenCharge{DueDate: s.due, Principal: try(s.principal), PaidPrincipal: s.pays}
		base := feeS(t, latefee.Constant(p, d(2000, 1, 1)), c, asOf)
		other := p
		other.RateBPSPerMonth = rapid.Int64Range(0, 500).Draw(t, "newRate")
		other.GraceDays = rapid.IntRange(0, 20).Draw(t, "newGrace")
		sched := latefee.PolicySchedule{Policies: []latefee.PolicyPeriod{
			{From: d(2000, 1, 1), To: &asOf, Policy: p}, // asOf is inclusive; accrual stops at asOf-1
			{From: asOf.AddDate(0, 0, 1), Policy: other},
		}}
		if got := feeS(t, sched, c, asOf); got != base {
			t.Fatalf("future policy changed the past: %d -> %d", base, got)
		}
	})
}

func TestPropReconcileConverges(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		expected := try(rapid.Int64Range(0, 1_000_000).Draw(t, "exp"))
		var booked []latefee.Booking
		for i := 0; i < rapid.IntRange(0, 6).Draw(t, "n"); i++ {
			booked = append(booked, latefee.Booking{Period: "p", Kind: latefee.BookingFee, Amount: try(rapid.Int64Range(0, 500_000).Draw(t, "b"))})
			booked = append(booked, latefee.Booking{Period: "p", Kind: latefee.BookingReversal, Amount: try(-rapid.Int64Range(0, 500_000).Draw(t, "r"))})
		}
		dl, err := latefee.Reconcile(expected, booked)
		if err != nil {
			t.Fatal(err)
		}
		next := latefee.Booking{Period: "next", Kind: latefee.BookingFee, Amount: dl.Amount}
		if dl.Amount.Amount < 0 {
			next.Kind = latefee.BookingReversal
		}
		booked = append(booked, next)
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
		a, err := latefee.Accrue(sch(pol), []latefee.OpenCharge{c}, asOf, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		exp, _ := latefee.Expected(a, booked)
		dl, err := latefee.Reconcile(exp.Fee, booked)
		if err != nil {
			t.Fatal(err)
		}
		kind := latefee.BookingFee
		if dl.Amount.Amount < 0 {
			kind = latefee.BookingReversal
		}
		booked = append(booked, latefee.Booking{Period: asOf.Format("2006-01"), Kind: kind, Amount: dl.Amount})
		return dl
	}
	// the job runs on the first of the next month, covering the whole of January
	if dl := run(d(2026, 2, 1), c); dl.Amount.Amount != 5000 || dl.Action != latefee.ActionBook {
		t.Fatalf("january: %+v", dl)
	}
	// on 20 February the manager records a payment that was received on 11 January
	c.PaidPrincipal = []latefee.Payment{pay(d(2026, 1, 11), 100000)}
	dl := run(d(2026, 3, 1), c)
	// expected: days 1..10 January = 10 x 161.29 = 1613; booked 5000
	if dl.Amount.Amount != -3387 || dl.Action != latefee.ActionReverse {
		t.Fatalf("february: %+v", dl)
	}
	if dl := run(d(2026, 4, 1), c); dl.Action != latefee.ActionNone {
		t.Fatalf("march should be idempotent: %+v", dl)
	}
}

func TestPropWaiverNeverNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		total := rapid.Int64Range(0, 1_000_000).Draw(t, "total")
		w := rapid.Int64Range(0, 2_000_000).Draw(t, "w")
		e, err := latefee.Expected(latefee.Accrual{Total: try(total)}, []latefee.Booking{{Kind: latefee.BookingWaiver, Amount: try(w)}})
		if err != nil || e.Fee.Amount < 0 || e.Fee.Amount > total || e.Fee.Amount+e.Waived.Amount != total || e.Waived.Amount+e.Surplus.Amount != w {
			t.Fatalf("%+v %v", e, err)
		}
	})
}
