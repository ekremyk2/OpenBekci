package money

import (
	"errors"
	"fmt"
)

// Code is an ISO 4217 alphabetic currency code.
type Code string

// Supported currencies.
const (
	TRY Code = "TRY"
	EUR Code = "EUR"
	USD Code = "USD"
	GBP Code = "GBP"
	JPY Code = "JPY"
	KWD Code = "KWD"
	CHF Code = "CHF"
)

// ErrUnknownCurrency is returned for a code missing from the currency table.
var ErrUnknownCurrency = errors.New("money: unknown currency")

// exponents maps ISO 4217 code to the number of minor-unit decimal digits.
var exponents = map[Code]int{
	TRY: 2, EUR: 2, USD: 2, GBP: 2, CHF: 2,
	JPY: 0,
	KWD: 3,
}

// symbols are display-only.
var symbols = map[Code]string{
	TRY: "₺", EUR: "€", USD: "$", GBP: "£", JPY: "¥", CHF: "CHF ", KWD: "KWD ",
}

// Exponent returns the number of decimal digits of the minor unit.
func (c Code) Exponent() (int, error) {
	e, ok := exponents[c]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCurrency, string(c))
	}
	return e, nil
}

// Valid reports whether c is in the currency table.
func (c Code) Valid() bool {
	_, ok := exponents[c]
	return ok
}

// ParseCode validates s as a currency code.
func ParseCode(s string) (Code, error) {
	c := Code(s)
	if !c.Valid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownCurrency, s)
	}
	return c, nil
}

// Symbol returns the display symbol of the currency (display only).
func (c Code) Symbol() string {
	if s, ok := symbols[c]; ok {
		return s
	}
	return string(c) + " "
}
