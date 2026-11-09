# Writing an OpenBekci pack

Status: skeleton for release 0.4 (milestones P0 and P1). The structure follows [plan 04-packs.md](../plans/04-packs.md) section 4;
sections marked *to be written* are filled in as the Turkish pack (P1, P2) exercises them. Everything described here
exists in the code today: the SDK (`sdk/`), the pack runtime (`server/internal/packs`), the test harness
(`server/internal/sdktest`) and the example pack (`packs/example`).

A pack is a Go module that implements `sdk.Pack`, is compiled into the server binary through an explicit list, and is
**enabled per organization**. It may contribute implementations for the extension points of the SDK and nothing else:
no client code, no overriding core behaviour, no identity providers. A site without any pack stays fully usable; packs
fill settings and add providers.

Licences: `sdk/` is Apache-2.0. Official packs are AGPL-3.0 like the server. Whether a private pack may stay private is
governed by the pack-linking exception in [LICENSING.md](../../LICENSING.md) (draft, pending legal review).

## 1. Start from `packs/example`

`packs/example` is deliberately tiny and implements **every** extension point minimally: a fake payment provider with
HMAC-signed webhooks, a CSV statement importer, the six legal presets, an SMS provider, an allocation policy and a
payment order, a template, a chart of accounts, an address format, a validator, reference data, consent texts, a label
catalog, a settings schema with a secret, a migration with two tenant tables, one webhook route, one job and one event
subscriber. Read `packs/example/example.go` first: `Register` is the table of contents.

```sh
tools/newpack acme            # copies packs/example to packs/acme, renames the id everywhere, adds it to go.work
tools/newpack acme github.com/acme/openbekci-pack-acme   # for a pack that lives in its own repository
```

(`tools/newpack` is a POSIX shell script; on Windows run it from Git Bash.) It prints the remaining steps: list the pack
in the `Official` slice, adjust the manifest and settings schema, add tests.

## 2. Manifest and registration

```go
func (p *Pack) Manifest() sdk.Manifest {
	return sdk.Manifest{ID: "acme", Version: "0.1.0", MinSDK: sdk.Version, Countries: []string{"DE"}}
}

func (p *Pack) Register(ctx context.Context, r sdk.Registry) error {
	r.RegisterPaymentProvider(&provider{})
	r.RegisterSettingsSchema(settings{})
	...
}
```

- **id**: lower-case letters and digits, starting with a letter, at most 24 characters. `enablement`, `site`, `goose`,
  `core`, `default` and `sdk` are reserved.
- **MinSDK**: the oldest SDK the pack builds against. The server refuses a pack that needs a newer SDK, and before 1.0 one
  of another minor line (minor versions may break until the project's 1.0).
- **Countries**: ISO 3166-1 alpha-2. They drive the "suggested packs" hint and which organizations receive the pack's
  presets when a site is created. An empty list means no restriction.
- **Register** hands every contribution to the `sdk.Registry`. There is no `init()` registration and nothing is found by
  reflection or name. `r.Services()` gives the services your handlers use; they may be kept.
- **Optional interfaces**, found by type assertion (implement only what you need): `OnEnable`, `OnDisable`,
  `ValidateSettings`, `StartupCheck` (an error aborts startup with the pack's name), `Ready` (contributes a warning to
  `/readyz`, never makes the server unready). That is the complete list.

Every interface in `sdk/` is documented with the core call sites that use it. The extension points, in short:

| Extension point | Interface | Called by |
|---|---|---|
| Payment provider | `PaymentProvider` | payments: checkout, status, webhooks, refunds, settlement |
| SMS provider | `SMSProvider` | the core SMS gateway, when the organization setting `sms_provider` names it |
| Statement importer | `StatementImporter` | the bank import module, next to the generic CSV importer |
| Legal presets | `QuorumPreset`, `ThresholdPreset`, `VoteWeightPreset`, `SchedulePreset`, `TransferRule`, `LateFeePreset` | site creation and pack enablement; they fill **site settings** |
| Allocation policy / payment order | `AllocationPolicy`, `PaymentAllocationOrder` | billing, payments |
| Document template | `DocumentTemplate` | the documents render job |
| Chart of accounts | `ChartOfAccounts` | site creation and enablement rename system accounts by code |
| Address format, validators, reference data | `AddressFormat`, `Validator`, `ReferenceData` | tenancy, people, vendors, imports, the reference-data endpoint |
| Consent texts, labels | `ConsentTexts`, `Catalog` | the privacy module, the codes endpoint |
| Settings | `SettingsSchema` | the pack list and settings endpoints, the web app's schema-driven forms |
| Migrations | `RegisterMigrations(fs.FS)` | `openbekci migrate` |
| Routes | `RegisterRoutes(sdk.Public(...))` | the HTTP layer, webhooks and callbacks only |
| Jobs, events | `RegisterJob`, `Subscribe` | the worker |

## 3. Lifecycle

| Phase | What happens |
|---|---|
| Compile | `server/cmd/openbekci/packs.go` lists the packs in `Official`. A fork adds its pack to the slice. the default build lists the Turkish pack (`packs_default.go`); `-tags nopacks` leaves it empty; `-tags examplepack` adds `packs/example` (tests) |
| Startup | The registry reads `Manifest()`, checks `MinSDK`, calls `Register`, runs `StartupCheck`, validates namespacing and collisions, then **freezes**. Any violation is a named error (`errors.Is(err, packs.ErrNamespace)` and friends) and the server does not start; a panic in pack code is recovered |
| `migrate` | Core migrations, then each pack's migrations in pack-id order, each with its own goose table `pack_<id>_goose_version`. They run even when no organization has the pack enabled: schema is global, data is per organization |
| Enable (per organization) | An org admin enables the pack with settings: validated against the schema and `ValidateSettings`, stored (secrets encrypted), presets applied to sites' unchanged settings, chart names localized, `OnEnable` called; if it fails the enablement is withdrawn |
| Running | The core selects pack implementations only for organizations where the pack is enabled; pack routes and webhooks answer 404 otherwise; jobs and event handlers of a disabled pack are skipped |
| Disable | `OnDisable`; data retained; implementations stop being selected. Settings filled by presets stay (they are the site's now) |
| Purge | `openbekci admin packs purge --org <id or slug> --pack <id>` deletes the pack's rows for that organization after confirmation; never automatic; the pack must be disabled first |
| Remove from build | Only by rebuilding without the pack; the tables stay until an operator drops them |

Org admins manage packs through four operations (`packs.list`, `packs.enable`, `packs.updateSettings`, `packs.disable`,
permissions `packs.read` and `packs.manage`). `GET /.well-known/openbekci.json` lists the pack ids enabled somewhere and
`sdk_version`; `GET /v1/instance` carries `pack_details`.

## 4. Namespacing rules

The registry rejects violations at startup, naming the pack and the identifier.

| Thing | Rule |
|---|---|
| Provider, importer, preset, policy, template, validator, address format, reference data, consent, catalog ids | `<id>` or `<id>.<name>` |
| Catalog label keys | `<id>.<key>` |
| Settings keys (top-level schema properties) | `<id>_<name>` |
| Job kinds, subscription names | `<id>.<name>` |
| Tables | `pack_<id>_<name>`; checked after the pack's migrations ran, together with "no core table was created, altered or dropped" |
| Routes | literal paths under `/v1/orgs/{org}/packs/{id}/`; `enable`, `disable`, `settings` and `payments` are the core's |
| Payment method | `card:<provider id>` |

## 5. Data

- **Tables** are `pack_<id>_*`, have an `org_id` column, a primary key that includes it, and call the core helper right
  after `CREATE TABLE`:

  ```sql
  CREATE TABLE pack_acme_thing (org_id uuid NOT NULL, id uuid NOT NULL, site_id uuid, ..., PRIMARY KEY (org_id, id));
  SELECT openbekci_pack_table('pack_acme_thing');
  ```

  `openbekci_pack_table` adds the foreign key to `organization`, the composite `(org_id, site_id)` foreign key to `site`
  when there is a `site_id` column, and forced row-level security (`app.org_id`). The migration runner refuses a tenant
  table without it.
- **Migrations** are goose files at the root of the `fs.FS` you register (`fs.Sub(embedded, "migrations")`).
- **`Services.DB()`** gives tenant transactions only (`InTx`); there is no raw pool. Packs touch their own tables and
  read core data through `UnitReader`, `PeopleReader`, `LedgerReader`; they write through `LedgerPoster`,
  `PaymentRecorder`, `Notifier`, `FileStore`. The core's invariants (balanced entries, lock date, idempotency, audit)
  apply to pack writes. No service takes an organization argument: the organization is in the context (`sdk.OrgFrom`).
- **Secrets** (`x-secret` settings) are sealed with the core field encryption (`OPENBEKCI_ENCRYPTION_KEY`) and read in
  clear text through `Services.Settings()`.
- Tables of a pack are not part of a site export (`export-site`); document that in your pack if it matters.

## 6. Settings

`SettingsSchema.Schema(scope)` returns a JSON Schema 2020-12 object (scope `org` and, optionally, `site`) with two
extensions: `x-ui` (order, groups, widgets, help keys) and `x-secret: true` on write-only fields: stored encrypted,
never returned (the API lists only the names of the secret fields that are set) and never written to the audit trail,
which records key names only. Property names start with `<id>_`. The core validates on write; implement
`ValidateSettings` for cross-field and live checks (return `sdk.ErrCredentials` for refused credentials).
Read with `Services().Settings().Get(ctx, sdk.OrgScope(), key)`.

## 7. Presets

Presets do not live in pack settings; they fill **core site settings** so a site without a pack can configure the same
rules by hand and a site with a pack can still deviate where its management plan allows. Each of the six interfaces
returns typed defaults that the core writes under one site setting key (`quorum`, `thresholds`, `vote_weight`, `schedule`,
`transfer_rule`, `late_fee_policy`), plus `legal_preset` naming the pack. They are applied when a site is created in an
organization whose country is one of the pack's `Countries`, and when the pack is enabled, **only to settings the site
has not changed**. `GET .../sites/{site}/settings` returns `pack_defaults` (key to pack and value) for the keys that
still hold what the pack wrote: the "pack default" marker. A failing or panicking preset is skipped and logged.

How the core uses the values (wired in P1): governance reads `quorum`, `thresholds`, `vote_weight` and `schedule` from
the site settings and falls back to its **generic defaults** (a majority of the units present on the first call, no
quorum on the second, a simple majority of those present for every topic, one vote per unit without caps, 15 days'
notice, a second meeting 7 to 15 days later, January) for a site without them; settings that are more permissive than
the site's defaults need a deviation reason. Billing enforces `late_fee_policy.monthly_rate_ceiling_bps` when a site
sets its own rate (none without a pack) and the site's automatic default fee row follows the preset's default rate and
grace days until the site sets its own. Two fields beyond the first SDK sketch exist for rules the Turkish law has:
`QuorumRule.Basis` may be `units_and_share`, and `VoteWeightDefaults` has an exact per-person cap fraction and a
small-building proxy limit.

## 8. Localization

- **Labels** for pack-provided codes (template names, preset names, validator codes, address field labels, consent
  names) come from `Catalog.Labels(locale)`, keys `<id>.<key>`. The core merges the catalogs of the organization's
  enabled packs into the `pack_labels` section of `GET /v1/orgs/{org}/codes?locale=`; the answer's locale is laid over
  English, so an untranslated key falls back to English and then to the raw code.
- **Documents**: a `DocumentTemplate` declares its `Locales()` and returns `text/markdown` (a small subset, see
  `sdk.Document`) that the core typesets with its own fonts; the core asks for a template of a `Kind` in the
  organization's (or the site's) locale and renders its generic document when no enabled pack has one, or when the
  template fails. The data each template receives is listed per kind in the pack's README (`packs/tr/README.md`,
  "Document data"). Kinds without a generic equivalent (`delivery_checklist`, `proxy_form`, `decision_book_draft`) are
  served by `GET .../meetings/{meeting}/documents/{kind}` only when a pack provides them.
- **Consent texts**: `ConsentTexts.Text(locale)` returns Markdown with a version, per locale; clients show it and
  record the version. `GET /v1/orgs/{org}/privacy/consents` serves the enabled packs' texts, else a generic notice.
- **Reference data** (`GET /v1/orgs/{org}/reference/{kind}?parent=`): administrative areas, holidays; `address_format`
  returns the field list. Names are in the language of the data; labels of fields are catalog keys.
- **Chart of accounts**: `ChartOfAccounts.Names(locale)` renames the site's system accounts by code in the
  organization's default locale (the chart is created when the pack is enabled or the site is created, so the names
  apply from the start); names only, never structure.
- **Validators** report failures with a code (`Code() string`, a catalog key such as `tr.validator.tax_id.checksum`);
  the core returns `invalid_format` with `params.validator` set to it, and the client labels it through the codes
  endpoint. The core validates vendor tax ids, vehicle plates (also in the unit import) and the IBAN of the site's
  bank details through the organization's enabled packs after its own checks.

## 9. Legal content

A pack that encodes law has a `LEGAL.md`: each rule with the article, the date of the law text consulted, known
ambiguities, and which settings the management plan may override; it opens with the disclaimer (not legal advice,
organizations remain responsible) and says what the software does **not** do (the Turkish pack lists tax and employer
obligations). Values the author could not confirm stay marked **(verify)** until the legal maintainer signs them off,
and the text says so rather than implying a check. Scenario tests double as documentation: name them like cases ("24
units, 18 owners present holding 55 % of shares, first call: quorum met") and evaluate the preset values the way the
core does. `packs/tr/LEGAL.md` and `packs/tr/presets_test.go` are the reference. See plan 04-packs.md section 5 and 6.4.

## 10. Payment providers

The provider's job is to talk to the provider: `CreateCheckout`, `Status`, `HandleWebhook` (verify the signature
first, parse, return events; record nothing), `Refund` (idempotent on `RefundRef`), `Settlements`. The **core** records:
gross to the resident's receivable, the fee to the fee expense account (`5050`), the net to a per-provider clearing
account (`1190.<provider>`) until the settlement moves it to the bank; refunds are reversals; everything is idempotent
per `(organization, provider, provider_txn_id)`. The webhook URL the provider is configured with is
`POST /v1/orgs/{org}/packs/{pack}/payments/{provider}/webhook`. Map failures to the SDK's errors (`ErrInvalidSignature`
401, `ErrNotFound` 404, `ErrRejected`/`ErrUnsupported`/`ErrCredentials` 422, `ErrUnavailable` 503; anything else is
`pack_error` 500). Partial refunds are unsupported in 0.4.

## 11. Testing

`server/internal/sdktest` boots the core against the local PostgreSQL test database (the same throwaway databases the
core's integration tests use; `OPENBEKCI_TEST_SUPERUSER_URL` as in CI), runs core and pack migrations, creates an
organization and a site from the money golden fixture, enables the pack and drives it through the real registry:

```go
func TestMyPack(t *testing.T) {
	h := sdktest.New(t, acme.New(), sdktest.WithSettings(map[string]any{"acme_api_key": "k"}))
	unit := h.Unit("A-1")
	h.PostCharge(unit, 10000)           // a charge through the billing module
	h.RecordPayment(unit, 10000)        // a payment through the payments module
	rec := h.CallRoute("POST", "/callback", body, "X-Signature", sig) // a pack route, as the server mounts it
	err := h.RunJob("acme.cleanup", nil)
	h.Emit(sdk.Event{Type: sdk.EventPaymentRecorded, IDs: ...})        // delivered like the core does, deduplicated
}
```

The two **conformance suites** cover the interfaces with external side effects:
`sdktest.ConformPaymentProvider(t, newPack, fixture)` (idempotent webhooks, gross/fee/net through clearing, refunds as
reversals, settlement, error mapping, checkout and status) and
`sdktest.ConformStatementImporter(t, newPack, importerID, samples, junk, opts...)` (format detection, totals, row
numbering, raw-payload stripping, an import through the core and the core's raw pruning). See
`server/internal/sdktest/example_test.go` for both.

`sdktest` lives inside the server module (it imports the core), so only packs built in this repository, or in a fork
that adds itself to `Official` and its tests to the server module, can use it. A mode that drives a built binary is a 1.0
item. Provider HTTP interactions are recorded with `go-vcr`; live tests run only with credentials, nightly (*to be written*).

Run everything:

```sh
cd server && go test -p 1 -count=1 ./internal/sdktest/ ./internal/packs/   # needs PostgreSQL, see tools/dev-db.sh
cd packs/example && go test ./...                                          # pack unit tests need only the SDK
cd server && go build -tags nopacks ./... && go build -tags examplepack ./...
```

## 12. Building a server with your pack

Kept minimal: add your module to `server/go.mod` (a `require` and, in development, a `replace`), import it in
`server/cmd/openbekci/packs.go` and append `acme.New()` to `Official`, then `go build ./cmd/openbekci`. A helper
(`tools/obkbuild`, xcaddy style) is a 1.0 item (milestone P3). `tools/newpack` does the `go.work` part for packs inside
this repository.

## 13. Publishing (*to be written*)

Repository layout, tagging, listing in the docs' pack index (builds with `obkbuild`, passes the conformance suites, has
`LEGAL.md`).

## Evolution rules

`sdk/` is versioned independently of the server; v1.0 is frozen at project release 1.0, and until then minor versions may
break with notes. Behaviour is added only through new optional interfaces checked by type assertion; an existing
interface is never widened. Breaking changes batch into `sdk/v2`.
