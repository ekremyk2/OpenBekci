// Package sdk is the public OpenBekci pack SDK.
//
// It is a separate Go module licensed under Apache-2.0 so that packs can be
// built against it without any copyleft obligation (LICENSING.md). It imports
// nothing from the server: the server imports the SDK, never the other way.
//
// # What a pack is
//
// A pack is a Go module that implements [Pack], is compiled into the server
// binary through an explicit list (cmd/openbekci/packs.go), and is enabled per
// organization. It contributes implementations for the extension points
// documented on the interfaces of this package, and nothing else. The core
// never contains country-specific branches: it asks the registry which
// implementation the organization's enabled packs provide.
//
// # Lifecycle
//
//	Compile   the pack is listed in the server's Official slice (no init()).
//	Startup   Manifest is read, MinSDK checked, Register called, StartupCheck
//	          run, namespacing validated, the registry frozen.
//	Migrate   core migrations, then each pack's [Registry.RegisterMigrations]
//	          with its own goose table pack_<id>_goose_version.
//	Enable    per organization: settings validated, OnEnable called, presets
//	          applied to the sites' unchanged settings.
//	Running   implementations are selected only for organizations where the
//	          pack is enabled; routes answer 404 otherwise.
//	Disable   OnDisable called; data retained; implementations stop being
//	          selected. Settings filled by presets stay.
//	Purge     openbekci admin packs purge --org --pack, after confirmation.
//
// # Namespacing
//
// Every identifier a pack contributes is prefixed with its pack id: provider,
// importer, preset, template, validator and catalog ids are "<id>" or
// "<id>.<name>"; settings keys are "<id>_<name>"; job kinds "<id>.<name>";
// tables "pack_<id>_<name>"; routes live under /v1/orgs/{org}/packs/{id}/. The
// registry rejects collisions at startup with a named error.
//
// # Tenant context
//
// The organization a call acts for travels in the context ([OrgFrom]); the
// [Services] never take an organization argument, so a pack cannot reach into
// another tenant.
//
// # Wiring status (milestone P0)
//
// The registry validates, namespaces and freezes every contribution below, and
// the host exposes each one for the organizations that enabled the pack. These
// core call sites are wired and tested in P0: payment providers (checkout,
// status, webhook route, refund and settlement entry points of the payments
// module), SMS providers (organization setting sms_provider, selected by the SMS
// gateway for messages sent for an organization), statement importers (the bank
// import module), the six presets and the chart of accounts (site creation and
// enablement), settings and their secrets, migrations, routes, jobs and event
// subscribers. Milestone P1 (the Turkish pack) wires the content contributions:
// document templates (governance call letter, attendance list and minutes, the
// receipt, statements, the ihtarname and the pack-only documents), validators
// (vendor tax ids, vehicle plates, the IBAN of the bank details, the unit
// import), reference data and address formats (GET .../reference/{kind}),
// consent texts (GET .../privacy/consents) and label catalogs (the codes
// endpoint), and the presets are read by governance and billing from the site
// settings. Allocation policies and payment orders are registered and
// selectable but their consuming
// modules (billing generation, payment allocation) call them as a pack needs
// them; the call sites documented on each interface are the intended ones.
//
// # Evolution
//
// Add behaviour only through new optional interfaces checked by type
// assertion; never widen an existing interface. Breaking changes batch into
// sdk/v2. From 1.0.0 the SDK is frozen: exported names, signatures and documented
// behaviour do not change within v1 (sdk/CHANGELOG.md, docs/packs/README.md).
package sdk
