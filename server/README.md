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

**Contract.** Phase B serves the draft contract's tenancy and people operations (`api/src/openapi-draft.yaml`) with the
draft's own path files and schemas; `TestServedContractOperationsAreTheDraftsDefinition` holds every served operation to the
draft's definition byte for byte. Resources are flat (`/v1/orgs/{org}/units?site_id=`), custom actions use the `:verb` suffix
(`/sites/{id}:export`), permissions have the draft's plural names (`sites.manage`, `people.national_id`, ...), household
self-service is `me.*`, labels are `GET /v1/orgs/{org}/codes`. The handlers (`tenancy_handlers.go`, `people_handlers.go`)
translate the draft shapes to the services' own types (`contract_view.go`); the services keep their Go API. Operations of the
draft that are not served yet are marked in `api/DRAFT-INDEX.md` (Status column).

**Import** (`POST /v1/orgs/{org}/units:import`, multipart `site_id`, `file`, optional `mapping` JSON, `dry_run=true`). CSV
(comma, semicolon or tab, UTF-8) or XLSX (first sheet). Columns: `block`, `door_number` (required), `legal_ref`, `type`,
`area_m2`, `arsa_payi` (`12/1000` or decimal), `owner_name`, `owner_phone`, `owner_email`, `owner_share_pct`, `owner_resides`,
`tenant_name`, `tenant_phone`, `tenant_email`, `plates`, `opening_balance`, `opening_balance_side`, `opening_balance_date`;
Turkish header aliases (`Daire No`, `Blok`, `Malik`, `Plaka`, ...) are recognized. Re-importing the same file changes nothing
(units are matched by door number, people by phone or email); several rows with one door number add co-owners. The answer is
the draft's `ImportReport` (200 for a dry run or a refused file, 202 when committed; the work is synchronous, `job_id` is null).
A real import with an error row writes nothing. Opening-balance columns are validated and stored in `import_row.opening` of the
applied batch for the money module (phase C).

**Invitations**: a manager invites an existing person (`POST /invitations`: it links the account; the occupancy exists
already), an owner or tenant invites a household member to their own unit (`/units/{id}/household-invitations`), and the
site's join code (`{BASE_URL}/join/{code}`) lets a signed-in resident file a unit claim that a manager approves. Links are
`{BASE_URL}/invite/{token}`; delivery goes through the SMS and mail gateways (`log` drivers write the link to the log).
Accepting (`POST /v1/invitations/{token}:accept`) finds or creates the account, links it to the invited person and signs the
caller in (the link proves the contact, as a magic link does): the answer is an `AuthResult`.

**Site hand-over**: `POST /sites/{site}:export` (re-authentication) stores a JSON archive (`GET /site-transfers/{id}/archive`),
`POST /sites:import` (multipart `archive`) creates the site in another organization with the same ids, linking people who
already have an account there. `POST /sites/{site}:offboard` (re-authentication, `confirm_site_name`, export first) closes the
source. The work is synchronous: transfers are `succeeded` when the call returns. CLI: `openbekci admin export-site|import-site|offboard-site`.
A CI test (`TestIntegrationExportManifestCoversEveryTenantTable`) fails when a tenant table is in neither `exportTables` nor
`excludedTables` (`internal/core/tenancy/export.go`).

## Files and storage

Files are the `file` table (metadata, scope, status, retention, image variants) plus blobs in a `storage.Blob`
(`internal/platform/storage`): the local driver under `DATA_DIR/files/<org>/<yy>/<mm>/<id>` (atomic writes) or S3
(`OPENBEKCI_STORAGE_DRIVER=s3`, `OPENBEKCI_STORAGE_S3_*`, any S3-compatible store). Uploads always stream through the
server: `POST /v1/orgs/{org_id}/files` (multipart; form fields first, then `file`). Settings:

| Variable | Default | Meaning |
|---|---|---|
| `OPENBEKCI_MAX_UPLOAD_BYTES` | 25 MiB | one file; the multipart route alone bypasses the 1 MiB JSON cap (`MAX_BODY_BYTES`), its request cap is this plus 1 MiB of framing, and uploads get a 10 minute timeout |
| `OPENBEKCI_FILES_ALLOWED_EXTENSIONS` | jpg jpeg png webp pdf txt csv docx xlsx pptx odt ods doc xls | narrows the built-in allowlist |
| `OPENBEKCI_STORAGE_DRIVER` | `local` | `local` or `s3` |
| `OPENBEKCI_CLAMAV_ADDR` | unset | clamd `host:port` or `unix:/path`; unset skips scanning, unreachable refuses uploads with 503 `scan_unavailable` |
| `OPENBEKCI_CLAMAV_TIMEOUT` | 60s | longest one scan may take (connecting is limited to 5 s); slower is 503 `scan_unavailable` |
| `OPENBEKCI_MAX_IMAGE_PIXELS` | 25000000 | images with more pixels are 422, decided from the header alone (a decoded image costs 4 bytes per pixel) |
| `OPENBEKCI_IMAGE_WORKERS` | max(1, CPUs/2) | images decoded and re-encoded at once, process wide; the others wait |

Each user may upload 30 files a minute (in-memory token bucket per instance), then 429 `rate_limited`.

What an upload goes through: the extension must be allowed and the first bytes must really be that type
(`http.DetectContentType`; a renamed executable is 415), over-limit files are 413 (no partial blob is kept), the body is
spooled to a temporary file and scanned from there when ClamAV is configured (nothing reaches storage before the scan; a
flagged file is `quarantined`, cannot be attached and expires after the draft lifetime), images are decoded from the
spooled file (header first: more than `OPENBEKCI_MAX_IMAGE_PIXELS` is 422) and re-encoded (EXIF including GPS, XMP, ICC
and comments are dropped, the EXIF rotation of JPEG, PNG and WebP is applied to the pixels, the stored original is capped at
4096 px, `thumb` 256 px and `medium` 1600 px variants are made, one at a time), everything else is stored as is, and the
SHA-256 of what is stored is recorded. A file without a scope is a draft that expires after 7 days and that only its
uploader (and `files.manage`) sees; once it is attached, visibility is the scope's resolver, for the uploader too. The form
fields (`scope` as `<type>:<id>`, `site_id`, `name`) come before `file`; a field after it is 422 `multipart_order` and the
file is discarded again. File names lose directories, control characters and bidi overrides. `Idempotency-Key` on an upload
compares content (the SHA-256 of the form), not length: a retry of the same file replays, another file is 422
`idempotency_mismatch`.

Contract (the draft's operations, one set of ids): `operations.uploadFile` `POST /files`, `operations.listFiles`
`GET /files`, `operations.getFileMeta` `GET /files/{id}/meta`, `operations.downloadFile` `GET /files/{id}` (the bytes, or a
302 to a presigned URL with S3; `?variant=thumb|medium`; `exp` and `sig` of a `links` URL stand in for a session and a
session sent along is ignored), `operations.deleteFile` `DELETE /files/{id}` (the uploader discards a draft with
`files.upload`; an attached file only `files.manage` removes, the uploader gets 409). Downloads are authorized per file,
images are `inline`, everything else `Content-Disposition: attachment`, always `X-Content-Type-Options: nosniff`,
`Content-Security-Policy: sandbox` and `Cache-Control: private, no-store`. `uploaded_by` is shown to the uploader and to
`files.manage` only. File metadata carries short-lived `links`: signed paths on this server for the local driver (usable
without a session), presigned GETs for S3. A `.csv` is served as an attachment with its bytes as uploaded: guarding
against spreadsheet formula injection (cells starting with `=`, `+`, `-`, `@`) is the job of whatever imports or
displays it. Deleting is soft; the hourly `maintenance.prune_files` job also expires drafts and files past `retention_until`,
removes blobs and gives the storage back to `org_storage_usage` (a deleted file another table still references keeps its
row), in batches of 200 up to 5000 files per run, skipping rows another transaction holds (`Attach` locks the row). At
start-up, `.tmp-*` leftovers of the local driver and spooled uploads older than an hour are removed.

For other modules (Go):

```go
// wiring: tell the file module who may read and attach to your resource (ticket photos: at most 10)
filesSvc.RegisterResolver("ticket", ticketFiles{}, files.WithMaxFiles(10)) // ticketFiles implements files.VisibilityResolver

// in your own tenant transaction, attach what the client uploaded first (atomic with the ticket insert)
f, err := filesSvc.Attach(ctx, tx, orgID, fileID, files.ScopeRef{Type: "ticket", ID: ticketID})

// generated documents (statements, receipts, minutes): server side, source "generated", no permission check
f, err := filesSvc.UploadGenerated(ctx, files.UploadParams{OrgID: orgID, Name: "statement.pdf", Content: r,
    Scope: &files.ScopeRef{Type: "document", ID: documentID}})
```

`files.upload` and `files.read` are held by every role (they only open the endpoints); `files.manage` (org_admin,
site_manager) reads and deletes any file and shows `Usage`. A table that references files should use a composite
foreign key `(org_id, file_id) REFERENCES file (org_id, id)`.

## Community (0.3)

`internal/core/community` serves the draft contract's community module (41 operations, `x-stability: preview`, path
files and components copied verbatim from the draft: facilities and bookings, visitor entries and recurring visitors,
approval requests, parcels, the guard console and PIN sessions). Decisions C1 to C4 in `docs/decisions/community.md`.

- **Bookings** confirm instantly when they satisfy the facility's `rules` (opening hours in the site zone, maximum duration,
  notice, horizon, quota per unit and week or month, who may book); a taken slot is `409 conflict` (the exclusion constraint
  `booking_no_overlap` on confirmed bookings is the last line, the facility row lock serializes the checks). Residents see
  slots only as taken (`availability`); names go to managers and the booker. A fee goes through `community.BookingCharger`
  (a `booking_fee` charge on the unit side, refund automatic before `cancel_refund_cutoff_hours` or when a manager cancels);
  until the money module is wired the default stores the fee on the booking and posts no charge.
- **Visitors**: `POST /visitor-entries` returns the QR text `obk1.<token>` the resident shares. The row keeps
  `sha256(token)` for lookup and the token sealed with `ENCRYPTION_KEY`, so the unit's people and managers can show the QR
  again; board, auditors and guards never get it. Valid for the window plus 30 minutes, single entry. A guard scans
  (`POST /gate/visitors:scan`, online, no agent needed) and admits with `:arrive`. Without a QR the guard registers a
  walk-in (`:walk-in`): the entry is `requested`, every current occupant of the unit gets a `jobs.Notify`
  (`visitor.approval_requested`, with a link) and answers from the portal (`:decide`, first answer wins); the request
  expires after 10 minutes (site setting `visitor_request_expiry_minutes`), then the guard calls the number a resident
  shared (`call-contact`, only for an open request, audited). Recurring visitors are weekly rules in the site zone with
  their own long-lived QR; the evaluation (`RecurringRule.Matches`) is the gate agent's: boundaries inclusive at the
  minute, an overnight window belongs to its start day.
- **Guard console**: the booth is a login with `gate.operate` (`guard` role); the guard picks their name (`gate.listGuards`)
  and types a PIN (`POST /gate/sessions`; PIN per person, argon2id, set by `gate.manage` with `PUT /persons/{id}/gate-pin`,
  which also clears a lock). The returned token goes in `X-Guard-Session` (12 h, bound to the console login and the site)
  and attributes audit events (`guard_person_id` in the diff) and `arrived_by`. 5 failed attempts in 15 minutes lock that
  guard for 15 minutes, 20 from one console in an hour lock the console for an hour (`429` with `Retry-After`, also for a
  correct PIN); every failed attempt is audited. Every guard response carries names, plates, door numbers and windows
  only (the tests scan response JSON keys for contacts, national ids, balances and tokens).
- **Retention (KVKK)**: `community.visitor_retention_prune` (every 6 hours) deletes visitor entries and their requests,
  ended recurring visitors, collected parcels, PIN attempts and guard sessions older than the site's
  `visitor_retention_days` (default 90). `community.visitor_request_expiry` (every minute) expires unanswered requests and
  unused entries.
- **Interfaces** (all with no-op or absent defaults in the wiring `newCommunity`): `BookingCharger` (money),
  `Notifier` taking `jobs.Notify` (notifications; `jobs.Notify` is defined in `platform/jobs/notify.go`, the same struct the
  operations module defines), `QRRenderer` (the two `.../qr` image operations stay draft-only until one is registered), and
  `Hooks.OnVisitorDecided` / `Hooks.OnVisitorChanged` for the access module. `community.VisitorCredentials(ctx, org, site,
  since)` (and `VisitorCredentialsTx` inside the caller's transaction) lists visitor QR hashes (`visitor_qr`, scope `all`)
  and plates (`plate`, scope `all_vehicle`) with their window plus slack or weekly rule, `max_uses`, and `Active`: a zero
  `since` is the allow-list snapshot (active only), otherwise every credential changed since then so revocations
  propagate.
- **Spec notes**: the draft's `x-openbekci-roles` role map is accepted next to the permission list (holding any one of the
  listed permissions passes the pipeline); `X-Guard-Session` is declared `x-go-name: GuardSessionHeader` in
  `components/draft-common.yaml` because the schema `GuardSession` has the same name; required headers and query
  parameters (`If-Match`, `Idempotency-Key`, `site_id`) are bound by the generated wrapper before the middleware, so the
  bind error handler authenticates first (an unauthenticated caller still gets `401`).
## Access control and the on-site agent (0.3)

Design: [07-access-control.md](../docs/plans/07-access-control.md); the agent is `openbekci-agent`. Module
`internal/core/access` (`protocol/` wire types and decoder, `session/` one agent connection independent of the transport,
`evaltest/` the reference evaluator, `accesstest/` a fake agent). Permissions `access.read|manage|open|view_events`.

**Operations served** (spec tags `access`, `agent`): access points (CRUD), agents (create, rename, re-enrol, revoke,
force rotation), credentials (list, get, revoke a manual one), `access-events` (newest first; `after=<sync_seq>` switches
to feed order for the guard console's poller), `access-points/{id}:open` and `access/commands/{id}`. Draft only:
`access.createCredential` (tag and PIN are never issued in 0.3), `access.rebuildAllowlist` (needs the jobs resource; the
service has `Reconcile` and the River job `access.reconcile`), `access.listGuardEvents` (`gate.operate` belongs to the
community module). The transport operations `agent.enrol|connect|poll|rotateToken` are in the spec but excluded from the
generated strict server (`exclude-tags: [agent]` in `oapi-codegen.yaml`): `Service.Mount` serves them outside the user
pipeline. An agent token never satisfies user authorization and a session never authenticates an agent.

**Tokens.** `obk_enr_<random>` (15 minutes, once) is exchanged at `POST /v1/agent/enrol` for `obk_agt_<agent id>.<secret>`
(only SHA-256 hashes are stored; an unknown, expired or used token answers one identical 404). The token carries the agent
id; two `SECURITY DEFINER` functions (`access_agent_org`, `access_enrol_agent`, owned by the migrator) map an id or an
enrolment hash to the organization so every later query is an ordinary RLS tenant transaction. Rotation
(`POST /v1/agent/token:rotate`) keeps the retired token valid for 10 minutes and returns the same new token if repeated
(it is kept sealed with `ENCRYPTION_KEY` for that window). Revocation sends `command revoked` and closes with 4401; a bare
401 means only an unknown token. `ws_url` and `poll_url` are derived from `OPENBEKCI_BASE_URL` and are https/wss only
unless `INSECURE_DEV`.

**Allow-list.** `credential` rows are derived (never edited): vehicles of a unit with a current occupancy, and whatever the
`CredentialSource` returns for visitor entries (the community module implements `VisitorCredentials(ctx, site, since)`;
the default `VehiclesOnly` returns nothing). The projection compares the entry it would publish with the one it last
published and only a change takes the next `access_site_state.version` (row lock, so versions are gap-free) and appends to
`allowlist_change` (kept 7 days / 50 000). Triggers on `vehicle` and `occupancy` queue sources in `access_dirty` (the
outbox); `access.project` drains it every 10 s, `access.reconcile` repairs drift nightly and at start-up, `access.prune`
applies retention. The community module calls `access.MarkDirty(ctx, tx, ...)` in the approval transaction and
`Service.Kick(site)` after the commit to reach a connected agent within seconds.

**Transport.** `/v1/agent/ws` (subprotocol `openbekci.agent.v1`, one JSON envelope per frame, 1 MiB, ping every 30 s) and
`/v1/agent/poll` share `session.Manager`. A poll session is the agent's current session: a request whose first message is
`hello` starts a new one, any other request without a live session is `410 agent_session_expired`, and `seq` numbers only
the responses that carry messages (the built agent ignores empty ones). Snapshots go out in parts of at most 2000 entries,
deltas in chunks of at most 500 changes, gap-free; a lower-version snapshot is sent only right after hello, after a `gap`
ack or a `request_snapshot`. Events are idempotent by the agent's UUIDv7; `occurred_at` is corrected by the batch's
clock offset (never later than the receive time). A manual open lives 15 seconds, is refused with `409 agent_offline` when
the agent is not connected (a live session or a frame within 90 s) or its clock is suspect, is audited with the actor and
is never retried. Limits: enrol 10/min per IP, WebSocket upgrade 20/min and poll 4/s per agent (`429` with `Retry-After`).

`server/testdata/access-protocol/` holds the shared conformance fixtures (valid and invalid messages, the evaluation
cases with the inclusive overnight boundary, plate normalization); every one is run against the decoder and the served
schemas in `api/agent-protocol/v1`.
## Money (phase C)

Modules: `internal/core/ledger` (funds, chart of accounts, cash boxes, fiscal years, the append-only journal, the `PostEntry` poster, lock date, receipt
sequences), `billing` (charge plans with effective-dated rates, charge generation, charges, late fees, opening balances, open-item view), `payments`
(cash and bank payments, declared transfers, receipts, allocation), `reports` (statements as JSON, CSV and PDF, debtor list, aging, trial balance, fund
balances, income and expense), `moneyhooks` (the move-out hook `people.MoneyHooks`, registered in the wiring). Pure calculators live in `core/money/*`;
`platform/pdf` writes the PDFs (Helvetica with Turkish letters, no font file).

- **Double entry, funds, immutability.** Every posting goes through `ledger.PostEntry`: balanced overall and per fund, `fund_id` on every line, refused on or
  before the site's lock date (`409 locked_period`; the database also refuses it by trigger). `journal_entry` and `journal_line` have no UPDATE or DELETE
  grant and refuse them by trigger; a deferred trigger checks balance at commit. Corrections are reversals dated today (`ledger.Reverse`). Balances are
  `SUM` over the `(account, date)` index; there is no cache. The default chart and the operating and reserve funds are created on first use of a site.
- **Debtors.** A receivable is keyed by `(unit, side)` or by a person (carried debt after move-out). `billing.Debtor` is the `DebtorRef` of the API. Each charge
  snapshots its debtor person (`tenancy.EffectivePayer` decides the side; a tenant side with no tenant on the due date bills the owner).
- **Jobs.** `billing.catch_up` (hourly, on start too) runs `GenerateCharges` and `CrystallizeLateFees` for every active site; both are idempotent catch-up
  jobs. Late fees are accrued on demand by `latefee.Accrue`; crystallization posts the delta between expected and booked fee (a reversal after a backdated payment).
  Money notifications are River jobs of kind `notifications.money` (see `ledger/notify.go` for types and payloads); without a notifications module a log-only worker drops them.
- **Re-authentication.** Moving the lock date back, posting or voiding opening balances (a dry run needs none) and waivers above the site threshold
  (`late_fee_waiver_reauth_minor`, default 1000.00) need `POST /v1/auth/reauth` first.
- **Commands.** `openbekci admin rebuild-charge-status --org --site` re-derives every `charge.status` from the allocations; `openbekci admin post-entry --org --site
  --date --line CODE:DEBIT:CREDIT[:FUND] ...` posts a manual entry (audited, lock date applies).
- **Served and draft.** The ledger, charge plans (list, create), charges (list), payments (list, create, get, void), opening balances (list, create), late fee
  policy and debtor report and unit statement operations are served; the rest of the draft `money` module (expenses, collections, bank imports, transfers,
  refunds, budgets, other reports, ...) is not implemented yet. A site hand-over does not carry money data yet (`tenancy/export_money.go`).
- **Bank statement import** (`core/payments/bankimport`). `POST /bank-statement-imports` (multipart: `site_id`, `file`, `importer=core_csv`, `asset_account_id`,
  optional `mapping`, `opening_balance`, `closing_balance`) takes CSV or XLSX up to 10 MiB. `core_csv` reads `booked_on,amount,counterparty,description,reference`
  (or a typed `ColumnMapping`; Turkish `1.234,50` and `05.03.2026` are understood, UTF-8 or windows-1254). Parsing is synchronous; a bad row makes the import
  `partial`, a bad file is 422, the same file twice into one account is 409. The upload is kept as bytes on the import row (TODO: move to the files module, then
  fill `file_id`); each line keeps its `raw` row for 90 days (`bank.prune_raw`, daily). Incoming lines get suggestions: reference `<door>-<period>` (for example
  `A-12-2026-03`), payer name against the person on each unit side, amount against the open charges. Person IBANs are not stored, so there is no IBAN match yet.
  Matching is manual only: `:match` with a `DebtorRef` creates and confirms a bank-transfer payment, or with `payment_id` confirms a declared payment of the same
  amount; both run in the caller's transaction (`payments.Service.WithTx`), allocation as usual. `:unmatch` voids that payment. The reconciliation compares the statement
  with the ledger movement of the bank account over the statement period (`difference` equals the unmatched total when only matched payments posted to the account).
  Linking an outgoing line to an expense (`expense_id`) answers 422 until expenses exist.
- **Collections** (`core/collections`). Dunning stages per site (days after the oldest overdue due date, channels `in_app|email|sms|letter`, template key); the
  hourly `collections.run_stages` job fires, per debtor, the highest enabled stage that escalates past what the debtor already received for the current
  delinquency (period = month of the oldest overdue charge), once per debtor, stage, period and channel, creating `dunning_event` rows and
  `notifications.money` jobs of type `dunning.reminder`; manual `dunning-runs:preview` and `dunning-runs` take `debtors[]` of `DebtorRef`. The `letter` channel stores
  an ihtarname PDF on the event (TODO: file it into documents; `getDunningLetter` also generates one on the spot for a debtor). The letter text is
  `locales/tr/ihtarname.txt`; the PDF embeds the Go fonts (BSD) so `ğşİı` are real text, and the missing `₺` is drawn as vector strokes with ActualText U+20BA
  (`platform/pdf/unicode.go`). A collection case (unit side or person) freezes the unit side's late fees (a `late_fee_freeze` from the opening date, created in
  the same transaction) and skips the debtor in reminders and runs; closing it ends the freeze and charges legal costs back as a `legal_cost` charge.
  `debt-breakdown` gives principal and the late fee still owed per charge as of a date (JSON, CSV, XLSX, PDF).

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
internal/core/     business modules (instance, authz, identity, tenancy, audit, files, platformadmin, ...)
internal/permissions/ the permission registry built from every module (also run by cmd/genpermissions)
internal/authztest/  seeded world for authorization tests
internal/platform/ db (pools, tenant tx, migrations), jobs (River), id (UUIDv7), idempotency,
                   crypto (argon2id, opaque tokens), sms (gateway, netgsm), mail (smtp), ratelimit, storage (local, s3),
                   pii (personal data registry for audit diffs)
internal/webassets web app embed; -tags nowebassets serves a placeholder
db/migrations/     goose SQL        db/queries/  sqlc inputs
```

## Daily operations (tickets, announcements, documents, notifications, dashboards, codes)

Modules `internal/core/{tickets,announcements,documents,notifications,dashboards}` plus `codes` (org labels). Other modules tell people things by inserting a `jobs.NotifyArgs` job (`notifications.deliver`) in their transaction through a `jobs.Notifier`; the dispatcher resolves recipients, applies mutes and the site channel defaults (decision O5) and writes in-app rows, emails and SMS (per-site monthly counter). Generated documents are filed with `documents.FileGenerated(ctx, tx, GeneratedParams)`; the dashboards take money figures from a `dashboards.MoneyFigures` implementation (zeros until the money module provides one). Per-unit private folders are created by a database trigger on `unit`. Scheduled announcements are published by the `announcements.publish_due` task (`jobs.Task`, every minute).
