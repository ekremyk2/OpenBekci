package tenancy

// collectTables are the tenant tables of bank import and collections (phase C3): like the other money tables they
// stay with the source organization on a site hand-over until money data can be replayed.
var collectTables = []string{"bank_statement_import", "bank_transaction", "dunning_stage", "dunning_run", "dunning_event", "collection_case"}

func init() {
	for _, t := range collectTables {
		excludedTables[t] = "money data (phase C) is not carried by a site hand-over yet; the ledger stays with the source organization"
	}
}
