# ADR-0001: OpenAPI code generator for the Go server

| | |
|---|---|
| Status | **Accepted** |
| Date | 2026-11-07 |
| Deciders | project owner (plan default), spike run by an agent |
| Supersedes | the "spike `ogen` in the skeleton phase" note in `docs/plans/01-backend.md` §2 |

## Context

The API is contract-first (`docs/plans/00-overview.md` §6.2, `docs/plans/01-backend.md` §8): OpenAPI is authored in the **3.0.3-compatible subset**; enums are **open** (clients must tolerate unknown values); errors are RFC 9457 `application/problem+json` with a stable `code`; lists use cursor pagination; money is `{amount:int64, currency}`; and the spec carries vendor extensions (`x-openbekci-roles`, `x-idempotent`, `x-ui`, `x-secret`, `x-stability`) that the server's middleware (authorization matrix, idempotency, stability gating) must be able to read. The server is `net/http` + `chi` v5 with `kin-openapi` request validation (always on in dev/test, configurable in prod).

Two Go generators were candidates: `oapi-codegen` (plan default, strict-server mode) and `ogen`. The plan asked for a spike in the skeleton phase; this ADR records it. Switching away from the default requires ogen to win clearly on validation/open-enum ergonomics **without** a worse middleware fit.

## The spike

`spike.yaml` (OpenAPI 3.0.3, 160 lines) exercised: `Money`; RFC 9457 `Problem` with a `code` enum; a cursor-paginated list (`limit`, `cursor` -> `next_cursor`); `nullable: true` string/date fields (including a required-but-nullable `next_cursor`); an enum that must be open; `oneOf` + `discriminator` with mapping; a POST with a required `Idempotency-Key` header and `x-idempotent: required`; an operation with `x-stability: preview`; an `x-openbekci-roles` object; UUID path parameters; `date-time` and `date` fields. A tiny handler and test per generator sent an unknown enum value, a malformed body, a wrong-typed field, a missing required header, a bad UUID and an out-of-range query parameter, and returned an unknown enum value in a response.

Versions used: `oapi-codegen` v2.8.2, `ogen` v1.24.0, `kin-openapi` v0.152.0, `oapi-codegen/nethttp-middleware` v1.3.1, `oapi-codegen/runtime` v1.8.0, `chi` v5.5.0, `openapi-typescript` 7.13.0, Go 1.27.3 on Windows 11.

## Options considered

| | `oapi-codegen` v2.8.2 (strict-server, chi) | `ogen` v1.24.0 |
|---|---|---|
| Compiles, tests pass | Yes | Yes |
| Generated size | 1,097 lines in 1 file (incl. gzip+base64 embedded spec); server + models only | 5,772 lines in 17 files with client; about 4,774 lines without the client-only files (no switch to omit the client was found) |
| Build time, only the generated package (deps cached) | 8.1 s | 7.3 s |
| Build time, cold cache incl. std and deps | 97.9 s | 99.6 s |
| Nullable | `*T` with `omitempty` by default (null and absent are indistinguishable); with `output-options.nullable-type: true` it uses `nullable.Nullable[T]` (tri-state, a small extra dependency). Required+nullable (`next_cursor`) is a plain `*string` with no `omitempty`, so it marshals as `null` | `OptNilString` / `NilString` tri-state types, always. Footgun: the zero value of a required+nullable `NilString` marshals as `""`, not `null` (observed: `"next_cursor":""`); handlers must call `SetToNull()` or use the constructors |
| Open enum, request | `kin-openapi` middleware rejects unknown value (400, "value is not one of the allowed values") - the same as a closed enum; fine for input | Rejects (400) in generated decoder |
| Open enum, response, server side | Typed `string` alias with constants and `Valid()`; marshals any value; no response validation | Marshals any value (response validation is an opt-in feature) |
| Open enum, response, **client side** | Decodes into the `string` alias; unknown value accepted and testable with `Valid()`. (Go SDK client not generated in the spike; behaviour follows from the model type having no validator) | **Fails the whole response**: `decode response: validate: invalid: items ... status (invalid value: frozen)`. Generated client validation cannot be switched off; only workaround is to drop `enum` from the schema (losing typed constants) |
| `oneOf` + discriminator | `union json.RawMessage` with `AsCardMethod()`, `FromCardMethod()`, `Discriminator()`, `ValueByDiscriminator()` (unknown discriminator returns an error you handle; the raw payload is preserved) | Sum type struct with `Type` tag and one field per variant; unknown discriminator fails decode |
| Malformed body | 400 from kin middleware (`failed to decode request body: unexpected EOF`) or from `RequestErrorHandlerFunc`; body is ours to format as problem+json | 400 `{"error_message": ...}`, reformattable through `WithErrorHandler` / `NewError` |
| Wrong-typed field, missing header, bad UUID, out-of-range `limit` | All 400 with precise messages (schema path, parameter name); UUID format is checked by the generated wrapper (plain-text 400 from the default handler, overridable with `ErrorHandlerFunc`) | All 400, validation built in with no extra dependency |
| Request validation | Separate `kin-openapi` middleware (the plan's choice) plus generated parameter binding; response validation not provided | Built in, always on, not configurable per environment |
| Vendor extensions at runtime | `GetSwagger()` returns the embedded `*openapi3.T`; `operation.Extensions["x-idempotent"]` etc. read directly (verified for `x-idempotent`, `x-stability`, `x-openbekci-roles`) | Not exposed. Generated code has no embedded spec; `middleware.Request` has `OperationID` only. Would need to load the spec separately (`go:embed` + kin-openapi/libopenapi) and map by operation id |
| Fit with chi middleware stack | Native: `HandlerWithOptions` with `ChiServerOptions{BaseURL, BaseRouter, Middlewares}`; chi routes, `chi.URLParam`, route groups and `r.Use` all work; `StrictHTTPServerOptions` for error mapping | `Server` is an `http.Handler` that does its own routing; mounts under chi (`r.Mount`/`WithPathPrefix`) so chi middleware wraps everything, but per-route chi middleware and chi route context are not available; operation-level hooks via ogen `middleware.Middleware` (has `OperationID`) |
| Spec features (3.0.3 subset) | `nullable`, `oneOf`/discriminator, `$ref`, `default` response all fine | Same, with warnings (for example "status matching HTTP code will not be enforced") |
| TypeScript side | Not applicable | Not applicable |

TypeScript (`openapi-typescript` 7.13.0, independent of the Go choice): `nullable: true` becomes `note?: string | null` and a required nullable is `next_cursor: string | null`; enums become **closed** literal unions (`status: "draft" | "issued" | "paid" | "void"`), which the compiler will treat as exhaustive; a discriminator adds the literal type to each variant (`kind: "card"`); vendor extensions are dropped.

## Decision

Keep **`oapi-codegen` v2.8.2** in strict-server mode with chi. `ogen` does not win clearly: its built-in validation is convenient, but its client rejects unknown enum values (the opposite of the project's open-enum rule, with no switch to disable it), it hides vendor extensions from middleware, it owns routing (worse fit with the chi stack and kin-openapi middleware the plan already chose) and it generates about 4x the code. The embedded spec plus `GetSwagger()` is the decisive feature: the authorization matrix, idempotency enforcement and stability gating can all be driven from `x-*` extensions on the operation that matched.

## Consequences

- One generated file per API package; spec is embedded and used at runtime for `kin-openapi` validation and for reading extensions. Generated code is committed (`go generate`-reproducible; CI fails on drift).
- Request validation is not generated: we rely on the `kin-openapi` middleware (as planned) plus generated parameter binding. Errors from both must be mapped to problem+json (`validation_failed`) by our own handlers; the spike's `ErrorHandlerWithOpts` and `StrictHTTPServerOptions` show the hooks.
- Responses are **not** validated; the server must be tested against the spec (contract tests) rather than relying on runtime checks. Unknown enum values on output are possible and harmless to the Go side.
- Enum fields in requests that are semantically open (rare; filters) must be declared without `enum` (plain `string` with the known values in the description), otherwise the validator rejects new values.
- The `nullable-type: true` option adds the `github.com/oapi-codegen/nullable` dependency; accepted to distinguish absent vs null in PATCH-style bodies.
- Generated TypeScript enums are closed unions; the web/SDK layer must wrap them (see below) so the UI tolerates unknown values. Dart/Kotlin generators were **not** spiked here and need their own check.
- Tool pinning: the generator is invoked only through `go tool` / `go run` at a pinned version, never installed globally.
- Revisit if `ogen` gains a switch to disable client-side enum validation and a way to expose operation extensions.

## Conventions for the spec (oapi-codegen v2.8.2)

Generator invocation (pin the version; run from `server/`):

```
# in go.mod:  tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen  (require v2.8.2)
go tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
# equivalent without a tool directive:
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2 -config api/oapi-codegen.yaml api/openapi.yaml
```

```yaml
# api/oapi-codegen.yaml
package: api
output: internal/api/api.gen.go
generate:
  chi-server: true
  strict-server: true
  models: true
  embedded-spec: true
output-options:
  nullable-type: true      # nullable.Nullable[T]; omit to get *T
```

Runtime pins used in the spike: `github.com/oapi-codegen/runtime` v1.8.0, `github.com/oapi-codegen/nethttp-middleware` v1.3.1 (set `SilenceServersWarning: true`), `github.com/getkin/kin-openapi` v0.152.0, `github.com/go-chi/chi/v5` v5.5.0. TypeScript: `pnpm dlx openapi-typescript@7 spec.yaml -o spec.d.ts` (7.13.0 tested).

Rules:

1. **OpenAPI 3.0.3 only.** Nullability is `nullable: true` (never `type: [string, "null"]` or 3.1 constructs). A required-but-nullable field is listed in `required` and `nullable: true` (for example `next_cursor`); an optional-nullable one is not in `required`.
2. **Open enums.** Write response enums as `type: string` + `enum: [...]` and state "open enum" in the description. Go gets a typed alias with constants and `Valid()`; on the server and in Go clients never validate output with `Valid()` for rejection, only to branch. In TypeScript the generated union is closed: clients must read it through an `Open<T>` helper (`T | (string & {})`) and always have a default branch. Enums that are semantically open **on input** (filters) have no `enum` list, since request validation rejects unknown values.
3. **Problem responses.** One `Problem` schema and one `components/responses/Problem` (`application/problem+json`) used as the `default` response of every operation; `code` is an enum (open by rule 2).
4. **Vendor extensions** sit on the operation (or schema/property for `x-ui`, `x-secret`) as plain JSON values and are read at runtime from `GetSwagger()` (`op.Extensions["x-idempotent"]`, values arrive as `json.RawMessage`/decoded `any` as in the spike; decode defensively). `x-idempotent` is `required` or `optional`; `x-stability` is `stable` or `preview`; `x-openbekci-roles` maps role to a list of permission strings. Never rely on extensions being propagated to generated types; they are not.
5. **Discriminated unions.** `oneOf` with `discriminator.propertyName` and an explicit `mapping`; every variant declares the discriminator property as `type: string` (not a single-value enum); use `Discriminator()` / `ValueByDiscriminator()` and handle the unknown-discriminator error.
6. **Idempotency.** `Idempotency-Key` is a header parameter (`required: true` where `x-idempotent: required`, with `minLength`/`maxLength`), declared on each such operation.
7. **Operation ids** are `<module>.<verb><Noun>`; the generator turns them into Go names (`billing.listInvoices` -> `BillingListInvoices`).
8. **Pagination.** `limit` (integer, `minimum`/`maximum`/`default`) and `cursor` (string) query parameters; response `{items: [...], next_cursor: <nullable string, required>}`.
9. **Server URLs.** The spec has `servers: [{url: /v1}]`; mount with `ChiServerOptions{BaseURL: "/v1"}` so the validator and router agree.

## Not measured

- Dart generator behaviour (planned mobile client) and the Go SDK client from either tool.
- Whether an unknown discriminator value is rejected by `ogen` at decode time was read from the generated code, not tested.
- Cold build times were taken on a busy machine (a concurrent `go get` was running) and are indicative only; warm package rebuild times are single runs.
