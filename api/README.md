# OpenBekci API

The HTTP API is contract-first: the OpenAPI document is the source of truth, and the Go server
(`server/internal/httpapi/gen`) and the TypeScript types (`clients/ts`) are generated from it.

```
api/src/openapi.yaml        root: info, security schemes, paths and components as $refs
api/src/paths/*.yaml        one file per path item
api/src/components/*.yaml   shared schemas, parameters, responses
api/openapi.yaml            bundled artifact (generated, committed; this is what generators read)
api/.redocly.yaml           lint ruleset;  api/plugins/openbekci.cjs  the project rules
```

## Workflow

1. Edit `api/src/**`.
2. `tools/task.ps1 gen` (Linux/macOS: `tools/task.sh gen`) bundles, lints, regenerates the Go server, sqlc
   and the TypeScript types. `api:lint` runs only the linter.
3. Commit the source and the generated files together. CI runs `gen:check`, which regenerates and fails on drift.

Tools run through `pnpm dlx` (Redocly CLI pinned to 2.76.0, openapi-typescript 7) and `go tool`
(oapi-codegen v2.8.2); nothing is installed globally. Rationale for the generator: `docs/adr/0001-openapi-codegen.md`.

## Conventions

Backend plan section 8 and overview section 6.2 are authoritative; this is the working summary.

- **OpenAPI 3.0.3 only**, in its subset. Nullability is `nullable: true` (never `type: [x, "null"]`). A
  required-but-nullable field is in `required` and `nullable: true` (for example `next_cursor`).
- **Paths** are written in full: `/v1/orgs/{org_id}/<resource>[/{id}]`, `/v1/auth/...`, `/v1/me/...`,
  `/v1/platform/...`, plus `/.well-known/openbekci.json`. The spec has `servers: [{url: /}]` (not `/v1`),
  because the discovery document lives outside `/v1`.
- **`operationId`** is `<module>.<verb><Noun>` and matches `^[a-z]+\.[a-z][A-Za-z]+$`
  (`instance.getInstance` becomes the Go method `InstanceGetInstance`).
- **JSON, snake_case** property names. IDs are UUIDv7 (`format: uuid`), timestamps RFC 3339 UTC, dates `YYYY-MM-DD`.
- **Money** is `Money {amount: int64 minor units, currency}`.
- **Lists** use cursor pagination: query parameters `Limit` (1..200, default 50) and `Cursor`; response
  `{items: [...], next_cursor: <string|null, required>}` (`PageInfo`). No `updated_since`/ETag.
- **Open enums.** Enums in responses are open: clients must tolerate unknown values. Write them as
  `type: string` + `enum: [...]` + `x-open-enum: true`, and say "open enum" in the description. Go gets a typed
  alias (never reject output with `Valid()`); TypeScript clients wrap the closed union in `Open<T>`
  (`clients/ts/open-enum.d.ts`). Enums that are open **on input** (filters, ids such as an auth method) have no
  `enum` list: request validation would reject new values. Put the known values in the description or in
  `x-known-values: [...]`.
- **Errors.** Every 4xx/5xx response, and a `default`, is `$ref: '#/components/responses/Problem'` (the lint rule
  enforces it). The body is RFC 9457 `application/problem+json`:

  ```json
  {
    "type": "about:blank", "title": "Unprocessable Entity", "status": 422,
    "code": "validation_failed", "detail": "the request is not valid",
    "instance": "<request id>",
    "errors": [ { "field": "limit", "code": "too_large", "params": { "max": 200 } } ]
  }
  ```

  `code` is a stable open enum (clients translate by it): `validation_failed` (422), `bad_request` (400, malformed
  request), `unauthenticated` (401), `not_found`, `conflict`, `version_conflict`, `idempotency_mismatch` (422),
  `locked_period`, `insufficient_permission` (403), `reauth_required`, `mfa_required` (reserved), `invalid_grant`,
  `rate_limited` (429), `pack_error`, `site_offboarded`, `internal_error` (500), `payload_too_large` (413; JSON request
  bodies are capped at 1 MiB, `OPENBEKCI_MAX_BODY_BYTES`, before validation). `instance` is the request id,
  also sent as `X-Request-Id`. `errors[]` carries per-field problems; `field` is a dotted path or the query/header
  parameter name, `code` one of `required`, `invalid`, `invalid_type`, `invalid_format`, `invalid_value`,
  `too_short`, `too_long`, `too_small`, `too_large`, `unknown_field`.
- **Security schemes** `cookieAuth` (session cookie) and `bearerAuth`; a public operation says `security: []`.
- **Idempotency.** Every `POST` that creates declares `x-idempotent` (the lint rule and the conformance test
  enforce it), and a required one also declares `Idempotency-Key` as a required header parameter (`IdempotencyKey`).
- **Discriminated unions:** `oneOf` with `discriminator.propertyName` and an explicit `mapping`; each variant
  declares the property as plain `type: string`.

## Vendor extensions

Defined once here; read at runtime from the embedded spec (`gen.GetSwagger()`), never from generated types.

| Extension | On | Values | Effect |
|---|---|---|---|
| `x-openbekci-roles` | operation | map role to a list of permission strings | the authorization matrix (wired with the first business modules) |
| `x-idempotent` | operation | `optional` or `required` | `Idempotency-Key` is honoured or mandatory (422 `validation_failed` without it). Same key and request replays the stored response; same key, different request is 422 `idempotency_mismatch`; a request still running is 409 `conflict`; keys live 7 days, scoped by (org, principal) |
| `x-ui` | schema or property | uiSchema hints | schema-driven settings forms |
| `x-secret` | property | `true` | write-only setting field, never returned |
| `x-stability` | operation | `stable` (default) or `preview` | `preview` adds `X-OpenBekci-Stability: preview` to responses; clients may depend only on `stable` |
| `x-open-enum` | schema | `true` | marks an open enum (see above) |
| `x-known-values` | schema | list | documents the known values of an open string without an `enum` |

## Request pipeline

For each operation, in order: response validation (when `OPENBEKCI_API_VALIDATE_RESPONSES=true`; always on in
tests) wraps stability header, request validation (kin-openapi; problem+json), authentication (only for operations
with `security`), idempotency, then the generated strict handler. Unknown paths under `/v1/` and
`/.well-known/` are 404 problems; everything else falls through to the web app.
