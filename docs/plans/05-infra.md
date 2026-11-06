# OpenBekci — Infrastructure, Operations and Release Plan

| | |
|---|---|
| Status | Draft v0.1 |
| Date | 2026-11-06 |
| Parent | [00-overview.md](00-overview.md). Server internals in [01-backend.md](01-backend.md); push expectations in [03-mobile.md](03-mobile.md) §9 |

---

## 1. Goals and non-goals

**Goals**

- A self-hosted installation that a property manager (or their nephew) can run: one `docker compose up`, one domain, automatic TLS, documented backup and upgrade. Reference hardware: a 2 vCPU / 2 GB VPS serves a 500-unit organization comfortably.
- The same artifacts scale to a hosted multi-organization service: stateless API replicas, separate workers, managed Postgres, object storage.
- The **push relay**: the one piece of shared infrastructure the project must operate so self-hosters' residents get push notifications from the store-published app — designed so anyone can run their own.
- Reproducible, signed, scannable releases on a predictable cadence.
- Operability: health, metrics, logs and traces that work out of the box without a vendor.

**Non-goals (v1)**

- Managed-hosting business logic (signup, plans, billing of organizations) — lives outside this repo.
- Multi-region active/active. HA Postgres tooling (Patroni etc.) — use managed Postgres when you need HA.
- A control plane for fleets of self-hosted instances.

---

## 2. Artifacts

| Artifact | Built by | Where |
|---|---|---|
| `openbekci` binary (linux/amd64, linux/arm64, darwin/arm64, windows/amd64) with embedded web app and official packs | GoReleaser on tag | GitHub Releases |
| `ghcr.io/openbekci/openbekci:<version>` container image (multi-arch, distroless, non-root) + `:latest` and `:<major>.<minor>` tags | GoReleaser + buildx | GHCR |
| `ghcr.io/openbekci/push-relay:<version>` | same pipeline | GHCR |
| Helm chart `oci://ghcr.io/openbekci/charts/openbekci` | chart-releaser | GHCR (OCI) |
| `openbekci-mobile` Android APK (`foss` flavor) | mobile CI | GitHub Releases + F-Droid |
| TypeScript and Dart clients | `task gen` + publish on tag | npm (`@openbekci/api`), pub.dev (`openbekci_api`) |
| SBOM (SPDX) and cosign signatures/attestations for every binary and image | syft + cosign (keyless, Sigstore) | Alongside each artifact |

Version alignment (overview §10): server + web share one version; relay, chart, mobile, SDK, packs and clients have their own. The Helm chart's `appVersion` pins a server version.

---

## 3. Deployment targets

### 3.1 Docker Compose (reference self-host)

`deploy/compose/` ships:

```
compose.yml            openbekci + postgres + caddy
compose.minio.yml      optional S3-compatible storage instead of local disk
compose.observe.yml    optional Grafana/Loki/Prometheus/Tempo stack
.env.example           every variable with comments
Caddyfile              reverse proxy with automatic Let's Encrypt
```

Shape:

```
                 ┌──────────┐  :443 (auto TLS)
   internet ───▶ │  caddy   │ ──▶ openbekci:8080
                 └──────────┘
                 ┌──────────┐        ┌───────────┐
                 │ openbekci│ ─────▶ │ postgres  │  (volume: pgdata)
                 │ serve    │        └───────────┘
                 │ (worker  │        volume: data  (local file storage)
                 │ embedded)│
                 └──────────┘
```

- `openbekci` runs `migrate up` as an init step (`OPENBEKCI_AUTO_MIGRATE=true` in the compose default) and then `serve`.
- Postgres 17 (minimum 15) with `pg_stat_statements`; two roles created by an init script: `openbekci_migrator` (owner) and `openbekci_app` (RLS-subject, backend §5).
- Caddy handles TLS, HTTP→HTTPS, HSTS, compression and request size limits; the app also works behind Traefik/nginx — documented headers: `X-Forwarded-For/Proto`, trusted proxy CIDRs via `OPENBEKCI_TRUSTED_PROXIES`.
- `deploy/compose/README.md` is the "install in 10 minutes" guide: domain, `.env`, `docker compose up -d`, `admin create-platform-admin`, first login, backups.

### 3.2 Bare binary + systemd

For people who dislike containers: `deploy/systemd/openbekci.service` (hardened unit: `DynamicUser`, `ProtectSystem=strict`, `StateDirectory=openbekci`), `/etc/openbekci/config.yaml`, Postgres from the distro, Caddy or nginx in front. Same `migrate`/`serve` commands.

### 3.3 Kubernetes (Helm)

`deploy/helm/openbekci/`:

- Deployments: `api` (`serve --no-worker`, HPA on CPU/RPS), `worker` (`worker`, 1–N, River leader election handles periodic jobs), optional `relay`.
- `Job` hook for migrations on upgrade (`pre-upgrade`, expand/contract-safe per backend §17).
- External Postgres and S3 expected (values for connection secrets); an optional in-cluster Postgres via a sub-chart for evaluation only.
- `Ingress` with cert-manager annotations; `PodDisruptionBudget`; `NetworkPolicy` templates; `ServiceMonitor` for Prometheus Operator.
- Readiness from `/readyz`, liveness from `/healthz`, graceful shutdown ≤ 30 s (River drains jobs; HTTP waits for in-flight).
- All config via `Secret`/`ConfigMap` mapped to `OPENBEKCI_*`.

### 3.4 Hosted service (reference architecture)

Not shipped as code here, but the artifacts must support it:

- Managed Postgres with PITR; connection pooling via PgBouncer (transaction mode is **not** compatible with `SET LOCAL` per-transaction tenant context — use session mode or pgx pooling in-process; documented).
- Object storage (S3/R2) with per-org key prefixes; lifecycle rules for exports.
- `api` replicas behind a load balancer; `worker` separate; relay separate; OpenTelemetry collector → metrics/logs/traces backend.
- Org onboarding, plans and invoicing in a private service using the pack SDK's platform hooks.

---

## 4. Push relay

### 4.1 Why

Mobile push needs FCM/APNs credentials that belong to whoever publishes the store app. A self-hosted server cannot have them. The relay is a small service that holds those credentials and forwards content-free notifications on behalf of any server (overview §6.5, mobile §9). The project runs the **official relay** for the store builds; anyone can run a relay for white-label builds.

### 4.2 Design

Stateless forwarding; the relay keeps **no device registry** and **no message content**.

```
device ──registers token──▶ server (stores token encrypted)
server ──POST /v1/send (signed)──▶ relay ──▶ FCM / APNs ──▶ device
                                   ◀── invalid-token feedback ──
```

- **Server enrollment**: `POST /v1/servers { base_url, contact_email? }` → `{ server_id, server_secret }`. The relay verifies the server by fetching `https://<base_url>/.well-known/openbekci.json` and checking a challenge value the server includes (`relay_enrollment_challenge`), so random clients can't enroll on behalf of a domain they don't control. The server stores the secret (`OPENBEKCI_PUSH_RELAY_*` or persisted in `setting`).
- **Send**: `POST /v1/send` with `Authorization: Relay-HMAC <server_id>:<timestamp>:<hmac-sha256(secret, body)>`, body:
  ```json
  { "messages": [
      { "push_kind": "fcm" | "apns",
        "token": "<device token>",
        "app_id": "org.openbekci.app",          // which store app's credentials to use
        "collapse_key": "ticket:…",
        "priority": "high" | "normal",
        "ttl_seconds": 3600,
        "data": { "notification_id": "…", "org_id": "…", "type": "ticket.updated", "badge": 3 },
        "apns": { "mutable_content": true, "generic_title_key": "push.ticket_updated" }
      } ] }
  ```
  Response lists per-message results: `ok`, `invalid_token` (server must delete the device), `rate_limited`, `error`. No bodies, titles or personal data are accepted; the relay rejects unknown keys in `data` beyond an allowlist (`notification_id`, `org_id`, `type`, `badge`, `collapse_key`).
- **UnifiedPush** devices bypass the relay: the server POSTs directly to the endpoint URL.
- **Credentials**: one FCM service account (HTTP v1) and one APNs `.p8` key per `app_id`, loaded from files/env. Multiple `app_id`s let one relay serve several white-label apps.
- **Limits and abuse**: per-server token bucket (default 60 msg/s burst 600), per-day quota, per-IP enrollment limits, optional `RELAY_ENROLLMENT=open|invite|closed`. Admin endpoints to disable a server. Metrics per server id only.
- **Privacy**: logs contain server id, counts and error classes; never tokens or `data`. Tokens are not persisted. Retention of request logs: 7 days.
- **Implementation**: Go, `relay/` in the monorepo, no database (an in-memory + optional SQLite store for enrollments and quotas; Postgres optional for multi-replica), `/healthz`, `/metrics`. Reuses the backend's lint/test/release pipeline. ~1–2 k lines.
- **Backend side** (B5): relay client with HMAC signing, batching, invalid-token handling, enrollment command `openbekci admin push-relay enroll --url …`, default `https://push.openbekci.org` baked in but overridable. Status visible in platform admin.

### 4.3 Operating the official relay

Two small instances behind a load balancer, SQLite-free (Postgres for enrollments), FCM/APNs credentials in a secrets manager, alerts on error ratio per provider. Terms: a short "relay policy" page (fair use, no content, may rate-limit). Cost is tiny — push messages are free; the cost is uptime.

---

## 5. TLS, domains and networking

- Recommended edge: **Caddy** (auto TLS). nginx/Traefik configurations in `deploy/proxy/` with the same headers and limits.
- The app serves plain HTTP on `:8080` and trusts `X-Forwarded-*` only from configured CIDRs. It refuses to issue cookies without `Secure` unless `OPENBEKCI_INSECURE_DEV=true`.
- Mobile requires valid HTTPS (mobile §5.1): the install guide pushes everyone to Let's Encrypt via Caddy; private-CA setups are documented as advanced.
- Outbound: SMTP, push relay/UnifiedPush endpoints, pack providers, S3. All configurable; an egress allowlist is documented for locked-down networks.
- Request limits at the edge: body 50 MB (uploads), timeouts 60 s; the app's own limits are the authoritative ones (backend §13).

---

## 6. Data: backups, restore, retention

- **What to back up**: the Postgres database and the file store (`DATA_DIR/files` or the S3 bucket). Nothing else is state (River jobs and outbox live in Postgres).
- **Compose default**: a `backup` sidecar (`prodrigestivill/postgres-backup-local` or our own tiny script) producing nightly `pg_dump -Fc` with 7 daily / 4 weekly / 6 monthly retention into a `backups` volume, plus an `rclone` example to push the volume and `files` to any remote (S3, Backblaze, a NAS). Encryption of off-site copies via `rclone crypt` documented.
- **Restore** is a first-class, tested command path: `docs/ops/restore.md` with exact commands; the CI nightly job restores the latest dump into a fresh compose stack, runs `migrate status` and a smoke test, and fails loudly — a backup that was never restored is not a backup.
- **PITR** for the hosted service (managed Postgres); for self-hosters, WAL archiving is documented as optional (`wal-g`).
- **Retention inside the app** (backend §16.5) is an application concern; backups follow the operator's policy. Erasure requests do not purge historic backups; the policy page says so (standard GDPR practice: backups age out).
- **Export**: `openbekci admin export-org --org …` produces a portable archive (JSON + files) so organizations can leave a hosted instance for self-hosting and vice versa; `import-org` is the inverse. Lands at M7.

---

## 7. Upgrades

- Semantic versions; `latest` tag only moves on stable releases.
- **Procedure (compose)**: `docker compose pull && docker compose up -d` — migrations run on start. Release notes list breaking configuration changes under a fixed heading; the server refuses to start on unknown/removed settings with a clear message.
- **Zero-downtime (k8s/hosted)**: expand/contract migrations (backend §17); `pre-upgrade` migration job; rolling API deployment; workers drain. Version N+1's migrations must be compatible with N's running code for the overlap window; a CI job runs the previous release's tests against the new schema.
- **Skipping versions**: supported across minors within a major; majors require passing through the last minor of the previous major (enforced by `migrate` with a helpful error).
- **Rollback**: previous image + `migrate down` for migrations flagged reversible; otherwise restore from the pre-upgrade backup the upgrade guide tells you to take (and the compose `backup` sidecar makes automatic right before `migrate up` via a pre-start hook).
- **Packs**: official packs ship inside the binary and upgrade with it; their migrations run in the same `migrate`.
- **Mobile/server compatibility**: the discovery document's `min_app_version` and the app's required API version (mobile §5.1); release notes state the supported range.

---

## 8. Observability

- The app emits JSON logs to stdout, Prometheus metrics at `/metrics` (token-protected), OTLP traces when configured, `/healthz` and `/readyz` (backend §15).
- `compose.observe.yml`: Grafana + Prometheus + Loki (+ Promtail) + Tempo + an OpenTelemetry Collector, with **provisioned dashboards** in `deploy/observe/dashboards/`: request rate/latency/errors, job queue depth and latency, outbox lag, notification delivery outcomes by channel, DB pool and slow queries, disk/volume usage, push relay results.
- **Alert rules** shipped alongside (Prometheus rules): readiness failing, error ratio > 2 %, job queue age > 10 min, outbox lag > 5 min, backup older than 36 h, disk > 85 %, certificate expiry < 14 d.
- Optional error reporting via a Sentry-compatible DSN; off by default; no telemetry phones home. (A separate, opt-in, anonymous "instance ping" — version, org count bucket — may be proposed later as an ADR; not in v1.)
- Hosted service: the same signals into a managed stack; per-org labels only on low-cardinality metrics.

---

## 9. Security operations

- Images: distroless, non-root, read-only root filesystem, no shell; `securityContext` in Helm; `cap_drop: ALL` in compose.
- Supply chain: Dependabot (Go, npm, pub, Actions), `govulncheck`, `pnpm audit`, Trivy image scan on every build; SBOMs attached; keyless cosign signing with GitHub OIDC; `cosign verify` instructions in the install guide; SLSA provenance attestations.
- Secrets: never in images or repos; `.env` for compose (documented permissions 600), Kubernetes `Secret`s or external-secrets; the app reads `_FILE` variants of secret env vars for Docker/K8s secrets mounts.
- Key rotation runbooks: `SECRET_KEY` (sessions invalidated — announce), `ENCRYPTION_KEY` (`admin rotate-keys`, backend §16.1), relay secrets, SMTP/provider keys.
- `SECURITY.md`: private disclosure address, 90-day coordinated disclosure, advisory via GitHub Security Advisories; security releases are patch versions on the latest two minors.
- A `docs/ops/hardening.md` checklist: firewall, automatic OS updates, Postgres not exposed, backups off-site, 2FA for admins, log retention.

---

## 10. Scaling guidance

| Size | Recommendation |
|---|---|
| ≤ 2 000 units, one org | Compose on a 2 vCPU / 2–4 GB VPS; embedded worker; local file storage |
| ≤ 20 000 units, a few orgs | Compose with `worker` as a separate container; Postgres on its own host or managed; S3 storage; `compose.observe.yml` |
| Hosted / many orgs | Kubernetes/Helm: 2+ `api` replicas, 1–2 `worker` replicas, managed Postgres with pooling in session mode, S3, relay, OTel |

Known hot paths and their mitigations: statement/report queries (balance cache, indexes led by `org_id`), monthly charge generation (batched per site, River concurrency caps), PDF rendering (worker-only when a separate worker exists; CPU cap otherwise), bank statement matching (bounded batch sizes).

---

## 11. Environments and CI/CD

- **Branches**: trunk-based; every PR builds all artifacts (not published); `main` publishes `:edge` images and a rolling `edge` compose example for testers.
- **Pipelines** (GitHub Actions, reusable workflows under `.github/workflows/`):
  - `ci-server`, `ci-web`, `ci-mobile`, `ci-relay`, `ci-api` (spec lint with Redocly, generate-check across all clients) — each triggered by path filters, all required on PRs that touch them.
  - `e2e-nightly`: compose up from `main` images, seed the golden 24-unit site, run backend API tests, Playwright and Patrol suites, backup→restore test, upgrade test from the last release.
  - `release`: on `v*` tags — GoReleaser (binaries, images, SBOM, signing), Helm chart release, clients publish, GitHub Release with generated notes (Conventional Commits), mobile `foss` APK attached; store submissions via fastlane from the mobile repo path on `mobile-v*` tags.
- **Cadence**: minor release monthly while pre-1.0; patch releases as needed; a `release-x.y` branch only for backports of security fixes.
- **Environments for the hosted service**: `dev` (ephemeral per PR via preview deploy — optional), `staging` (auto-deploy from `main`, seeded data, used by mobile staging flavor), `prod` (tag-driven, manual approval). Self-hosters only see tags.
- **Test data**: one canonical seed (`server/testdata/golden-site`) shared by backend golden tests, web and mobile e2e, demo instances and docs screenshots.
- **Demo instance**: `demo.openbekci.org` reset nightly from the seed with a read-mostly demo org; shows the product and doubles as a smoke test of the release pipeline.

---

## 12. Documentation site

- `docs/` sources (this plan set, ADRs, user guide, admin/operator guide, pack authoring guide, API reference generated from the spec) built with a static generator (Starlight/Astro or MkDocs Material — pick at I0) and deployed to `docs.openbekci.org` on `main`.
- API reference from `api/openapi.yaml` via Redocly or Scalar; versioned per release.
- Operator pages that this plan commits to: install (compose/systemd/helm), proxy configs, backups & restore, upgrades, hardening, push relay (use official / run your own), white-label mobile, troubleshooting (readyz failures, migrations, email, push).

---

## 13. Repository additions

This plan adds to the overview's layout (§5): `relay/` (push relay service), `deploy/compose`, `deploy/systemd`, `deploy/proxy`, `deploy/helm`, `deploy/observe`, and `docs/ops/`.

---

## 14. Milestones

| # | Name | Deliverables | Exit criteria |
|---|---|---|---|
| **I0** | Foundations (M0) | GitHub org/repo settings, branch protection, CI reusable workflows, Dockerfile (distroless, multi-arch), `deploy/compose` with Caddy + Postgres, `.env.example`, GoReleaser config with cosign/SBOM, docs site skeleton, `SECURITY.md`/`CONTRIBUTING.md` | `docker compose up` on a clean VPS gives a TLS login page; a tagged pre-release produces signed binaries/images |
| **I1** | Operability (M1–M2) | Backup sidecar + rclone example, restore runbook + nightly restore test, `compose.observe.yml` with dashboards and alert rules, systemd unit, proxy configs, hardening checklist, `_FILE` secrets | Nightly restore job green; dashboards show the golden site under load test |
| **I2** | Nightly e2e + upgrades (M3–M4) | `e2e-nightly` pipeline (API, Playwright, later Patrol), upgrade-from-previous-release test, expand/contract CI check, demo instance with nightly reset | Upgrade test catches a deliberately breaking migration in a PR |
| **I3** | Push relay (M5) | `relay/` service, enrollment with domain challenge, FCM/APNs senders, quotas, metrics, Helm/compose for relays, official relay deployed with alerts, relay policy page, backend enrollment command | Push from a fresh self-hosted compose instance reaches a store-build device via the official relay |
| **I4** | Kubernetes + hosted readiness (M6) | Helm chart (api/worker/relay, migration hook, PDB, NetworkPolicy, ServiceMonitor), staging environment, PgBouncer guidance, S3 lifecycle examples, Trivy/SLSA in release | Chart installs on kind in CI and passes the smoke suite; staging auto-deploys from `main` |
| **I5** | 1.0 (M7) | Org export/import, documented support matrix (Postgres, browsers, OS), release cadence and backport policy, docs complete, cost/sizing guide, security review of the relay | A self-hoster can migrate to/from the hosted service with `export-org`/`import-org` |

---

## 15. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| Who owns and funds the official relay and demo/docs domains | Project owner decision (overview §11). Design keeps it cheap; white-label relays remove the dependency |
| Relay enrollment abuse (spam pushes through official credentials) | Domain challenge, quotas, invalid-token feedback, kill switch per server; FCM/APNs themselves rate-limit |
| PgBouncer transaction pooling breaks `SET LOCAL` tenant context | Documented; session mode or in-process pgx pooling; a startup check detects pooled connections that drop settings and refuses to run |
| Self-hosters skipping backups | Backup sidecar on by default in compose; platform admin dashboard shows "last backup" from a marker file and nags |
| Migrations that aren't expand/contract-safe slip in | CI upgrade test from the previous release; PR template checkbox; reviewer checklist |
| Docs generator choice (Starlight vs MkDocs) | I0 decision; both fine; prefer the one contributors know (MkDocs Material if the team is Go/Python-leaning, Starlight if JS-leaning) |
| Opt-in telemetry debate | Not in v1; ADR later with a clear opt-in and public aggregate numbers if ever done |
| Store builds vs server versions drifting for self-hosters who don't upgrade | `min_app_version`/API range checks; support the last two server minors from the app |
