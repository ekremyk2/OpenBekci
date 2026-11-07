package tenancy

// moneyTables are the tenant tables of the money modules (phase C). A site
// archive does not carry them yet: the journal is append-only, guarded by the
// lock-date trigger, and a faithful hand-over needs the ledger replayed with
// the lock date applied last. Until that is built a hand-over carries units
// and people only, and the money history stays with the source organization.
var moneyTables = []string{
	"fund", "account", "cash_box", "fiscal_year", "sequence", "journal_entry", "journal_line",
}

func init() {
	for _, t := range moneyTables {
		excludedTables[t] = "money data (phase C) is not carried by a site hand-over yet; the ledger stays with the source organization"
	}
}
