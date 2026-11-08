package reports

import "github.com/openbekci/openbekci/server/internal/core/money"

// currencyLabel names a currency in a document header, with its sign where it has one the PDF can draw.
func currencyLabel(c money.Code) string {
	if c == "TRY" {
		return "₺ (TRY)"
	}
	return string(c)
}

// FormatAmount renders an amount for a CSV cell: major units with the currency's decimals, no symbol.
func FormatAmount(m money.Money) string { return fmtMoney(m) }
