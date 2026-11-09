# SDK reference

The pack SDK is the Go module `github.com/openbekci/openbekci/sdk` (directory `sdk/`, Apache-2.0). It imports nothing from the server. The reference **is** the Go documentation of the module: every interface says which core code calls it and what it must return.

| Where | What |
|---|---|
| `sdk/doc.go` | The package overview and the pack lifecycle. Start here. |
| `sdk/pack.go` | `Pack`, `Manifest`, the optional interfaces (`OnEnable`, `OnDisable`, `ValidateSettings`, `StartupCheck`, `Ready`). |
| `sdk/registry.go` | `Registry`: every `Register*` method, and the namespacing rules the registry enforces. |
| `sdk/services.go` | `Services`: the tenant database, readers (`UnitReader`, `PeopleReader`, `LedgerReader`), writers (`LedgerPoster`, `PaymentRecorder`, `Notifier`, `FileStore`), settings. |
| `sdk/payment.go` | `PaymentProvider`, checkout, webhook and settlement types, the error mapping. |
| `sdk/statement.go`, `sdk/sms.go` | `StatementImporter`, `SMSProvider`. |
| `sdk/presets.go`, `sdk/billing.go` | The six legal preset interfaces, allocation policy and payment order. |
| `sdk/content.go`, `sdk/reference.go`, `sdk/settings.go` | Templates, chart of accounts, consent texts, catalogs, address formats, validators, reference data, settings schemas. |
| `server/internal/sdktest` | The test harness and the two conformance suites: see [Testing](authoring.md#11-testing). |
| `packs/example` | A working pack that implements every extension point. |

Browse the rendered documentation with `go doc github.com/openbekci/openbekci/sdk` from the repository, or the same page on pkg.go.dev once the module is public.

## Versioning

`sdk/` is versioned independently of the server. Behaviour is added only through new optional interfaces, found by type assertion; an existing interface is never widened. Breaking changes batch into `sdk/v2`. Until the 1.0 freeze, minor versions may break with notes, and the server refuses a pack whose `MinSDK` is newer than the SDK it was built with (and, before 1.0, one of another minor line). See the [authoring guide](authoring.md#evolution-rules) for the evolution rules and known limitations.
