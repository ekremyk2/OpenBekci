# OpenBekci server

Go modular monolith: API, embedded web app and job worker in one binary, PostgreSQL as the only dependency. Architecture: [docs/plans/01-backend.md](../docs/plans/01-backend.md). Licensed AGPL-3.0-or-later with the pack-linking exception ([LICENSING.md](../LICENSING.md)).

## Local setup (Windows; use `tools/dev-db.sh` and `tools/task.sh` elsewhere)

```powershell
$env:PATH = "C:\Program Files\Go\bin;" + $env:PATH        # Go 1.27+ on PATH
tools/dev-db.ps1 init; tools/dev-db.ps1 start             # local PostgreSQL 17 (user process); then:
tools/dev-db.ps1 roles                                    # three roles + openbekci database (dev password: openbekci)
Copy-Item .env.example .env                               # dev settings, listens on :8484
tools/task.ps1 migrate                                    # goose + River migrations, as the migrator role
tools/task.ps1 run                                        # then: curl localhost:8484/readyz
```

`tools/task.ps1` / `tools/task.sh` run the Go-pinned [go-task](https://taskfile.dev) (`go tool task`), so nothing needs installing globally. `tools/task.ps1 --list` shows all tasks: `gen`, `gen:permissions`, `gen:check`, `api:lint`, `lint`, `test`, `test:integration`, `run`, `migrate`, `db:init|start|stop|roles`.

## Commands

```
openbekci serve [--no-worker]     HTTP server (+ job worker unless --no-worker / OPENBEKCI_WORKER=off)
openbekci worker                  job worker only
openbekci migrate up|down|status  as OPENBEKCI_MIGRATOR_DATABASE_URL
openbekci admin create-platform-admin --email|--phone [--password]
openbekci admin create-org --slug --name [--country TR] --admin-email|--admin-phone
openbekci admin set-password --user <email|phone>
openbekci version
```

Configuration is `OPENBEKCI_*` environment variables (secrets also accept `_FILE`), optionally a `--config file.yaml` with the lowercase names (`database_url: ...`); the environment wins. See [.env.example](../.env.example) and [deploy/compose/.env.example](../deploy/compose/.env.example).

## Accounts, organizations and signing in

Local walkthrough (after `tools/task.ps1 migrate`; `.env` from `.env.example` has `SMS_PROVIDER=log` and
`MAIL_PROVIDER=log`, so codes and links are written to the server log instead of being sent):

```powershell
cd server
# an operator account; the password comes from the prompt, OPENBEKCI_ADMIN_PASSWORD or --password
$env:OPENBEKCI_ADMIN_PASSWORD = "correct horse battery"
go run ./cmd/openbekci admin create-platform-admin --email root@example.org
# an organization with its first administrator (created without a password; a one-time sign-in link is printed)
go run ./cmd/openbekci admin create-org --slug gul-sitesi --name "Gul Sitesi" --country TR --admin-email ayse@example.org
go run ./cmd/openbekci admin set-password --user ayse@example.org
go run ./cmd/openbekci serve
```

```sh
# browser-style session: the cookie obk_session is httpOnly; mutations with it also need Origin and X-Requested-With
curl -i -c jar.txt -H 'Content-Type: application/json' localhost:8484/v1/auth/session \
  -d '{"method":"password","identifier":"root@example.org","password":"correct horse battery"}'
curl -b jar.txt localhost:8484/v1/me
curl -b jar.txt localhost:8484/v1/instance
curl -X DELETE -b jar.txt -H 'Origin: http://localhost:8484' -H 'X-Requested-With: openbekci-web' localhost:8484/v1/auth/session

# mobile-style tokens: access token (15 min) + rotating refresh token (60 days)
curl -s -H 'Content-Type: application/json' localhost:8484/v1/auth/token \
  -d '{"method":"password","identifier":"root@example.org","password":"correct horse battery"}'
curl -H "Authorization: Bearer $ACCESS" localhost:8484/v1/me
curl -s -H 'Content-Type: application/json' localhost:8484/v1/auth/token/refresh -d "{\"refresh_token\":\"$REFRESH\"}"

# SMS code (the code is in the server log with SMS_PROVIDER=log), then sign in with it
curl -s -H 'Content-Type: application/json' localhost:8484/v1/auth/session -d '{"method":"sms_otp","identifier":"+905321234567"}'
curl -s -H 'Content-Type: application/json' localhost:8484/v1/auth/session -d '{"method":"sms_otp","identifier":"+905321234567","code":"123456"}'
# magic link (the link is in the log), then exchange its token
curl -s -H 'Content-Type: application/json' localhost:8484/v1/auth/session -d '{"method":"magic_link","identifier":"ayse@example.org"}'
curl -s -H 'Content-Type: application/json' localhost:8484/v1/auth/magic-link/consume -d '{"token":"<token from the link>"}'
```

Methods: `password`, `sms_otp` (needs `SMS_PROVIDER`), `magic_link` (needs `MAIL_PROVIDER` or `SMTP_HOST`);
`GET /.well-known/openbekci.json` lists what is available. With a provider unset, requesting a code or link answers
`503 sms_unavailable` / `mail_unavailable`. `SMS_PROVIDER` is `netgsm` (`SMS_NETGSM_USERCODE`, `SMS_NETGSM_PASSWORD`,
`SMS_NETGSM_MSGHEADER`) or `log`; `MAIL_PROVIDER` is `smtp` (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`,
`SMTP_PASSWORD`, `SMTP_FROM`, `SMTP_TLS=starttls|tls|none`) or `log`. The `log` drivers write sign-in codes and links
to the log and are refused unless `OPENBEKCI_INSECURE_DEV=true`.

Sign-in failures answer `401 invalid_grant` (wrong password, unknown account, wrong or used code, bad link, reused
refresh token); five consecutive failures per account lock it for 30 s, doubling per further failure up to 30 min, and
per-address and per-target send limits answer `429 rate_limited` with `Retry-After`. Sensitive operations need
`POST /v1/auth/reauth` first (valid 10 minutes); changing the password signs the user out everywhere else.

## API spec and code generation

The API is contract-first; conventions are in [api/README.md](../api/README.md). To change the API:

1. Edit `api/src/**` (root `openapi.yaml`, `paths/*.yaml`, `components/*.yaml`).
2. Run `tools/task.ps1 gen`: it bundles to `api/openapi.yaml`, lints (`api:lint`), runs the pinned
   `oapi-codegen` (config `server/oapi-codegen.yaml`, output `internal/httpapi/gen/`), sqlc and
   `openapi-typescript` (output `clients/ts/index.d.ts`).
3. Implement the new method on `apiServer` in `internal/httpapi/api.go` (the compiler lists what is missing) and commit
   source and generated files together. `tools/task.ps1 gen:check` regenerates and fails on drift (CI runs it).

`MountAPI` mounts the generated strict server and a spec-driven middleware chain (request validation to
`application/problem+json`, authentication via `Deps.Authenticator`, `Idempotency-Key` from `x-idempotent`,
`x-stability` header, optional response validation with `OPENBEKCI_API_VALIDATE_RESPONSES=true`). Business code
returns `*problem.Error` (package `internal/problem`) and the HTTP layer renders it. Tools need Node/pnpm for the
Redocly bundle and TypeScript steps (`pnpm dlx`, nothing global).

## Authorization and audit

Rights come from **memberships** (role, scope org/site/block, validity window); occupancies join them in phase B
(`authz.CanAccessUnit` is a documented 403 stub until then). Permissions are string constants declared by the module
that owns them and collected in a registry (`internal/permissions`, an explicit list, no `init()`); the role to
permission matrix and the `Permission` enum are generated from it into the spec (`task gen:permissions`, part of
`task gen`; `gen:check` and `TestPermissionsSpecInSync` fail on drift). A platform administrator holds only
`platform.manage`; to act inside an organization they need a membership there.

For `/v1/orgs/{org_id}/...` the pipeline resolves the caller's active memberships once (`authz.Resolver`, cached in the
context as `authz.Grants`) and answers `404 not_found` when the caller has none there, so organizations do not leak.
Handlers call a service; the **service** calls `authz.Require(ctx, perm, authz.Scope{OrgID: ...})` (a grant covers a
scope when it contains it: org contains site contains block). A site or block scope is **never written by hand**:
`authz.ScopeOfSite(ctx, tx, siteID)` and `authz.ScopeOfBlock(ctx, tx, blockID)` load the parents under RLS through an
`authz.ScopeLoader` that phase B supplies (`authz.SetScopeLoader` in the wiring; the default answers
`ErrScopeUnavailable`), and `TestNoScopeLiteralsOutsideAuthz` fails for an `authz.Scope{... SiteID/BlockID ...}`
literal outside `internal/core/authz` (otherwise a caller could pair their own site with another site's block). Handlers that need
raw SQL use `httpapi.OrgTx(ctx, fn)`, which is `InTenantTx` for the request's organization.

### Adding an endpoint

1. **Permission.** If no existing permission fits, declare it in the module's `permissions.go`
   (`Permissions() []authz.PermissionDef{Name, Description, Roles, Scopes}`; constants next to it), make sure the module
   is in `internal/permissions/permissions.go`, and run `tools/task.ps1 gen`.
2. **Spec.** Add the operation under `api/src/paths/`. Under `/v1/orgs/{org_id}` it **must** carry
   `x-openbekci-roles: [<permission>, ...]` (all are required). Mutations declare `x-idempotent`.
3. **Service.** Call `authz.Require(ctx, perm, scope)` first thing in the service method (not in the handler), then
   work inside `InTenantTx`.
4. **Audit.** Write changes with `audit.Record(ctx, tx, audit.Event{OrgID, Action: "<area>.<verb>", TargetType, TargetID, Diff})`
   inside the same transaction. Build the diff with `audit.Diff(entity, before, after)`: fields registered in
   `internal/platform/pii` (names, contacts, national ids, plates, notes) appear only as hashes. Register any new
   personal data field in `pii.Registered`; `TestSensitiveStructFieldsAreClassified` fails for a struct field named like
   email, phone, national id, plate, note, address or IBAN that is neither registered nor in `pii.Allowlist`.
5. **Matrix row.** Add the operation to `authzMatrixRows` in `internal/httpapi/authzmatrix_test.go`
   (`perms` = what the service requires, `status` = what a permitted caller gets, `build` = the request). The test
   runs every operation against every role, a non-member and a platform administrator, in the own and another
   organization and anonymously, and derives the expected status from the spec; a missing row or a missing
   `x-openbekci-roles` fails with a message saying what to add. Use `override` only for public or auth endpoints with
   their own rules.

`internal/authztest` seeds the world (two organizations, a member per role, a non-member, a platform administrator,
bearer tokens) for module tests that need a signed-in caller. `GET /v1/orgs/{org_id}/audit` (`audit.read`) lists
`audit_event` (cursor pagination over `(at, id)`); events that belong to no organization (password change, session
revocation, platform-administrator promotion) go to `platform_audit_event`, readable by platform administrators through
`GET /v1/platform/audit` (`platform.manage`, cursor pagination). That table has no `org_id` and is **not touched by
privacy erasure** by design: its diffs hold ids and keyed hashes, never personal values, and the IP is stored truncated
to the /24 (IPv4) or /48 (IPv6) network.
Both tables are append-only twice over: the application roles have no UPDATE, DELETE or TRUNCATE, and `BEFORE UPDATE OR
DELETE` / `BEFORE TRUNCATE` triggers owned by the migrator refuse them for every role, so a later `GRANT` cannot reopen
them. Events are stamped with `clock_timestamp()` (insert time); insert order is still not commit order, so an export
that must not miss events pages with `from`/`to` snapshots taken a little in the past. `httpapi.OrgTx` has no caller
yet; it is kept for phase B handlers that need raw SQL in the request's organization. `me.read` and
`sessions.manage_own` are account permissions: every authenticated principal holds them as instance grants, and the
`me` operations check them with `authz.Require(ctx, perm, authz.Scope{})`.

`org.read` and `audit.read` are grantable at org, site and block scope; `org.manage` is org-only. The pipeline checks
the operation's permissions with `Grants.HasInOrg` (held anywhere in the organization) before the idempotency step;
services use `authz.RequireInOrg` and narrow with `Grants.ReachOf`: `audit.list` shows an org-wide holder everything
and a site or block holder only the events of their sites (`site_id`) and blocks (`target_type = block`).

**PII in audit diffs (implementation note).** `audit.Diff` is fail-closed. A field is personal data when
`pii.IsPII(entity, path)` says so: entries for one entity match the dotted path, the `*` entries match the **last
path segment**, case-insensitively, in plural or array form and as the last word of a compound name (`billing.email`,
`owner_phone`, `ContactEmail`, `emails[]`). An entity that is neither registered nor listed in `pii.PlainEntities`
(only `organization` today) records the **names** of the changed keys and no values. A personal value appears as
`k1:` + 32 hex digits: `HMAC-SHA256(key, "entity.field" 0x00 canonical-JSON)` truncated to 128 bits, with
`key = HKDF-SHA256(SECRET_KEY, info "openbekci/audit-pii/v1")`. The hash is domain-separated by entity and field
and depends on the instance secret, so a phone number cannot be brute-forced from the retained trail (erasure
leaves the hashes in place) or matched across fields. `k1` is the key id, so a later key can rotate while old hashes
stay verifiable. `SECRET_KEY` is part of the backup set. The wiring calls `audit.SetPIIKey(cfg.SecretKey)` (in
`newIdentity`); without a key a PII field records that it changed but no hash. See `internal/platform/pii/hash.go`.

## Sites, units and people (phase B)

Modules: `internal/core/tenancy` (sites, blocks, units, allocation keys, tags, vehicles, site export/import, offboarding;
`tenancy.ScopeLoader` implements `authz.ScopeLoader`) and `internal/core/people` (persons, occupancies, memberships,
invitations, join code and claims, directory, move-out, vendors, the spreadsheet import). Both write plain SQL through
`InTenantTx` (no sqlc queries for them); the PK of their tables is `(org_id, id)` so a site can be handed over to another
organization with its identifiers. Every site-scoped foreign key also carries `site_id`.

**Portal rights come from occupancies** (`authz.Grants.Occupancies`, loaded by the Resolver): `authz.CanAccessUnit(ctx,
unit, side)` (current occupancy: owner both sides, tenant the tenant side, household their side only with `may_see_money`),
`authz.CanActOnUnit(ctx, unit)` (any current occupancy), `authz.CanReadUnitAt(ctx, unit, side, day)` (former occupants,
bounded by their dates), `authz.CurrentOccupancy`. A permission that lists `Occupants` in its `PermissionDef` is also held,
at the site, by those occupancy kinds; the service narrows it to the caller's own unit.

**Import** (`POST /v1/orgs/{org}/sites/{site}/import`, multipart `file`, optional `dry_run=true`, optional `mapping`
JSON `{"door_number": "Daire No"}`). CSV (comma, semicolon or tab, UTF-8) or XLSX (first sheet). Columns: `block`,
`door_number` (required), `legal_ref`, `type`, `area_m2`, `arsa_payi` (`12/1000` or decimal), `owner_name`,
`owner_phone`, `owner_email`, `owner_share_pct`, `owner_resides`, `tenant_name`, `tenant_phone`, `tenant_email`,
`plates`, `opening_balance`, `opening_balance_side`, `opening_balance_date`; Turkish header aliases (`Daire No`, `Blok`,
`Malik`, `Plaka`, ...) are recognized. Re-importing the same file changes nothing (units are matched by door number,
people by phone or email); several rows with one door number add co-owners. The response is a row-level report; a real
import with an error row writes nothing (`status: failed`). Opening-balance columns are validated and stored in
`import_row.opening` of the applied batch for the money module (phase C).

**Invitations**: `manager` (to a person the manager added), `household` (an owner or tenant invites to their own unit, no
money access), `site_code` (the site's QR, `{BASE_URL}/join/{code}`; a signed-in resident files a unit claim that a manager
approves). Links are `{BASE_URL}/invite/{token}`; delivery goes through the SMS and mail gateways (`log` drivers write the
link to the log). Accepting is public (the link proves the contact): the account is found or created, linked to the invited
person, the occupancy is created, and the caller then signs in by any method.

**Site hand-over**: `POST /sites/{site}/export` stores a JSON archive (`GET /site-exports/{id}/archive`), `POST
/site-imports` (multipart `file`) creates the site in another organization with the same ids, linking people who already
have an account there. `POST /sites/{site}/offboard` (re-auth, export first) closes the source. CLI:
`openbekci admin export-site --org <slug> --site <id> --out f.json`, `admin import-site --org <slug> --file f.json`,
`admin offboard-site`. A CI test (`TestIntegrationExportManifestCoversEveryTenantTable`) fails when a tenant table is in
neither `exportTables` nor `excludedTables` (`internal/core/tenancy/export.go`).

## Tests

```powershell
tools/task.ps1 test               # unit tests only
go test ./...                     # (in server/) unit + integration
```

Integration tests need a PostgreSQL superuser: `OPENBEKCI_TEST_SUPERUSER_URL` (default `postgres://postgres:postgres@localhost:5432/postgres`). Each test creates a throwaway database, creates the three roles if missing, migrates, and drops the database afterwards. They skip with a message when the server is unreachable.

## Linting

`tools/task.ps1 lint` runs `go vet` and the pinned golangci-lint v2 (`go tool golangci-lint`). `internal/core/**` may not import `internal/platform/db` or `pgxpool` (depguard): modules declare a small interface for `InTenantTx` and are handed the `*db.DB` in wiring.

## Layout

```
cmd/openbekci/     cobra commands
internal/app/      config, startup checks, wiring, lifecycle
internal/httpapi/  router, middleware, /healthz, /readyz, MountAPI, spec-driven pipeline, gen/ (generated)
internal/problem/  RFC 9457 problem+json errors
internal/core/     business modules (instance, authz, identity, tenancy, audit, platformadmin, ...)
internal/permissions/ the permission registry built from every module (also run by cmd/genpermissions)
internal/authztest/  seeded world for authorization tests
internal/platform/ db (pools, tenant tx, migrations), jobs (River), id (UUIDv7), idempotency,
                   crypto (argon2id, opaque tokens), sms (gateway, netgsm), mail (smtp), ratelimit,
                   pii (personal data registry for audit diffs)
internal/webassets web app embed; -tags nowebassets serves a placeholder
db/migrations/     goose SQL        db/queries/  sqlc inputs
```
