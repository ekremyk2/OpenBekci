package moneydocs

import (
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/money"
)

// Documents show money in the number format of the organization's locale; the
// Turkish format is unchanged, every other locale gets the English one.
func TestAmountFollowsTheOrganizationLocale(t *testing.T) {
	m := money.New(123450, money.TRY)
	for locale, want := range map[string]string{"tr": "₺1.234,50", "tr-TR": "₺1.234,50", "TR": "₺1.234,50", "en": "₺1,234.50", "en-GB": "₺1,234.50", "": "₺1,234.50", "de": "₺1,234.50"} {
		if got := amount(locale, m); got != want {
			t.Errorf("amount(%q) = %q, want %q", locale, got, want)
		}
	}
	if got := amount("en", money.New(-5, money.USD)); got != "-$0.05" {
		t.Errorf("a negative dollar amount: %q", got)
	}
}
