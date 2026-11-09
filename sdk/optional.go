package sdk

import "context"

// Optional interfaces added by the paper-pack exercise of docs/packs/paper-packs.md
// (Germany WEG, Netherlands VvE). They are found by type assertion; implement only
// what you need. The host adds their call sites in a 1.x minor release (additive);
// until then a pack may implement them and the host ignores them.

// MultiStatementImporter is an optional interface of a [StatementImporter] for
// formats that carry several accounts in one file (ISO 20022 camt.053 of a
// property manager's bank, which holds one account per owners' association).
//
// Core call site: the bank import module, which prefers ParseAll over Parse when
// the importer implements it and routes each statement to the site whose bank
// details carry the same IBAN. Parse must still work for single-account files.
type MultiStatementImporter interface {
	ParseAll(ctx context.Context, filename string, data []byte) ([]AccountStatement, error)
}

// AccountStatement is one account's statement of a multi-account file.
type AccountStatement struct {
	// IBAN of the account the statement belongs to, upper case, no spaces.
	IBAN string
	Statement
}

// ComplianceChecker is an optional interface of a [Pack]: statutory duties that
// depend on data the core already holds, such as the Dutch reserve fund minimum
// or the German deadline for the annual accounts. The pack only reports; it
// never changes data.
//
// Core call site: the site dashboard and the nightly checks, for sites of
// organizations that enabled the pack.
type ComplianceChecker interface {
	Checks(ctx context.Context, site SiteInfo) ([]ComplianceFinding, error)
}

// ComplianceFinding is one result of a check.
type ComplianceFinding struct {
	// Code is "<packid>.<name>"; stable, so dashboards can mute a finding.
	Code string
	// Severity is "info", "warning" or "action_required".
	Severity string
	// LabelKey is a key of the pack's [Catalog] ("<packid>.<key>").
	LabelKey string
	// Params fill the placeholders of the label.
	Params map[string]string
}
