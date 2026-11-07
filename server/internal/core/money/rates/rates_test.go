package rates_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/rates"
)

func d(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }
func dp(y int, m time.Month, dd int) *time.Time {
	t := d(y, m, dd)
	return &t
}
func try(n int64) money.Money { return money.New(n, money.TRY) }

func TestScheduleAt(t *testing.T) {
	dec := uuid.New()
	s := rates.Schedule{Rates: []rates.Rate{
		{From: d(2026, 7, 1), Amount: try(2750000), DecisionID: &dec},
		{From: d(2026, 1, 1), To: dp(2026, 6, 30), Amount: try(2400000)},
	}}
	if err := s.Validate(time.UTC, true); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   time.Time
		want int64
		ok   bool
	}{
		{d(2025, 12, 31), 0, false},
		{d(2026, 1, 1), 2400000, true},
		{d(2026, 6, 30), 2400000, true},
		{time.Date(2026, 6, 30, 23, 59, 0, 0, time.UTC), 2400000, true},
		{d(2026, 7, 1), 2750000, true},
		{d(2030, 1, 1), 2750000, true},
	}
	for _, c := range cases {
		r, ok := s.At(c.at, time.UTC)
		if ok != c.ok || r.Amount.Amount != c.want {
			t.Errorf("At(%v) = %v,%v want %d,%v", c.at, r.Amount, ok, c.want, c.ok)
		}
	}
	if r, _ := s.At(d(2026, 8, 1), time.UTC); r.DecisionID == nil || *r.DecisionID != dec {
		t.Error("decision id lost")
	}
}

func TestScheduleValidate(t *testing.T) {
	cases := []struct {
		name       string
		rs         []rates.Rate
		contiguous bool
		want       error
	}{
		{"ok with gap not required", []rates.Rate{{From: d(2026, 1, 1), To: dp(2026, 1, 31), Amount: try(1)}, {From: d(2026, 3, 1), Amount: try(2)}}, false, nil},
		{"gap", []rates.Rate{{From: d(2026, 1, 1), To: dp(2026, 1, 31), Amount: try(1)}, {From: d(2026, 3, 1), Amount: try(2)}}, true, rates.ErrGap},
		{"overlap", []rates.Rate{{From: d(2026, 1, 1), To: dp(2026, 3, 31), Amount: try(1)}, {From: d(2026, 3, 31), Amount: try(2)}}, false, rates.ErrOverlap},
		{"open not last", []rates.Rate{{From: d(2026, 1, 1), Amount: try(1)}, {From: d(2026, 3, 1), Amount: try(2)}}, false, rates.ErrOpenNotLast},
		{"reversed", []rates.Rate{{From: d(2026, 2, 1), To: dp(2026, 1, 1), Amount: try(1)}}, false, rates.ErrBadRange},
		{"negative", []rates.Rate{{From: d(2026, 2, 1), Amount: try(-1)}}, false, rates.ErrNegativeRate},
		{"mixed currency", []rates.Rate{{From: d(2026, 1, 1), To: dp(2026, 1, 31), Amount: try(1)}, {From: d(2026, 2, 1), Amount: money.New(1, money.EUR)}}, false, rates.ErrCurrency},
		{"bad currency", []rates.Rate{{From: d(2026, 1, 1), Amount: money.New(1, "XXX")}}, false, money.ErrUnknownCurrency},
		{"empty", nil, true, nil},
		{"contiguous ok", []rates.Rate{{From: d(2026, 1, 1), To: dp(2026, 1, 31), Amount: try(1)}, {From: d(2026, 2, 1), Amount: try(2)}}, true, nil},
	}
	for _, c := range cases {
		err := rates.Schedule{Rates: c.rs}.Validate(nil, c.contiguous)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// the zero schedule has no rate
	if _, ok := (rates.Schedule{}).At(d(2026, 1, 1), time.UTC); ok {
		t.Error("empty schedule has no rate")
	}
}

func keys(ps []rates.Period) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Key
	}
	return out
}

func TestPeriodsMonthlyClamp(t *testing.T) {
	plan := rates.Plan{Frequency: rates.Monthly, Start: d(2026, 1, 15), DueDay: 31}
	ps, err := rates.Periods(plan, d(2026, 1, 1), d(2026, 4, 30), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	wantDue := []time.Time{d(2026, 1, 31), d(2026, 2, 28), d(2026, 3, 31), d(2026, 4, 30)}
	if len(ps) != 4 {
		t.Fatalf("got %v", keys(ps))
	}
	for i, p := range ps {
		if !p.DueDate.Equal(wantDue[i]) {
			t.Errorf("%s due %v want %v", p.Key, p.DueDate, wantDue[i])
		}
	}
	if ps[0].Key != "2026-01" || !ps[0].Start.Equal(d(2026, 1, 1)) || !ps[0].End.Equal(d(2026, 1, 31)) {
		t.Errorf("first period %+v", ps[0])
	}
	// leap year
	ps, _ = rates.Periods(rates.Plan{Frequency: rates.Monthly, Start: d(2028, 2, 1), DueDay: 30}, d(2028, 2, 1), d(2028, 2, 29), time.UTC)
	if len(ps) != 1 || !ps[0].DueDate.Equal(d(2028, 2, 29)) {
		t.Errorf("leap: %+v", ps)
	}
}

func TestPeriodsEndAndWindow(t *testing.T) {
	plan := rates.Plan{Frequency: rates.Monthly, Start: d(2026, 1, 1), End: dp(2026, 3, 10), DueDay: 5}
	ps, err := rates.Periods(plan, d(2025, 1, 1), d(2027, 1, 1), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(ps); len(got) != 3 || got[2] != "2026-03" {
		t.Fatalf("got %v", got)
	}
	// catch-up window: only periods due in [from, to]
	ps, _ = rates.Periods(plan, d(2026, 2, 6), d(2026, 3, 5), time.UTC)
	if got := keys(ps); len(got) != 1 || got[0] != "2026-03" {
		t.Fatalf("window got %v", got)
	}
	// plan starting after its first due day still bills the containing period, due on the next DueDay
	late := rates.Plan{Frequency: rates.Monthly, Start: d(2026, 1, 20), DueDay: 5}
	ps, _ = rates.Periods(late, d(2026, 1, 1), d(2026, 2, 28), time.UTC)
	if len(ps) != 2 || ps[0].Key != "2026-01" || !ps[0].DueDate.Equal(d(2026, 2, 5)) {
		t.Fatalf("late start: %v", keys(ps))
	}
	// empty window
	ps, _ = rates.Periods(plan, d(2026, 5, 1), d(2026, 4, 1), time.UTC)
	if len(ps) != 0 {
		t.Fatal("empty window")
	}
}

func TestPeriodsQuarterlyYearly(t *testing.T) {
	q := rates.Plan{Frequency: rates.Quarterly, Start: d(2026, 2, 10), DueDay: 15}
	ps, _ := rates.Periods(q, d(2026, 1, 1), d(2026, 12, 31), time.UTC)
	if got := keys(ps); len(got) != 4 || got[0] != "2026-Q1" || got[3] != "2026-Q4" {
		t.Fatalf("quarterly %v", got)
	}
	if !ps[1].DueDate.Equal(d(2026, 4, 15)) || !ps[1].Start.Equal(d(2026, 4, 1)) || !ps[1].End.Equal(d(2026, 6, 30)) {
		t.Errorf("Q2 %+v", ps[1])
	}
	if !ps[0].DueDate.Equal(d(2026, 2, 15)) { // 15 Jan is before Start (10 Feb)
		t.Errorf("Q1 due %v", ps[0].DueDate)
	}
	y := rates.Plan{Frequency: rates.Yearly, Start: d(2026, 6, 1), End: dp(2027, 6, 1), DueDay: 31}
	ps, _ = rates.Periods(y, d(2026, 1, 1), d(2030, 1, 1), time.UTC)
	if got := keys(ps); len(got) != 2 || got[0] != "2026" || got[1] != "2027" {
		t.Fatalf("yearly %v", got)
	}
	if !ps[0].DueDate.Equal(d(2026, 6, 30)) || !ps[0].End.Equal(d(2026, 12, 31)) { // 31 clamped to 30 June, first on or after Start
		t.Errorf("yearly %+v", ps[0])
	}
}

func TestPeriodsDSTSafe(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	ny, _ := time.LoadLocation("America/New_York")
	for _, tz := range []*time.Location{ist, ny} {
		plan := rates.Plan{Frequency: rates.Monthly, Start: time.Date(2026, 1, 1, 0, 0, 0, 0, tz), DueDay: 8}
		ps, err := rates.Periods(plan, time.Date(2026, 1, 1, 0, 0, 0, 0, tz), time.Date(2026, 12, 31, 23, 0, 0, 0, tz), tz)
		if err != nil || len(ps) != 12 {
			t.Fatalf("%v: %d %v", tz, len(ps), err)
		}
		for i, p := range ps {
			y, m, dd := p.DueDate.In(tz).Date()
			if y != 2026 || int(m) != i+1 || dd != 8 {
				t.Errorf("%v: period %s due %v", tz, p.Key, p.DueDate)
			}
		}
	}
	// a time late in the evening in a western zone is still that local date
	plan := rates.Plan{Frequency: rates.Monthly, Start: time.Date(2026, 3, 1, 0, 0, 0, 0, ny), DueDay: 1}
	ps, _ := rates.Periods(plan, time.Date(2026, 3, 1, 23, 30, 0, 0, ny), time.Date(2026, 3, 1, 23, 30, 0, 0, ny), ny)
	if len(ps) != 1 {
		t.Fatalf("local date handling: %v", keys(ps))
	}
	// nil zone defaults to UTC
	if ps, _ := rates.Periods(plan, d(2026, 3, 1), d(2026, 3, 1), nil); len(ps) != 1 {
		t.Fatal("nil tz")
	}
}

func TestPeriodsErrors(t *testing.T) {
	good := rates.Plan{Frequency: rates.Monthly, Start: d(2026, 1, 1), DueDay: 1}
	bads := []rates.Plan{
		{Frequency: rates.Monthly, Start: d(2026, 1, 1), DueDay: 0},
		{Frequency: rates.Monthly, Start: d(2026, 1, 1), DueDay: 32},
		{Frequency: "weekly", Start: d(2026, 1, 1), DueDay: 1},
		{Frequency: rates.Monthly, Start: d(2026, 2, 1), End: dp(2026, 1, 1), DueDay: 1},
	}
	for i, p := range bads {
		if _, err := rates.Periods(p, d(2026, 1, 1), d(2026, 2, 1), time.UTC); !errors.Is(err, rates.ErrBadPlan) {
			t.Errorf("%d: %v", i, err)
		}
	}
	if _, err := rates.Periods(good, d(2026, 1, 1), d(2026, 2, 1), time.UTC); err != nil {
		t.Error(err)
	}
}

func TestPropMonthlyPeriods(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		startY := rapid.IntRange(2020, 2030).Draw(t, "y")
		startM := rapid.IntRange(1, 12).Draw(t, "m")
		months := rapid.IntRange(1, 60).Draw(t, "months")
		due := rapid.IntRange(1, 31).Draw(t, "due")
		start := d(startY, time.Month(startM), rapid.IntRange(1, 28).Draw(t, "sd"))
		plan := rates.Plan{Frequency: rates.Monthly, Start: start, DueDay: due}
		from := d(startY, time.Month(startM), 1)
		to := from.AddDate(0, months, -1)
		ps, err := rates.Periods(plan, from, to, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		// strictly increasing, unique keys, every due date in the window and day <= month length
		seen := map[string]bool{}
		var prev time.Time
		for _, p := range ps {
			if seen[p.Key] {
				t.Fatalf("duplicate key %s", p.Key)
			}
			seen[p.Key] = true
			if p.DueDate.Before(prev) || p.DueDate.Before(start) || p.DueDate.Before(from) || p.DueDate.After(to) {
				t.Fatalf("bad due %v", p.DueDate)
			}
			prev = p.DueDate
			if p.End.Before(p.Start) || p.End.Month() != p.Start.Month() {
				t.Fatalf("bad bounds %+v", p)
			}
		}
		// idempotent catch-up: splitting the window yields the same union
		mid := from.AddDate(0, months/2, 0)
		a, _ := rates.Periods(plan, from, mid.AddDate(0, 0, -1), time.UTC)
		b, _ := rates.Periods(plan, mid, to, time.UTC)
		if len(a)+len(b) != len(ps) {
			t.Fatalf("split %d+%d != %d", len(a), len(b), len(ps))
		}
	})
}
