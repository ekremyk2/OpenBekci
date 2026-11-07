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

`tools/task.ps1` / `tools/task.sh` run the Go-pinned [go-task](https://taskfile.dev) (`go tool task`), so nothing needs installing globally. `tools/task.ps1 --list` shows all tasks: `gen`, `gen:check`, `api:lint`, `lint`, `test`, `test:integration`, `run`, `migrate`, `db:init|start|stop|roles`.

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
internal/core/     business modules (instance, authz, identity, platformadmin, ...)
internal/platform/ db (pools, tenant tx, migrations), jobs (River), id (UUIDv7), idempotency,
                   crypto (argon2id, opaque tokens), sms (gateway, netgsm), mail (smtp), ratelimit
internal/webassets web app embed; -tags nowebassets serves a placeholder
db/migrations/     goose SQL        db/queries/  sqlc inputs
```
