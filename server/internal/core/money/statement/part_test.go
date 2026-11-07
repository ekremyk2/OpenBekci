package statement_test

import (
	"errors"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/money"
	st "github.com/openbekci/openbekci/server/internal/core/money/statement"
)

// F8: a waiver of principal states its part, so it can never silently become a
// negative fee line; a tampered statement fails Check.
func TestPrincipalWaiverKeepsFeeNonNegative(t *testing.T) {
	lines := []st.Line{
		line(d(time.January, 1), st.KindCharge, st.Owner, alice, 100, "charge"),
		{Date: d(time.January, 2), Kind: st.KindWaiver, DebtorKind: st.Owner, PersonID: alice, Amount: try(-100), Part: st.PartPrincipal, Ref: "w"},
	}
	s, err := st.Build(money.TRY, lines, st.Range{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Closing.Principal.Amount != 0 || s.Closing.Fee.Amount != 0 || s.Closing.Total.Amount != 0 {
		t.Fatalf("closing %+v", s.Closing)
	}
	// the same waiver with no part is refused
	lines[1].Part = ""
	if _, err := st.Build(money.TRY, lines, st.Range{}, nil); !errors.Is(err, st.ErrMissingPart) {
		t.Fatalf("%v", err)
	}
}

func TestCheckRejectsMismatchedPart(t *testing.T) {
	lines := []st.Line{
		line(d(time.January, 1), st.KindCharge, st.Owner, alice, 100, "charge"),
		line(d(time.January, 2), st.KindFee, st.Owner, alice, 5, "fee"),
	}
	s, err := st.Build(money.TRY, lines, st.Range{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	s.Rows[1].Line.Part = st.PartPrincipal // a fee line moved to principal
	if err := s.Check(); !errors.Is(err, st.ErrPartKind) {
		t.Fatalf("%v", err)
	}
	s.Rows[1].Line.Part = ""
	if err := s.Check(); !errors.Is(err, st.ErrMissingPart) {
		t.Fatalf("%v", err)
	}
}

func TestAccruedFeeLine(t *testing.T) {
	asOf := d(time.March, 15)
	l := st.AccruedFeeLine(asOf, st.Tenant, try(1234))
	if l.Kind != st.KindFee || l.Part != st.PartFee || l.DebtorKind != st.Tenant || l.Amount.Amount != 1234 || !l.Date.Equal(asOf) || l.Ref != "accrued" {
		t.Fatalf("%+v", l)
	}
	lines := []st.Line{
		line(d(time.March, 1), st.KindCharge, st.Tenant, bob, 10000, "mar"),
		l,
	}
	s, err := st.Build(money.TRY, lines, st.Range{}, nil)
	if err != nil || s.Closing.Fee.Amount != 1234 || s.Closing.Principal.Amount != 10000 || s.Closing.Total.Amount != 11234 {
		t.Fatalf("%+v %v", s.Closing, err)
	}
}
