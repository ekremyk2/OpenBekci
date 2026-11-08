package latefee

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/internal/civil"
)

const (
	// MaxRateBPS is the statutory ceiling: 5 % per month = 500 basis points.
	// A site may set a lower rate, never a higher one. (verify: Law 634 art. 20)
	MaxRateBPS = 500
	bpsDenom   = 10000
)

// Errors returned by the calculators.
var (
	ErrRateTooHigh    = errors.New("latefee: rate above the statutory maximum")
	ErrBadPolicy      = errors.New("latefee: invalid policy")
	ErrNegativeAmount = errors.New("latefee: negative amount")
	ErrCurrency       = errors.New("latefee: currency does not match the policy")
	ErrBadBooking     = errors.New("latefee: invalid booking")
	ErrNotPrincipal   = errors.New("latefee: only principal charges accrue a fee")
	ErrBadSchedule    = errors.New("latefee: invalid policy schedule")
)

// Policy is the site's late-fee setting (late_fee_policy) for one effective
// period.
type Policy struct {
	// Currency is the site currency; all amounts must use it.
	Currency money.Code
	// RateBPSPerMonth is the fee in basis points of principal per calendar
	// month (500 = 5 %, the statutory maximum). A day accrues
	// RateBPSPerMonth / daysInMonth(that day), so a fully late month costs
	// exactly this rate.
	RateBPSPerMonth int64
	// GraceDays: the fee starts accruing GraceDays days after the due date.
	// Paying on due date + GraceDays or earlier costs nothing; paying one day
	// later costs one day of fee.
	GraceDays int
	// Simple must be true: fees never compound (decision M4). Reserved so a
	// future pack could add compounding without a schema change.
	Simple bool
}

// Validate checks the policy against the statutory cap.
func (p Policy) Validate() error {
	if !p.Currency.Valid() {
		return fmt.Errorf("%w: currency %q", ErrBadPolicy, string(p.Currency))
	}
	if p.RateBPSPerMonth < 0 || p.GraceDays < 0 {
		return fmt.Errorf("%w: negative rate or grace", ErrBadPolicy)
	}
	if p.RateBPSPerMonth > MaxRateBPS {
		return ErrRateTooHigh
	}
	if !p.Simple {
		return fmt.Errorf("%w: compounding is not supported", ErrBadPolicy)
	}
	return nil
}

// PolicyPeriod is a Policy in force on [From, To], both inclusive calendar
// dates in the site time zone (To nil = open-ended).
type PolicyPeriod struct {
	From   time.Time
	To     *time.Time
	Policy Policy
}

// PolicySchedule is the effective-dated late-fee policy of a site. Accrue
// applies, on each accrual day, the policy in force on that day, so changing
// a rate for the future never changes what accrued in the past. A day with no
// policy in force accrues nothing.
type PolicySchedule struct {
	Policies []PolicyPeriod
}

// Constant is a one-period schedule: p in force from the given date, open-ended.
func Constant(p Policy, from time.Time) PolicySchedule {
	return PolicySchedule{Policies: []PolicyPeriod{{From: from, Policy: p}}}
}

type pspan struct {
	from, to int
	p        Policy
}

const maxDay = int(^uint(0) >> 1)

func (s PolicySchedule) spans(tz *time.Location) []pspan {
	out := make([]pspan, len(s.Policies))
	for i, pp := range s.Policies {
		out[i] = pspan{from: civil.Day(pp.From, tz), to: maxDay, p: pp.Policy}
		if pp.To != nil {
			out[i].to = civil.Day(*pp.To, tz)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].from < out[j].from })
	return out
}

// Validate checks every policy, that they share one currency, that each To is
// not before its From, that periods do not overlap and that an open-ended
// period is the last. Dates are read as calendar dates in tz.
func (s PolicySchedule) Validate(tz *time.Location) error {
	if tz == nil {
		tz = time.UTC
	}
	if len(s.Policies) == 0 {
		return fmt.Errorf("%w: no policy", ErrBadSchedule)
	}
	sp := s.spans(tz)
	for i, x := range sp {
		if err := x.p.Validate(); err != nil {
			return err
		}
		if x.p.Currency != sp[0].p.Currency {
			return fmt.Errorf("%w: mixed currencies", ErrBadSchedule)
		}
		if x.to < x.from {
			return fmt.Errorf("%w: period %d ends before it starts", ErrBadSchedule, i)
		}
		if i > 0 {
			switch prev := sp[i-1]; {
			case prev.to == maxDay:
				return fmt.Errorf("%w: open-ended period %d is not last", ErrBadSchedule, i-1)
			case x.from <= prev.to:
				return fmt.Errorf("%w: periods %d and %d overlap", ErrBadSchedule, i-1, i)
			}
		}
	}
	return nil
}

// At returns the policy in force on the calendar date of date in tz, if any.
func (s PolicySchedule) At(date time.Time, tz *time.Location) (Policy, bool) {
	if tz == nil {
		tz = time.UTC
	}
	return policyAt(s.spans(tz), civil.Day(date, tz))
}

func policyAt(sp []pspan, day int) (Policy, bool) {
	var found Policy
	ok := false
	for _, x := range sp { // on overlap the latest From wins
		if day >= x.from && day <= x.to {
			found, ok = x.p, true
		}
	}
	return found, ok
}

// Payment is principal paid against a charge on a date. On is the payment's
// effective date (when the money was received), not the date it was recorded:
// a backdated payment reduces the fee from On.
type Payment struct {
	On     time.Time
	Amount money.Money
}

// ChargeKind says what an open charge is. Only principal accrues a fee.
type ChargeKind string

// Charge kinds. The zero value means principal.
const (
	KindPrincipal ChargeKind = "principal"
	KindFee       ChargeKind = "fee"
)

// OpenCharge is a principal charge with its payments to date. Kind must be
// empty or KindPrincipal: a booked late-fee line passed by mistake is
// rejected with ErrNotPrincipal instead of silently compounding.
type OpenCharge struct {
	ID            uuid.UUID
	Kind          ChargeKind
	DueDate       time.Time
	Principal     money.Money
	PaidPrincipal []Payment
}

// Freeze suspends accrual on [From, To] inclusive (To nil = open-ended), for a
// unit side in legal collection. It skips days of any charge, whenever the
// charge was created.
type Freeze struct {
	From time.Time
	To   *time.Time
}

// ChargeFee is the fee accrued on one charge.
type ChargeFee struct {
	ChargeID uuid.UUID
	Fee      money.Money
}

// Accrual is the result of Accrue; Total is the sum of PerCharge, in the order
// of the input charges.
type Accrual struct {
	PerCharge []ChargeFee
	Total     money.Money
}

type span struct{ from, to int }

// Accrue computes the fee accrued by asOf on each charge's unpaid principal.
//
// # Day semantics
//
// Let due be the charge's due date, G the GraceDays of the policy in force on
// the due date, and a "late day" k a calendar day (in tz) with
// due+G <= k <= asOf-1. Each late day costs
//
//	unpaid(k) * rateBPS(k) / 10000 / daysInMonth(k)
//
// where rateBPS(k) is the policy in force on day k and unpaid(k) is the
// principal minus every payment whose effective date is <= k. Consequences,
// all pinned by tests:
//
//   - A payment on day D stops accrual for D: day D is never charged for the
//     part paid on D, and a quote asOf = D (which charges days up to D-1)
//     equals the fee after paying on D.
//   - The free window is exactly G days after the due date: paying on
//     due+G costs 0, paying on due+G+1 costs one day. Paying on the due date
//     with no grace costs 0, one day later costs one day.
//   - A full calendar month late costs exactly the monthly rate, whatever the
//     month length (28 to 31 days).
//
// # Statutory cap
//
// Each calendar month's accrual on one charge is clamped at
// principal * maxRateBPS(month) / 10000, where maxRateBPS is the highest rate
// in force that month (never above MaxRateBPS), and the rounded total is
// clamped at the sum of those monthly ceilings (rounded down). So the fee a
// charge accrues inside any calendar month never exceeds its principal times
// the statutory 5 %.
//
// Rounding is half-up to the minor unit, once per charge on the exact sum, so
// the fee is monotone in asOf and a backdated payment can never increase it.
// Fees accrue on principal only and never compound. Dates are calendar dates
// in tz; a freeze skips its days.
//
// (verify) Legal points to confirm with counsel before release: that the 5 %
// per month may be pro-rated by calendar day (the code satisfies the strict
// reading in which a month never costs more than 5 %); that the grace period
// delays the start of accrual; whether a partial month is charged pro rata
// or as a full month.
func Accrue(sched PolicySchedule, charges []OpenCharge, asOf time.Time, tz *time.Location, freezes ...Freeze) (Accrual, error) {
	if tz == nil {
		tz = time.UTC
	}
	if err := sched.Validate(tz); err != nil {
		return Accrual{}, err
	}
	cur := sched.Policies[0].Policy.Currency
	sp := sched.spans(tz)
	asOfDay := civil.Day(asOf, tz)
	frozen := mergeFreezes(freezes, tz)

	out := Accrual{PerCharge: make([]ChargeFee, 0, len(charges)), Total: money.Zero(cur)}
	for _, c := range charges {
		fee, err := accrueOne(cur, sp, c, asOfDay, tz, frozen)
		if err != nil {
			return Accrual{}, fmt.Errorf("charge %s: %w", c.ID, err)
		}
		out.PerCharge = append(out.PerCharge, ChargeFee{ChargeID: c.ID, Fee: fee})
		if out.Total, err = out.Total.Add(fee); err != nil {
			return Accrual{}, err
		}
	}
	return out, nil
}

type dayPay struct {
	day int
	amt int64
}

// monthAcc is the exact accrual of one calendar month and the highest rate
// in force in it.
type monthAcc struct {
	key    int
	acc    *big.Rat
	maxBPS int64
}

func accrueOne(cur money.Code, sp []pspan, c OpenCharge, asOfDay int, tz *time.Location, frozen []span) (money.Money, error) {
	zero := money.Zero(cur)
	if c.Kind != "" && c.Kind != KindPrincipal {
		return zero, ErrNotPrincipal
	}
	if c.Principal.Currency != cur {
		return zero, ErrCurrency
	}
	if c.Principal.Amount < 0 {
		return zero, ErrNegativeAmount
	}
	pays := make([]dayPay, 0, len(c.PaidPrincipal))
	for _, pm := range c.PaidPrincipal {
		if pm.Amount.Currency != cur {
			return zero, ErrCurrency
		}
		if pm.Amount.Amount < 0 {
			return zero, ErrNegativeAmount
		}
		pays = append(pays, dayPay{civil.Day(pm.On, tz), pm.Amount.Amount})
	}
	sort.SliceStable(pays, func(i, j int) bool { return pays[i].day < pays[j].day })

	dueDay := civil.Day(c.DueDate, tz)
	grace := 0
	if p, ok := policyAt(sp, dueDay); ok {
		grace = p.GraceDays
	}
	lo := dueDay + grace
	hi := asOfDay - 1
	if hi < lo || c.Principal.Amount == 0 {
		return zero, nil
	}

	var paid int64
	idx := 0
	absorb := func(upTo int) {
		for idx < len(pays) && pays[idx].day <= upTo {
			paid += pays[idx].amt
			if paid > c.Principal.Amount || paid < 0 { // clamp; also guards int64 overflow
				paid = c.Principal.Amount
			}
			idx++
		}
	}
	absorb(lo)
	var months []monthAcc
	for day := lo; day <= hi; {
		end := hi
		if idx < len(pays) && pays[idx].day-1 < end {
			end = pays[idx].day - 1
		}
		if bal := c.Principal.Amount - paid; bal > 0 {
			months = accrueRange(months, sp, frozen, day, end, bal)
		}
		day = end + 1
		absorb(day)
	}

	total := new(big.Rat)
	limit := new(big.Int)
	for _, m := range months {
		capR := new(big.Rat).SetFrac(
			new(big.Int).Mul(big.NewInt(c.Principal.Amount), big.NewInt(m.maxBPS)), big.NewInt(bpsDenom))
		if m.acc.Cmp(capR) > 0 {
			m.acc = capR
		}
		total.Add(total, m.acc)
		limit.Add(limit, new(big.Int).Quo(capR.Num(), capR.Denom()))
	}
	// round half-up
	r := new(big.Int).Quo(
		new(big.Int).Add(new(big.Int).Mul(total.Num(), big.NewInt(2)), total.Denom()),
		new(big.Int).Mul(total.Denom(), big.NewInt(2)))
	if r.Cmp(limit) > 0 {
		r = limit
	}
	if !r.IsInt64() {
		return zero, money.ErrOverflow
	}
	return money.New(r.Int64(), cur), nil
}

// accrueRange adds the accrual of days [a, b] on an unpaid balance bal,
// splitting at month ends and at policy boundaries.
func accrueRange(months []monthAcc, sp []pspan, frozen []span, a, b int, bal int64) []monthAcc {
	for d := a; d <= b; {
		y, m, _ := civil.YMD(d)
		dim := civil.DaysInMonth(y, m)
		end := civil.FromYMD(y, m, dim)
		if b < end {
			end = b
		}
		p, ok := policyAt(sp, d)
		// the piece ends where the policy in force ends, or before the next one starts
		for _, x := range sp {
			if ok && d >= x.from && d <= x.to && x.to < end {
				end = x.to
			}
			if !ok && x.from > d && x.from-1 < end {
				end = x.from - 1
			}
		}
		if ok && p.RateBPSPerMonth > 0 {
			days := int64(end-d+1) - frozenDays(frozen, d, end)
			key := y*12 + int(m)
			if n := len(months); n == 0 || months[n-1].key != key {
				months = append(months, monthAcc{key: key, acc: new(big.Rat)})
			}
			ma := &months[len(months)-1]
			if p.RateBPSPerMonth > ma.maxBPS {
				ma.maxBPS = p.RateBPSPerMonth
			}
			num := new(big.Int).Mul(big.NewInt(bal), big.NewInt(days))
			num.Mul(num, big.NewInt(p.RateBPSPerMonth))
			ma.acc.Add(ma.acc, new(big.Rat).SetFrac(num, big.NewInt(int64(bpsDenom*dim))))
		}
		d = end + 1
	}
	return months
}

func mergeFreezes(fs []Freeze, tz *time.Location) []span {
	spans := make([]span, 0, len(fs))
	for _, f := range fs {
		s := span{civil.Day(f.From, tz), maxDay}
		if f.To != nil {
			s.to = civil.Day(*f.To, tz)
		}
		if s.to >= s.from {
			spans = append(spans, s)
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && (merged[n-1].to == maxDay || s.from <= merged[n-1].to+1) {
			if s.to > merged[n-1].to {
				merged[n-1].to = s.to
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// frozenDays counts frozen days inside [a, b].
func frozenDays(frozen []span, a, b int) int64 {
	var n int64
	for _, s := range frozen {
		lo, hi := s.from, s.to
		if lo < a {
			lo = a
		}
		if hi > b {
			hi = b
		}
		if hi >= lo {
			n += int64(hi - lo + 1)
		}
	}
	return n
}

// BookingKind is the type of a late_fee_booking row.
type BookingKind string

// Booking kinds.
const (
	// BookingFee crystallizes accrued fee; Amount >= 0.
	BookingFee BookingKind = "fee"
	// BookingReversal takes back booked fee (backdated payment); Amount <= 0.
	BookingReversal BookingKind = "reversal"
	// BookingWaiver is an audited, permanent waiver of fee; Amount >= 0 is the
	// amount waived. It is recorded once, here, and reduces the expected fee;
	// it is not part of what has been charged.
	BookingWaiver BookingKind = "waiver"
)

// Booking is a row of late_fee_booking.
type Booking struct {
	Period string
	Kind   BookingKind
	Amount money.Money
}

func (b Booking) validate() error {
	switch b.Kind {
	case BookingFee, BookingWaiver:
		if b.Amount.Amount < 0 {
			return fmt.Errorf("%w: %s %q must not be negative", ErrBadBooking, b.Kind, b.Period)
		}
	case BookingReversal:
		if b.Amount.Amount > 0 {
			return fmt.Errorf("%w: reversal %q must not be positive", ErrBadBooking, b.Period)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrBadBooking, string(b.Kind))
	}
	return nil
}

// Expectation is the fee that should have been charged to date.
type Expectation struct {
	// Fee is accrued minus waived, never below zero.
	Fee money.Money
	// Waived is the waiver amount that actually reduced the fee (capped at
	// the accrued total).
	Waived money.Money
	// Surplus is waiver amount beyond the accrued total: the fee that was
	// waived before it accrued. It reduces nothing (the fee never goes below
	// zero) and is reported so the caller can show or reject it.
	Surplus money.Money
}

// Expected derives the fee that should be booked to date from the accrual and
// the bookings: accrued total minus the waiver-kind bookings, never below
// zero. A waiver is recorded once, as a waiver booking; there is no other way
// to pass one, so it can neither be forgotten nor counted twice.
func Expected(a Accrual, bookings []Booking) (Expectation, error) {
	cur := a.Total.Currency
	waived := money.Zero(cur)
	for _, b := range bookings {
		if err := b.validate(); err != nil {
			return Expectation{}, err
		}
		if b.Kind != BookingWaiver {
			continue
		}
		var err error
		if waived, err = waived.Add(b.Amount); err != nil {
			return Expectation{}, err
		}
	}
	e := Expectation{Fee: a.Total, Waived: waived, Surplus: money.Zero(cur)}
	if waived.Amount > a.Total.Amount {
		e.Waived = a.Total
		e.Fee = money.Zero(cur)
		var err error
		if e.Surplus, err = waived.Sub(a.Total); err != nil {
			return Expectation{}, err
		}
		return e, nil
	}
	var err error
	if e.Fee, err = a.Total.Sub(waived); err != nil {
		return Expectation{}, err
	}
	return e, nil
}

// Action says what the monthly job must post.
type Action string

// Reconcile outcomes.
const (
	ActionNone    Action = "none"
	ActionBook    Action = "book"    // post a new fee booking for Amount
	ActionReverse Action = "reverse" // post a reversal booking of Amount (negative)
)

// Delta is the reconciliation result for a unit side.
type Delta struct {
	Expected money.Money
	Booked   money.Money
	// Amount = Expected - Booked: positive to book, negative to reverse.
	Amount money.Money
	Action Action
}

// Reconcile compares the fee expected to date (Expectation.Fee) with the fee
// and reversal bookings so far; waiver bookings are not charges and are
// ignored here (Expected already applied them). Posting Delta.Amount as a new
// fee (positive) or reversal (negative) booking makes the booked total equal
// the expected total, so the monthly crystallization job is idempotent.
func Reconcile(expected money.Money, booked []Booking) (Delta, error) {
	var charged []money.Money
	for _, b := range booked {
		if err := b.validate(); err != nil {
			return Delta{}, err
		}
		if b.Kind != BookingWaiver {
			charged = append(charged, b.Amount)
		}
	}
	total, err := money.Sum(expected.Currency, charged...)
	if err != nil {
		return Delta{}, err
	}
	amt, err := expected.Sub(total)
	if err != nil {
		return Delta{}, err
	}
	a := ActionNone
	switch {
	case amt.Amount > 0:
		a = ActionBook
	case amt.Amount < 0:
		a = ActionReverse
	}
	return Delta{Expected: expected, Booked: total, Amount: amt, Action: a}, nil
}
