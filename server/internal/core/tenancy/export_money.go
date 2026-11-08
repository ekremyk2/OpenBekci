package tenancy

import "strings"

// moneyTables are the tenant tables of the money modules, in the order an archive inserts them
// (parents first): the chart and funds, fiscal years and receipt counters, the journal, billing,
// payments, late fees, then transfers, refunds, expenses, bills and budgets. A site hand-over carries
// the whole ledger: the import inserts the journal with the site's lock date left off and sets the
// lock date again once every table is in, so the lock-date trigger never refuses an imported entry.
var moneyTables = []string{
	"fund", "account", "cash_box", "fiscal_year", "sequence", "journal_entry", "journal_line",
	"charge_plan", "charge_plan_rate", "charge", "opening_balance", "payment", "payment_allocation",
	"late_fee_policy", "late_fee_booking", "late_fee_waiver", "late_fee_freeze",
	"transfer", "refund", "recurring_expense", "expense", "bill", "bill_payment", "budget", "budget_line",
}

func init() {
	for _, t := range moneyTables {
		order := "t.id"
		switch t {
		case "sequence":
			order = "t.kind, t.year"
		case "budget_line":
			order = "t.budget_id, t.expense_category_account_id, t.fund_id"
		case "journal_entry":
			order = "t.posted_at, t.id"
		}
		exportTables = append(exportTables, exportTable{t, `SELECT to_jsonb(t) FROM ` + t + ` t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY ` + order})
	}
}

// personColumns are the columns of the money tables that name a person; an imported archive remaps them
// like person_id when the person already had an account in the target organization.
var personColumns = []string{"person_id", "debtor_person_id", "payer_person_id"}

// fileColumns are the columns that name a file: a hand-over carries no files yet, so they are cleared.
var fileColumns = []string{"file_id"}

func isMoneyTable(name string) bool {
	for _, t := range moneyTables {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}
