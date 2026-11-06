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
openbekci admin ...               administrative commands (added by later tasks)
openbekci version
```

Configuration is `OPENBEKCI_*` environment variables (secrets also accept `_FILE`), optionally a `--config file.yaml` with the lowercase names (`database_url: ...`); the environment wins. See [.env.example](../.env.example) and [deploy/compose/.env.example](../deploy/compose/.env.example).

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
internal/core/     business modules (instance, authz, ...)
internal/platform/ db (pools, tenant tx, migrations), jobs (River), id (UUIDv7), idempotency
internal/webassets web app embed; -tags nowebassets serves a placeholder
db/migrations/     goose SQL        db/queries/  sqlc inputs
```
