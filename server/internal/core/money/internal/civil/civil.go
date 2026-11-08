// Package civil converts instants to calendar-day numbers in a given time zone.
// Working in day numbers (days since 1970-01-01 of the local calendar date)
// makes every date computation independent of DST transitions and of the
// wall-clock time carried by a time.Time.
package civil

import "time"

// Day returns the calendar date of t in tz as a day number (days since 1970-01-01).
func Day(t time.Time, tz *time.Location) int {
	y, m, d := t.In(tz).Date()
	secs := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
	q := secs / 86400
	if secs%86400 != 0 && secs < 0 {
		q--
	}
	return int(q)
}

// Time returns the start of the given day number in tz. If midnight does not
// exist in tz on that date (a DST gap at 00:00), the first valid instant of the
// day is returned; the calendar date is always the requested one.
func Time(day int, tz *time.Location) time.Time {
	y, m, d := time.Unix(int64(day)*86400, 0).UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, tz)
}

// YMD returns the year, month and day of a day number.
func YMD(day int) (int, time.Month, int) {
	return time.Unix(int64(day)*86400, 0).UTC().Date()
}

// FromYMD returns the day number of a calendar date.
func FromYMD(y int, m time.Month, d int) int {
	return Day(time.Date(y, m, d, 0, 0, 0, 0, time.UTC), time.UTC)
}

// DaysInMonth returns the number of days of month m in year y.
func DaysInMonth(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
