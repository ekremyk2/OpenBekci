package governance

import (
	"slices"
	"testing"
	"time"
)

func TestAssemblyYearDue(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name  string
		today time.Time
		month int
		year  int
		ok    bool
	}{
		{"the month before a January assembly", d(2026, time.December, 1), 1, 2027, true},
		{"the assembly month itself", d(2027, time.January, 31), 1, 2027, true},
		{"after it", d(2027, time.February, 1), 1, 0, false},
		{"mid-year, far from it", d(2026, time.June, 15), 1, 0, false},
		{"the day before the window", d(2026, time.November, 30), 1, 0, false},
		{"a June assembly, reminded in May", d(2026, time.May, 20), 6, 2026, true},
	}
	for _, c := range cases {
		year, ok := assemblyYearDue(c.today, c.month)
		if ok != c.ok || year != c.year {
			t.Errorf("%s: (%d, %v), want (%d, %v)", c.name, year, ok, c.year, c.ok)
		}
	}
}

func TestBelowLegalDefaults(t *testing.T) {
	if got := belowLegalDefaults(law634(), law634()); len(got) != 0 {
		t.Fatalf("the preset deviates from itself: %v", got)
	}
	s := law634()
	s.Quorum.FirstCallByCountPct = 0
	s.CallMinDays = 1
	s.ProxyCapPct = 50
	s.MaxVotesPerPersonPct = 100
	for i := range s.Thresholds {
		if s.Thresholds[i].Topic == "plan_amendment" {
			s.Thresholds[i].Rule = RuleSimpleMajorityPresent
		}
	}
	got := belowLegalDefaults(s, law634())
	for _, want := range []string{"quorum.first_call_by_count_pct", "call_min_days", "proxy_cap_pct", "max_votes_per_person_pct", "thresholds.plan_amendment"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not reported as a deviation: %v", want, got)
		}
	}
	// stricter than the law is not a deviation
	strict := law634()
	strict.CallMinDays = 30
	strict.Quorum.FirstCallBySharePct = 75
	if got := belowLegalDefaults(strict, law634()); len(got) != 0 {
		t.Errorf("stricter settings reported as deviations: %v", got)
	}
}
