package rates

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/internal/civil"
)

// Errors returned by schedule validation and period enumeration.
var (
	ErrOverlap      = errors.New("rates: overlapping rates")
	ErrGap          = errors.New("rates: gap between rates")
	ErrBadRange     = errors.New("rates: To is before From")
	ErrCurrency     = errors.New("rates: rates use more than one currency")
	ErrOpenNotLast  = errors.New("rates: an open-ended rate must be the last one")
	ErrNegativeRate = errors.New("rates: negative amount")
	ErrBadPlan      = errors.New("rates: invalid plan")
)

// Rate is an amount valid from From to To, both inclusive calendar dates (the
// date part of the time value in the zone the value carries; the clock time is
// ignored). A nil To means open-ended. DecisionID optionally links the rate to
// the assembly decision that set it (governance G5).
type Rate struct {
	From       time.Time
	To         *time.Time
	Amount     money.Money
	DecisionID *uuid.UUID
}

// Schedule is an effective-dated rate list (charge_plan_rate, decision M3).
type Schedule struct {
	Rates []Rate
}

func day(t time.Time) int { return civil.Day(t, t.Location()) }

func (r Rate) bounds() (from, to int) {
	from = day(r.From)
	to = int(^uint(0) >> 1)
	if r.To != nil {
		to = day(*r.To)
	}
	return from, to
}

// sorted returns the rates ordered by From (stable).
func (s Schedule) sorted() []Rate {
	rs := make([]Rate, len(s.Rates))
	copy(rs, s.Rates)
	sort.SliceStable(rs, func(i, j int) bool { return day(rs[i].From) < day(rs[j].From) })
	return rs
}

// Validate checks the schedule: amounts non-negative in one currency, each
// To >= From, no overlaps, an open-ended rate only last. With contiguous true
// there must also be no gap between a rate's To and the next From (each rate
// then ends the day before the next begins), which plans that must always
// have an amount require.
func (s Schedule) Validate(contiguous bool) error {
	rs := s.sorted()
	for i, r := range rs {
		if err := r.Amount.Validate(); err != nil {
			return err
		}
		if r.Amount.Amount < 0 {
			return ErrNegativeRate
		}
		if r.Amount.Currency != rs[0].Amount.Currency {
			return ErrCurrency
		}
		from, to := r.bounds()
		if to < from {
			return fmt.Errorf("%w: rate %d", ErrBadRange, i)
		}
		if i == 0 {
			continue
		}
		_, prevTo := rs[i-1].bounds()
		switch {
		case prevTo == int(^uint(0)>>1):
			return fmt.Errorf("%w: rate %d", ErrOpenNotLast, i-1)
		case from <= prevTo:
			return fmt.Errorf("%w: rates %d and %d", ErrOverlap, i-1, i)
		case contiguous && from != prevTo+1:
			return fmt.Errorf("%w: between rates %d and %d", ErrGap, i-1, i)
		}
	}
	return nil
}

// At returns the rate in force on date (calendar date of the value), if any.
// On an invalid (overlapping) schedule the rate with the latest From wins, so
// the lookup is deterministic; Validate before saving.
func (s Schedule) At(date time.Time) (Rate, bool) {
	d := day(date)
	var found Rate
	ok := false
	for _, r := range s.sorted() {
		from, to := r.bounds()
		if d >= from && d <= to {
			found, ok = r, true
		}
	}
	return found, ok
}

// Frequency is how often a recurring plan generates charges.
type Frequency string

// Frequencies.
const (
	Monthly   Frequency = "monthly"
	Quarterly Frequency = "quarterly"
	Yearly    Frequency = "yearly"
)

// Plan is the part of a charge plan that decides its periods.
type Plan struct {
	Frequency Frequency
	Start     time.Time  // first date the plan applies (inclusive)
	End       *time.Time // last date the plan applies (inclusive); nil = open
	DueDay    int        // 1..31; clamped to the month length
}

// Period is one billing period of a plan.
type Period struct {
	Key     string    // "2026-03", "2026-Q1", "2026"
	DueDate time.Time // start of the due day in the plan's time zone
	Start   time.Time // first day of the period (start of day)
	End     time.Time // last day of the period (start of day, inclusive)
}

// Periods enumerates, in chronological order, the plan's periods whose due
// date falls on a calendar date in [from, to] (inclusive, dates taken in tz).
// It is the basis of the idempotent catch-up charge generation job: run with
// from = the plan start (or the last generated due date + 1) and to = today,
// then skip (plan, unit, period key) rows that already exist.
//
// A period belongs to the plan if it overlaps [Start, End]: the period that
// contains Start is the first and the one that contains End is the last
// (periods are never prorated here). The due date is DueDay of the first
// month of the period, clamped to the month length (31 becomes 28/29/30).
// All arithmetic is on calendar dates, so DST transitions never shift or
// duplicate a period; returned times are the start of the day in tz (the
// first valid instant of that date if midnight does not exist).
func Periods(p Plan, from, to time.Time, tz *time.Location) ([]Period, error) {
	step, err := p.step()
	if err != nil {
		return nil, err
	}
	if tz == nil {
		tz = time.UTC
	}
	fromD, toD := civil.Day(from, tz), civil.Day(to, tz)
	startD := civil.Day(p.Start, tz)
	endD := int(^uint(0) >> 1)
	if p.End != nil {
		endD = civil.Day(*p.End, tz)
		if endD < startD {
			return nil, fmt.Errorf("%w: End before Start", ErrBadPlan)
		}
	}
	y, m, _ := civil.YMD(startD)
	// align the first period to the period boundary containing Start
	m = alignMonth(p.Frequency, m)

	var out []Period
	for {
		pStart := civil.FromYMD(y, m, 1)
		ey, em := y, m+time.Month(step)
		for em > 12 {
			em -= 12
			ey++
		}
		pEnd := civil.FromYMD(ey, em, 1) - 1
		if pStart > endD {
			break
		}
		dd := p.DueDay
		if dim := civil.DaysInMonth(y, m); dd > dim {
			dd = dim
		}
		due := civil.FromYMD(y, m, dd)
		if pStart > toD {
			break
		}
		if pEnd >= startD && due >= fromD && due <= toD {
			out = append(out, Period{
				Key:     periodKey(p.Frequency, y, m),
				DueDate: civil.Time(due, tz),
				Start:   civil.Time(pStart, tz),
				End:     civil.Time(pEnd, tz),
			})
		}
		y, m = ey, em
	}
	return out, nil
}

func (p Plan) step() (int, error) {
	if p.DueDay < 1 || p.DueDay > 31 {
		return 0, fmt.Errorf("%w: due day %d", ErrBadPlan, p.DueDay)
	}
	switch p.Frequency {
	case Monthly:
		return 1, nil
	case Quarterly:
		return 3, nil
	case Yearly:
		return 12, nil
	}
	return 0, fmt.Errorf("%w: frequency %q", ErrBadPlan, p.Frequency)
}

func alignMonth(f Frequency, m time.Month) time.Month {
	switch f {
	case Quarterly:
		return time.Month((int(m)-1)/3*3 + 1)
	case Yearly:
		return time.January
	}
	return m
}

func periodKey(f Frequency, y int, m time.Month) string {
	switch f {
	case Quarterly:
		return fmt.Sprintf("%04d-Q%d", y, (int(m)-1)/3+1)
	case Yearly:
		return fmt.Sprintf("%04d", y)
	}
	return fmt.Sprintf("%04d-%02d", y, int(m))
}
