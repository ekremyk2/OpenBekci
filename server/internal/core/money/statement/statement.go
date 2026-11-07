package statement

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/internal/civil"
)

// Errors returned by Build.
var (
	ErrBadKind     = errors.New("statement: unknown line kind")
	ErrBadDebtor   = errors.New("statement: unknown debtor kind")
	ErrBadPart     = errors.New("statement: unknown part")
	ErrMissingPart = errors.New("statement: line without a part")
	ErrPartKind    = errors.New("statement: part does not match the line kind")
	ErrBadRange    = errors.New("statement: range end is before its start")
	ErrNoCurrency  = errors.New("statement: invalid currency")
	ErrNotBalanced = errors.New("statement: closing balance does not equal opening plus lines")
)

// Kind is the type of a statement line.
type Kind string

// Line kinds.
const (
	KindCharge     Kind = "charge"
	KindPayment    Kind = "payment"
	KindFee        Kind = "fee"
	KindWaiver     Kind = "waiver"
	KindRefund     Kind = "refund"
	KindAdjustment Kind = "adjustment"
	KindOpening    Kind = "opening"
)

// DebtorKind is the side of a unit a line belongs to (decision M1).
type DebtorKind string

// Debtor sides.
const (
	Owner  DebtorKind = "owner"
	Tenant DebtorKind = "tenant"
)

// Part says which component of the debt a line moves.
type Part string

// Parts.
const (
	PartPrincipal Part = "principal"
	PartFee       Part = "fee"
)

// Line is one ledger-derived row. Amount is signed from the debtor's point
// of view: positive increases what is owed (charge, fee, opening debt,
// refund of a credit), negative decreases it (payment, waiver) or is a credit.
// A negative opening line is an opening credit.
type Line struct {
	Date       time.Time
	Kind       Kind
	DebtorKind DebtorKind
	// PersonID is the person the line was assigned to (charge snapshot or
	// payer); uuid.Nil if none.
	PersonID uuid.UUID
	Amount   money.Money
	// Part is the debt component the line moves and is required: a waiver or
	// payment of principal is PartPrincipal, of fee PartFee, so a wrong default
	// can never turn a principal waiver into a negative fee. KindFee lines
	// must be PartFee and KindCharge lines PartPrincipal; the others (payment,
	// waiver, refund, adjustment, opening) carry either, explicitly.
	Part Part
	Ref  string
}

func (l Line) part() Part { return l.Part }

// AccruedFeeLine is the statement line for the late fee accrued up to asOf
// that has not been booked yet (money.md M4: a statement always shows the fee
// accrued so far). amount is the accrual minus what is already booked, as
// returned by latefee.Reconcile (Delta.Amount); the line is dated asOf, has
// kind fee and part fee, and Ref "accrued".
func AccruedFeeLine(asOf time.Time, debtor DebtorKind, amount money.Money) Line {
	return Line{Date: asOf, Kind: KindFee, DebtorKind: debtor, Amount: amount, Part: PartFee, Ref: "accrued"}
}

// Range selects the period shown, as inclusive calendar dates in the zone
// given to Build. A nil bound is open. Lines before From fold into the opening
// balance; lines after To are left out.
type Range struct {
	From *time.Time
	To   *time.Time
}

// Balance is a principal/fee/total triple. Total = Principal + Fee.
type Balance struct {
	Principal money.Money
	Fee       money.Money
	Total     money.Money
}

func (b *Balance) add(part Part, m money.Money) error {
	var err error
	if part == PartFee {
		b.Fee, err = b.Fee.Add(m)
	} else {
		b.Principal, err = b.Principal.Add(m)
	}
	if err != nil {
		return err
	}
	b.Total, err = b.Total.Add(m)
	return err
}

// Row is a line inside the range with the running balance after it.
type Row struct {
	Line    Line
	Balance Balance
}

// SideSummary aggregates one debtor side.
type SideSummary struct {
	DebtorKind DebtorKind
	Opening    Balance
	Closing    Balance
	// Charged is the sum of positive amounts in range, Settled the sum of
	// negative amounts (so Closing.Total = Opening.Total + Charged + Settled).
	Charged money.Money
	Settled money.Money
}

// PersonSummary aggregates one person across sides and units.
type PersonSummary struct {
	PersonID uuid.UUID
	Opening  Balance
	Closing  Balance
	Charged  money.Money
	Settled  money.Money
}

// Statement is the built statement, consumed by the PDF and CSV renderers.
type Statement struct {
	Currency money.Code
	Opening  Balance
	Closing  Balance
	Rows     []Row
	// ByKind sums the in-range amounts per line kind (kinds with no lines are absent).
	ByKind map[Kind]money.Money
	// Sides are ordered owner, tenant (only sides that have lines).
	Sides []SideSummary
	// People are ordered by person id (uuid.Nil first).
	People []PersonSummary
}

// Build produces a statement from lines. Lines are ordered by calendar date
// in tz (opening lines first within a day), otherwise in input order, so the
// same input gives the same statement. Currency is the site currency.
func Build(currency money.Code, lines []Line, r Range, tz *time.Location) (Statement, error) {
	if !currency.Valid() {
		return Statement{}, ErrNoCurrency
	}
	if tz == nil {
		tz = time.UTC
	}
	const unbounded = int(^uint(0) >> 1)
	fromD, toD := -unbounded, unbounded
	if r.From != nil {
		fromD = civil.Day(*r.From, tz)
	}
	if r.To != nil {
		toD = civil.Day(*r.To, tz)
	}
	if toD < fromD {
		return Statement{}, ErrBadRange
	}

	type dl struct {
		Line
		day int
	}
	ls := make([]dl, len(lines))
	for i, l := range lines {
		if err := validate(l, currency); err != nil {
			return Statement{}, fmt.Errorf("line %d: %w", i, err)
		}
		ls[i] = dl{l, civil.Day(l.Date, tz)}
	}
	sort.SliceStable(ls, func(i, j int) bool {
		if ls[i].day != ls[j].day {
			return ls[i].day < ls[j].day
		}
		return ls[i].Kind == KindOpening && ls[j].Kind != KindOpening
	})

	zero := zeroBalance(currency)
	st := Statement{Currency: currency, Opening: zero, Closing: zero, ByKind: map[Kind]money.Money{}}
	sides := map[DebtorKind]*SideSummary{}
	people := map[uuid.UUID]*PersonSummary{}
	side := func(k DebtorKind) *SideSummary {
		if s, ok := sides[k]; ok {
			return s
		}
		s := &SideSummary{DebtorKind: k, Opening: zero, Closing: zero, Charged: money.Zero(currency), Settled: money.Zero(currency)}
		sides[k] = s
		return s
	}
	person := func(id uuid.UUID) *PersonSummary {
		if p, ok := people[id]; ok {
			return p
		}
		p := &PersonSummary{PersonID: id, Opening: zero, Closing: zero, Charged: money.Zero(currency), Settled: money.Zero(currency)}
		people[id] = p
		return p
	}

	running := zero
	for _, l := range ls {
		if l.day > toD {
			continue
		}
		sd, pp := side(l.DebtorKind), person(l.PersonID)
		if err := running.add(l.part(), l.Amount); err != nil {
			return Statement{}, err
		}
		if err := sd.Closing.add(l.part(), l.Amount); err != nil {
			return Statement{}, err
		}
		if err := pp.Closing.add(l.part(), l.Amount); err != nil {
			return Statement{}, err
		}
		if l.day < fromD {
			st.Opening, sd.Opening, pp.Opening = running, sd.Closing, pp.Closing
			continue
		}
		st.Rows = append(st.Rows, Row{Line: l.Line, Balance: running})
		prev := st.ByKind[l.Kind]
		if prev.Currency == "" {
			prev = money.Zero(currency)
		}
		var err error
		if st.ByKind[l.Kind], err = prev.Add(l.Amount); err != nil {
			return Statement{}, err
		}
		for _, agg := range []*money.Money{&sd.Charged, &pp.Charged} {
			if l.Amount.Amount > 0 {
				if *agg, err = agg.Add(l.Amount); err != nil {
					return Statement{}, err
				}
			}
		}
		for _, agg := range []*money.Money{&sd.Settled, &pp.Settled} {
			if l.Amount.Amount < 0 {
				if *agg, err = agg.Add(l.Amount); err != nil {
					return Statement{}, err
				}
			}
		}
	}
	st.Closing = running

	for _, k := range []DebtorKind{Owner, Tenant} {
		if s, ok := sides[k]; ok {
			st.Sides = append(st.Sides, *s)
		}
	}
	ids := make([]uuid.UUID, 0, len(people))
	for id := range people {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i][:]) < string(ids[j][:]) })
	for _, id := range ids {
		st.People = append(st.People, *people[id])
	}
	return st, st.Check()
}

func validate(l Line, cur money.Code) error {
	switch l.Kind {
	case KindCharge, KindPayment, KindFee, KindWaiver, KindRefund, KindAdjustment, KindOpening:
	default:
		return fmt.Errorf("%w: %q", ErrBadKind, string(l.Kind))
	}
	if l.DebtorKind != Owner && l.DebtorKind != Tenant {
		return fmt.Errorf("%w: %q", ErrBadDebtor, string(l.DebtorKind))
	}
	switch {
	case l.Part == "":
		return ErrMissingPart
	case l.Part != PartPrincipal && l.Part != PartFee:
		return fmt.Errorf("%w: %q", ErrBadPart, string(l.Part))
	case l.Kind == KindFee && l.Part != PartFee, l.Kind == KindCharge && l.Part != PartPrincipal:
		return fmt.Errorf("%w: %s line with part %s", ErrPartKind, l.Kind, l.Part)
	}
	if l.Amount.Currency != cur {
		return money.ErrCurrencyMismatch
	}
	return nil
}

func zeroBalance(c money.Code) Balance {
	z := money.Zero(c)
	return Balance{z, z, z}
}

// Check verifies the statement's internal invariants: closing = opening + Σ rows,
// the last row's running balance is the closing balance, principal + fee = total,
// and each side's and person's closing balance reconciles.
func (s Statement) Check() error {
	sum := s.Opening
	for _, r := range s.Rows {
		if err := validate(r.Line, s.Currency); err != nil {
			return fmt.Errorf("row %s: %w", r.Line.Ref, err)
		}
		if err := sum.add(r.Line.part(), r.Line.Amount); err != nil {
			return err
		}
		if sum != r.Balance {
			return fmt.Errorf("%w: running balance diverges at %s", ErrNotBalanced, r.Line.Ref)
		}
	}
	if sum != s.Closing {
		return ErrNotBalanced
	}
	if s.Closing.Principal.Amount+s.Closing.Fee.Amount != s.Closing.Total.Amount {
		return fmt.Errorf("%w: principal + fee != total", ErrNotBalanced)
	}
	var sideTotal, personTotal int64
	for _, sd := range s.Sides {
		sideTotal += sd.Closing.Total.Amount
		if sd.Opening.Total.Amount+sd.Charged.Amount+sd.Settled.Amount != sd.Closing.Total.Amount {
			return fmt.Errorf("%w: side %s", ErrNotBalanced, sd.DebtorKind)
		}
	}
	for _, p := range s.People {
		personTotal += p.Closing.Total.Amount
		if p.Opening.Total.Amount+p.Charged.Amount+p.Settled.Amount != p.Closing.Total.Amount {
			return fmt.Errorf("%w: person %s", ErrNotBalanced, p.PersonID)
		}
	}
	if sideTotal != s.Closing.Total.Amount || personTotal != s.Closing.Total.Amount {
		return fmt.Errorf("%w: sides or people do not sum to the closing balance", ErrNotBalanced)
	}
	return nil
}
