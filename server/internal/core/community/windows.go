package community

import (
	"slices"
	"strings"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Scan results (the ScanResult.reason values of the contract).
const (
	ReasonMatch         = "match"
	ReasonUnknown       = "unknown"
	ReasonOutsideWindow = "outside_window"
	ReasonUsedUp        = "used_up"
	ReasonRevoked       = "revoked"
	ReasonWrongSite     = "wrong_site"
)

// InWindow reports whether t is inside the window of a visitor entry,
// extended by the 30 minute slack on either side.
func InWindow(from, to, t time.Time) bool {
	return !t.Before(from.Add(-visitorSlack)) && !t.After(to.Add(visitorSlack))
}

// RecurringRule is the weekly rule of a recurring visitor: ISO weekdays and
// wall-clock times in the site's time zone.
type RecurringRule struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Matches reports whether t falls inside the rule in loc. It is the same
// evaluation the gate agent runs (access design 3.4), with boundaries
// inclusive at the minute:
//
//   - start <= end: today's weekday is in days and the local time is in [start, end];
//   - start > end (overnight): today is in days and the time is >= start, or
//     yesterday is in days and the time is <= end.
func (r RecurringRule) Matches(t time.Time, loc *time.Location) bool {
	lt := t.In(loc)
	now := lt.Hour()*60 + lt.Minute()
	s, e := minutesOf(r.Start), minutesOf(r.End)
	today := isoWeekday(lt)
	if s <= e {
		return slices.Contains(r.Days, today) && now >= s && now <= e
	}
	yesterday := today - 1
	if yesterday == 0 {
		yesterday = 7
	}
	return slices.Contains(r.Days, today) && now >= s || slices.Contains(r.Days, yesterday) && now <= e
}

// Validate checks a rule as entered.
func (r RecurringRule) Validate() []problem.Field {
	var f []problem.Field
	if len(r.Days) == 0 || len(r.Days) > 7 {
		f = append(f, problem.Field{Field: "days", Code: "invalid_value"})
	}
	seen := map[int]bool{}
	for _, d := range r.Days {
		if d < 1 || d > 7 || seen[d] {
			f = append(f, problem.Field{Field: "days", Code: "invalid_value"})
			break
		}
		seen[d] = true
	}
	if !hhmm.MatchString(r.Start) {
		f = append(f, problem.Field{Field: "start", Code: "invalid_format"})
	}
	if !hhmm.MatchString(r.End) {
		f = append(f, problem.Field{Field: "end", Code: "invalid_format"})
	}
	return f
}

// normalizePlate reuses the vehicle registry's normalization so a visitor's
// plate compares equal to a resident's. An empty plate is no plate.
func normalizePlate(field, s string) (*string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil //nolint:nilnil // no plate
	}
	p, ok := tenancy.NormalizePlate(s)
	if !ok {
		return nil, validation(field, "invalid_format")
	}
	return &p, nil
}

// checkWindow validates the window of a pre-registration against now.
func checkWindow(now, from, to time.Time) []problem.Field {
	var f []problem.Field
	switch {
	case to.Before(from):
		f = append(f, problem.Field{Field: "window_to", Code: "end_before_start"})
	case to.Sub(from) > maxVisitorWindow:
		f = append(f, problem.Field{Field: "window_to", Code: "too_long", Params: map[string]any{"max_hours": int(maxVisitorWindow / time.Hour)}})
	case to.Add(visitorSlack).Before(now):
		f = append(f, problem.Field{Field: "window_to", Code: "in_the_past"})
	}
	return f
}
