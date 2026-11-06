package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/openbekci/openbekci/server/internal/core/money"
)

func TestCodeTable(t *testing.T) {
	cases := map[money.Code]int{money.TRY: 2, money.EUR: 2, money.USD: 2, money.GBP: 2, money.JPY: 0, money.KWD: 3, money.CHF: 2}
	for c, want := range cases {
		got, err := c.Exponent()
		if err != nil || got != want {
			t.Errorf("%s: exponent %d err %v, want %d", c, got, err, want)
		}
	}
	if _, err := money.Code("XXX").Exponent(); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Errorf("unknown currency: %v", err)
	}
	if _, err := money.ParseCode("nope"); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Errorf("ParseCode: %v", err)
	}
	if c, err := money.ParseCode("TRY"); err != nil || c != money.TRY {
		t.Errorf("ParseCode TRY: %v %v", c, err)
	}
	if money.Code("ZZZ").Symbol() != "ZZZ " || money.TRY.Symbol() != "₺" {
		t.Error("symbol")
	}
}

func TestArithmetic(t *testing.T) {
	a, b := money.New(1000, money.TRY), money.New(250, money.TRY)
	if s, err := a.Add(b); err != nil || s.Amount != 1250 {
		t.Errorf("add %v %v", s, err)
	}
	if s, err := a.Sub(b); err != nil || s.Amount != 750 {
		t.Errorf("sub %v %v", s, err)
	}
	if n, err := a.Neg(); err != nil || n.Amount != -1000 {
		t.Errorf("neg %v %v", n, err)
	}
	if p, err := a.Mul(3); err != nil || p.Amount != 3000 {
		t.Errorf("mul %v %v", p, err)
	}
	if c, err := a.Cmp(b); err != nil || c != 1 {
		t.Errorf("cmp %v %v", c, err)
	}
	if c, _ := b.Cmp(a); c != -1 {
		t.Error("cmp lt")
	}
	if c, _ := a.Cmp(a); c != 0 {
		t.Error("cmp eq")
	}
	eur := money.New(1, money.EUR)
	if _, err := a.Add(eur); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("add mismatch: %v", err)
	}
	if _, err := a.Sub(eur); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("sub mismatch: %v", err)
	}
	if _, err := a.Cmp(eur); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("cmp mismatch: %v", err)
	}
	max := money.New(math.MaxInt64, money.TRY)
	if _, err := max.Add(b); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("add overflow: %v", err)
	}
	if _, err := money.New(math.MinInt64, money.TRY).Add(money.New(-1, money.TRY)); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("add neg overflow: %v", err)
	}
	if _, err := max.Sub(money.New(-1, money.TRY)); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("sub overflow: %v", err)
	}
	if _, err := a.Sub(money.New(math.MinInt64, money.TRY)); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("sub minint: %v", err)
	}
	if _, err := money.New(math.MinInt64, money.TRY).Neg(); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("neg overflow: %v", err)
	}
	if _, err := max.Mul(2); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("mul overflow: %v", err)
	}
	z := money.Zero(money.JPY)
	if !z.IsZero() || z.Sign() != 0 || a.Sign() != 1 || money.New(-3, money.TRY).Sign() != -1 {
		t.Error("sign")
	}
}

func TestSum(t *testing.T) {
	s, err := money.Sum(money.TRY, money.New(1, money.TRY), money.New(2, money.TRY))
	if err != nil || s.Amount != 3 {
		t.Errorf("sum %v %v", s, err)
	}
	if _, err := money.Sum(money.TRY, money.New(1, money.EUR)); err == nil {
		t.Error("sum mismatch should fail")
	}
	z, err := money.Sum(money.TRY)
	if err != nil || !z.IsZero() {
		t.Error("empty sum")
	}
}

func TestJSON(t *testing.T) {
	b, err := json.Marshal(money.New(123450, money.TRY))
	if err != nil || string(b) != `{"amount":123450,"currency":"TRY"}` {
		t.Fatalf("marshal %s %v", b, err)
	}
	var m money.Money
	if err := json.Unmarshal(b, &m); err != nil || m != money.New(123450, money.TRY) {
		t.Fatalf("unmarshal %v %v", m, err)
	}
	for _, bad := range []string{`{"amount":1,"currency":"XXX"}`, `{"currency":"TRY"}`, `{"amount":"x","currency":"TRY"}`, `[]`} {
		var m money.Money
		if err := json.Unmarshal([]byte(bad), &m); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
	if err := money.New(1, "ABC").Validate(); err == nil {
		t.Error("validate")
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		m      money.Money
		locale string
		want   string
	}{
		{money.New(123450, money.TRY), "tr-TR", "₺1.234,50"},
		{money.New(123450, money.TRY), "en-US", "₺1,234.50"},
		{money.New(-5, money.USD), "en-US", "-$0.05"},
		{money.New(1500, money.JPY), "en-US", "¥1,500"},
		{money.New(1234567, money.KWD), "en-US", "KWD 1,234.567"},
		{money.New(100, money.EUR), "garbage-locale-!!", "€1.00"},
	}
	for _, c := range cases {
		got, err := c.m.Format(c.locale)
		if err != nil || got != c.want {
			t.Errorf("Format(%v,%s) = %q %v, want %q", c.m, c.locale, got, err, c.want)
		}
	}
	if _, err := money.New(1, "XXX").Format("en-US"); err == nil {
		t.Error("unknown currency format")
	}
}

func TestString(t *testing.T) {
	cases := map[money.Money]string{
		money.New(123450, money.TRY): "1234.50 TRY",
		money.New(-5, money.TRY):     "-0.05 TRY",
		money.New(7, money.JPY):      "7 JPY",
		money.New(1234, money.KWD):   "1.234 KWD",
		money.New(3, "XXX"):          "3 XXX",
	}
	for m, want := range cases {
		if m.String() != want {
			t.Errorf("String(%v) = %q want %q", m.Amount, m.String(), want)
		}
	}
}

func TestPropAddSubInverse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := money.New(rapid.Int64Range(-1<<40, 1<<40).Draw(t, "a"), money.TRY)
		b := money.New(rapid.Int64Range(-1<<40, 1<<40).Draw(t, "b"), money.TRY)
		s, err := a.Add(b)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.Sub(b)
		if err != nil || d != a {
			t.Fatalf("(a+b)-b != a: %v %v", d, err)
		}
	})
}
