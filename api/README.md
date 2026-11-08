# OpenBekci API

The HTTP API is contract-first: the OpenAPI document is the source of truth, and the Go server
(`server/internal/httpapi/gen`) and the TypeScript types (`clients/ts`) are generated from it.

```
api/src/openapi.yaml        root: info, security schemes, paths and components as $refs
api/src/paths/*.yaml        one file per path item
api/src/components/*.yaml   shared schemas, parameters, responses
api/openapi.yaml            bundled artifact (generated, committed; this is what generators read)
api/src/openapi-draft.yaml  draft root for releases 0.1 to 0.3 (see below); bundled to api/openapi-draft.yaml
api/.redocly.yaml           lint ruleset;  api/plugins/openbekci.cjs  the project rules
```

## Workflow

1. Edit `api/src/**`.
2. `tools/task.ps1 gen` (Linux/macOS: `tools/task.sh gen`) bundles, lints, regenerates the Go server, sqlc
   and the TypeScript types. `api:lint` runs only the linter.
3. Commit the source and the generated files together. CI runs `gen:check`, which regenerates and fails on drift.

Tools run through `pnpm dlx` (Redocly CLI pinned to 2.76.0, openapi-typescript 7.13.0) and `go tool`
(oapi-codegen v2.8.2); nothing is installed globally. Rationale for the generator: `docs/adr/0001-openapi-codegen.md`.

## Conventions

**Layout notes.** `servers: [{url: /}]` with full paths (`/v1/...`, `/.well-known/openbekci.json`) is the
convention: the discovery document lives outside `/v1`, so the server root cannot be `/v1`. The generator config
`oapi-codegen.yaml` lives in `server/` (next to the generated code's module), not in `api/`.

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
  `rate_limited` (429, with `Retry-After`), `pack_error`, `site_offboarded`, `internal_error` (500), `csrf_failed` (403, cookie-session mutations without the right `Origin` and `X-Requested-With`), `sms_unavailable` and `mail_unavailable` (503), `method_not_allowed` (405, with `Allow`), `unsupported_media_type` (415), `timeout` (504), `payload_too_large` (413; JSON request
  bodies are capped at 1 MiB, `OPENBEKCI_MAX_BODY_BYTES`, before validation). `invalid_grant` is a 401, and 401 responses carry
  `WWW-Authenticate`. `instance` (also `request_id`) is the request id,
  also sent as `X-Request-Id`. `errors[]` carries per-field problems; `field` is a dotted path or the query/header
  parameter name, `code` one of `required`, `invalid`, `invalid_type`, `invalid_format`, `invalid_value`,
  `too_short`, `too_long`, `too_small`, `too_large`, `unknown_field`.
- **Security schemes** `cookieAuth` (session cookie) and `bearerAuth`; **every operation declares `security`
  explicitly** (a lint rule and the conformance test enforce it): a public one says `security: []`, optional
  authentication is `[{}, {bearerAuth: []}, ...]`. An operation that somehow lacks the key is treated as requiring
  authentication.
- **Idempotency.** Every `POST` that creates declares `x-idempotent` (the lint rule and the conformance test
  enforce it), and a required one also declares `Idempotency-Key` as a required header parameter (`IdempotencyKey`).
- **Discriminated unions:** `oneOf` with `discriminator.propertyName` and an explicit `mapping`; each variant
  declares the property as plain `type: string`.

## Vendor extensions

Defined once here; read at runtime from the embedded spec (`gen.GetSwagger()`), never from generated types.

| Extension | On | Values | Effect |
|---|---|---|---|
| `x-openbekci-roles` | root | map role (the seven built-in roles and `platform_admin`) to its permission names | the role to permission matrix; **generated** from the Go permission registry into `components/permissions.yaml` by `task gen:permissions`, never edited by hand |
| `x-openbekci-roles` | operation | list of permission names, or the draft's map role to permission names | the permission(s) the operation requires, all of them (in the map form, the distinct permission names of all roles; each draft operation names exactly one). Mandatory under `/v1/orgs/{org_id}`; optional documentation elsewhere. The authz matrix test fails when it is missing or does not match what the handler requires |
| `x-idempotent` | operation | `optional`, `required` or `none` | `Idempotency-Key` is honoured or mandatory (422 `validation_failed` without it). Same key and request replays the stored response; same key, different request is 422 `idempotency_mismatch`; a request still running is 409 `conflict`; keys live 7 days, scoped by (org, principal). A replay sets `Idempotent-Replayed: true` and restores only `Content-Type`, `Location` and `X-OpenBekci-Stability`. Keys need a signed-in caller: a `required` operation must require authentication (conformance test), and an anonymous call to an `optional` one ignores the header. `none` (operations whose response carries a one-time secret, such as an enrolment token) never stores the response and ignores the header |
| `x-ui` | schema or property | uiSchema hints | schema-driven settings forms |
| `x-secret` | property | `true` | write-only setting field, never returned |
| `x-stability` | operation | `stable` (default) or `preview` | `preview` adds `X-OpenBekci-Stability: preview` to responses; clients may depend only on `stable` |
| `x-open-enum` | schema | `true` | marks an open enum (see above) |
| `x-known-values` | schema | list | documents the known values of an open string without an `enum` |
| `x-requires-setting` | operation | a site setting name | the operation exists only while that site setting is on (for example `bills_enabled`); otherwise 404 `not_found` |

## Request pipeline

Before routing, on every request: request id, logging, panic recovery, the body-size cap (413 `payload_too_large`),
timeout. Then, for each operation, in order: response validation (when `OPENBEKCI_API_VALIDATE_RESPONSES=true`;
always on in tests) wraps stability header, authentication, request validation (kin-openapi; problem+json),
idempotency, then the generated strict handler. Authentication runs first, so an unauthenticated caller gets 401
(`WWW-Authenticate: Bearer`) and never a validation detail. `Authenticate` is called for every operation whose
`security` is not `[]`: required ones answer 401 on failure, optional ones (`security: [{}, ...]`) continue
anonymously when no valid credential is present. Unknown paths under `/v1` and `/.well-known/` are 404 problems,
a known path with the wrong method is a 405 problem with `Allow`; everything else falls through to the web app.

## The draft contract (releases 0.1 to 0.3)

`api/src/openapi-draft.yaml` is a second root that `$ref`s everything: the paths already served plus the contract for
releases 0.1, 0.2 and 0.3 (`api/src/paths/<module>/*.yaml`, `api/src/components/<module>.yaml`). It is bundled to
`api/openapi-draft.yaml` and generates `clients/ts/draft.d.ts`; web builders work against those. Nothing in it is served.
The Go generator never reads it. Both roots are linted with the same strict rules (`api:lint`), and
`server/internal/httpapi/draft_spec_test.go` holds every draft operation to the conventions (explicit `security`,
`x-openbekci-roles` on org-scoped operations, `x-idempotent` on mutations, pagination, permission list).

**Adopting an operation (backend builders).** In `src/openapi.yaml` add the same `paths:` entry the draft has (it points at
`./paths/<module>/<file>.yaml`), and add the schemas the operation references to `components.schemas` (copy the entries from
the draft root). Run `tools/task.ps1 gen`; the Go server interface grows the new methods. Leave the draft root as it is:
it keeps listing everything, so web types do not change when an operation moves to the served spec. Update the operation's
`x-stability` only when the release it belongs to ships. Checklist of operations: `api/DRAFT-INDEX.md`; permission names:
`api/PERMISSIONS.md`.

Draft-only conventions on top of the list above:

- **Site scoping:** `site_id` is a query parameter on lists (required where the resource is site-scoped) and a body field on
  create; resources that are plainly part of a site live under `/sites/{site_id}/...` (settings, bank details, lock date).
- **Updates** are `PATCH` with `If-Match: "<version>"` (stale: 409 `version_conflict`); every updatable read model returns `version`. Singleton settings written with `PUT` (governance settings, budget lines, late-fee policy, lock date, notification defaults, polls, unit tag members) take the same required `If-Match`.
- **Re-authentication.** Operations that answer 403 `reauth_required` without a re-authentication in the last 10 minutes (backend plan section 16): contact and password change, moving the lock date back, opening-balance create, void and import, waivers above the site threshold, ending a site-owned membership, site export, import and offboard, erasing a person.
- **Idempotency.** Every operation that moves money, sends messages or creates a rule that generates charges is `x-idempotent: required` (dunning runs, decision effects, fiscal-year close and approval, plan rates, statements, announcements and decision notices included).
- **Dates and periods.** A `format: date` field is named `*_on` (`paid_on`, `booked_on`, `opened_on`, `closed_on`); older names such as `due_date`, `as_of` and `valid_from` stay; `*_at` is always a timestamp. A `period` is the accrual month `^\d{4}-(0[1-9]|1[0-2])$` for every plan frequency; there are no quarter or year period strings.
- **Debtors.** `DebtorRef` addresses who owes: `(unit_id, debtor_kind)` or `person_id` (carried debt of a former occupant), exactly one mode.
- **Custom actions** use the Google style suffix: `/charges/{id}:cancel`, `/charge-plans:preview`, `/units:import`.
- **Reports and statements** take `format=json|csv|xlsx|pdf` (`json` default) and answer with the matching content type;
  `POST /exports` is for jobs too big for that.
- **Imports** take `dry_run=true` and return `ImportReport`; committing returns 202 with a job id (`GET /jobs/{id}`).
- **Problem codes added for the draft:** `agent_offline` (409) and `agent_session_expired` (410) are in `Problem.code`
  (the draft needs no other code beyond the served enum). Statuses used: `locked_period` 409, `version_conflict` 409, `site_offboarded` 409,
  `reauth_required` 403, `idempotency_mismatch` 422.
- **Agent transport** (`/v1/agent/*`, tag `agent`) authenticates with the `agentAuth` bearer scheme (an agent token, not a user
  session); the message schemas are JSON Schema 2020-12 in `api/agent-protocol/v1/*.schema.json`, referenced by the
  `x-agent-message-schemas` extension. `x-stability: preview` until 0.3.0 ships.
