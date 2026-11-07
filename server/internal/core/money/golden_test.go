package money_test

// The golden-site test runs a full year of the money calculators over
// testdata/golden-site/site.json and compares the outcome with the committed
// testdata/golden-site/expected.json (regenerate with `go test -run Golden
// ./internal/core/money/ -update` and review the diff). It also asserts the
// invariants of backend plan section 9.13 that the pure calculators can
// prove: every allocation sums exactly to its total, statements reconcile,
// late-fee totals match their per-charge parts, snapshots are stable.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the golden site runs in Europe/Istanbul on any machine

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/allocate"
	"github.com/openbekci/openbekci/server/internal/core/money/latefee"
	pa "github.com/openbekci/openbekci/server/internal/core/money/paymentalloc"
	"github.com/openbekci/openbekci/server/internal/core/money/rates"
	"github.com/openbekci/openbekci/server/internal/core/money/statement"
)

var update = flag.Bool("update", false, "rewrite testdata/golden-site/expected.json")

const goldenDir = "testdata/golden-site"

// --- site description ---

type siteJSON struct {
	Currency money.Code `json:"currency"`
	Timezone string     `json:"timezone"`
	Year     int        `json:"year"`
	ShareDen int64      `json:"share_denominator"`
	LateFee  struct {
		RateBPS   int64 `json:"rate_bps"`
		GraceDays int   `json:"grace_days"`
	} `json:"late_fee"`
	Units []struct {
		Door        string   `json:"door"`
		Block       string   `json:"block"`
		Area        string   `json:"area_m2"`
		ShareNum    int64    `json:"share_num"`
		ExemptPlans []string `json:"exempt_plans"`
	} `json:"units"`
	Occupancies []struct {
		Unit          string  `json:"unit"`
		Kind          string  `json:"kind"`
		Person        string  `json:"person"`
		Correspondent bool    `json:"correspondent"`
		SharePct      int64   `json:"share_pct"`
		From          string  `json:"from"`
		To            *string `json:"to"`
	} `json:"occupancies"`
	Plans []planJSON `json:"plans"`
	// LumpPayments are one-off payments computed on the day: Covers "principal"
	// pays the principal due plus Extra (which spills into the fee), "all" pays
	// principal due and the outstanding fee plus Extra (which becomes credit).
	LumpPayments []struct {
		Unit   string `json:"unit"`
		Side   string `json:"side"`
		On     string `json:"on"`
		Covers string `json:"covers"`
		Extra  int64  `json:"extra"`
	} `json:"lump_payments"`
	// Tables hold per-unit numbers by door: consumption quantities or amounts.
	Tables  map[string]map[string]int64 `json:"tables"`
	Opening []struct {
		Unit      string `json:"unit"`
		Side      string `json:"side"`
		Principal int64  `json:"principal"`
		Fee       int64  `json:"fee"`
	} `json:"opening"`
	Behaviours []struct {
		Unit          string `json:"unit"`
		Mode          string `json:"mode"`
		DelayDays     int    `json:"delay_days"`
		FractionPct   int64  `json:"fraction_pct"`
		RecordLagDays int    `json:"record_lag_days"`
	} `json:"payment_behaviours"`
	Waivers []struct {
		Unit   string `json:"unit"`
		Side   string `json:"side"`
		On     string `json:"on"`
		Amount int64  `json:"amount"`
	} `json:"waivers"`
	Freezes []struct {
		Unit string  `json:"unit"`
		Side string  `json:"side"`
		From string  `json:"from"`
		To   *string `json:"to"`
	} `json:"freezes"`
}

type planJSON struct {
	Name          string  `json:"name"`
	DefaultDebtor string  `json:"default_debtor"`
	Frequency     string  `json:"frequency"`
	DueDay        int     `json:"due_day"`
	Start         string  `json:"start"`
	End           *string `json:"end"`
	Scope         string  `json:"scope"`
	Key           keyJSON `json:"key"`
	Rates         []struct {
		From     string  `json:"from"`
		To       *string `json:"to"`
		Amount   int64   `json:"amount"`
		Decision string  `json:"decision"`
	} `json:"rates"`
}

type keyJSON struct {
	Kind  string `json:"kind"`
	Table string `json:"table"`
	Parts []struct {
		Fraction string  `json:"fraction"`
		Key      keyJSON `json:"key"`
	} `json:"parts"`
}

var ns = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

func handle(kind, name string) uuid.UUID { return uuid.NewSHA1(ns, []byte(kind+"/"+name)) }

// --- simulation state ---

type chg struct {
	id     uuid.UUID
	plan   string
	period string
	due    time.Time
	amount int64
	paid   []latefee.Payment
}

func (c *chg) remaining() int64 {
	r := c.amount
	for _, p := range c.paid {
		r -= p.Amount.Amount
	}
	return r
}

type side struct {
	unit, kind  string
	charges     []*chg
	bookings    []latefee.Booking
	waivePosted int64 // waiver already shown on the statement
	freezes     []latefee.Freeze
	feePaid     int64
	credit      int64
	lines       []statement.Line
	monthly     [12]int64
}

func (s *side) feeOutstanding() int64 {
	var b int64
	for _, bk := range s.bookings {
		if bk.Kind != latefee.BookingWaiver {
			b += bk.Amount.Amount
		}
	}
	if o := b - s.feePaid; o > 0 {
		return o
	}
	return 0
}

type payEvent struct {
	st      *side
	person  uuid.UUID
	on, rec time.Time
	recDay  int
	amount  int64
	ref     string
}

type chargeSetOut struct {
	Scope       string  `json:"scope"`
	Key         string  `json:"key"`
	Total       int64   `json:"total"`
	Amounts     []int64 `json:"amounts_by_door_order"`
	BasisSHA256 string  `json:"basis_sha256"`
}

type sideOut struct {
	ChargedPrincipal int64     `json:"charged_principal"`
	PaidPrincipal    int64     `json:"paid_principal"`
	OpenPrincipal    int64     `json:"open_principal"`
	FeeAccrued       int64     `json:"fee_accrued_to_year_end"`
	FeeWaived        int64     `json:"fee_waived"`
	FeeBooked        int64     `json:"fee_booked"`
	FeePaid          int64     `json:"fee_paid"`
	Credit           int64     `json:"credit"`
	ClosingPrincipal int64     `json:"closing_principal"`
	ClosingFee       int64     `json:"closing_fee"`
	ClosingTotal     int64     `json:"closing_total"`
	StatementRows    int       `json:"statement_rows"`
	MonthlyFeeDelta  [12]int64 `json:"monthly_fee_delta"`
}

type expectedOut struct {
	Charges       map[string]chargeSetOut `json:"charges"`
	Sides         map[string]sideOut      `json:"sides"`
	PeopleClosing map[string]int64        `json:"people_closing_total"`
	Totals        map[string]int64        `json:"totals"`
}

type site struct {
	j      siteJSON
	tz     *time.Location
	policy latefee.Policy
	sched  latefee.PolicySchedule
	doors  []string
	ids    map[string]uuid.UUID
	byID   map[uuid.UUID]string
	order  allocate.OrderFn
}

func (s *site) date(v string) time.Time {
	d, err := time.ParseInLocation("2006-01-02", v, s.tz)
	if err != nil {
		panic(err)
	}
	return d
}

func loadSite(t *testing.T) *site {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenDir, "site.json"))
	if err != nil {
		t.Fatal(err)
	}
	var j siteJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	tz, err := time.LoadLocation(j.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	s := &site{j: j, tz: tz, ids: map[string]uuid.UUID{}, byID: map[uuid.UUID]string{}}
	s.policy = latefee.Policy{Currency: j.Currency, RateBPSPerMonth: j.LateFee.RateBPS, GraceDays: j.LateFee.GraceDays, Simple: true}
	s.sched = latefee.Constant(s.policy, time.Date(2000, 1, 1, 0, 0, 0, 0, tz))
	if err := s.sched.Validate(tz); err != nil {
		t.Fatal(err)
	}
	keys := map[uuid.UUID]string{}
	for _, u := range j.Units {
		id := handle("unit", u.Door)
		s.ids[u.Door], s.byID[id], keys[id] = id, u.Door, u.Door
		s.doors = append(s.doors, u.Door)
	}
	s.order = allocate.ByKeys(keys)
	return s
}

// calendar day number (days since 1970-01-01 of the local date)
func dayNum(t time.Time, tz *time.Location) int {
	y, m, d := t.In(tz).Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func (s *site) occupant(unit, kind string, at time.Time) uuid.UUID {
	d := dayNum(at, s.tz)
	for _, o := range s.j.Occupancies {
		if o.Unit != unit || o.Kind != kind || !o.Correspondent {
			continue
		}
		if dayNum(s.date(o.From), s.tz) <= d && (o.To == nil || d <= dayNum(s.date(*o.To), s.tz)) {
			return handle("person", o.Person)
		}
	}
	return uuid.Nil
}

type coOwner struct {
	person uuid.UUID
	pct    int64
}

// owners lists the owners of a unit on a date with their share_pct (B-2 is
// co-owned 50/50; its owner-side charges are split between them).
func (s *site) owners(unit string, at time.Time) []coOwner {
	d := dayNum(at, s.tz)
	var out []coOwner
	for _, o := range s.j.Occupancies {
		if o.Unit != unit || o.Kind != "owner" {
			continue
		}
		if dayNum(s.date(o.From), s.tz) <= d && (o.To == nil || d <= dayNum(s.date(*o.To), s.tz)) {
			out = append(out, coOwner{handle("person", o.Person), o.SharePct})
		}
	}
	return out
}

func (s *site) rat(v string) *big.Rat {
	r, ok := new(big.Rat).SetString(v)
	if !ok {
		panic("bad rational " + v)
	}
	return r
}

func (s *site) buildKey(k keyJSON) allocate.Key {
	switch k.Kind {
	case "equal":
		return allocate.Equal()
	case "area", "share":
		vals := map[uuid.UUID]*big.Rat{}
		for _, u := range s.j.Units {
			if k.Kind == "area" {
				vals[s.ids[u.Door]] = s.rat(u.Area)
			} else {
				vals[s.ids[u.Door]] = big.NewRat(u.ShareNum, s.j.ShareDen)
			}
		}
		if k.Kind == "area" {
			return allocate.Area(vals)
		}
		return allocate.LandShare(vals)
	case "consumption":
		vals := map[uuid.UUID]*big.Rat{}
		for door, v := range s.j.Tables[k.Table] {
			vals[s.ids[door]] = big.NewRat(v, 1)
		}
		return allocate.Consumption(vals)
	case "table":
		amts := map[uuid.UUID]int64{}
		for door, v := range s.j.Tables[k.Table] {
			amts[s.ids[door]] = v
		}
		return allocate.Table(amts)
	case "composite":
		var parts []allocate.Part
		for _, p := range k.Parts {
			parts = append(parts, allocate.Part{Fraction: s.rat(p.Fraction), Key: s.buildKey(p.Key)})
		}
		return allocate.Composite(parts...)
	}
	panic("unknown key " + k.Kind)
}

type planPeriod struct {
	plan   planJSON
	period rates.Period
	res    allocate.Result
	total  money.Money
}

func (s *site) generate(t *testing.T) ([]*planPeriod, map[string]chargeSetOut) {
	t.Helper()
	yStart, yEnd := time.Date(s.j.Year, 1, 1, 0, 0, 0, 0, s.tz), time.Date(s.j.Year, 12, 31, 0, 0, 0, 0, s.tz)
	var out []*planPeriod
	sets := map[string]chargeSetOut{}
	for _, p := range s.j.Plans {
		var sched rates.Schedule
		for _, r := range p.Rates {
			rr := rates.Rate{From: s.date(r.From), Amount: money.New(r.Amount, s.j.Currency)}
			if r.To != nil {
				to := s.date(*r.To)
				rr.To = &to
			}
			if r.Decision != "" {
				id := handle("decision", r.Decision)
				rr.DecisionID = &id
			}
			sched.Rates = append(sched.Rates, rr)
		}
		if err := sched.Validate(s.tz, false); err != nil {
			t.Fatalf("plan %s: %v", p.Name, err)
		}
		plan := rates.Plan{Frequency: rates.Frequency(p.Frequency), Start: s.date(p.Start), DueDay: p.DueDay}
		if p.End != nil {
			e := s.date(*p.End)
			plan.End = &e
		}
		periods, err := rates.Periods(plan, yStart, yEnd, s.tz)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := rates.Periods(plan, yStart, yEnd, s.tz)
		if fmt.Sprint(periods) != fmt.Sprint(again) {
			t.Fatalf("plan %s: periods not deterministic", p.Name)
		}
		var units []allocate.Unit
		for _, u := range s.j.Units {
			if p.Scope != "site" && p.Scope != "block:"+u.Block {
				continue
			}
			ex := false
			for _, e := range u.ExemptPlans {
				ex = ex || e == p.Name
			}
			units = append(units, allocate.Unit{ID: s.ids[u.Door], Exempt: ex})
		}
		key := s.buildKey(p.Key)
		for _, per := range periods {
			rate, ok := rates.ForPeriod(sched, per, s.tz)
			if !ok {
				t.Fatalf("plan %s: no rate on %v", p.Name, per.DueDate)
			}
			res, err := allocate.ByKey(rate.Amount, p.Scope, units, key, s.order, allocate.WithRotation(p.Name+"/"+per.Key))
			if err != nil {
				t.Fatalf("plan %s %s: %v", p.Name, per.Key, err)
			}
			// invariant: every allocation sums exactly to its total
			var sum int64
			amounts := make([]int64, len(res.Shares))
			for i, sh := range res.Shares {
				sum += sh.Amount.Amount
				amounts[i] = sh.Amount.Amount
			}
			if sum != rate.Amount.Amount {
				t.Fatalf("plan %s %s: shares sum %d != total %d", p.Name, per.Key, sum, rate.Amount.Amount)
			}
			// invariant: the basis snapshot is stable, also for a different input order
			b1, _ := json.Marshal(res)
			rev := make([]allocate.Unit, len(units))
			for i := range units {
				rev[len(units)-1-i] = units[i]
			}
			res2, err := allocate.ByKey(rate.Amount, p.Scope, rev, key, s.order, allocate.WithRotation(p.Name+"/"+per.Key))
			if err != nil {
				t.Fatal(err)
			}
			if b2, _ := json.Marshal(res2); !bytes.Equal(b1, b2) {
				t.Fatalf("plan %s %s: allocation depends on input order", p.Name, per.Key)
			}
			bb, _ := json.Marshal(res.Basis)
			h := sha256.Sum256(bb)
			sets[p.Name+"/"+per.Key] = chargeSetOut{Scope: p.Scope, Key: string(res.Basis.Key), Total: rate.Amount.Amount, Amounts: amounts, BasisSHA256: hex.EncodeToString(h[:8])}
			out = append(out, &planPeriod{plan: p, period: per, res: res, total: rate.Amount})
		}
	}
	return out, sets
}

func TestGoldenSite(t *testing.T) {
	s := loadSite(t)
	cur := s.j.Currency
	pps, chargeSets := s.generate(t)

	sides := map[string]*side{}
	getSide := func(unit, kind string) *side {
		k := unit + "/" + kind
		if sides[k] == nil {
			sides[k] = &side{unit: unit, kind: kind}
		}
		return sides[k]
	}
	for _, f := range s.j.Freezes {
		fr := latefee.Freeze{From: s.date(f.From)}
		if f.To != nil {
			to := s.date(*f.To)
			fr.To = &to
		}
		st := getSide(f.Unit, f.Side)
		st.freezes = append(st.freezes, fr)
	}

	byDue := map[int][]*planPeriod{}
	for _, pp := range pps {
		byDue[dayNum(pp.period.DueDate, s.tz)] = append(byDue[dayNum(pp.period.DueDate, s.tz)], pp)
	}
	waiversByDay := map[int][]int{}
	for i, w := range s.j.Waivers {
		waiversByDay[dayNum(s.date(w.On), s.tz)] = append(waiversByDay[dayNum(s.date(w.On), s.tz)], i)
	}
	events := map[int][]*payEvent{}
	behaviour := map[string]int{}
	for i, b := range s.j.Behaviours {
		behaviour[b.Unit] = i
	}

	line := func(st *side, at time.Time, k statement.Kind, who uuid.UUID, amt int64, part statement.Part, ref string) {
		kind := statement.DebtorKind(st.kind)
		st.lines = append(st.lines, statement.Line{Date: at, Kind: k, DebtorKind: kind, PersonID: who, Amount: money.New(amt, cur), Part: part, Ref: ref})
	}
	feeID := func(st *side) uuid.UUID { return handle("fee", st.unit+"/"+st.kind) }
	personOf := map[*side]uuid.UUID{}
	var openingTotal int64

	// opening balances (go-live on 1 January)
	jan1 := time.Date(s.j.Year, 1, 1, 0, 0, 0, 0, s.tz)
	for _, o := range s.j.Opening {
		st := getSide(o.Unit, o.Side)
		who := s.occupant(o.Unit, o.Side, jan1)
		personOf[st] = who
		st.charges = append(st.charges, &chg{id: handle("charge", "opening/"+o.Unit+"/"+o.Side), plan: "opening", period: "2025", due: time.Date(s.j.Year-1, 12, 31, 0, 0, 0, 0, s.tz), amount: o.Principal})
		line(st, jan1, statement.KindOpening, who, o.Principal, statement.PartPrincipal, "opening principal")
		openingTotal += o.Principal
		if o.Fee > 0 {
			st.bookings = append(st.bookings, latefee.Booking{Period: "opening", Kind: latefee.BookingFee, Amount: money.New(o.Fee, cur)})
			line(st, jan1, statement.KindOpening, who, o.Fee, statement.PartFee, "opening fee")
			openingTotal += o.Fee
		}
	}

	createCharges := func(pp *planPeriod, at time.Time) {
		for _, sh := range pp.res.Shares {
			if sh.Amount.Amount == 0 {
				continue
			}
			door := s.byID[sh.UnitID]
			kind := pp.plan.DefaultDebtor
			who := s.occupant(door, kind, at)
			if who == uuid.Nil && kind == "tenant" {
				kind, who = "owner", s.occupant(door, "owner", at)
			}
			if who == uuid.Nil {
				t.Fatalf("no %s for %s on %v", kind, door, at)
			}
			st := getSide(door, kind)
			personOf[st] = who
			c := &chg{id: handle("charge", pp.plan.Name+"/"+pp.period.Key+"/"+door), plan: pp.plan.Name, period: pp.period.Key, due: at, amount: sh.Amount.Amount}
			st.charges = append(st.charges, c)
			if os := s.owners(door, at); kind == "owner" && len(os) > 1 {
				// co-owned unit: the owner side owes the whole charge jointly, the
				// statement shows each co-owner's share (B-2, 50/50)
				ws := make([]allocate.Weight, len(os))
				for i, o := range os {
					ws[i] = allocate.IntWeight(o.person, o.pct)
				}
				split, err := allocate.Allocate(money.New(c.amount, cur), ws, nil, allocate.WithRotation(c.plan+"/"+c.period))
				if err != nil {
					t.Fatal(err)
				}
				var shown int64
				for _, sh := range split.Shares {
					shown += sh.Amount.Amount
					line(st, at, statement.KindCharge, sh.UnitID, sh.Amount.Amount, statement.PartPrincipal, c.plan+"/"+c.period+" (share)")
				}
				if shown != c.amount {
					t.Fatalf("co-owner shares %d != charge %d", shown, c.amount)
				}
			} else {
				line(st, at, statement.KindCharge, who, c.amount, statement.PartPrincipal, c.plan+"/"+c.period)
			}
			b := s.j.Behaviours[behaviour[door]]
			if b.Mode != "pay" {
				continue
			}
			on := at.AddDate(0, 0, b.DelayDays)
			rec := on.AddDate(0, 0, b.RecordLagDays)
			if rec.Year() != s.j.Year {
				continue
			}
			ev := &payEvent{st: st, person: who, on: on, rec: rec, recDay: dayNum(rec, s.tz), amount: c.amount * b.FractionPct / 100, ref: "pay/" + c.plan + "/" + c.period}
			events[ev.recDay] = append(events[ev.recDay], ev)
		}
	}

	sortedSides := func() []*side {
		keys := make([]string, 0, len(sides))
		for k := range sides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]*side, len(keys))
		for i, k := range keys {
			out[i] = sides[k]
		}
		return out
	}
	openCharges := func(st *side) []latefee.OpenCharge {
		oc := make([]latefee.OpenCharge, len(st.charges))
		for i, c := range st.charges {
			oc[i] = latefee.OpenCharge{ID: c.id, DueDate: c.due, Principal: money.New(c.amount, cur), PaidPrincipal: c.paid}
		}
		return oc
	}
	accrue := func(st *side, asOf time.Time) latefee.Accrual {
		a, err := latefee.Accrue(s.sched, openCharges(st), asOf, s.tz, st.freezes...)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	// the year, day by day
	for d := 0; ; d++ {
		today := time.Date(s.j.Year, 1, 1+d, 0, 0, 0, 0, s.tz)
		if today.Year() != s.j.Year {
			break
		}
		dn := dayNum(today, s.tz)
		for _, pp := range byDue[dn] {
			createCharges(pp, today)
		}
		evs := append([]*payEvent(nil), events[dn]...)
		for _, lp := range s.j.LumpPayments {
			if dayNum(s.date(lp.On), s.tz) != dn {
				continue
			}
			st := getSide(lp.Unit, lp.Side)
			var due int64
			for _, c := range st.charges {
				if !c.due.After(today) {
					due += c.remaining()
				}
			}
			amount := due + lp.Extra
			if lp.Covers == "all" {
				amount += st.feeOutstanding()
			}
			evs = append(evs, &payEvent{st: st, person: personOf[st], on: today, rec: today, recDay: dn, amount: amount, ref: "lump"})
		}
		sort.SliceStable(evs, func(i, j int) bool {
			if evs[i].st.unit != evs[j].st.unit {
				return evs[i].st.unit < evs[j].st.unit
			}
			return evs[i].ref < evs[j].ref
		})
		for _, ev := range evs {
			st := ev.st
			var items []pa.OpenItem
			byID := map[uuid.UUID]*chg{}
			for _, c := range st.charges {
				if c.remaining() > 0 && !c.due.After(today) {
					byID[c.id] = c
					items = append(items, pa.OpenItem{ChargeID: c.id, DueDate: c.due, Kind: pa.Principal, Remaining: money.New(c.remaining(), cur)})
				}
			}
			if fo := st.feeOutstanding(); fo > 0 {
				items = append(items, pa.OpenItem{ChargeID: feeID(st), DueDate: jan1, Kind: pa.Fee, Remaining: money.New(fo, cur)})
			}
			res, err := pa.Allocate(money.New(ev.amount, cur), items, pa.OldestFirstPrincipalBeforeFee, s.tz)
			if err != nil {
				t.Fatal(err)
			}
			var allocated int64
			for _, a := range res.Allocations {
				allocated += a.Amount.Amount
				if a.Kind == pa.Fee {
					st.feePaid += a.Amount.Amount
					line(st, today, statement.KindPayment, ev.person, -a.Amount.Amount, statement.PartFee, ev.ref)
					continue
				}
				c := byID[a.ChargeID]
				c.paid = append(c.paid, latefee.Payment{On: ev.on, Amount: a.Amount})
				line(st, today, statement.KindPayment, ev.person, -a.Amount.Amount, statement.PartPrincipal, ev.ref)
			}
			if allocated+res.Credit.Amount != ev.amount {
				t.Fatalf("payment allocation does not add up: %d + %d != %d", allocated, res.Credit.Amount, ev.amount)
			}
			if res.Credit.Amount > 0 {
				st.credit += res.Credit.Amount
				line(st, today, statement.KindPayment, ev.person, -res.Credit.Amount, statement.PartPrincipal, ev.ref+" (credit)")
			}
		}
		for _, wi := range waiversByDay[dn] {
			w := s.j.Waivers[wi]
			st := getSide(w.Unit, w.Side)
			// single entry: the waiver is recorded once; the month-end job derives its effect
			st.bookings = append(st.bookings, latefee.Booking{Period: "waiver-" + w.On, Kind: latefee.BookingWaiver, Amount: money.New(w.Amount, cur)})
		}
		if next := today.AddDate(0, 0, 1); next.Month() != today.Month() { // month end: crystallize
			for _, st := range sortedSides() {
				if len(st.charges) == 0 {
					continue
				}
				acc := accrue(st, today)
				exp, err := latefee.Expected(acc, st.bookings)
				if err != nil {
					t.Fatal(err)
				}
				dl, err := latefee.Reconcile(exp.Fee, st.bookings)
				if err != nil {
					t.Fatal(err)
				}
				if dl.Action == latefee.ActionNone {
					continue
				}
				period := today.Format("2006-01")
				kind := latefee.BookingFee
				if dl.Amount.Amount < 0 {
					kind = latefee.BookingReversal
				}
				st.bookings = append(st.bookings, latefee.Booking{Period: period, Kind: kind, Amount: dl.Amount})
				st.monthly[today.Month()-1] = dl.Amount.Amount
				if amt := dl.Amount.Amount; amt > 0 {
					line(st, today, statement.KindFee, personOf[st], amt, statement.PartFee, "fee "+period)
					continue
				}
				// a reversal: the part that comes from a newly effective waiver is shown
				// as a waiver line, the rest (a backdated payment) as a fee reversal
				back := -dl.Amount.Amount
				w := exp.Waived.Amount - st.waivePosted
				if w > back {
					w = back
				}
				if w > 0 {
					st.waivePosted += w
					line(st, today, statement.KindWaiver, personOf[st], -w, statement.PartFee, "waiver "+period)
				}
				if back-w > 0 {
					line(st, today, statement.KindFee, personOf[st], -(back - w), statement.PartFee, "fee reversal "+period)
				}
			}
		}
	}

	// --- invariants and expected output ---
	yEnd := time.Date(s.j.Year, 12, 31, 0, 0, 0, 0, s.tz)
	out := expectedOut{Charges: chargeSets, Sides: map[string]sideOut{}, PeopleClosing: map[string]int64{}, Totals: map[string]int64{}}
	var allLines []statement.Line
	var sumClosing, sumRemaining, sumFeeBooked, sumFeePaid, negDeltas int64
	for _, st := range sortedSides() {
		so := sideOut{StatementRows: len(st.lines), MonthlyFeeDelta: st.monthly, Credit: st.credit, FeePaid: st.feePaid}
		for _, c := range st.charges {
			so.ChargedPrincipal += c.amount
			so.OpenPrincipal += c.remaining()
		}
		so.PaidPrincipal = so.ChargedPrincipal - so.OpenPrincipal
		for _, bk := range st.bookings {
			if bk.Kind != latefee.BookingWaiver {
				so.FeeBooked += bk.Amount.Amount
			}
		}
		for _, m := range st.monthly {
			if m < 0 {
				negDeltas++
			}
		}

		// late-fee totals match the per-charge sums, and the books match what is expected
		acc := accrue(st, yEnd)
		var perCharge int64
		for _, pc := range acc.PerCharge {
			perCharge += pc.Fee.Amount
		}
		if perCharge != acc.Total.Amount {
			t.Fatalf("%s/%s: accrual total %d != per-charge sum %d", st.unit, st.kind, acc.Total.Amount, perCharge)
		}
		exp, _ := latefee.Expected(acc, st.bookings)
		if dl, err := latefee.Reconcile(exp.Fee, st.bookings); err != nil || dl.Action != latefee.ActionNone {
			t.Fatalf("%s/%s: year-end books do not match expected: %+v %v", st.unit, st.kind, dl, err)
		}
		so.FeeWaived = exp.Waived.Amount
		if st.waivePosted > exp.Waived.Amount {
			t.Fatalf("%s/%s: waiver shown on the statement %d exceeds applied %d", st.unit, st.kind, st.waivePosted, exp.Waived.Amount)
		}
		so.FeeAccrued = acc.Total.Amount

		// statement closing balance == sum of lines, split and sides reconcile
		stm, err := statement.Build(cur, st.lines, statement.Range{}, s.tz)
		if err != nil {
			t.Fatalf("%s/%s: %v", st.unit, st.kind, err)
		}
		var lineSum int64
		for _, l := range st.lines {
			lineSum += l.Amount.Amount
		}
		if stm.Closing.Total.Amount != lineSum {
			t.Fatalf("%s/%s: closing %d != sum of lines %d", st.unit, st.kind, stm.Closing.Total.Amount, lineSum)
		}
		if stm.Closing.Principal.Amount != so.OpenPrincipal-st.credit {
			t.Fatalf("%s/%s: closing principal %d != open principal %d - credit %d", st.unit, st.kind, stm.Closing.Principal.Amount, so.OpenPrincipal, st.credit)
		}
		if stm.Closing.Fee.Amount != so.FeeBooked-st.feePaid {
			t.Fatalf("%s/%s: closing fee %d != booked %d - paid %d", st.unit, st.kind, stm.Closing.Fee.Amount, so.FeeBooked, st.feePaid)
		}
		so.ClosingPrincipal, so.ClosingFee, so.ClosingTotal = stm.Closing.Principal.Amount, stm.Closing.Fee.Amount, stm.Closing.Total.Amount
		out.Sides[st.unit+"/"+st.kind] = so
		allLines = append(allLines, st.lines...)
		sumClosing += lineSum
		sumRemaining += so.OpenPrincipal - st.credit
		sumFeeBooked += so.FeeBooked
		sumFeePaid += st.feePaid
	}

	// per-person aggregation covers everything exactly once
	all, err := statement.Build(cur, allLines, statement.Range{}, s.tz)
	if err != nil {
		t.Fatal(err)
	}
	var peopleSum int64
	for _, p := range all.People {
		peopleSum += p.Closing.Total.Amount
		out.PeopleClosing[p.PersonID.String()] = p.Closing.Total.Amount
	}
	if peopleSum != sumClosing || all.Closing.Total.Amount != sumClosing {
		t.Fatalf("people %d / site %d != sum of side closings %d", peopleSum, all.Closing.Total.Amount, sumClosing)
	}

	// scenario assertions: waiver, freeze, tenant change, owner sale, co-owned unit, shop
	waived := out.Sides["A-6/owner"]
	if waived.FeeWaived != 50000 || waived.FeeBooked != waived.FeeAccrued-50000 {
		t.Errorf("waiver not reflected: %+v", waived)
	}
	frozen := sides["A-10/owner"]
	if a, b := accrue(frozen, time.Date(s.j.Year, 3, 1, 0, 0, 0, 0, s.tz)), accrue(frozen, yEnd); a.Total != b.Total {
		t.Errorf("frozen unit kept accruing: %v -> %v", a.Total, b.Total)
	}
	if sides["A-5/tenant"] == nil || len(distinctPersons(sides["A-5/tenant"].lines)) != 2 {
		t.Errorf("tenant change should give the tenant side two debtors")
	}
	if got := distinctPersons(sides["B-5/owner"].lines); len(got) != 2 {
		t.Errorf("owner sale should give the owner side two persons, got %d", len(got))
	}
	// co-owned unit (B-2, 50/50): the statement shows each co-owner's share of
	// every owner-side charge; shares sum to the charge, the side owes it jointly
	b2 := sides["B-2/owner"]
	perOwner := map[uuid.UUID]int64{}
	var b2Charged, b2Lines int64
	for _, l := range b2.lines {
		if l.Kind == statement.KindCharge {
			perOwner[l.PersonID] += l.Amount.Amount
			b2Lines++
		}
	}
	for _, c := range b2.charges {
		b2Charged += c.amount
	}
	oa, ob := perOwner[handle("person", "owner-B-2-a")], perOwner[handle("person", "owner-B-2-b")]
	if len(perOwner) != 2 || oa+ob != b2Charged || oa == 0 || ob == 0 {
		t.Errorf("co-owners not both charged: %v (side charged %d)", perOwner, b2Charged)
	}
	if d := oa - ob; d > int64(len(b2.charges)) || -d > int64(len(b2.charges)) {
		t.Errorf("co-owner split is not 50/50: %d vs %d", oa, ob)
	}
	// owner sale (B-5 on 15 Jul): the seller's debt stays on the unit's owner side;
	// moving it to a personal receivable (backend plan section 9 rule 5) is not
	// covered by these pure calculators.
	// fee payments and overpayment credit run through the simulation
	if out.Sides["A-8/tenant"].FeePaid == 0 {
		t.Errorf("A-8 lump payment should have paid part of the fee")
	}
	if out.Sides["B-6/owner"].Credit != 20000 {
		t.Errorf("B-6 overpayment credit = %d, want 20000", out.Sides["B-6/owner"].Credit)
	}
	// rate change inside a period: staff-A rises on 10 July, after the July due date (5th)
	staffJul, staffAug := chargeSets["staff-A/2026-07"], chargeSets["staff-A/2026-08"]
	if staffJul.Total != 800000 || staffAug.Total != 850000 {
		t.Errorf("staff-A rate by due date: July %d (want 800000), August %d (want 850000)", staffJul.Total, staffAug.Total)
	}
	// plan starting mid-quarter (15 Feb): Q1 is billed, due 15 Feb, never before the start
	if g, ok := chargeSets["garden/2026-Q1"]; !ok || g.Total != 480000 {
		t.Errorf("garden Q1 missing or wrong: %+v", g)
	}
	// hand-verified anchors, committed as literals so -update cannot bless a regression
	var aidat int64
	for k, c := range chargeSets {
		if strings.HasPrefix(k, "aidat/") {
			aidat += c.Total
		}
	}
	if aidat != 45_360_000 { // 6 x 3,600,000 + 6 x 3,960,000
		t.Errorf("aidat total %d, want 45360000", aidat)
	}
	// A-10 owner, January fee, by hand: 21 late days (due 5 Jan, 5 grace, to 30 Jan) at 5 %/31
	// per day: aidat 151,200 -> 5121.29 = 5121; staff 66,666 -> 2258.04 = 2258;
	// elevator 50,526 over 11 days -> 896.43 = 896; heating 106,051 over 1 day -> 171.05 = 171
	if got := out.Sides["A-10/owner"].MonthlyFeeDelta[0]; got != 5121+2258+896+171 {
		t.Errorf("A-10 January fee delta %d, want %d (8446)", got, 5121+2258+896+171)
	}
	for _, st := range sides {
		if st.unit == "A-1" {
			for _, c := range st.charges {
				if c.plan == "elevator" {
					t.Errorf("the shop must be exempt from the elevator plan")
				}
			}
		}
	}
	out.Totals = map[string]int64{
		"opening_balances":             openingTotal,
		"closing_total_all_sides":      sumClosing,
		"open_principal_net_of_credit": sumRemaining,
		"fee_booked":                   sumFeeBooked,
		"fee_paid":                     sumFeePaid,
		"months_with_fee_reversal":     negDeltas,
	}
	if sumClosing != sumRemaining+sumFeeBooked-sumFeePaid {
		t.Errorf("closing %d != principal %d + fee %d - paid %d", sumClosing, sumRemaining, sumFeeBooked, sumFeePaid)
	}

	compareExpected(t, out)
}

func distinctPersons(ls []statement.Line) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, l := range ls {
		if l.PersonID != uuid.Nil {
			m[l.PersonID] = true
		}
	}
	return m
}

func compareExpected(t *testing.T, out expectedOut) {
	t.Helper()
	got, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join(goldenDir, "expected.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (generate it with -update)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
		gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("golden mismatch at line %d:\n got: %s\nwant: %s\n(run with -update after reviewing the change)", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("golden mismatch: length differs (got %d lines, want %d)", len(gl), len(wl))
	}
}
