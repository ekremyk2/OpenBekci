package money

import (
	"fmt"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// absUint is |a| as a uint64; it is correct for math.MinInt64.
func absUint(a int64) uint64 {
	if a < 0 {
		return -uint64(a) //nolint:gosec // two's complement magnitude, exact for MinInt64
	}
	return uint64(a)
}

// Format renders m for display in a locale ("tr-TR": ₺1.234,50, "en-US":
// ₺1,234.50). It is display only and goes through a float64, so it is not
// exact above 2^53 minor units; never parse its output. Unparsable locale
// tags fall back to en-US. Every int64 amount, including math.MinInt64,
// renders with the right sign and magnitude (to float64 precision).
func (m Money) Format(locale string) (string, error) {
	exp, err := m.Currency.Exponent()
	if err != nil {
		return "", err
	}
	tag, perr := language.Parse(locale)
	if perr != nil {
		tag = language.AmericanEnglish
	}
	neg := m.Amount < 0
	scale := 1.0
	for i := 0; i < exp; i++ {
		scale *= 10
	}
	p := message.NewPrinter(tag)
	s := p.Sprint(number.Decimal(float64(absUint(m.Amount))/scale, number.Scale(exp)))
	out := m.Currency.Symbol() + s
	if neg {
		out = "-" + out
	}
	return out, nil
}

// String implements fmt.Stringer with an unlocalized "1234.50 TRY" form. It
// is exact for every int64 amount, including math.MinInt64.
func (m Money) String() string {
	exp, err := m.Currency.Exponent()
	if err != nil || exp == 0 {
		return fmt.Sprintf("%d %s", m.Amount, m.Currency)
	}
	sign := ""
	if m.Amount < 0 {
		sign = "-"
	}
	abs := absUint(m.Amount)
	div := uint64(1)
	for i := 0; i < exp; i++ {
		div *= 10
	}
	return fmt.Sprintf("%s%d.%0*d %s", sign, abs/div, exp, abs%div, m.Currency)
}
