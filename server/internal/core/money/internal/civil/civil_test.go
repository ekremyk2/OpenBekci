package civil_test

import (
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/money/internal/civil"
)

func TestDayRoundTrip(t *testing.T) {
	ist := time.FixedZone("TRT", 3*3600)
	// 22:30 UTC on 31 Jan is 1 Feb in Istanbul
	at := time.Date(2026, 1, 31, 22, 30, 0, 0, time.UTC)
	if got, want := civil.Day(at, ist), civil.FromYMD(2026, 2, 1); got != want {
		t.Fatalf("Day = %d want %d", got, want)
	}
	if civil.Day(at, time.UTC) != civil.FromYMD(2026, 1, 31) {
		t.Fatal("utc day")
	}
	y, m, d := civil.YMD(civil.FromYMD(2028, 2, 29))
	if y != 2028 || m != time.February || d != 29 {
		t.Fatalf("YMD %d %v %d", y, m, d)
	}
	if got := civil.Time(civil.FromYMD(2026, 3, 8), ist); got.Day() != 8 || got.Hour() != 0 {
		t.Fatalf("Time %v", got)
	}
	if civil.Day(time.Date(1969, 12, 31, 12, 0, 0, 0, time.UTC), time.UTC) != -1 {
		t.Fatal("pre-epoch floor")
	}
	if civil.Day(time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC), time.UTC) != -1 {
		t.Fatal("pre-epoch exact")
	}
	if civil.DaysInMonth(2026, time.February) != 28 || civil.DaysInMonth(2028, time.February) != 29 || civil.DaysInMonth(2026, time.December) != 31 {
		t.Fatal("days in month")
	}
}
