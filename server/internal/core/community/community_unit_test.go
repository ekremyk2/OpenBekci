package community

import (
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/problem"
)

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func intp(i int) *int       { return &i }
func strp(s string) *string { return &s }

func baseRules() Rules {
	return Rules{
		OpeningHours:       []OpeningHours{{Days: []int{1, 2, 3, 4, 5, 6, 7}, Start: "08:00", End: "22:00"}},
		MaxDurationMinutes: 240,
		MaxDaysAhead:       30,
		WhoMayBook:         []string{"owner", "tenant"},
	}
}

func codes(fs []problem.Field) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return out
}

func TestRulesValidate(t *testing.T) {
	r := baseRules()
	if bad := r.Validate("TRY"); len(bad) != 0 {
		t.Fatalf("valid rules rejected: %v", bad)
	}
	cases := map[string]func(*Rules){
		"no hours":                func(r *Rules) { r.OpeningHours = nil },
		"bad weekday":             func(r *Rules) { r.OpeningHours[0].Days = []int{0} },
		"bad time":                func(r *Rules) { r.OpeningHours[0].Start = "25:00" },
		"zero duration":           func(r *Rules) { r.MaxDurationMinutes = 0 },
		"quota without period":    func(r *Rules) { r.QuotaPerUnit = intp(2) },
		"unknown period":          func(r *Rules) { r.QuotaPerUnit, r.QuotaPeriod = intp(2), strp("year") },
		"nobody may book":         func(r *Rules) { r.WhoMayBook = nil },
		"staff may not book":      func(r *Rules) { r.WhoMayBook = []string{"staff"} },
		"fee in another currency": func(r *Rules) { r.Fee = &Money{Amount: 100, Currency: "EUR"} },
		"zero fee":                func(r *Rules) { r.Fee = &Money{Amount: 0, Currency: "TRY"} },
		"negative cutoff":         func(r *Rules) { r.CancelRefundCutoffHours = intp(-1) },
	}
	for name, mutate := range cases {
		r := baseRules()
		mutate(&r)
		if bad := r.Validate("TRY"); len(bad) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWithinHours(t *testing.T) {
	loc := mustLoc(t)
	r := baseRules() // 08:00 to 22:00 every day, Istanbul
	at := func(h, m int) time.Time { return time.Date(2027, 3, 10, h, m, 0, 0, loc) }
	if !r.WithinHours(at(8, 0), at(12, 0), loc) {
		t.Error("08:00 to 12:00 is inside")
	}
	if !r.WithinHours(at(18, 0), at(22, 0), loc) {
		t.Error("ending exactly at closing is inside")
	}
	if r.WithinHours(at(7, 59), at(9, 0), loc) || r.WithinHours(at(21, 0), at(22, 1), loc) {
		t.Error("starting early or ending late is outside")
	}
	// one window per day: a booking across midnight is not inside the 08:00 to 22:00 windows
	if r.WithinHours(at(21, 0), time.Date(2027, 3, 11, 9, 0, 0, 0, loc), loc) {
		t.Error("crossing closing time is outside")
	}
	// weekdays: 2027-03-10 is a Wednesday (3)
	r.OpeningHours = []OpeningHours{{Days: []int{1, 2}, Start: "08:00", End: "22:00"}}
	if r.WithinHours(at(10, 0), at(11, 0), loc) {
		t.Error("Wednesday is not an opening day")
	}
	// overnight window belongs to its start day: Wednesday 20:00 to Thursday 02:00
	r.OpeningHours = []OpeningHours{{Days: []int{3}, Start: "20:00", End: "02:00"}}
	if !r.WithinHours(at(21, 0), time.Date(2027, 3, 11, 1, 0, 0, 0, loc), loc) {
		t.Error("inside the overnight window")
	}
	if !r.WithinHours(time.Date(2027, 3, 11, 0, 30, 0, 0, loc), time.Date(2027, 3, 11, 1, 30, 0, 0, loc), loc) {
		t.Error("the small hours of Thursday belong to Wednesday's window")
	}
	if r.WithinHours(time.Date(2027, 3, 11, 1, 0, 0, 0, loc), time.Date(2027, 3, 11, 3, 0, 0, 0, loc), loc) {
		t.Error("after the window ends is outside")
	}
	// 00:00 to 00:00 is the whole day
	r.OpeningHours = []OpeningHours{{Days: []int{3}, Start: "00:00", End: "00:00"}}
	if !r.WithinHours(at(0, 0), at(23, 59), loc) {
		t.Error("whole-day window")
	}
}

func TestCheckBooking(t *testing.T) {
	loc := mustLoc(t)
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, loc)
	r := baseRules()
	r.MinNoticeMinutes = intp(120)
	r.QuotaPerUnit, r.QuotaPeriod = intp(2), strp("month")
	start := time.Date(2027, 3, 5, 10, 0, 0, 0, loc)
	if bad := r.CheckBooking(now, start, start.Add(3*time.Hour), loc, 1); len(bad) != 0 {
		t.Fatalf("a booking within the rules: %v", codes(bad))
	}
	check := func(name string, s, e time.Time, used int, want string) {
		t.Helper()
		got := codes(r.CheckBooking(now, s, e, loc, used))
		for _, c := range got {
			if c == want {
				return
			}
		}
		t.Errorf("%s: want %s in %v", name, want, got)
	}
	check("too long", start, start.Add(5*time.Hour), 0, "too_long")
	check("too soon", now.Add(time.Hour), now.Add(2*time.Hour), 0, "too_soon")
	check("too far ahead", now.AddDate(0, 0, 40), now.AddDate(0, 0, 40).Add(time.Hour), 0, "too_far_ahead")
	check("outside hours", time.Date(2027, 3, 5, 23, 0, 0, 0, loc), time.Date(2027, 3, 5, 23, 30, 0, 0, loc), 0, "outside_opening_hours")
	check("quota", start, start.Add(time.Hour), 2, "quota_exceeded")
	check("backwards", start, start.Add(-time.Hour), 0, "end_before_start")
}

func TestPeriodBounds(t *testing.T) {
	loc := mustLoc(t)
	// Wednesday 2027-03-10: the ISO week is Monday 8 to Monday 15; the month March
	from, to := PeriodBounds("week", time.Date(2027, 3, 10, 15, 0, 0, 0, loc), loc)
	if !from.Equal(time.Date(2027, 3, 8, 0, 0, 0, 0, loc)) || !to.Equal(time.Date(2027, 3, 15, 0, 0, 0, 0, loc)) {
		t.Errorf("week: %v to %v", from, to)
	}
	// Sunday belongs to the week that started on the Monday before
	from, _ = PeriodBounds("week", time.Date(2027, 3, 14, 23, 0, 0, 0, loc), loc)
	if !from.Equal(time.Date(2027, 3, 8, 0, 0, 0, 0, loc)) {
		t.Errorf("sunday week start: %v", from)
	}
	from, to = PeriodBounds("month", time.Date(2027, 12, 31, 23, 0, 0, 0, loc), loc)
	if !from.Equal(time.Date(2027, 12, 1, 0, 0, 0, 0, loc)) || !to.Equal(time.Date(2028, 1, 1, 0, 0, 0, 0, loc)) {
		t.Errorf("month: %v to %v", from, to)
	}
}

func TestRefundDue(t *testing.T) {
	r := baseRules()
	start := time.Date(2027, 3, 5, 10, 0, 0, 0, time.UTC)
	if r.RefundDue(start.Add(-72*time.Hour), start) {
		t.Error("no cutoff configured: no automatic refund")
	}
	r.CancelRefundCutoffHours = intp(24)
	if !r.RefundDue(start.Add(-48*time.Hour), start) || !r.RefundDue(start.Add(-24*time.Hour), start) {
		t.Error("cancelled at or before the cutoff is refunded")
	}
	if r.RefundDue(start.Add(-23*time.Hour), start) {
		t.Error("cancelled inside the cutoff is not refunded")
	}
}

func TestInWindowSlack(t *testing.T) {
	from := time.Date(2027, 3, 5, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	for _, c := range []struct {
		at   time.Time
		want bool
	}{
		{from.Add(-31 * time.Minute), false}, {from.Add(-30 * time.Minute), true}, {from, true},
		{to, true}, {to.Add(30 * time.Minute), true}, {to.Add(31 * time.Minute), false},
	} {
		if InWindow(from, to, c.at) != c.want {
			t.Errorf("InWindow at %v: want %v", c.at, c.want)
		}
	}
}

// The recurring evaluation is the one the gate agent runs (access design 3.4):
// boundaries inclusive at the minute, overnight windows belong to their start day.
func TestRecurringMatches(t *testing.T) {
	loc := mustLoc(t)
	day := func(d, h, m int) time.Time { return time.Date(2027, 3, d, h, m, 0, 0, loc) } // 2027-03-10 is a Wednesday (3)
	r := RecurringRule{Days: []int{1, 3}, Start: "09:00", End: "17:00"}
	for _, c := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"start inclusive", day(10, 9, 0), true},
		{"end inclusive", day(10, 17, 0), true},
		{"one minute after", day(10, 17, 1), false},
		{"one minute before", day(10, 8, 59), false},
		{"wrong weekday (Thursday)", day(11, 10, 0), false},
		{"Monday", day(8, 12, 0), true},
	} {
		if got := r.Matches(c.at, loc); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// overnight, Wednesday only: 22:00 to 06:00 belongs to Wednesday
	night := RecurringRule{Days: []int{3}, Start: "22:00", End: "06:00"}
	for _, c := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"Wednesday 22:00 inclusive", day(10, 22, 0), true},
		{"Wednesday 23:59", day(10, 23, 59), true},
		{"Thursday 00:00 (still Wednesday's window)", day(11, 0, 0), true},
		{"Thursday 06:00 inclusive", day(11, 6, 0), true},
		{"Thursday 06:01", day(11, 6, 1), false},
		{"Wednesday 06:00 belongs to Tuesday's window", day(10, 6, 0), false},
		{"Wednesday 12:00", day(10, 12, 0), false},
		{"Thursday 22:00 is Thursday's own start", day(11, 22, 0), false},
	} {
		if got := night.Matches(c.at, loc); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// Sunday night into Monday: ISO weekday 7 wraps to 1
	sun := RecurringRule{Days: []int{7}, Start: "22:00", End: "02:00"}
	if !sun.Matches(time.Date(2027, 3, 15, 1, 0, 0, 0, loc), loc) { // Monday 01:00
		t.Error("Sunday's overnight window runs into Monday")
	}
	// wall clock across a DST change in another zone (the site zone decides)
	ny, _ := time.LoadLocation("America/New_York")
	dst := RecurringRule{Days: []int{7}, Start: "01:30", End: "03:30"} // 2027-03-14 is the US spring-forward Sunday
	if !dst.Matches(time.Date(2027, 3, 14, 3, 0, 0, 0, ny), ny) {
		t.Error("03:00 local after the jump is inside the wall-clock window")
	}
}

func TestRecurringRuleValidate(t *testing.T) {
	ok := RecurringRule{Days: []int{1, 2}, Start: "09:00", End: "17:00"}
	if bad := ok.Validate(); len(bad) != 0 {
		t.Fatalf("valid rule rejected: %v", bad)
	}
	for name, r := range map[string]RecurringRule{
		"no days":       {Days: nil, Start: "09:00", End: "17:00"},
		"weekday 8":     {Days: []int{8}, Start: "09:00", End: "17:00"},
		"duplicate day": {Days: []int{1, 1}, Start: "09:00", End: "17:00"},
		"bad start":     {Days: []int{1}, Start: "9:00", End: "17:00"},
		"bad end":       {Days: []int{1}, Start: "09:00", End: "24:00"},
	} {
		if len(r.Validate()) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPlateNormalizationReusesVehicleRegistry(t *testing.T) {
	for in, want := range map[string]string{"34 abc 123": "34ABC123", "34-ABC-123": "34ABC123", "06 ŞÖ 1234": "06SO1234"} {
		p, err := normalizePlate("plate", in)
		if err != nil || p == nil || *p != want {
			t.Errorf("%q: got %v %v, want %s", in, p, err, want)
		}
	}
	if p, err := normalizePlate("plate", "  "); err != nil || p != nil {
		t.Errorf("blank plate means none: %v %v", p, err)
	}
	if _, err := normalizePlate("plate", "34/ABC"); err == nil {
		t.Error("a plate with a slash is invalid")
	}
}

func TestParseQR(t *testing.T) {
	tok := "0123456789abcdefghij0123456789abcdefghij0123"
	h, ok := ParseQR(" obk1." + tok + "\n")
	if !ok || HashHex(h) != TokenHashHex(tok) {
		t.Fatalf("hash of the token part: ok=%v %s", ok, HashHex(h))
	}
	for _, bad := range []string{"", tok, "obk2." + tok, "obk1.short", "OBK1." + tok} {
		if _, ok := ParseQR(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestEvalEntryAndRecurring(t *testing.T) {
	now := time.Date(2027, 3, 5, 10, 0, 0, 0, time.UTC)
	e := VisitorEntry{ApprovalStatus: "pre_approved", WindowFrom: now.Add(-time.Hour), WindowTo: now.Add(time.Hour)}
	if got := evalEntry(e, now); got != ReasonMatch {
		t.Errorf("inside the window: %s", got)
	}
	e2 := e
	e2.WindowFrom, e2.WindowTo = now.Add(2*time.Hour), now.Add(3*time.Hour)
	if got := evalEntry(e2, now); got != ReasonOutsideWindow {
		t.Errorf("before the window: %s", got)
	}
	arrived := now
	e3 := e
	e3.ArrivedAt = &arrived
	if got := evalEntry(e3, now); got != ReasonUsedUp {
		t.Errorf("already arrived: %s", got)
	}
	e4 := e
	e4.Cancelled = true
	if got := evalEntry(e4, now); got != ReasonRevoked {
		t.Errorf("cancelled: %s", got)
	}
	e5 := e
	e5.ApprovalStatus = "denied"
	if got := evalEntry(e5, now); got != ReasonRevoked {
		t.Errorf("denied: %s", got)
	}
	loc := mustLoc(t)
	rv := RecurringVisitor{Status: "active", Days: []int{5}, Start: "00:00", End: "23:59", ValidFrom: "2027-01-01"}
	if got := evalRecurring(rv, time.Date(2027, 3, 5, 10, 0, 0, 0, loc), loc); got != ReasonMatch { // Friday
		t.Errorf("recurring match: %s", got)
	}
	if got := evalRecurring(rv, time.Date(2026, 12, 31, 10, 0, 0, 0, loc), loc); got != ReasonOutsideWindow {
		t.Errorf("before valid_from: %s", got)
	}
	end := "2027-03-04"
	rv.ValidTo = &end
	if got := evalRecurring(rv, time.Date(2027, 3, 5, 10, 0, 0, 0, loc), loc); got != ReasonOutsideWindow {
		t.Errorf("after valid_to: %s", got)
	}
	rv.Status = "revoked"
	if got := evalRecurring(rv, time.Date(2027, 3, 5, 10, 0, 0, 0, loc), loc); got != ReasonRevoked {
		t.Errorf("revoked: %s", got)
	}
}

func TestCheckWindow(t *testing.T) {
	now := time.Date(2027, 3, 5, 10, 0, 0, 0, time.UTC)
	if bad := checkWindow(now, now, now.Add(2*time.Hour)); len(bad) != 0 {
		t.Errorf("ordinary window: %v", bad)
	}
	for name, c := range map[string][2]time.Time{
		"ends before it starts": {now, now.Add(-time.Hour)},
		"longer than a week":    {now, now.Add(8 * 24 * time.Hour)},
		"entirely in the past":  {now.Add(-5 * time.Hour), now.Add(-4 * time.Hour)},
	} {
		if len(checkWindow(now, c[0], c[1])) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if bad := checkWindow(now, now.Add(-time.Hour), now.Add(-10*time.Minute)); len(bad) != 0 {
		t.Errorf("a window that ended 10 minutes ago is still inside the slack: %v", bad)
	}
}
