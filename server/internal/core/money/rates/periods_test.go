package rates_test

import (
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/money/rates"
)

// F4: a due date is never before Start, the first due date is the first
// DueDay on or after Start, and the result does not depend on from.
func TestPeriodsFirstDueNeverBeforeStart(t *testing.T) {
	cases := []struct {
		name string
		plan rates.Plan
		want map[string]time.Time
	}{
		{"monthly 20 Mar due 5", rates.Plan{Frequency: rates.Monthly, Start: d(2026, 3, 20), DueDay: 5},
			map[string]time.Time{"2026-03": d(2026, 4, 5), "2026-04": d(2026, 4, 5), "2026-05": d(2026, 5, 5)}},
		{"monthly start on the due day", rates.Plan{Frequency: rates.Monthly, Start: d(2026, 3, 5), DueDay: 5},
			map[string]time.Time{"2026-03": d(2026, 3, 5), "2026-04": d(2026, 4, 5)}},
		{"monthly due 31 clamped", rates.Plan{Frequency: rates.Monthly, Start: d(2026, 2, 28), DueDay: 31},
			map[string]time.Time{"2026-02": d(2026, 2, 28), "2026-03": d(2026, 3, 31)}},
		{"quarterly 15 Feb due 15", rates.Plan{Frequency: rates.Quarterly, Start: d(2026, 2, 15), DueDay: 15},
			map[string]time.Time{"2026-Q1": d(2026, 2, 15), "2026-Q2": d(2026, 4, 15)}},
		{"quarterly 16 Feb due 15", rates.Plan{Frequency: rates.Quarterly, Start: d(2026, 2, 16), DueDay: 15},
			map[string]time.Time{"2026-Q1": d(2026, 3, 15), "2026-Q2": d(2026, 4, 15)}},
		{"yearly 15 Jun due 1", rates.Plan{Frequency: rates.Yearly, Start: d(2026, 6, 15), DueDay: 1},
			map[string]time.Time{"2026": d(2026, 7, 1), "2027": d(2027, 1, 1)}},
		{"yearly 15 Dec due 1 rolls the year", rates.Plan{Frequency: rates.Yearly, Start: d(2026, 12, 15), DueDay: 1},
			map[string]time.Time{"2026": d(2027, 1, 1), "2027": d(2027, 1, 1)}},
	}
	for _, c := range cases {
		early, err := rates.Periods(c.plan, d(2024, 1, 1), d(2027, 12, 31), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		for k, due := range c.want {
			found := false
			for _, p := range early {
				if p.Key == k {
					found = true
					if !p.DueDate.Equal(due) {
						t.Errorf("%s: %s due %v want %v", c.name, k, p.DueDate, due)
					}
				}
			}
			if !found {
				t.Errorf("%s: period %s missing", c.name, k)
			}
		}
		for _, p := range early {
			if p.DueDate.Before(c.plan.Start) {
				t.Errorf("%s: %s due %v before start", c.name, p.Key, p.DueDate)
			}
		}
		// from = Start gives the same periods as an earlier from
		atStart, _ := rates.Periods(c.plan, c.plan.Start, d(2027, 12, 31), time.UTC)
		if len(atStart) != len(early) {
			t.Errorf("%s: from=Start gives %v, earlier from gives %v", c.name, keys(atStart), keys(early))
		}
	}
}

func TestForPeriodUsesDueDate(t *testing.T) {
	s := rates.Schedule{Rates: []rates.Rate{
		{From: d(2026, 1, 1), To: dp(2026, 7, 9), Amount: try(100)},
		{From: d(2026, 7, 10), Amount: try(200)},
	}}
	plan := rates.Plan{Frequency: rates.Monthly, Start: d(2026, 1, 1), DueDay: 5}
	ps, _ := rates.Periods(plan, d(2026, 6, 1), d(2026, 9, 30), time.UTC)
	// the rate effective 10 July does not apply to July, which is due on 5 July
	want := map[string]int64{"2026-06": 100, "2026-07": 100, "2026-08": 200, "2026-09": 200}
	for _, p := range ps {
		r, ok := rates.ForPeriod(s, p, time.UTC)
		if !ok || r.Amount.Amount != want[p.Key] {
			t.Errorf("%s: %v %v want %d", p.Key, r.Amount, ok, want[p.Key])
		}
	}
	if _, ok := rates.ForPeriod(rates.Schedule{}, ps[0], nil); ok {
		t.Error("no rate")
	}
}

// F5: dates are read in the site time zone, not the value's own location.
func TestScheduleUsesSiteTimezone(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, ist).UTC() // 2026-06-30T21:00Z
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, ist).UTC()  // 2026-06-29T21:00Z
	s := rates.Schedule{Rates: []rates.Rate{
		{From: d(2026, 1, 1), To: &to, Amount: try(100)},
		{From: from, Amount: try(200)},
	}}
	if err := s.Validate(ist, true); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.At(d(2026, 2, 1), nil); r.Amount.Amount != 100 {
		t.Errorf("nil tz: %v", r.Amount)
	}
	if r, _ := s.At(time.Date(2026, 6, 30, 12, 0, 0, 0, ist), ist); r.Amount.Amount != 100 {
		t.Errorf("30 June: %v", r.Amount)
	}
	if r, _ := s.At(time.Date(2026, 7, 1, 0, 0, 0, 0, ist).UTC(), ist); r.Amount.Amount != 200 {
		t.Errorf("1 July: %v", r.Amount)
	}
	// read in UTC the same instants put the boundary a day early
	if r, _ := s.At(time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC), time.UTC); r.Amount.Amount != 200 {
		t.Errorf("utc: %v", r.Amount)
	}
}
