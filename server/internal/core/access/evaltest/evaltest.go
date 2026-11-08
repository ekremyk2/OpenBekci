// Package evaltest is the reference evaluator of the allow-list (design 3.4):
// what the agent decides for a presentation at a point, from the entries and
// the clock. It exists to pin the rules (the shared evaluation fixtures, the
// overnight inclusive boundary, DST wall-clock behaviour) against the server's
// reading of them; the production evaluator is the agent's. Tests only: no
// production code imports this package.
package evaltest

import (
	"fmt"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

// Reasons.
const (
	ReasonMatch         = "match"
	ReasonUnknown       = "unknown"
	ReasonOutsideWindow = "outside_window"
	ReasonWrongPoint    = "wrong_point"
	ReasonUsedUp        = "used_up"
)

// Outcome is the result of an evaluation.
type Outcome struct {
	Allowed bool
	Reason  string
	// EntryID is the matching entry when allowed, else the entry behind the
	// denial ("" for unknown).
	EntryID string
}

// Evaluate applies design 3.4 steps 2 to 5 to the candidate entries (those
// whose kind and value equal the presented credential) at instant now in the
// site timezone loc. uses returns the local use count of an entry (nil: 0).
func Evaluate(candidates []protocol.Entry, pointID string, now time.Time, loc *time.Location, uses func(id string) int) Outcome {
	if len(candidates) == 0 {
		return Outcome{Reason: ReasonUnknown}
	}
	rank := map[string]int{ReasonOutsideWindow: 3, ReasonWrongPoint: 2, ReasonUsedUp: 1}
	var denial Outcome
	for i := range candidates {
		e := &candidates[i]
		reason := ReasonMatch
		switch {
		case !hasPoint(e, pointID):
			reason = ReasonWrongPoint
		case !InWindow(e, now, loc):
			reason = ReasonOutsideWindow
		case e.MaxUses != nil && uses != nil && uses(e.ID) >= *e.MaxUses:
			reason = ReasonUsedUp
		}
		if reason == ReasonMatch {
			return Outcome{Allowed: true, Reason: ReasonMatch, EntryID: e.ID}
		}
		if rank[reason] > rank[denial.Reason] {
			denial = Outcome{Reason: reason, EntryID: e.ID}
		}
	}
	return denial
}

func hasPoint(e *protocol.Entry, pointID string) bool {
	for _, p := range e.Points {
		if p == pointID {
			return true
		}
	}
	return false
}

// InWindow reports whether now lies inside the validity bounds and, when set,
// the recurring rule of e.
func InWindow(e *protocol.Entry, now time.Time, loc *time.Location) bool {
	if now.Before(e.ValidFrom.Time) {
		return false
	}
	if e.ValidTo != nil && now.After(e.ValidTo.Time) {
		return false
	}
	if e.Recurring == nil {
		return true
	}
	return InRecurring(*e.Recurring, now, loc)
}

// InRecurring evaluates a weekly wall-clock window in the site timezone.
// Boundaries are inclusive at the minute on both ends: 17:00:59 is still
// inside a window ending 17:00 (the design text said "< end"; the shared
// fixture settles it as "<= end"). start > end is an overnight window that
// belongs to the day it starts on; it matches on the start day from start on
// and on the next day up to and including end. Wall-clock semantics make DST
// changes follow the clock on the wall.
func InRecurring(r protocol.Recurring, now time.Time, loc *time.Location) bool {
	local := now.In(loc)
	start, ok1 := hhmm(r.Start)
	end, ok2 := hhmm(r.End)
	if !ok1 || !ok2 {
		return false
	}
	minute := local.Hour()*60 + local.Minute()
	today := isoWeekday(local)
	yesterday := (today+5)%7 + 1
	in := func(day int) bool {
		for _, d := range r.Days {
			if d == day {
				return true
			}
		}
		return false
	}
	if start <= end {
		return in(today) && minute >= start && minute <= end
	}
	return (in(today) && minute >= start) || (in(yesterday) && minute <= end)
}

func isoWeekday(t time.Time) int {
	if d := int(t.Weekday()); d != 0 {
		return d
	}
	return 7
}

func hhmm(s string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
