package sdk

import "context"

// DocumentTemplate renders a document (statement, receipt, call letter,
// minutes, ...). It adds to the core's generic templates; a site picks one by id.
//
// Core call sites: the documents render job looks the template id up among the
// templates of the organization's enabled packs before the core set.
type DocumentTemplate interface {
	// ID is "<packid>.<name>".
	ID() string
	// Kind is the document kind it renders: "statement", "receipt",
	// "closing_statement", "debt_certificate", "call_letter", "attendance_list",
	// "minutes", "decision_list" or a pack-specific kind.
	Kind() string
	// Locales lists the locales the template is available in.
	Locales() []string
	// Render produces the document. data is the same document data the core
	// templates receive, as JSON-shaped values.
	Render(ctx context.Context, req RenderRequest) (Document, error)
}

// RenderRequest is the input of DocumentTemplate.Render.
type RenderRequest struct {
	Locale string
	Data   map[string]any
}

// Document is a rendered document.
type Document struct {
	Filename string
	MIME     string
	Body     []byte
}

// ChartOfAccounts localizes the names of the generic chart's accounts. Packs
// localize names, never structure.
//
// Core call sites: site creation and pack enablement rename the site's system
// accounts by account code.
type ChartOfAccounts interface {
	ID() string
	// Names maps account codes of the generic chart to names in a locale.
	Names(locale string) map[string]string
}

// AddressFormat describes the address fields of a country as data, so clients
// render forms without country code.
//
// Core call sites: tenancy (address validation) and the clients, through the
// address-format part of the reference data endpoint.
type AddressFormat interface {
	ID() string
	// Country is the ISO 3166-1 alpha-2 code the format is for.
	Country() string
	Fields() []AddressField
}

// AddressField is one field of an address form.
type AddressField struct {
	Key      string // "line1", "district", "province", "postal_code"
	LabelKey string // catalog key of the label
	Required bool
	// Pattern is an optional RE2 regular expression the value must match.
	Pattern string
	// Reference, when set, names a ReferenceData kind that offers the options
	// (cascading selects: province, district, neighbourhood).
	Reference string
}

// Validator checks one kind of value: a tax id, an IBAN variant, a plate.
//
// Core call sites: people and vendors (tax id, national id), imports, and
// vehicles (plates), by validator kind and the organization's country.
type Validator interface {
	ID() string
	// Kind is "tax_id", "national_id", "iban", "plate" or a pack-specific kind.
	Kind() string
	// Country is the ISO 3166-1 alpha-2 code the validator applies to.
	Country() string
	// Validate returns nil for a valid value, a descriptive error otherwise.
	Validate(value string) error
}

// ReferenceData serves a reference data set: administrative areas, banks,
// holidays.
//
// Core call sites: the core reference-data endpoint (cascading address selects,
// due date rolling over holidays).
type ReferenceData interface {
	ID() string
	// Kinds lists the kinds it serves ("province", "district", "bank", "holiday").
	Kinds() []string
	// List returns the items of a kind, optionally below a parent code.
	List(ctx context.Context, kind, parent string) ([]ReferenceItem, error)
}

// ReferenceItem is one entry of a reference data set.
type ReferenceItem struct {
	Code   string
	Name   string
	Parent string
}

// ConsentTexts supplies consent and privacy texts per locale.
//
// Core call sites: the privacy module's consents endpoint, which returns the
// Markdown with its version; clients show it and record acceptance of the version.
type ConsentTexts interface {
	ID() string
	// Text returns the text in a locale, ok false when it has none.
	Text(locale string) (ConsentText, bool)
}

// ConsentText is one consent document.
type ConsentText struct {
	Version  string
	Markdown string
}

// Catalog supplies localized labels for pack-provided codes (provider
// statuses, importer ids, template names). Keys are "<packid>.<key>".
//
// Core call sites: merged into GET /v1/orgs/{org}/codes?locale= for
// organizations that have the pack enabled; clients fall back to the raw code.
type Catalog interface {
	ID() string
	// Labels returns key to label for a locale, following the core fallback
	// chain (the host asks for the locale, then its fallbacks).
	Labels(locale string) map[string]string
}
