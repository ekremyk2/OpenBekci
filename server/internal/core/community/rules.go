package community

import (
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// OpeningHours is one weekly window a facility may be booked in, in the
// site's time zone. start >= end crosses midnight and belongs to its start
// day; "00:00" to "00:00" is the whole day.
type OpeningHours struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Rules are the booking rules of a facility (decision C1).
type Rules struct {
	OpeningHours            []OpeningHours `json:"opening_hours"`
	MaxDurationMinutes      int            `json:"max_duration_minutes"`
	MaxDaysAhead            int            `json:"max_days_ahead"`
	MinNoticeMinutes        *int           `json:"min_notice_minutes"`
	QuotaPerUnit            *int           `json:"quota_per_unit"`
	QuotaPeriod             *string        `json:"quota_period"`
	WhoMayBook              []string       `json:"who_may_book"`
	Fee                     *Money         `json:"fee"`
	CancelRefundCutoffHours *int           `json:"cancel_refund_cutoff_hours"`
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Validate checks the rules as a manager enters them; siteCurrency is the
// currency a fee must be in.
func (r Rules) Validate(siteCurrency string) []problem.Field {
	var f []problem.Field
	bad := func(field, code string) { f = append(f, problem.Field{Field: "rules." + field, Code: code}) }
	if len(r.OpeningHours) == 0 {
		bad("opening_hours", "required")
	}
	for i, h := range r.OpeningHours {
		p := fmt.Sprintf("opening_hours[%d]", i)
		if len(h.Days) == 0 {
			bad(p+".days", "required")
		}
		for _, d := range h.Days {
			if d < 1 || d > 7 {
				bad(p+".days", "invalid_value")
				break
			}
		}
		if !hhmm.MatchString(h.Start) {
			bad(p+".start", "invalid_format")
		}
		if !hhmm.MatchString(h.End) {
			bad(p+".end", "invalid_format")
		}
	}
	if r.MaxDurationMinutes < 1 || r.MaxDurationMinutes > 7*24*60 {
		bad("max_duration_minutes", "invalid_value")
	}
	if r.MaxDaysAhead < 0 || r.MaxDaysAhead > 730 {
		bad("max_days_ahead", "invalid_value")
	}
	if r.MinNoticeMinutes != nil && (*r.MinNoticeMinutes < 0 || *r.MinNoticeMinutes > 60*24*365) {
		bad("min_notice_minutes", "invalid_value")
	}
	if (r.QuotaPerUnit == nil) != (r.QuotaPeriod == nil) {
		bad("quota_period", "required_with_quota")
	}
	if r.QuotaPerUnit != nil && *r.QuotaPerUnit < 1 {
		bad("quota_per_unit", "invalid_value")
	}
	if r.QuotaPeriod != nil && *r.QuotaPeriod != "week" && *r.QuotaPeriod != "month" {
		bad("quota_period", "invalid_value")
	}
	if len(r.WhoMayBook) == 0 {
		bad("who_may_book", "required")
	}
	for _, w := range r.WhoMayBook {
		if w != "owner" && w != "tenant" && w != "household" {
			bad("who_may_book", "invalid_value")
			break
		}
	}
	if r.Fee != nil {
		if r.Fee.Amount < 1 {
			bad("fee.amount", "invalid_value")
		}
		if siteCurrency != "" && r.Fee.Currency != siteCurrency {
			bad("fee.currency", "currency_mismatch")
		}
	}
	if r.CancelRefundCutoffHours != nil && *r.CancelRefundCutoffHours < 0 {
		bad("cancel_refund_cutoff_hours", "invalid_value")
	}
	return f
}

// minutesOf converts "HH:MM" to minutes after midnight (the string is valid).
func minutesOf(s string) int {
	return int(s[0]-'0')*600 + int(s[1]-'0')*60 + int(s[3]-'0')*10 + int(s[4]-'0')
}

// at returns the instant of "HH:MM" on the calendar day of base in loc (wall
// clock, so DST follows the clock).
func at(base time.Time, hhmmStr string, loc *time.Location) time.Time {
	m := minutesOf(hhmmStr)
	y, mo, d := base.In(loc).Date()
	return time.Date(y, mo, d, m/60, m%60, 0, 0, loc)
}

// isoWeekday is 1 for Monday to 7 for Sunday.
func isoWeekday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// WithinHours reports whether [start, end) lies entirely inside one opening
// window of the rules, in loc.
func (r Rules) WithinHours(start, end time.Time, loc *time.Location) bool {
	s := start.In(loc)
	for back := 0; back <= 1; back++ {
		day := s.AddDate(0, 0, -back)
		for _, h := range r.OpeningHours {
			if !slices.Contains(h.Days, isoWeekday(day)) {
				continue
			}
			ws, we := at(day, h.Start, loc), at(day, h.End, loc)
			if !we.After(ws) {
				y, mo, d := day.Date()
				we = time.Date(y, mo, d+1, minutesOf(h.End)/60, minutesOf(h.End)%60, 0, 0, loc)
			}
			if !start.Before(ws) && !end.After(we) {
				return true
			}
		}
	}
	return false
}

// PeriodBounds returns the calendar week (Monday to Monday) or month of t in
// loc as [from, to).
func PeriodBounds(period string, t time.Time, loc *time.Location) (from, to time.Time) {
	t = t.In(loc)
	y, m, d := t.Date()
	if period == "month" {
		return time.Date(y, m, 1, 0, 0, 0, 0, loc), time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
	}
	back := isoWeekday(t) - 1
	from = time.Date(y, m, d-back, 0, 0, 0, 0, loc)
	return from, time.Date(y, m, d-back+7, 0, 0, 0, 0, loc)
}

// CheckBooking evaluates a booking request against the rules at time now and
// returns the broken rules as field problems. used is how many confirmed
// bookings the unit already has in the quota period of start.
func (r Rules) CheckBooking(now, start, end time.Time, loc *time.Location, used int) []problem.Field {
	var f []problem.Field
	bad := func(field, code string, params map[string]any) {
		f = append(f, problem.Field{Field: field, Code: code, Params: params})
	}
	if !end.After(start) {
		bad("end", "end_before_start", nil)
		return f
	}
	if end.Sub(start) > time.Duration(r.MaxDurationMinutes)*time.Minute {
		bad("end", "too_long", map[string]any{"max_minutes": r.MaxDurationMinutes})
	}
	notice := time.Duration(0)
	if r.MinNoticeMinutes != nil {
		notice = time.Duration(*r.MinNoticeMinutes) * time.Minute
	}
	if start.Before(now.Add(notice)) {
		bad("start", "too_soon", map[string]any{"min_notice_minutes": int(notice / time.Minute)})
	}
	if start.After(now.AddDate(0, 0, r.MaxDaysAhead)) {
		bad("start", "too_far_ahead", map[string]any{"max_days_ahead": r.MaxDaysAhead})
	}
	if !r.WithinHours(start, end, loc) {
		bad("start", "outside_opening_hours", nil)
	}
	if r.QuotaPerUnit != nil && used >= *r.QuotaPerUnit {
		bad("unit_id", "quota_exceeded", map[string]any{"quota": *r.QuotaPerUnit, "period": *r.QuotaPeriod})
	}
	return f
}

// RefundDue reports whether cancelling at now earns the fee back under the
// rules: the facility defines a cutoff and the booking starts at least that
// many hours away.
func (r Rules) RefundDue(now, start time.Time) bool {
	if r.CancelRefundCutoffHours == nil {
		return false
	}
	return !now.Add(time.Duration(*r.CancelRefundCutoffHours) * time.Hour).After(start)
}

// MayBook reports whether an occupancy kind ("owner", "tenant", "household") may book.
func (r Rules) MayBook(kind string) bool { return slices.Contains(r.WhoMayBook, kind) }
