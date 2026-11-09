package sdk

import (
	"context"
	"io/fs"
	"net/http"
	"time"
)

// Registry is what Pack.Register receives: one narrow method per extension
// point. It is valid only during Register; calling it later panics with a
// named error (the registry is frozen after startup). Contributions are
// validated against the namespacing rules when Register returns, and
// violations abort startup.
type Registry interface {
	// Services returns the services the pack's handlers use. They are bound
	// to the organization in the call's context and may be kept.
	Services() Services

	// RegisterPaymentProvider adds a payment provider (called by payments).
	RegisterPaymentProvider(PaymentProvider)
	// RegisterSMSProvider adds an SMS provider (called by the core SMS gateway).
	RegisterSMSProvider(SMSProvider)
	// RegisterStatementImporter adds a bank statement importer (payments import).
	RegisterStatementImporter(StatementImporter)

	// The six legal presets of plan section 3.7. Each fills existing site
	// settings with defaults; none is read by governance or billing directly.
	RegisterQuorumPreset(QuorumPreset)
	RegisterThresholdPreset(ThresholdPreset)
	RegisterVoteWeightPreset(VoteWeightPreset)
	RegisterSchedulePreset(SchedulePreset)
	RegisterTransferRule(TransferRule)
	RegisterLateFeePreset(LateFeePreset)

	// RegisterAllocationPolicy adds a way to split a charge over units (billing).
	RegisterAllocationPolicy(AllocationPolicy)
	// RegisterPaymentAllocationOrder adds an order in which payments settle open items (payments).
	RegisterPaymentAllocationOrder(PaymentAllocationOrder)
	// RegisterDocumentTemplate adds a document template (documents render job).
	RegisterDocumentTemplate(DocumentTemplate)
	// RegisterChartOfAccounts adds localized account names (site creation, enable).
	RegisterChartOfAccounts(ChartOfAccounts)
	// RegisterAddressFormat adds an address field list (tenancy, clients).
	RegisterAddressFormat(AddressFormat)
	// RegisterValidator adds a validator for tax ids, IBAN variants, plates (people, vendors, imports).
	RegisterValidator(Validator)
	// RegisterReferenceData adds a reference data set (the reference-data endpoint).
	RegisterReferenceData(ReferenceData)
	// RegisterConsentTexts adds consent texts (privacy module).
	RegisterConsentTexts(ConsentTexts)
	// RegisterCatalog adds localized labels for pack-provided codes
	// (merged into the core codes endpoint).
	RegisterCatalog(Catalog)
	// RegisterSettingsSchema sets the pack's settings schema (settings module).
	// At most one schema per pack.
	RegisterSettingsSchema(SettingsSchema)

	// RegisterMigrations sets the pack's goose migrations: the files are at
	// the root of fsys (for example fs.Sub(embedded, "migrations")). Tables
	// must be named pack_<id>_* and tenant tables must call
	// openbekci_pack_table (see the authoring guide).
	RegisterMigrations(fsys fs.FS)
	// RegisterRoutes adds webhook and callback routes, mounted under
	// /v1/orgs/{org}/packs/{id}/. Only Public routes exist.
	RegisterRoutes(...Route)
	// RegisterJob adds a background job definition (worker).
	RegisterJob(JobDefinition)
	// Subscribe registers a handler for a core event type. name identifies the
	// subscription inside the pack ("<id>.<name>"); delivery is one job per
	// (event, subscriber) with dedupe, so handlers must be idempotent on the ids.
	Subscribe(eventType, name string, h EventHandler)
}

// Route is a webhook or callback route of a pack. It is created with [Public]:
// the tenant context is set from the path, no session is required, and the
// pack must verify the caller (the provider's signature) before doing anything.
// Routes never appear in the OpenAPI spec.
type Route struct {
	Method string
	// Path is relative to /v1/orgs/{org}/packs/{id}, starts with "/" and is
	// literal (no parameters). The first segments "enable", "disable",
	// "settings" and "payments" are reserved for the core.
	Path    string
	Handler http.HandlerFunc
	public  bool
}

// Public builds a public route. The request context carries the organization
// ([OrgFrom]); the host answers 404 when the pack is not enabled for it and
// recovers panics as pack_error.
func Public(method, path string, h http.HandlerFunc) Route {
	return Route{Method: method, Path: path, Handler: h, public: true}
}

// IsPublic reports whether the route was built with [Public]. The registry
// rejects every other route.
func (r Route) IsPublic() bool { return r.public }

// JobDefinition is a background job of a pack, run by the core worker.
type JobDefinition struct {
	// Kind is "<packid>.<name>".
	Kind string
	// Every, when positive, runs the job periodically for every organization
	// that has the pack enabled (the host fans out; Job.Payload is empty).
	Every time.Duration
	// Handler runs one job. The context carries the organization. Jobs of a
	// disabled pack are skipped, never errored. Handlers must be idempotent.
	Handler func(ctx context.Context, job Job) error
}

// Job is one run of a JobDefinition.
type Job struct {
	Kind    string
	Org     ID
	Payload []byte
	Attempt int
}

// EventHandler handles one core event.
type EventHandler func(ctx context.Context, e Event) error

// Event is the payload of a subscription: the type, a payload version and the
// ids of the records concerned, never the records themselves, so core internals
// are not frozen into the SDK. Fetch what you need through the readers.
type Event struct {
	Type    string
	Version int
	Org     ID
	IDs     map[string]ID
}

// Event types the core emits. The ids each carries are listed.
const (
	// EventSiteCreated: ids "site".
	EventSiteCreated = "site.created"
	// EventPaymentRecorded: ids "payment", "site", "unit" (when the payment is a unit's).
	EventPaymentRecorded = "payment.recorded"
	// EventPaymentRefunded: ids "payment", "site".
	EventPaymentRefunded = "payment.refunded"
	// EventPackEnabled and EventPackDisabled are delivered to the pack itself: ids none.
	EventPackEnabled  = "pack.enabled"
	EventPackDisabled = "pack.disabled"
)

// EventTypes lists every event type a pack may subscribe to.
var EventTypes = []string{EventSiteCreated, EventPaymentRecorded, EventPaymentRefunded, EventPackEnabled, EventPackDisabled}
