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
	// DaysPerMonth is the pro-rating convention: one month = 30 days.
	DaysPerMonth = 30
	bpsDenom     = 10000
)

// Errors returned by the calculators.
var (
	ErrRateTooHigh    = errors.New("latefee: rate above the statutory maximum")
	ErrBadPolicy      = errors.New("latefee: invalid policy")
	ErrNegativeAmount = errors.New("latefee: negative amount")
	ErrCurrency       = errors.New("latefee: currency does not match the policy")
)

// Policy is the site's late-fee setting (late_fee_policy).
type Policy struct {
	// Currency is the site currency; all amounts must use it.
	Currency money.Code
	// RateBPSPerMonth is the fee in basis points of principal per 30-day month
	// (500 = 5 %, the statutory maximum).
	RateBPSPerMonth int64
	// GraceDays: no fee accrues until this many days after the due date have
	// passed; the fee then accrues from the day after the grace period ends
	// (the grace days themselves are never charged).
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

// Payment is principal paid against a charge on a date. On is the payment's
// effective date (when the money was received), not the date it was recorded:
// a backdated payment reduces the fee from On.
type Payment struct {
	On     time.Time
	Amount money.Money
}

// OpenCharge is a principal charge with its payments to date.
type OpenCharge struct {
	ID            uuid.UUID
	DueDate       time.Time
	Principal     money.Money
	PaidPrincipal []Payment
}

// Freeze suspends accrual on [From, To] inclusive (To nil = open-ended), for a
// unit side in legal collection.
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

const maxDay = int(^uint(0) >> 1)

// Accrue computes the fee accrued by asOf on each charge's unpaid principal.
//
// Formula (simple interest, day-based, per charge):
//
//	fee = round( sum over chargeable days k of unpaid(k) * rateBPS / 10000 / 30 )
//
// where day k counts calendar days after the due date (k = 1 the day after),
// the chargeable days are k > GraceDays up to and including asOf, unpaid(k) is
// the principal minus all payments with effective date <= day k (so a payment
// stops accrual from the day it is received, and a payment on the due date or
// on day GraceDays costs nothing), and days inside a Freeze are skipped.
// Rounding is half-up to the minor unit, done once per charge on the exact
// sum, so the fee is monotone in asOf and a backdated payment can never
// increase it. Fees accrue on principal only and never compound. Dates are
// calendar dates in tz.
//
// (verify) Legal points to confirm with counsel before release: that 5 % per
// month may be pro-rated by day with a 30-day month; that the grace period
// delays the start of accrual rather than waiving the first days; and
// whether a partial month is charged pro rata or as a full month.
func Accrue(p Policy, charges []OpenCharge, asOf time.Time, tz *time.Location, freezes ...Freeze) (Accrual, error) {
	if err := p.Validate(); err != nil {
		return Accrual{}, err
	}
	if tz == nil {
		tz = time.UTC
	}
	asOfDay := civil.Day(asOf, tz)
	frozen := mergeFreezes(freezes, tz)

	out := Accrual{PerCharge: make([]ChargeFee, 0, len(charges)), Total: money.Zero(p.Currency)}
	for _, c := range charges {
		fee, err := accrueOne(p, c, asOfDay, tz, frozen)
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

func accrueOne(p Policy, c OpenCharge, asOfDay int, tz *time.Location, frozen []span) (money.Money, error) {
	zero := money.Zero(p.Currency)
	if c.Principal.Currency != p.Currency {
		return zero, ErrCurrency
	}
	if c.Principal.Amount < 0 {
		return zero, ErrNegativeAmount
	}
	pays := make([]dayPay, 0, len(c.PaidPrincipal))
	for _, pm := range c.PaidPrincipal {
		if pm.Amount.Currency != p.Currency {
			return zero, ErrCurrency
		}
		if pm.Amount.Amount < 0 {
			return zero, ErrNegativeAmount
		}
		pays = append(pays, dayPay{civil.Day(pm.On, tz), pm.Amount.Amount})
	}
	sort.SliceStable(pays, func(i, j int) bool { return pays[i].day < pays[j].day })

	lo := civil.Day(c.DueDate, tz) + p.GraceDays + 1
	hi := asOfDay
	if hi < lo || p.RateBPSPerMonth == 0 || c.Principal.Amount == 0 {
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
	balDays := new(big.Int)
	for cur := lo; cur <= hi; {
		end := hi
		if idx < len(pays) && pays[idx].day-1 < end {
			end = pays[idx].day - 1
		}
		if bal := c.Principal.Amount - paid; bal > 0 {
			days := int64(end-cur+1) - frozenDays(frozen, cur, end)
			balDays.Add(balDays, new(big.Int).Mul(big.NewInt(bal), big.NewInt(days)))
		}
		cur = end + 1
		absorb(cur)
	}
	// fee = balDays * bps / (10000 * 30), rounded half-up
	num := new(big.Int).Mul(balDays, big.NewInt(p.RateBPSPerMonth))
	den := big.NewInt(bpsDenom * DaysPerMonth)
	num.Mul(num, big.NewInt(2))
	num.Add(num, den)
	den.Mul(den, big.NewInt(2))
	fee := num.Quo(num, den)
	if !fee.IsInt64() {
		return zero, money.ErrOverflow
	}
	return money.New(fee.Int64(), p.Currency), nil
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

// Waiver is an audited, permanent reduction of the fee owed by a unit side.
type Waiver struct {
	Amount money.Money
}

// Expected is the fee that should be booked to date: the accrued total minus
// the waivers, never below zero.
func Expected(a Accrual, waivers ...Waiver) (money.Money, error) {
	exp := a.Total
	for _, w := range waivers {
		if w.Amount.Amount < 0 {
			return money.Money{}, ErrNegativeAmount
		}
		var err error
		if exp, err = exp.Sub(w.Amount); err != nil {
			return money.Money{}, err
		}
	}
	if exp.Amount < 0 {
		exp = money.Zero(exp.Currency)
	}
	return exp, nil
}

// Booking is a row of late_fee_booking: the amount crystallized for a period
// (negative for a reversal).
type Booking struct {
	Period string
	Amount money.Money
}

// Action says what the monthly job must post.
type Action string

// Reconcile outcomes.
const (
	ActionNone    Action = "none"
	ActionBook    Action = "book"    // post a new fee line for Amount
	ActionReverse Action = "reverse" // post a reversal of -Amount (backdated payment, waiver)
)

// Delta is the reconciliation result for a unit side.
type Delta struct {
	Expected money.Money
	Booked   money.Money
	// Amount = Expected - Booked: positive to book, negative to reverse.
	Amount money.Money
	Action Action
}

// Reconcile compares the fee expected to date with everything booked so far
// (including earlier reversals as negative bookings). Posting Delta.Amount as a
// new booking makes the booked total equal the expected total, so the
// monthly crystallization job is idempotent: running it twice posts nothing
// the second time.
func Reconcile(expected money.Money, booked []Booking) (Delta, error) {
	total, err := money.Sum(expected.Currency, bookedAmounts(booked)...)
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

func bookedAmounts(bs []Booking) []money.Money {
	out := make([]money.Money, len(bs))
	for i, b := range bs {
		out[i] = b.Amount
	}
	return out
}
