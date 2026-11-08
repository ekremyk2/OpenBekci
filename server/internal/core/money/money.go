package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
)

var (
	// ErrCurrencyMismatch is returned when two amounts of different currencies are combined.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrOverflow is returned when an operation would overflow int64.
	ErrOverflow = errors.New("money: amount overflow")
)

// Money is an amount in minor units of a currency (123450 TRY = 1.234,50 TRY).
type Money struct {
	Amount   int64 `json:"amount"`
	Currency Code  `json:"currency"`
}

// New builds a Money without validating the currency.
func New(amount int64, c Code) Money { return Money{Amount: amount, Currency: c} }

// Zero returns a zero amount of currency c.
func Zero(c Code) Money { return Money{Currency: c} }

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.Amount == 0 }

// Sign returns -1, 0 or 1.
func (m Money) Sign() int {
	switch {
	case m.Amount < 0:
		return -1
	case m.Amount > 0:
		return 1
	}
	return 0
}

func (m Money) same(o Money) error {
	if m.Currency != o.Currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	return nil
}

// Add returns m + o.
func (m Money) Add(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	s := m.Amount + o.Amount
	if (o.Amount > 0 && s < m.Amount) || (o.Amount < 0 && s > m.Amount) {
		return Money{}, ErrOverflow
	}
	return Money{s, m.Currency}, nil
}

// Sub returns m - o.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	if o.Amount == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return m.Add(Money{-o.Amount, o.Currency})
}

// Neg returns -m.
func (m Money) Neg() (Money, error) {
	if m.Amount == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{-m.Amount, m.Currency}, nil
}

// Mul returns m * k.
func (m Money) Mul(k int64) (Money, error) {
	p := new(big.Int).Mul(big.NewInt(m.Amount), big.NewInt(k))
	if !p.IsInt64() {
		return Money{}, ErrOverflow
	}
	return Money{p.Int64(), m.Currency}, nil
}

// Cmp compares m and o: -1, 0 or 1.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.same(o); err != nil {
		return 0, err
	}
	switch {
	case m.Amount < o.Amount:
		return -1, nil
	case m.Amount > o.Amount:
		return 1, nil
	}
	return 0, nil
}

// Sum adds all amounts, starting from zero of currency c.
func Sum(c Code, ms ...Money) (Money, error) {
	acc := Zero(c)
	var err error
	for _, m := range ms {
		if acc, err = acc.Add(m); err != nil {
			return Money{}, err
		}
	}
	return acc, nil
}

// Validate checks that the currency is known.
func (m Money) Validate() error {
	if !m.Currency.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownCurrency, string(m.Currency))
	}
	return nil
}

// UnmarshalJSON decodes {"amount": n, "currency": "XXX"} and rejects unknown currencies.
func (m *Money) UnmarshalJSON(b []byte) error {
	var aux struct {
		Amount   *int64 `json:"amount"`
		Currency Code   `json:"currency"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if aux.Amount == nil {
		return errors.New("money: missing amount")
	}
	v := Money{*aux.Amount, aux.Currency}
	if err := v.Validate(); err != nil {
		return err
	}
	*m = v
	return nil
}
