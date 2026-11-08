package ledger

// TypedSources are the journal source types that have their own void or cancel
// operation; their entries are never reversed directly through the journal API.
var TypedSources = []string{"charge", "payment", "late_fee", "late_fee.reversal", "opening_balance", "move_out", "credit_apply",
	"expense", "bill", "bill_payment", "transfer", "refund", "budget"}
