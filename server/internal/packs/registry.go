package packs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbekci/openbekci/sdk"
)

var (
	packIDRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,23}$`)
	nameRE   = regexp.MustCompile(`^[a-z0-9_]+$`)
	// providerIDRE is the shape payment.method allows after "card:".
	providerIDRE = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9_]+)?$`)
	// reservedIDs would clash with the core's own tables pack_enablement*, pack_goose or the instance's names.
	reservedIDs = map[string]bool{"enablement": true, "site": true, "goose": true, "core": true, "default": true, "sdk": true}
	// reservedSegments are the first path segments of /v1/orgs/{org}/packs/{id}/ the core serves itself.
	reservedSegments = map[string]bool{"enable": true, "disable": true, "settings": true, "payments": true}
)

// Subscription is one registered event handler.
type Subscription struct {
	EventType string
	Name      string
	Handler   sdk.EventHandler
}

// Contributions is everything a pack registered, as the host and the core see it.
type Contributions struct {
	PaymentProviders []sdk.PaymentProvider
	SMSProviders     []sdk.SMSProvider
	ChatProviders    []sdk.ChatProvider
	Importers        []sdk.StatementImporter
	Quorum           []sdk.QuorumPreset
	Thresholds       []sdk.ThresholdPreset
	VoteWeight       []sdk.VoteWeightPreset
	Schedule         []sdk.SchedulePreset
	Transfer         []sdk.TransferRule
	LateFee          []sdk.LateFeePreset
	AllocationPolicy []sdk.AllocationPolicy
	PaymentOrders    []sdk.PaymentAllocationOrder
	Templates        []sdk.DocumentTemplate
	Charts           []sdk.ChartOfAccounts
	AddressFormats   []sdk.AddressFormat
	Validators       []sdk.Validator
	ReferenceData    []sdk.ReferenceData
	Consents         []sdk.ConsentTexts
	Catalogs         []sdk.Catalog
	Settings         sdk.SettingsSchema
	Migrations       fs.FS
	Routes           []sdk.Route
	Jobs             []sdk.JobDefinition
	Subscriptions    []Subscription
}

// HasPresets reports whether the pack offers any legal preset.
func (c *Contributions) HasPresets() bool {
	return len(c.Quorum)+len(c.Thresholds)+len(c.VoteWeight)+len(c.Schedule)+len(c.Transfer)+len(c.LateFee) > 0
}

// Provides lists what the pack contributes, as short labels for the pack list ("payment_provider:example").
func (c *Contributions) Provides() []string {
	var out []string
	add := func(kind string, ids ...string) {
		for _, id := range ids {
			out = append(out, kind+":"+id)
		}
	}
	for _, p := range c.PaymentProviders {
		add("payment_provider", p.ID())
	}
	for _, p := range c.SMSProviders {
		add("sms_provider", p.ID())
	}
	for _, p := range c.ChatProviders {
		add("chat_provider", p.ID())
	}
	for _, p := range c.Importers {
		add("statement_importer", p.ID())
	}
	for _, p := range c.Quorum {
		add("quorum_preset", p.ID())
	}
	for _, p := range c.Thresholds {
		add("threshold_preset", p.ID())
	}
	for _, p := range c.VoteWeight {
		add("vote_weight_preset", p.ID())
	}
	for _, p := range c.Schedule {
		add("schedule_preset", p.ID())
	}
	for _, p := range c.Transfer {
		add("transfer_rule", p.ID())
	}
	for _, p := range c.LateFee {
		add("late_fee_preset", p.ID())
	}
	for _, p := range c.AllocationPolicy {
		add("allocation_policy", p.ID())
	}
	for _, p := range c.PaymentOrders {
		add("payment_allocation_order", p.ID())
	}
	for _, p := range c.Templates {
		add("document_template", p.ID())
	}
	for _, p := range c.Charts {
		add("chart_of_accounts", p.ID())
	}
	for _, p := range c.AddressFormats {
		add("address_format", p.ID())
	}
	for _, p := range c.Validators {
		add("validator", p.ID())
	}
	for _, p := range c.ReferenceData {
		add("reference_data", p.ID())
	}
	for _, p := range c.Consents {
		add("consent_texts", p.ID())
	}
	for _, p := range c.Catalogs {
		add("catalog", p.ID())
	}
	if c.Settings != nil {
		add("settings_schema", "settings")
	}
	for _, r := range c.Routes {
		add("route", r.Method+" "+r.Path)
	}
	for _, j := range c.Jobs {
		add("job", j.Kind)
	}
	for _, s := range c.Subscriptions {
		add("subscription", s.EventType+" "+s.Name)
	}
	return out
}

// registry implements sdk.Registry for one pack. It is valid until the host freezes it.
type registry struct {
	pack   string
	svc    sdk.Services
	frozen *atomic.Bool
	errs   []error
	seen   map[string]bool
	c      Contributions
	// compiled settings schemas by scope, and the secret property names of each
	schemas map[sdk.ScopeKind]*schemaInfo
}

type schemaInfo struct {
	raw     json.RawMessage
	schema  *jsonschema.Schema
	secrets map[string]bool
}

var _ sdk.Registry = (*registry)(nil)

func newRegistry(pack string, svc sdk.Services, frozen *atomic.Bool) *registry {
	return &registry{pack: pack, svc: svc, frozen: frozen, seen: map[string]bool{}, schemas: map[sdk.ScopeKind]*schemaInfo{}}
}

func (r *registry) fail(kind error, format string, args ...any) {
	r.errs = append(r.errs, &RegistryError{Pack: r.pack, Kind: kind, Detail: fmt.Sprintf(format, args...)})
}

// guard panics when the registry is used after startup: that is a programming
// error of the pack (it kept the registry), never a runtime condition.
func (r *registry) guard() {
	if r.frozen.Load() {
		panic(&RegistryError{Pack: r.pack, Kind: ErrFrozen, Detail: "Registry methods may only be called from Register"})
	}
}

// claim records an identifier of a kind, checking the namespace and uniqueness.
func (r *registry) claim(kind, id string) {
	r.guard()
	if !r.namespaced(id, ".") {
		r.fail(ErrNamespace, "%s id %q must be %q or start with %q (lower-case letters, digits and underscores after the dot)", kind, id, r.pack, r.pack+".")
		return
	}
	key := kind + "|" + id
	if r.seen[key] {
		r.fail(ErrCollision, "%s id %q is registered twice", kind, id)
	}
	r.seen[key] = true
}

// namespaced reports whether id is the pack id or "<pack><sep><name>".
func (r *registry) namespaced(id, sep string) bool {
	if id == r.pack && sep == "." {
		return true
	}
	rest, ok := strings.CutPrefix(id, r.pack+sep)
	return ok && nameRE.MatchString(strings.ReplaceAll(rest, ".", "_"))
}

func (r *registry) Services() sdk.Services { return r.svc }

func (r *registry) RegisterPaymentProvider(p sdk.PaymentProvider) {
	r.claim("payment provider", p.ID())
	// the id becomes the payment method "card:<id>" and the clearing account code
	// "1190.<id>": a shape the database check and the chart cannot take would fail
	// at the first resident payment instead of at startup
	if !providerIDRE.MatchString(p.ID()) {
		r.fail(ErrInvalid, "payment provider id %q must be %q or %q followed by a dot and lower-case letters, digits and underscores", p.ID(), r.pack, r.pack)
	}
	r.c.PaymentProviders = append(r.c.PaymentProviders, p)
}

func (r *registry) RegisterSMSProvider(p sdk.SMSProvider) {
	r.claim("sms provider", p.ID())
	r.c.SMSProviders = append(r.c.SMSProviders, p)
}

// RegisterChatProvider implements sdk.ChatRegistrar (an optional extension point of SDK 1.1).
func (r *registry) RegisterChatProvider(p sdk.ChatProvider) {
	r.claim("chat provider", p.ID())
	r.c.ChatProviders = append(r.c.ChatProviders, p)
}

var _ sdk.ChatRegistrar = (*registry)(nil)

func (r *registry) RegisterStatementImporter(p sdk.StatementImporter) {
	r.claim("statement importer", p.ID())
	r.c.Importers = append(r.c.Importers, p)
}

func (r *registry) RegisterQuorumPreset(p sdk.QuorumPreset) {
	r.claim("quorum preset", p.ID())
	r.c.Quorum = append(r.c.Quorum, p)
}

func (r *registry) RegisterThresholdPreset(p sdk.ThresholdPreset) {
	r.claim("threshold preset", p.ID())
	r.c.Thresholds = append(r.c.Thresholds, p)
}

func (r *registry) RegisterVoteWeightPreset(p sdk.VoteWeightPreset) {
	r.claim("vote weight preset", p.ID())
	r.c.VoteWeight = append(r.c.VoteWeight, p)
}

func (r *registry) RegisterSchedulePreset(p sdk.SchedulePreset) {
	r.claim("schedule preset", p.ID())
	r.c.Schedule = append(r.c.Schedule, p)
}

func (r *registry) RegisterTransferRule(p sdk.TransferRule) {
	r.claim("transfer rule", p.ID())
	r.c.Transfer = append(r.c.Transfer, p)
}

func (r *registry) RegisterLateFeePreset(p sdk.LateFeePreset) {
	r.claim("late fee preset", p.ID())
	r.c.LateFee = append(r.c.LateFee, p)
}

func (r *registry) RegisterAllocationPolicy(p sdk.AllocationPolicy) {
	r.claim("allocation policy", p.ID())
	r.c.AllocationPolicy = append(r.c.AllocationPolicy, p)
}

func (r *registry) RegisterPaymentAllocationOrder(p sdk.PaymentAllocationOrder) {
	r.claim("payment allocation order", p.ID())
	r.c.PaymentOrders = append(r.c.PaymentOrders, p)
}

func (r *registry) RegisterDocumentTemplate(p sdk.DocumentTemplate) {
	r.claim("document template", p.ID())
	r.c.Templates = append(r.c.Templates, p)
}

func (r *registry) RegisterChartOfAccounts(p sdk.ChartOfAccounts) {
	r.claim("chart of accounts", p.ID())
	r.c.Charts = append(r.c.Charts, p)
}

func (r *registry) RegisterAddressFormat(p sdk.AddressFormat) {
	r.claim("address format", p.ID())
	r.c.AddressFormats = append(r.c.AddressFormats, p)
}

func (r *registry) RegisterValidator(p sdk.Validator) {
	r.claim("validator", p.ID())
	r.c.Validators = append(r.c.Validators, p)
}

func (r *registry) RegisterReferenceData(p sdk.ReferenceData) {
	r.claim("reference data", p.ID())
	r.c.ReferenceData = append(r.c.ReferenceData, p)
}

func (r *registry) RegisterConsentTexts(p sdk.ConsentTexts) {
	r.claim("consent texts", p.ID())
	r.c.Consents = append(r.c.Consents, p)
}

func (r *registry) RegisterCatalog(p sdk.Catalog) {
	r.claim("catalog", p.ID())
	r.c.Catalogs = append(r.c.Catalogs, p)
	// every label key of the catalog is a catalog key of the pack: check them in the default locale set
	for _, loc := range []string{"en", "tr", ""} {
		for k := range p.Labels(loc) {
			if !r.namespaced(k, ".") || k == r.pack {
				r.fail(ErrNamespace, "catalog %q key %q must start with %q", p.ID(), k, r.pack+".")
			}
		}
	}
}

func (r *registry) RegisterSettingsSchema(s sdk.SettingsSchema) {
	r.guard()
	if r.c.Settings != nil {
		r.fail(ErrCollision, "a settings schema is already registered")
		return
	}
	r.c.Settings = s
	for _, scope := range []sdk.ScopeKind{sdk.ScopeOrg, sdk.ScopeSite} {
		raw := s.Schema(scope)
		if len(raw) == 0 {
			continue
		}
		info, err := compileSchema(r.pack, raw)
		if err != nil {
			kind := ErrInvalid
			if errors.Is(err, ErrNamespace) {
				kind = ErrNamespace
			}
			r.fail(kind, "settings schema (%s scope): %v", scope, err)
			continue
		}
		r.schemas[scope] = info
	}
}

// compileSchema compiles a settings schema and checks its top-level property
// names against the namespace rule; it also finds the x-secret properties.
func compileSchema(pack string, raw json.RawMessage) (*schemaInfo, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	top, ok := doc.(map[string]any)
	if !ok || top["type"] != "object" {
		return nil, fmt.Errorf(`the top level must be {"type": "object", ...}`)
	}
	if path := straySecret(doc, nil); path != "" {
		return nil, fmt.Errorf(`%w: "x-secret" at %s is only honored on a top-level property; a nested secret would be stored and returned in clear text`, ErrInvalid, path)
	}
	info := &schemaInfo{raw: raw, secrets: map[string]bool{}}
	props, _ := top["properties"].(map[string]any)
	for name, p := range props {
		if !strings.HasPrefix(name, pack+"_") {
			return nil, fmt.Errorf("%w: property %q must start with %q", ErrNamespace, name, pack+"_")
		}
		if pm, ok := p.(map[string]any); ok {
			if sec, _ := pm["x-secret"].(bool); sec {
				info.secrets[name] = true
			}
		}
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("pack-settings.json", doc); err != nil {
		return nil, err
	}
	if info.schema, err = c.Compile("pack-settings.json"); err != nil {
		return nil, err
	}
	return info, nil
}

// straySecret returns the path of an "x-secret" keyword anywhere but directly
// on a top-level property of the schema ("" when there is none).
func straySecret(node any, path []string) string {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if k == "x-secret" && (len(path) != 2 || path[0] != "properties") {
				return "/" + strings.Join(path, "/")
			}
			if hit := straySecret(v, append(path[:len(path):len(path)], k)); hit != "" {
				return hit
			}
		}
	case []any:
		for i, v := range n {
			if hit := straySecret(v, append(path[:len(path):len(path)], fmt.Sprint(i))); hit != "" {
				return hit
			}
		}
	}
	return ""
}

func (r *registry) RegisterMigrations(fsys fs.FS) {
	r.guard()
	if r.c.Migrations != nil {
		r.fail(ErrCollision, "migrations are already registered")
		return
	}
	if fsys == nil {
		r.fail(ErrInvalid, "nil migrations file system")
		return
	}
	r.c.Migrations = fsys
}

func (r *registry) RegisterRoutes(routes ...sdk.Route) {
	r.guard()
	for _, rt := range routes {
		where := rt.Method + " " + rt.Path
		switch {
		case !rt.IsPublic():
			r.fail(ErrInvalid, "route %s: only routes built with sdk.Public exist (webhooks and callbacks)", where)
		case rt.Handler == nil:
			r.fail(ErrInvalid, "route %s has no handler", where)
		case rt.Method != http.MethodGet && rt.Method != http.MethodPost && rt.Method != http.MethodPut:
			r.fail(ErrInvalid, "route %s: method must be GET, POST or PUT", where)
		case !strings.HasPrefix(rt.Path, "/") || strings.ContainsAny(rt.Path, "{}*?#") || strings.Contains(rt.Path, "//") || strings.Contains(rt.Path, ".."):
			r.fail(ErrInvalid, "route %s: the path must start with / and be literal", where)
		case rt.Path == "/" || reservedSegments[strings.SplitN(rt.Path[1:], "/", 2)[0]]:
			r.fail(ErrNamespace, "route %s: the path may not be empty or start with a segment reserved by the core (enable, disable, settings, payments)", where)
		default:
			key := "route|" + where
			if r.seen[key] {
				r.fail(ErrCollision, "route %s is registered twice", where)
			}
			r.seen[key] = true
			r.c.Routes = append(r.c.Routes, rt)
		}
	}
}

func (r *registry) RegisterJob(j sdk.JobDefinition) {
	r.guard()
	if !r.namespaced(j.Kind, ".") || j.Kind == r.pack {
		r.fail(ErrNamespace, "job kind %q must start with %q", j.Kind, r.pack+".")
		return
	}
	if j.Handler == nil || j.Every < 0 {
		r.fail(ErrInvalid, "job %q needs a handler and a non-negative interval", j.Kind)
		return
	}
	key := "job|" + j.Kind
	if r.seen[key] {
		r.fail(ErrCollision, "job kind %q is registered twice", j.Kind)
	}
	r.seen[key] = true
	r.c.Jobs = append(r.c.Jobs, j)
}

func (r *registry) Subscribe(eventType, name string, h sdk.EventHandler) {
	r.guard()
	known := false
	for _, t := range sdk.EventTypes {
		known = known || t == eventType
	}
	switch {
	case !known:
		r.fail(ErrInvalid, "subscription %q: unknown event type %q", name, eventType)
		return
	case h == nil:
		r.fail(ErrInvalid, "subscription %q has no handler", name)
		return
	case !r.namespaced(name, ".") || name == r.pack:
		r.fail(ErrNamespace, "subscription name %q must start with %q", name, r.pack+".")
		return
	}
	key := "subscription|" + name
	if r.seen[key] {
		r.fail(ErrCollision, "subscription %q is registered twice", name)
	}
	r.seen[key] = true
	r.c.Subscriptions = append(r.c.Subscriptions, Subscription{EventType: eventType, Name: name, Handler: h})
}
