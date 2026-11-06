# OpenBekci — Infrastructure, Operations and Release Plan

| | |
|---|---|
| Status | **Draft v0.2** — revised on 2026-11-06 against the [plan review](06-review.md) and [`docs/decisions/`](../decisions/) |
| Parent | [00-overview.md](00-overview.md) — decisions (D4 deployment model, D6 gate control, D8 repositories) and the release sequence (§8) bind this document. Server internals, configuration and commands in [01-backend.md](01-backend.md) §5, §12, §16, §17, §19 |

---

## 1. Goals and non-goals

**Goals**

- A self-hosted installation that a property manager (or their nephew) can run: one `docker compose up`, one domain, automatic TLS, documented backup and upgrade. Reference hardware: a 2 vCPU / 2 GB VPS serves a 500-unit organization comfortably.
- The same artifacts scale to a hosted multi-organization service (D4): stateless API replicas, separate workers, managed Postgres, object storage.
- Two small services around the server, each in its own repository (D8): the **push relay** (0.4) so residents of self-hosted sites get push from the store app, and the **on-site access agent** (0.3) that drives gates and doors on the site LAN (D6).
- Reproducible, signed, scannable releases on a predictable cadence.
- Operability without a vendor: health, metrics, logs, a visible "last backup".

**Non-goals (through 1.0)**

- Managed-hosting business logic (signup, plans, billing of organizations) — lives outside this repo.
- Multi-region active/active; HA Postgres tooling (Patroni etc.) — use managed Postgres when you need HA.
- A control plane for fleets of self-hosted instances; telemetry or an "instance ping" of any kind.

---

## 2. Artifacts

| Artifact | Built by | Where |
|---|---|---|
| `openbekci` binary (linux/amd64, linux/arm64, darwin/arm64, windows/amd64) with embedded web app and official packs | GoReleaser on `v*` tags of the core repo | GitHub Releases |
| `ghcr.io/openbekci/openbekci:<version>` (multi-arch, distroless, non-root) + `:<major>.<minor>` and `:latest` tags | GoReleaser + buildx | GHCR |
| TypeScript types `clients/ts` (committed, consumed by `web/`) and the Dart client `openbekci_api` (**generated and published in CI only**, never committed) | `task gen` / release workflow | in-repo; pub.dev or a release asset for the mobile repo |
| SBOM (SPDX) + keyless cosign signatures and SLSA provenance for every binary and image | syft + cosign (Sigstore, GitHub OIDC) | alongside each artifact |
| `ghcr.io/openbekci/agent:<version>` + binaries (linux/arm64, linux/amd64, windows/amd64) | `openbekci-agent` repo, same GoReleaser setup (0.3) | GHCR + that repo's releases |
| `ghcr.io/openbekci/relay:<version>` | `openbekci-relay` repo (0.4) | GHCR |
| Helm chart `oci://ghcr.io/openbekci/charts/openbekci` (1.0) | chart-releaser | GHCR (OCI) |
| Android/iOS apps | `openbekci-mobile` repo, its own CI and `v*` tags; store submissions from there | stores / that repo's releases |

Version alignment (overview §10): server + web share one version; mobile, SDK, packs, relay, agent, chart and clients are versioned separately. The chart's `appVersion` pins a server version; the relay and agent state the server versions they speak to.

---

## 3. Deployment targets

### 3.1 Docker Compose (reference self-host, 0.1)

`deploy/compose/` ships:

```
compose.yml            openbekci + postgres + caddy + backup sidecar
compose.dev.yml        contributors: Postgres, Mailpit, MinIO (optional), SMS stub (backend §19)
compose.minio.yml      optional S3-compatible storage instead of local disk
compose.tunnel.yml     optional cloudflared / tailscale sidecar for sites without public 80/443 (§5)
compose.observe.yml    optional Grafana/Loki/Prometheus/Tempo stack (1.0)
.env.example           every variable with comments; README shows how to generate the keys
Caddyfile              reverse proxy with automatic TLS
README.md              "install in 10 minutes"
```

```
                 ┌──────────┐  :443 (auto TLS)
   internet ───▶ │  caddy   │ ──▶ openbekci:8080
                 └──────────┘
                 ┌──────────┐        ┌───────────┐      ┌──────────┐
                 │ openbekci│ ─────▶ │ postgres  │ ◀─── │  backup  │ nightly pg_dump
                 │ serve    │        └───────────┘      │  sidecar │ + files + secrets
                 │ (worker  │        volume: data       └──────────┘ → volume: backups
                 │ embedded)│        (local file storage)
                 └──────────┘
```

- `openbekci` runs `migrate up` first (`OPENBEKCI_AUTO_MIGRATE=true` in the compose default), then `serve` with the embedded worker.
- Postgres 17 (minimum 15) with `pg_stat_statements`; an init script creates the three roles of backend §5: `openbekci_migrator` (owner, BYPASSRLS), `openbekci_app` (RLS-subject), `openbekci_platform` (BYPASSRLS, small pool).
- The compose network has a **pinned subnet**, and `OPENBEKCI_TRUSTED_PROXIES` defaults to that CIDR (Caddy's container IP is not stable). `OPENBEKCI_BASE_URL` is the public name; `OPENBEKCI_EXTRA_ORIGINS` adds a LAN name next to the public domain.
- Secrets: the README generates `OPENBEKCI_SECRET_KEY` and `OPENBEKCI_ENCRYPTION_KEY` with `openssl rand -base64 48` into `.env` (mode 600); the server **refuses placeholder or short keys** at startup (backend §16). `_FILE` variants read Docker secrets.
- First run: `admin create-platform-admin`, first login, confirm the backup sidecar wrote a dump and the marker (§6).

### 3.2 Bare binary + systemd (0.1)

`deploy/systemd/openbekci.service` (hardened: `DynamicUser`, `ProtectSystem=strict`, `StateDirectory=openbekci`, `EnvironmentFile=/etc/openbekci/env`), Postgres from the distro, Caddy or nginx in front using `deploy/proxy/`. Same `migrate` / `serve` commands; the backup script from the sidecar runs from a timer unit.

### 3.3 Kubernetes (Helm, 1.0)

`deploy/helm/openbekci/`: `api` (`serve --no-worker`, HPA), `worker` (`worker`, River leader election for periodic jobs), `pre-upgrade` migration `Job`, external Postgres and S3 expected (in-cluster Postgres sub-chart for evaluation only), `Ingress` with cert-manager, `PodDisruptionBudget`, `NetworkPolicy`, `ServiceMonitor`, readiness from `/readyz`, liveness from `/healthz`, graceful shutdown ≤ 30 s. All config via `Secret`/`ConfigMap` → `OPENBEKCI_*`. The relay chart lives in the relay repository.

### 3.4 Hosted service (reference architecture, not shipped)

- Managed Postgres with PITR; **PgBouncer in transaction mode is fine**: `SET LOCAL` / `set_config(…, true)` inside the explicit tenant transaction is pinned to the transaction (backend §5). What a transaction pooler breaks is River's `LISTEN/NOTIFY` — run River with `PollOnly` — and pgx's prepared-statement cache — use PgBouncer ≥ 1.21 with `max_prepared_statements`, or pgx's describe/simple exec mode. The server's startup check **detects a pooler and configures both**, and logs what it chose.
- Object storage (S3/R2) with per-org prefixes; `api` replicas behind a load balancer; `worker` separate; the relay separate; OpenTelemetry collector → managed metrics/logs/traces.
- Organization onboarding, plans and invoicing in a private service outside this repo (D4).

---

## 4. Push relay (0.4, repository `openbekci-relay`)

### 4.1 Why

Mobile push needs FCM/APNs credentials that belong to whoever publishes the store app; a self-hosted server cannot hold them. The relay holds those credentials and forwards **content-free** notifications on behalf of any enrolled server (overview §6.5, backend §12). The project runs the **official relay** for the store builds; anyone can run one for a white-label build.

### 4.2 Design

The relay keeps **no device registry and no message content**. It is *not* stateless: it persists enrollments, nonces and quotas in Postgres.

```
device ──registers token──▶ server (device.token encrypted, device.relay_id)
server ──POST /v1/send (HMAC, ts, nonce)──▶ relay ──▶ FCM / APNs ──▶ device
                                            ◀── invalid-token feedback ──
```

**Server configuration (backend §17).** A server configures relays as a list, `OPENBEKCI_PUSH_RELAYS` = `relay_id=url;server_id;secret_file;app_id`, one entry per relay. A device registers with the `relay_id` of its app build (mobile §9); the server routes through that entry. The official relay is **off until enrolled**; the discovery document's `push.relay_ids` lists what is enrolled so the app knows whether push will work. UnifiedPush endpoints are plain HTTPS URLs the server posts to directly; they bypass the relay.

**Enrollment** (`openbekci admin push-relay enroll --relay official --url https://push.openbekci.org --secret-file … [--invite CODE] [--manual]`):

1. The server generates a random **ephemeral challenge**, keeps it for 10 minutes and serves it in the discovery document as `relay_enrollment.challenge` (backend §8) only while the enrollment is pending.
2. The server calls `POST /v1/enrollments { base_url, challenge, app_id, invite_code? }`.
3. The relay fetches `https://<base_url>/.well-known/openbekci.json` under **SSRF guards**: resolve DNS first and reject private, loopback, link-local and cloud-metadata ranges (re-checked on the resolved address, not the name); HTTPS only; no redirects; 64 KB body cap; 5 s total timeout. The challenge must match; the session then closes and the challenge cannot be reused.
4. The relay returns `{ server_id, server_secret }` once; the command writes the secret file (mode 600) and prints the `PUSH_RELAYS` entry to add. The challenge disappears from the discovery document.

`RELAY_ENROLLMENT=open|invite|closed`, **default `invite`**: an enrollment needs a one-shot invite code issued by the relay operator (the official relay issues them from a request page described on its policy page; the mode may become `open` once the abuse picture is clear). **Manual path** for LAN/VPN instances the relay cannot reach: an invite code marked `manual` lets `--manual` skip step 3; the server row is flagged `unverified` and gets a lower default quota.

**Send.** `POST /v1/send`, `Authorization: Relay-HMAC <server_id>:<ts>:<nonce>:<hex hmac-sha256(secret, ts "." nonce "." sha256(body))>`. The relay rejects `|now − ts| > 5 min`, a nonce seen within the window (stored), and bodies over 256 KB. Body:

```json
{ "messages": [
    { "push_kind": "fcm" | "apns",
      "token": "<device token>",
      "app_id": "org.openbekci.app",
      "priority": "high" | "normal",
      "ttl_seconds": 3600,
      "data": { "instance_id": "…", "org_id": "…", "notification_id": "…",
                "type": "ticket.updated", "collapse_key": "ticket:…", "badge": 3 },
      "apns": { "mutable_content": true, "generic_title_key": "push.ticket_updated" } } ] }
```

`data` is an **allowlist** — `instance_id`, `org_id`, `notification_id`, `type`, `collapse_key`, `badge` (backend §12) — anything else is rejected, never forwarded. `collapse_key` maps to FCM `collapse_key` / APNs `apns-collapse-id`. The response lists per-message results: `ok`, `invalid_token` (the server deletes the device), `rate_limited`, `error`.

**Credentials, limits, control.** One FCM service account (HTTP v1) and one APNs `.p8` key per `app_id`, from files or a secrets manager; several `app_id`s let one relay serve several white-label apps. Per-server token bucket (default 60 msg/s, burst 600) and daily quota; per-IP enrollment limits. **Per-server kill switch on day one** (`relay admin servers disable <server_id>`), plus `enable`, `list`, `invites create`. `/healthz`, `/metrics` (per server id only).

**Trust model (written down, not hidden).** The relay sees device tokens, `org_id`, `instance_id`, notification ids and types — never titles, bodies or names. Because it holds the store credentials it *can* push to any device registered through any enrolled server; a compromised relay could spam or correlate activity per organization, not read notifications. The official relay's logs are content-free by construction: the log schema lives in the relay repo (`docs/log-schema.md`) and contains server id, counts, error classes and timing; request logs are kept 7 days. Tokens are never persisted.

**Degradation.** iOS has no UnifiedPush escape: when the relay is unreachable, disabled or the server is not enrolled, the app degrades to periodic fetch of the unread endpoint (backend §8 realtime: polling). The relay is a **shared point of failure and ban** for every self-hoster using the store app; this is a product risk (§15), mitigated by white-label relays and the fetch fallback, not removed.

**Implementation.** Go, its own repo (AGPL-3.0), Postgres (tables: `server`, `invite`, `nonce`, `quota`), reuses the core's lint/test/release setup, ≈ 2 k lines, one compose file and a chart. The server side (relay client with HMAC, batching, invalid-token handling, the enroll command, status in platform admin) is backend 0.4 work.

### 4.3 Operating the official relay

Two small replicas behind a load balancer, managed Postgres, FCM/APNs credentials in a secrets manager, alerts on error ratio per provider and per server, a status line on the policy page. Terms: a short "relay policy" page (fair use, content-free, invite-based enrollment, may rate-limit or disable). Push messages are free; the cost is uptime and the person on call.

---

## 5. TLS, domains and networking

- **Default: Caddy with automatic TLS** (HTTP-01 / TLS-ALPN-01), which needs a public name resolving to the box and ports 80/443 reachable. nginx and Traefik configurations in `deploy/proxy/` set the same headers and limits.
- The app serves plain HTTP on `:8080`, trusts `X-Forwarded-*` only from `OPENBEKCI_TRUSTED_PROXIES`, and refuses to issue cookies without `Secure` unless `OPENBEKCI_INSECURE_DEV=true` (localhost only).
- **The mobile app (0.4) and the on-site agent (0.3) require valid HTTPS**, so the install guide has three documented paths for sites that cannot get a public certificate the easy way:
  1. **Behind CGNAT / NAT with no public 80/443** — Caddy with a **DNS-01 challenge** (`deploy/proxy/caddy-dns/Dockerfile` builds Caddy with the common DNS provider modules; the README lists them), or a **tunnel**: `compose.tunnel.yml` with Cloudflare Tunnel, or Tailscale Serve/Funnel for VPN-only sites. Both keep every port closed.
  2. **Private CA, first-class** (`docs/ops/private-ca.md`): Caddy's internal CA (`tls internal`) or `step-ca`; export the root; install it on the guard PC, the agent box and residents' phones (Android: user-installed CA; iOS: profile + full trust). The mobile app's network security configuration must trust user-installed CAs for this to work — a requirement handed to the mobile plan. `BASE_URL` is the LAN name; `EXTRA_ORIGINS` adds a public name if both exist.
  3. A LAN-only site cannot be reached by the official relay: use the manual enrollment path (§4.2) or a white-label relay inside the VPN.
- Outbound: SMTP, SMS provider, relay/UnifiedPush endpoints, S3, pack providers (0.4). An egress allowlist is documented for locked-down networks. The agent needs outbound HTTPS to the server only.
- Edge limits: body 50 MB (uploads), 60 s timeouts; the app's own limits are authoritative (backend §13).

---

## 6. Data: backups, restore, retention

- **What is state**: the Postgres database, the file store (`DATA_DIR/files` or the S3 bucket), **and the secrets** — `OPENBEKCI_SECRET_KEY`, `OPENBEKCI_ENCRYPTION_KEY`, the `.env` / secret files, relay secret files. Losing `ENCRYPTION_KEY` makes national ids, device tokens and provider credentials unrecoverable (review §5, overview §6.9). **Every backup includes the secrets**, and the restore test restores them.
- **Order**: dump first, then files, then secrets. A file uploaded between the dump and the file copy yields a `file` row without a blob after restore; the server tolerates dangling file rows (download returns `not_found`, the platform health page counts them) rather than failing.
- **Compose default (0.1)**: a `backup` sidecar (our own small image: `pg_dump -Fc` + `tar` of files and secrets, 7 daily / 4 weekly / 6 monthly retention into the `backups` volume), then `openbekci admin backup-marker` so the platform dashboard shows "last backup" (`/v1/platform/backup-status`) and nags after 36 h. An `rclone` example pushes the volume off-site; **off-site copies must be encrypted** (`rclone crypt`) because they now contain the keys — the README says so in bold.
- **Restore** is a first-class, tested path: `docs/ops/restore.md` with exact commands (secrets → `.env`, database, files, `migrate status`, start, smoke). The CI nightly job restores the latest dump of the golden site into a fresh stack **including the secrets**, and proves an encrypted field decrypts by reading a masked national id through the API — a backup that was never restored is not a backup.
- **PITR** for the hosted service (managed Postgres); `wal-g` documented as optional for self-hosters.
- **Retention inside the app** (backend §16) is an application concern; erasure requests do not purge historic backups and the policy page says so (backups age out).
- **Site export/import** (D2, backend §5): `admin export-site` / `import-site` ship in 0.1; moving a site between a hosted and a self-hosted instance in both directions is tested in CI at 1.0.

---

## 7. Upgrades

- Semantic versions; `latest` moves only on stable releases; pre-1.0 minors may contain breaking configuration changes listed under a fixed heading in the release notes. The server refuses to start on unknown or removed settings with a clear message.
- **Compose**: the pre-start hook takes a backup, then `migrate up`, then `serve` — `docker compose pull && docker compose up -d` is the whole procedure. The upgrade guide still says "take a backup first" for people who turned the hook off.
- **Expand/contract** migrations (backend §17). From 0.2 on, CI runs the **upgrade test**: install the previous release on the golden site, upgrade to the PR's build, run the smoke suite. Rolling deployments (Helm, 1.0) additionally need version N's code to run against N+1's schema during the overlap; that compatibility job arrives with the chart.
- **Skipping versions**: supported across minors within a major; majors require passing through the last minor of the previous major (`migrate` enforces with a helpful error).
- **Rollback**: previous image + `migrate down` for migrations flagged reversible; otherwise restore the pre-upgrade backup.
- **Packs** (0.4) ship inside the binary and migrate with it.
- **Mobile/server compatibility**: the server states `min_app_version` in the discovery document and supports the app versions it names; the app supports the previous two server minors (§11 contract tests). Release notes state the supported range.

---

## 8. Observability

- **0.1**: JSON logs on stdout (`slog`, never emails or phone numbers), Prometheus `/metrics` behind `OPENBEKCI_METRICS_TOKEN`, `/healthz`, `/readyz` (DB, migrations, storage, SMS gateway), OTLP traces when configured (backend §15), and the **"last backup" marker** written by `admin backup-marker` and shown in the platform dashboard. That is enough to run a site responsibly.
- **1.0**: `compose.observe.yml` (Grafana + Prometheus + Loki/Promtail + Tempo + OTel Collector) with provisioned dashboards in `deploy/observe/dashboards/` — request rate/latency/errors, River queue depth and job lag, notification outcomes by channel, SMS per site, DB pool and slow queries, disk usage, relay results, agent last-seen — and Prometheus **alert rules**: readiness failing, error ratio > 2 %, job lag > 10 min, backup older than 36 h, disk > 85 %, certificate expiry < 14 d, agent silent > 15 min.
- Optional Sentry-compatible DSN, off by default. **No telemetry phones home**; an opt-in anonymous instance ping is not in v1 and would need an ADR.
- Hosted: the same signals into a managed stack; per-org labels only on low-cardinality metrics.

---

## 9. Security operations

- Images: distroless, non-root, read-only root filesystem; `cap_drop: ALL` in compose; `securityContext` in Helm.
- Supply chain: Dependabot (Go, npm, Actions; pub in the mobile repo), `govulncheck`, `pnpm audit`, Trivy image scan on every build; SBOMs attached; keyless cosign signing; `cosign verify` instructions in the install guide; SLSA provenance. The relay and agent repos inherit the same workflow.
- Secrets never in images or repos; `.env` mode 600; Kubernetes `Secret`s or external-secrets; `_FILE` variants.
- **Rotation runbooks** (`docs/ops/rotate-secrets.md`): `SECRET_KEY` signs links and short-lived file URLs only — with opaque session tokens its rotation **no longer logs anyone out**, it just invalidates in-flight magic links and signed URLs; `ENCRYPTION_KEY` via `admin rotate-keys` (key id stored with every ciphertext) covers core **and pack tables** through the same helper (backend §16); relay secrets by re-enrolling; SMTP/SMS/provider keys per provider.
- `SECURITY.md`: private disclosure address, 90-day coordinated disclosure, GitHub Security Advisories; security fixes are patch releases on the latest two minors.
- `docs/ops/hardening.md` checklist: firewall (only 80/443 or nothing with a tunnel), automatic OS updates, Postgres not exposed, backups off-site and encrypted, platform-admin re-auth habits, log retention, agent box physical security.

---

## 10. Scaling guidance

| Size | Recommendation |
|---|---|
| ≤ 2 000 units, one org | Compose on a 2 vCPU / 2–4 GB VPS; embedded worker; local file storage |
| ≤ 20 000 units, a few orgs | Compose with `worker` as a separate container; Postgres on its own host or managed; S3 storage; the observability stack (1.0) |
| Hosted / many orgs | Kubernetes/Helm (1.0): 2+ `api`, 1–2 `worker`, managed Postgres behind PgBouncer (transaction mode, §3.4), S3, relay, OTel |

Known hot paths: statements and reports are `SUM` queries over the index led by `(org_id, site_id, account_id, entry_date)` — there is **no `account_balance` cache** (backend §4.4), which is sub-millisecond at 500 units and fine at 20 000; charge generation and late-fee crystallization are hourly catch-up jobs batched per site under River concurrency caps; PDF rendering and imports are capped at `max(1, NumCPU/2)` (worker-only when a separate worker exists); bank statement matching uses bounded batches.

---

## 11. On-site access agent (0.3, repository `openbekci-agent`)

**What it is.** A small Go service on a Raspberry-Pi-class box or the guard PC on the site LAN. It is the hands of the access-control module (backend §4.15, core `access/`): it hears from the cameras and readers, opens the barrier or door, and keeps working when the internet does not. Concepts (plate registry) are core from 0.1; the agent and drivers arrive in 0.3 (D6). The **agent design document precedes the phase** (backend §20) and fixes the protocol and table names; this section states the shape.

**Connection.** Outbound only: a WebSocket to the server (long-poll fallback for awkward proxies), **token-enrolled per site**. The site manager creates an agent in the access settings and gets a one-time enrollment code (15 min); the agent exchanges it for a long-lived opaque agent credential (hash stored server-side, instantly revocable, same pattern as sessions). The protocol is part of `api/` so the agent's client is generated like every other one, and it is versioned: the server supports agents from the previous two minors.

**Offline-first allow-list.** The server pushes a snapshot and then deltas of what may open which access point: registered plates, visitor QR tokens with their validity windows and recurring rules (C2), later cards and PINs. The agent stores it locally (encrypted at rest with a key in its state directory), decides at the gate **without the server**, logs every opening locally, and syncs the opening log (`access_event`) when the connection returns, with agent timestamps preserved. The guard's "open" button goes server → agent as an idempotent command and fails visibly within seconds if the agent is offline. Visitor QR codes are site-bound (C2 defaults) until per-gate binding exists.

**Device drivers.** A `Driver` interface (events in: plate read, button, reader; commands out: open an access point), registered through an explicit slice the way packs are (`var Drivers = []driver.Driver{…}`, no `init()`). First drivers: **plate-recognition camera events** (HTTP push from the camera or polling its API — the brand is the first real site's, overview §11) and a **dry-contact relay** via USB serial boards, Pi GPIO, or HTTP relays. Later: Wiegand/OSDP readers, intercoms. Driver endpoints that must listen (camera push) bind to the LAN interface only, with a shared secret.

**Packaging.** Single static binary (linux/arm64, linux/amd64, windows/amd64 for the guard PC), a hardened systemd unit with a dedicated user in the device groups it needs (`dialout`, `gpio`), an optional Docker image (device passthrough documented), GoReleaser with SBOM and cosign like the server. **Auto-update** is opt-in: the agent checks releases, verifies the cosign signature, applies at the site's maintenance hour, keeps the previous binary for rollback; the server shows "update available" either way.

**Enrollment and revocation.** Revoking from the site admin invalidates the credential at once; the agent receives `revoked`, wipes its local allow-list and stops acting. While offline it keeps working for a grace period (site setting, default 72 h), then stops automatic opening; a stolen box therefore carries at most a 72-hour-old encrypted allow-list and nothing else — no contacts, no balances.

**Security.** No inbound ports to the agent from outside the LAN; signed updates; least privilege; the credential in a mode-600 file; valid HTTPS to the server (so the private-CA flow of §5 applies to the agent box); logs carry event ids and access-point ids, not plates (plates are PII under C2 retention).

**Monitoring.** Heartbeat every 30 s with version and driver status; `last_seen` shown in the site's access settings and in platform admin; the server notifies the manager when an agent is silent longer than a site-configured threshold; an optional `/metrics` on localhost.

**Estimate.** ≈ 6 engineer-weeks (overview §8): protocol + enrollment 1.5, local store and sync 1.5, two drivers 2, packaging/update/docs 1. The access-control module on the server side is backend 0.3 work.

---

## 12. Environments and CI/CD

- **Branches**: trunk-based; every PR builds the artifacts it touches (not published); `main` publishes `:edge` images and a rolling `edge` compose example for testers.
- **Path-filtered required checks per directory** (GitHub Actions, reusable workflows): `ci-api` (Redocly lint, generate-check across Go/TS, Dart in CI), `ci-server` (lint, unit, integration on real Postgres, `-tags nowebassets` build so Go-only contributors never need Node), `ci-web` (lint, typecheck, unit/component, build + size budget), `ci-sdk` and `ci-packs` (0.4), `ci-deploy` (compose config validation, shell lint). A merge gate turns "skipped by filter" into "passed" so protection rules stay simple. Mobile CI lives in `openbekci-mobile`; relay and agent CI in their repos.
- **Generated-code policy**: committed — `server/internal/httpapi/gen`, `clients/ts`; CI-generated, never committed — the Dart client, MSW handlers, zod schemas (backend §19). **Dart generation and pub.dev publishing happen only in CI** (openapi-generator needs a JVM); the mobile repo pins a spec version and consumes the published client.
- `e2e-nightly`: compose from `main` images, seed the golden site (`server/testdata/golden-site`), backend API tests, Playwright, backup → restore test including secrets (§6), upgrade test from the last release (0.2 on).
- **Contract tests against store/self-host drift**: the mobile repo runs the current app's integration tests nightly against the **previous two server releases** (the core publishes `:<major>.<minor>` images for this). The app tolerates unknown values because **spec enums are open** (overview §6.2); the server supports the app versions it names in `min_app_version`.
- `release` on `v*` tags of the core repo: GoReleaser (binaries, images, SBOM, signing, provenance), Dart client publish, GitHub Release with generated notes (Conventional Commits). Chart release joins at 1.0. No mobile artifacts are attached here; the mobile repo tags `v*` itself and submits to the stores from there.
- **Cadence**: a minor roughly monthly while pre-1.0; patches as needed; `release-x.y` branches only for backports of security fixes.
- **Hosted environments**: `staging` (auto-deploy from `main`, golden data, used by the mobile staging build), `prod` (tag-driven, manual approval). Self-hosters only see tags.
- **Demo instance** (0.4, **required before store submission** — reviewers need a login): `demo.openbekci.org` reset nightly from the golden site with a read-mostly demo org; doubles as a smoke test of the release pipeline.

---

## 13. Documentation site

- Through 0.4 the operator docs are Markdown in `docs/ops/` rendered on GitHub: install (compose / systemd), proxy configs, TLS paths (§5), backups & restore, upgrades, hardening, secrets rotation, private CA, agent install (0.3), push relay — use the official one / run your own (0.4), troubleshooting (`readyz` failures, migrations, email, SMS, push, agent offline).
- **1.0**: a generated site at `docs.openbekci.org` from `docs/` plus the API reference from `api/openapi.yaml` (Redocly or Scalar, versioned per release). **Generator still open** (Starlight/Astro vs MkDocs Material — prefer what the contributors know; decide when the site is built, not before).

---

## 14. Repository additions

Core monorepo (overview §5): `deploy/compose/` (`compose.yml`, `compose.dev.yml`, `compose.minio.yml`, `compose.tunnel.yml`, `.env.example`, `Caddyfile`, `README.md`; `compose.observe.yml` at 1.0), `deploy/systemd/`, `deploy/proxy/` (nginx, Traefik, `caddy-dns/`), `deploy/helm/` and `deploy/observe/` (1.0), `docs/ops/`, `.github/workflows/` (path-filtered reusable workflows), `tools/backup/` (the sidecar image).

**Not in the core repo**: no `relay/`, `agent/` or `mobile/` directories. `openbekci-relay` (0.4) and `openbekci-agent` (0.3) are created when built, AGPL-3.0, each with its own GoReleaser, Dockerfile, compose example, `SECURITY.md` pointing at the core's process, and a dependency on the published spec/clients rather than on the core module.

---

## 15. Infra deliverables by release

| Release | Deliverables | Exit criteria | Estimate |
|---|---|---|---|
| **0.1** | `deploy/compose` (Caddy + Postgres + backup sidecar with nightly `pg_dump` **+ files + secrets**), key generation in the README, `TRUSTED_PROXIES` compose default, restore runbook + **nightly restore test**, `admin backup-marker` wiring, systemd unit, proxy configs incl. the DNS-01/tunnel/private-CA paths, GoReleaser (binaries, multi-arch distroless images, SBOM, cosign, provenance), path-filtered CI with the merge gate, `e2e-nightly` skeleton, `SECURITY.md` / `CONTRIBUTING.md`, docs: install, backup, restore, upgrade, hardening | `docker compose up` on a clean VPS gives a TLS login page; a tag produces signed, verifiable artifacts; the nightly restore job is green and proves decryption | ≈ 4 engineer-weeks |
| **0.2** | Nothing new beyond the **upgrade test from the previous release** (0.1 → 0.2) in CI, PR template checkbox for expand/contract | The upgrade job catches a deliberately breaking migration in a PR | ≈ 0.5 wk |
| **0.3** | `openbekci-agent` repository: packaging (binaries, systemd, Docker), enrollment/revocation, local store and sync, first two drivers, signed opt-in auto-update, agent install docs, last-seen in the platform and site admin | A guest's QR opens the barrier with the uplink unplugged; the log appears after reconnecting; revocation stops the agent within one heartbeat | ≈ 6 wks (agent) |
| **0.4** | `openbekci-relay` repository and the **official relay in operation** (invite mode, kill switch, policy page, content-free log schema, alerts); Dart client generation + pub.dev publishing for the mobile repo; **demo instance** with nightly reset (before store submission); relay enrollment docs | Push from a fresh self-hosted compose instance reaches a store-build device through the official relay; a LAN instance enrolls through the manual path; the demo login works for store reviewers | ≈ 4 wks (relay) + ≈ 1 wk |
| **1.0** | Helm chart (api/worker, migration hook, PDB, NetworkPolicy, ServiceMonitor) installed on `kind` in CI; `compose.observe.yml` with dashboards and alert rules; docs site; site export/import tested hosted ↔ self-hosted; support matrix (Postgres, browsers, OS), sizing/cost guide, external security review of the relay | Chart passes the smoke suite; a self-hoster migrates to the hosted service with `export-site` and back | ≈ 5 wks of the 1.0 total |

---

## 16. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| Who owns and funds the official relay, the demo and docs domains | Owner decision (overview §11); the design keeps it cheap; white-label relays remove the dependency |
| **The relay is a shared point of failure and ban** for every self-hoster using the store app; iOS has no UnifiedPush escape | Product risk, accepted: invite-mode enrollment, per-server kill switch, quotas, content-free logs, app falls back to periodic fetch; documented on the policy page |
| Relay enrollment abuse or SSRF through the discovery fetch | Ephemeral challenge, invite codes, DNS-resolved private-range rejection, HTTPS only, no redirects, 64 KB / 5 s caps, HMAC with timestamp window and nonce |
| Transaction poolers | Not `SET LOCAL` (safe); River `PollOnly` and prepared-statement mode — the startup check configures both; documented in §3.4 |
| Self-hosters skipping backups, or keeping backups that now contain keys unencrypted | Sidecar on by default; "last backup" nag in the platform dashboard; off-site encryption in bold in the README; restore test in CI |
| Which plate-recognition camera brand the first site uses — decides the first driver | Owner, before 0.3 (overview §11); the driver interface keeps the second brand cheap |
| Sites behind CGNAT or without a domain cannot get a public certificate, and the agent and app need valid HTTPS | DNS-01, tunnel and private-CA paths all documented first-class (§5); the mobile app must trust user-installed CAs |
| A stolen or compromised agent box | Encrypted local store with only the allow-list, revocation wipes it, offline grace period bounds exposure, hardening page |
| Migrations that are not expand/contract-safe slip in | Upgrade test from the previous release (0.2 on); PR checklist; the overlap-window job at 1.0 |
| Store builds vs server versions drifting for self-hosters who do not upgrade | `min_app_version`, open enums, contract tests against the previous two server releases |
| Docs generator choice | Open until 1.0; both candidates fine |
| Opt-in telemetry debate | Not in v1; an ADR with public aggregate numbers if ever proposed |
