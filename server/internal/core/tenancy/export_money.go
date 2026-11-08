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
	// bank statement import and collections (C3)
	"bank_statement_import", "bank_transaction", "dunning_stage", "dunning_run", "dunning_event", "collection_case",
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

// moneyPersonColumns are the columns of the money tables that name a person; the import checks that
// each names a person of the archive.
var moneyPersonColumns = map[string][]string{
	"cash_box": {"collector_person_id"}, "journal_line": {"person_id"}, "charge": {"debtor_person_id"},
	"payment": {"debtor_person_id", "payer_person_id"}, "late_fee_booking": {"person_id"}, "refund": {"person_id"},
	"dunning_event": {"person_id"}, "collection_case": {"debtor_person_id"},
}

// fileColumns are the columns of the money tables that name a file: the money tables are carried with the
// ledger, the files are not tied to them in the target (the receipt files travel in the file table, the
// links are cleared so a missing blob never breaks an imported expense), so they are cleared.
var fileColumns = []string{"file_id", "document_file_id"}

func isMoneyTable(name string) bool {
	for _, t := range moneyTables {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}
