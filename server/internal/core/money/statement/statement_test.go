package statement_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	st "github.com/openbekci/openbekci/server/internal/core/money/statement"
)

func d(m time.Month, dd int) time.Time { return time.Date(2026, m, dd, 12, 0, 0, 0, time.UTC) }
func dp(m time.Month, dd int) *time.Time {
	t := d(m, dd)
	return &t
}
func try(n int64) money.Money { return money.New(n, money.TRY) }

var (
	alice = uuid.UUID{15: 1}
	bob   = uuid.UUID{15: 2}
)

func line(date time.Time, k st.Kind, side st.DebtorKind, who uuid.UUID, amt int64, ref string) st.Line {
	part := st.PartPrincipal
	if k == st.KindFee {
		part = st.PartFee
	}
	return st.Line{Date: date, Kind: k, DebtorKind: side, PersonID: who, Amount: try(amt), Part: part, Ref: ref}
}

func sample() []st.Line {
	return []st.Line{
		line(d(time.January, 1), st.KindOpening, st.Owner, alice, 50000, "open"),
		line(d(time.January, 5), st.KindCharge, st.Tenant, bob, 100000, "jan-aidat"),
		line(d(time.January, 5), st.KindCharge, st.Owner, alice, 20000, "jan-demirbas"),
		line(d(time.January, 20), st.KindPayment, st.Tenant, bob, -100000, "pay1"),
		line(d(time.February, 5), st.KindCharge, st.Tenant, bob, 100000, "feb-aidat"),
		line(d(time.February, 28), st.KindFee, st.Tenant, bob, 1500, "fee-feb"),
		{Date: d(time.March, 3), Kind: st.KindPayment, DebtorKind: st.Tenant, PersonID: bob, Amount: try(-1500), Part: st.PartFee, Ref: "pay-fee"},
		line(d(time.March, 4), st.KindWaiver, st.Owner, alice, 0, "waiver0"),
	}
}

func TestBuildFullYear(t *testing.T) {
	s, err := st.Build(money.TRY, sample(), st.Range{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Opening.Total.Amount != 0 || len(s.Rows) != 8 {
		t.Fatalf("opening/rows: %+v", s.Opening)
	}
	// 50000+100000+20000-100000+100000+1500-1500+0
	if s.Closing.Total.Amount != 170000 || s.Closing.Principal.Amount != 170000 || s.Closing.Fee.Amount != 0 {
		t.Fatalf("closing %+v", s.Closing)
	}
	if s.Rows[0].Line.Ref != "open" || s.Rows[2].Balance.Total.Amount != 170000 {
		t.Fatalf("rows %+v", s.Rows[:3])
	}
	if s.ByKind[st.KindCharge].Amount != 220000 || s.ByKind[st.KindPayment].Amount != -101500 {
		t.Fatalf("by kind %+v", s.ByKind)
	}
	if len(s.Sides) != 2 || s.Sides[0].DebtorKind != st.Owner || s.Sides[0].Closing.Total.Amount != 70000 || s.Sides[1].Closing.Total.Amount != 100000 {
		t.Fatalf("sides %+v", s.Sides)
	}
	if len(s.People) != 2 || s.People[0].PersonID != alice || s.People[0].Closing.Total.Amount != 70000 || s.People[1].Closing.Total.Amount != 100000 {
		t.Fatalf("people %+v", s.People)
	}
	if s.Sides[1].Charged.Amount != 201500 || s.Sides[1].Settled.Amount != -101500 {
		t.Fatalf("tenant side %+v", s.Sides[1])
	}
}

func TestBuildRange(t *testing.T) {
	s, err := st.Build(money.TRY, sample(), st.Range{From: dp(time.February, 1), To: dp(time.February, 28)}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// before Feb 1: 50000 + 100000 + 20000 - 100000 = 70000
	if s.Opening.Total.Amount != 70000 || len(s.Rows) != 2 || s.Closing.Total.Amount != 171500 {
		t.Fatalf("%+v rows=%d", s, len(s.Rows))
	}
	// fee line moves the fee component
	if s.Closing.Fee.Amount != 1500 || s.Closing.Principal.Amount != 170000 {
		t.Fatalf("split %+v", s.Closing)
	}
	// side opening balances only count lines before From
	if s.Sides[0].Opening.Total.Amount != 70000 || s.Sides[0].Charged.Amount != 0 {
		t.Fatalf("owner side %+v", s.Sides[0])
	}
	// a range ending before any line is empty
	e, err := st.Build(money.TRY, sample(), st.Range{To: dp(time.December, 1), From: dp(time.November, 1)}, nil)
	if err != nil || len(e.Rows) != 0 || e.Opening.Total.Amount != 170000 || e.Closing != e.Opening {
		t.Fatalf("empty range %+v %v", e, err)
	}
	// lines after To are excluded entirely
	b, _ := st.Build(money.TRY, sample(), st.Range{To: dp(time.January, 5)}, nil)
	if b.Closing.Total.Amount != 170000 || len(b.Rows) != 3 || len(b.Sides) != 2 {
		t.Fatalf("to-bounded %+v", b)
	}
	only, _ := st.Build(money.TRY, sample(), st.Range{To: dp(time.January, 1)}, nil)
	if len(only.Sides) != 1 || only.Sides[0].DebtorKind != st.Owner {
		t.Fatalf("sides only owner: %+v", only.Sides)
	}
}

func TestBuildOrdersByDateAndOpeningFirst(t *testing.T) {
	lines := []st.Line{
		line(d(time.March, 1), st.KindCharge, st.Owner, alice, 100, "c"),
		line(d(time.January, 1), st.KindCharge, st.Owner, alice, 10, "a"),
		line(d(time.January, 1), st.KindOpening, st.Owner, alice, 5, "o"),
		line(d(time.January, 1), st.KindCharge, st.Owner, alice, 20, "b"),
	}
	s, err := st.Build(money.TRY, lines, st.Range{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var refs string
	for _, r := range s.Rows {
		refs += r.Line.Ref
	}
	if refs != "oabc" {
		t.Fatalf("order %q", refs)
	}
}

func TestBuildTimezone(t *testing.T) {
	ist := time.FixedZone("TRT", 3*3600)
	// 22:30 UTC on Jan 31 is Feb 1 01:30 in Istanbul: belongs to February there
	l := st.Line{Date: time.Date(2026, 1, 31, 22, 30, 0, 0, time.UTC), Kind: st.KindCharge, DebtorKind: st.Owner, Amount: try(100), Part: st.PartPrincipal}
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, ist)
	s, err := st.Build(money.TRY, []st.Line{l}, st.Range{From: &from}, ist)
	if err != nil || len(s.Rows) != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	fromUTC := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	s, _ = st.Build(money.TRY, []st.Line{l}, st.Range{From: &fromUTC}, time.UTC)
	if len(s.Rows) != 0 || s.Opening.Total.Amount != 100 {
		t.Fatalf("utc view %+v", s)
	}
}

func TestBuildErrors(t *testing.T) {
	ok := line(d(time.January, 1), st.KindCharge, st.Owner, alice, 1, "x")
	mut := func(f func(*st.Line)) []st.Line { l := ok; f(&l); return []st.Line{l} }
	cases := []struct {
		name  string
		cur   money.Code
		lines []st.Line
		r     st.Range
		want  error
	}{
		{"currency", "XXX", nil, st.Range{}, st.ErrNoCurrency},
		{"kind", money.TRY, mut(func(l *st.Line) { l.Kind = "nope" }), st.Range{}, st.ErrBadKind},
		{"debtor", money.TRY, mut(func(l *st.Line) { l.DebtorKind = "" }), st.Range{}, st.ErrBadDebtor},
		{"part", money.TRY, mut(func(l *st.Line) { l.Part = "x" }), st.Range{}, st.ErrBadPart},
		{"missing part", money.TRY, mut(func(l *st.Line) { l.Part = "" }), st.Range{}, st.ErrMissingPart},
		{"charge with fee part", money.TRY, mut(func(l *st.Line) { l.Part = st.PartFee }), st.Range{}, st.ErrPartKind},
		{"fee with principal part", money.TRY, mut(func(l *st.Line) { l.Kind = st.KindFee }), st.Range{}, st.ErrPartKind},
		{"line currency", money.TRY, mut(func(l *st.Line) { l.Amount = money.New(1, money.EUR) }), st.Range{}, money.ErrCurrencyMismatch},
		{"range", money.TRY, nil, st.Range{From: dp(time.March, 1), To: dp(time.January, 1)}, st.ErrBadRange},
	}
	for _, c := range cases {
		if _, err := st.Build(c.cur, c.lines, c.r, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: %v want %v", c.name, err, c.want)
		}
	}
	// overflow
	big := []st.Line{
		line(d(time.January, 1), st.KindCharge, st.Owner, alice, 1<<62, "a"),
		line(d(time.January, 2), st.KindCharge, st.Owner, alice, 1<<62, "b"),
	}
	if _, err := st.Build(money.TRY, big, st.Range{}, nil); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("overflow: %v", err)
	}
}

func TestCheckDetectsTampering(t *testing.T) {
	s, _ := st.Build(money.TRY, sample(), st.Range{}, nil)
	t1 := s
	t1.Closing.Total.Amount++
	t2 := s
	t2.Rows = append([]st.Row(nil), s.Rows...)
	t2.Rows[1].Balance.Total.Amount++
	t3 := s
	t3.Closing.Fee.Amount += 5
	t3.Closing.Principal.Amount -= 5
	t3.Closing.Fee.Amount -= 5 // principal+fee != total
	t4 := s
	t4.Sides = append([]st.SideSummary(nil), s.Sides...)
	t4.Sides[0].Charged.Amount++
	t5 := s
	t5.People = append([]st.PersonSummary(nil), s.People...)
	t5.People[0].Settled.Amount--
	t6 := s
	t6.People = s.People[:1]
	for i, bad := range []st.Statement{t1, t2, t3, t4, t5, t6} {
		if err := bad.Check(); !errors.Is(err, st.ErrNotBalanced) {
			t.Errorf("case %d: %v", i+1, err)
		}
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestPropClosingEqualsOpeningPlusLines(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 60).Draw(t, "n")
		kinds := []st.Kind{st.KindCharge, st.KindPayment, st.KindFee, st.KindWaiver, st.KindRefund, st.KindAdjustment, st.KindOpening}
		people := []uuid.UUID{alice, bob, uuid.Nil}
		var lines []st.Line
		var total, fee int64
		for i := 0; i < n; i++ {
			l := st.Line{
				Date:       d(time.January, 1).AddDate(0, 0, rapid.IntRange(0, 400).Draw(t, "day")),
				Kind:       rapid.SampledFrom(kinds).Draw(t, "kind"),
				DebtorKind: rapid.SampledFrom([]st.DebtorKind{st.Owner, st.Tenant}).Draw(t, "side"),
				PersonID:   rapid.SampledFrom(people).Draw(t, "who"),
				Amount:     try(rapid.Int64Range(-100000, 100000).Draw(t, "amt")),
			}
			l.Part = rapid.SampledFrom([]st.Part{st.PartFee, st.PartPrincipal}).Draw(t, "part")
			switch l.Kind {
			case st.KindFee:
				l.Part = st.PartFee
			case st.KindCharge:
				l.Part = st.PartPrincipal
			}
			lines = append(lines, l)
			total += l.Amount.Amount
			isFee := l.Part == st.PartFee
			if isFee {
				fee += l.Amount.Amount
			}
		}
		s, err := st.Build(money.TRY, lines, st.Range{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if s.Closing.Total.Amount != total || s.Closing.Fee.Amount != fee || s.Closing.Principal.Amount != total-fee {
			t.Fatalf("closing %+v, want total %d fee %d", s.Closing, total, fee)
		}
		// any range: closing - opening == sum of the rows in it
		from := d(time.January, 1).AddDate(0, 0, rapid.IntRange(0, 200).Draw(t, "from"))
		to := from.AddDate(0, 0, rapid.IntRange(0, 200).Draw(t, "len"))
		r, err := st.Build(money.TRY, lines, st.Range{From: &from, To: &to}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var rows int64
		for _, row := range r.Rows {
			rows += row.Line.Amount.Amount
		}
		if r.Closing.Total.Amount-r.Opening.Total.Amount != rows {
			t.Fatalf("range closing-opening != Σ rows")
		}
	})
}
