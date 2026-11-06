# OpenBekci — Access-Control Module and On-Site Agent (0.3 design)

| | |
|---|---|
| Status | **Draft v0.1** — 2026-11-07 |
| Parent | [00-overview.md](00-overview.md) D6 (concepts in core, hardware in a module + on-site agent), D8 (agent in its own repository `openbekci-agent`). Reserved tables in [01-backend.md](01-backend.md) §4.15; agent shape in [05-infra.md](05-infra.md) §11; product behaviour in [decisions/community.md](../decisions/community.md) C2 |
| Audience | Two implementers who will not talk to each other: **A** builds `server/internal/core/access` (and the `api/` additions); **B** builds the standalone Go agent in `openbekci-agent`. Everything they must agree on is in §3, §4, §7.1 and the appendix; the shared fixtures (§10) are the arbiter |
| Conflicts | This document fixes the table and protocol names that backend §4.15 and infra §11 left open. Where it differs from those two, this one wins and they are updated with it |

---

## 1. Scope and non-goals (0.3)

**In scope**

- Access points (vehicle gates, pedestrian doors) per site, each served by one on-site agent through a driver.
- Two credential kinds issued in 0.3: **plate** (resident vehicles from the 0.1 registry; visitor plates) and **visitor QR** (C2 pre-registered and recurring visitors). Kinds `tag` and `pin` exist in the schema and protocol and are **never issued** in 0.3.
- Offline-first decisions at the gate by the agent, from a synced allow-list; opening log (`access_event`) synced back.
- Guard "open" command (web → server → agent → driver) with a visible result, and a live "who just arrived" feed.
- Agent enrolment, revocation, token rotation, monitoring.
- Drivers: dry-contact relay (serial, HTTP, GPIO), plate-camera driver *interface* with a `mock` implementation, plus the first real camera driver once the first site's brand is known.

**Non-goals**

| Out | When / why |
|---|---|
| Opening doors from the resident app (phone as credential) | 0.4, with mobile. The protocol already carries `command.open_point` with an `actor`, so nothing changes on the agent |
| Intercom, video calls, door-station integrations | Later; no design here |
| Cards, fobs, PIN pads, Wiegand/OSDP, fingerprint/face | Later drivers; `tag`/`pin` kinds reserved |
| Anti-passback, occupancy counting, lane logic, parking capacity | Not planned |
| Storing camera images on the server | Not in 0.3. `raw_ref` points at something local to the agent |
| Global single-use enforcement across several offline agents | Single-use is enforced per agent; see §6.4 |
| Agent-side user interface | The agent has a CLI and logs only; operators use the web admin |

---

## 2. Domain model (core `access` module)

All tables are tenant tables (leading `org_id`, RLS, composite FKs per backend §5). IDs are UUIDv7. Timestamps are `timestamptz` (UTC); API output RFC 3339.

### 2.1 Tables

**`access_point`**

| Field | Notes |
|---|---|
| `id`, `org_id`, `site_id` | |
| `name` | "Main gate — in" |
| `kind` | `vehicle_gate` / `pedestrian_door` (open enum) |
| `direction` | `in` / `out` / `both` — informational and for filtering the guard feed; no anti-passback |
| `agent_id` | the agent that drives it; nullable until assigned; an agent may serve many points |
| `driver_ref` | `jsonb {instance, channel}`: `instance` names a driver instance in the **agent's local config file**; `channel` selects a relay/lane within it. Hardware details and secrets never live on the server |
| `enabled` | disabled points are omitted from snapshots and refuse `open` |
| `created_at`, `updated_at` | |

**`credential`** — *derived*, never edited by hand in 0.3 (§2.2).

| Field | Notes |
|---|---|
| `id`, `site_id` | |
| `kind` | `plate` / `visitor_qr` / `tag` / `pin` |
| `value` | plate: **normalized plate** (§7.4). `visitor_qr`: `hex(sha256(token))`. `tag`/`pin` (reserved): hex SHA-256 with a per-site salt |
| `source_type`, `source_id` | `vehicle` / `visitor_entry` / `manual`; unique `(site_id, kind, source_type, source_id)` |
| `unit_id?`, `person_id?` | for display and reporting; **never sent to the agent** |
| `point_scope` | `all_vehicle` / `all_pedestrian` / `all` / `ids` (+ `point_ids[]`). Resolved to explicit point ids when building allow-list entries, so adding a gate re-projects (§2.2) |
| `valid_from`, `valid_to?` | UTC; null `valid_to` = open-ended |
| `recurring?` | `jsonb {days:[1..7], start:"HH:MM", end:"HH:MM"}` in the **site timezone** (§3.4); ISO weekdays, 1 = Monday. `start > end` means the window crosses midnight and belongs to its start day |
| `max_uses?` | `1` for single-entry visitors, null otherwise |
| `uses` | maintained from `access_event` (allowed events naming this credential) |
| `status` | `active` / `suspended` / `revoked` / `expired` |
| `status_reason`, `created_at`, `updated_at` | |

**`access_event`** — **append-only** (no UPDATE/DELETE grant; pruning and PII scrubbing only through `SECURITY DEFINER` functions owned by the migrator, like `privacy_erase`, backend §16).

| Field | Notes |
|---|---|
| `id` | the **client-generated UUIDv7** from the agent (generated by the server for server-originated events); primary key together with `org_id` → idempotent ingest |
| `site_id`, `point_id` | |
| `agent_id?` | null for server-originated events |
| `credential_id?` | the matched credential, if any |
| `decision` | `allow` / `deny` / `manual_open` |
| `reason` | `match` `unknown` `outside_window` `wrong_point` `revoked` `used_up` `agent_stopped` `guard` `manager` `remote` `driver_error` (open enum) |
| `presented_kind?`, `observed_plate?` | what the reader saw. Plate stored normalized for the guard feed; QR/tag values are not stored (the credential id suffices) |
| `actor_user_id?`, `actor_person_id?` | the guard or manager for `manual_open` |
| `command_id?` | the open command that caused it |
| `visitor_entry_id?` | when the console linked the opening to an expected visitor |
| `occurred_at` | agent clock as reported, **corrected** per §4.9 |
| `synced_at` | server receive time |
| `sync_seq` | `bigint identity`, the feed cursor (§5.2) |
| `raw_ref?` | opaque string meaningful to the agent (e.g. local snapshot filename); not dereferenced by the server |
| `raw_payload?` | optional small `jsonb` (≤ 2 KB) of driver detail (confidence, lane); removed by retention with the row |

Retention follows the site visitor-log setting (default 90 days, C2 defaults). `observed_plate` is cleared by `privacy_erase` for the person owning the matched credential.

**`agent`**

| Field | Notes |
|---|---|
| `id`, `org_id`, `site_id`, `name` | |
| `status` | `pending` (created, not enrolled) / `active` / `revoked` |
| `enrol_token_hash?`, `enrol_expires_at?` | one-time token, 15 minutes, SHA-256 |
| `token_hash`, `previous_token_hash?`, `previous_expires_at?` | long-lived agent token: SHA-256 of a 256-bit secret (high entropy, so no password hash); the previous token stays valid 10 minutes after rotation (§4.10) |
| `last_seen` | updated on every received frame, at most once per 10 s |
| `version`, `platform`, `protocol_version` | from `hello` |
| `snapshot_version` | allow-list version the agent last confirmed |
| `queue_depth`, `drivers_status jsonb`, `clock_offset_ms` | from `heartbeat` |
| `created_by`, `revoked_at?`, `revoked_by?` | |

**Support tables**

- `access_site_state(site_id, version bigint)` — the per-site allow-list version counter. Writers `SELECT … FOR UPDATE` the row, increment, and append changes in the same transaction, so versions are gap-free and ordered.
- `allowlist_change(site_id, version, op, credential_id, entry jsonb)` — the change log behind deltas; rows older than 7 days (and beyond the newest 50 000 per site) are pruned; an agent older than the oldest retained version is sent a snapshot.
- `agent_command(id, agent_id, point_id?, command, payload jsonb, actor_user_id?, status, result jsonb?, issued_at, expires_at, delivered_at?, completed_at?)` — status `pending` / `delivered` / `done` / `failed` / `expired`. Commands are the only server → agent state that is persisted; snapshots and deltas are derived.

Site settings (`site.settings.access`): `offline_grace_hours` (default 72), `agent_silent_minutes` (default 5), `plate_match` (`exact` default / `tolerant`, §7.4), `maintenance_hour` (for updates, infra §11). The visitor window slack (±30 min) is a community setting, applied at projection.

### 2.2 Projection of vehicles and visitor entries into credentials

Principle: **derive, do not duplicate.** `vehicle` and `visitor_entry` stay the source of truth; `credential` rows are a materialized, idempotent function of them. Nobody edits a credential in 0.3; the admin UI shows them read-only with a link to the source.

| Source | Credential | Active when | Validity | Scope | `max_uses` |
|---|---|---|---|---|---|
| `vehicle` (0.1: plate, unit) | one `plate` | the unit has an active owner/tenant/household occupancy today and the vehicle is not archived | open-ended; `valid_to` = end of the occupancy if it has one | `all_vehicle` | null |
| `visitor_entry` with `plate`, pre-approved or approved | one `plate` | `approval_status ∈ {pre_approved, approved}` and not cancelled | `window ± slack`; or the recurring rule | `all_vehicle` | 1 unless `recurring_rule` |
| `visitor_entry` with `qr_token`, pre-approved or approved | one `visitor_qr` (`value = sha256(qr_token)`) | same | same; recurring entries use `recurring` and their own `valid_from`/`valid_to` | `all` | 1 unless recurring |
| walk-in approved by the resident on the spot (C2.3) | one `plate` if a plate was entered | `approved` | `now … now + slack` | `all_vehicle` | 1 |

Rules:

1. **Statuses:** `denied`, `expired`, cancellation by the resident, and revocation of a recurring visitor set the credential `revoked` (reason recorded); `valid_to` in the past is `expired` (set by the nightly sweep; agents evaluate `valid_to` themselves, so the sweep is bookkeeping, not enforcement).
2. **Recompute triggers** — a River job `access.project{source_type, source_id}` is enqueued **in the same transaction** as the change (River is the outbox), unique per source, for: vehicle create/update/archive; occupancy start/end; `visitor_entry` create, approval decision, cancel, `recurring_rule` change, arrival/leave; `access_point` create/enable/disable/delete (re-resolves `point_scope`). The worker upserts the credential and, **only if the allow-list entry changed**, appends an `allowlist_change` row in the same transaction. No change, no version bump. A site timezone change bumps `config_version` instead (the agent re-reads `site.timezone` from `update_config`/snapshot).
3. **Safety net:** `access.reconcile` runs nightly and on demand (site admin "rebuild allow-list"): re-projects every source of the site, diffs against credentials, repairs drift, expires stale rows. Idempotent; safe alongside live jobs (per-site advisory lock).
4. **Latency target:** a visitor approval reaches a connected agent in **≤ 5 s**. The approval handler enqueues the job with priority and wakes the hub directly (§4.8).
5. **Plates** are normalized with the single function of §7.4 on projection; the same function runs in the agent. A vehicle whose plate normalizes to empty projects nothing and shows a warning on the registry page.
6. Two vehicles with the same plate in different units produce two credentials and two entries; the agent allows if any entry matches; the event names the first match by `id` order.

---

## 3. Allow-list model

### 3.1 Entry

One entry per credential; **no names, no unit ids, no contacts** — the agent holds only what a decision needs.

```json
{ "id": "credential uuid", "kind": "plate", "value": "34ABC123",
  "points": ["point uuid", "…"],
  "valid_from": "2026-11-07T08:00:00Z", "valid_to": null,
  "recurring": { "days": [1,3,5], "start": "09:00", "end": "17:00" },
  "max_uses": 1 }
```

`valid_to` null = open-ended; `recurring` null = always inside the validity bounds; `max_uses` null = unlimited. Schema in Appendix B.

### 3.2 Snapshot and deltas

- **Version:** a per-site monotonically increasing integer (`access_site_state.version`). A **snapshot** is the complete set of entries as of version *N*, split in parts of ≤ 2 000 entries (`part`/`parts`); the agent applies it atomically only when all parts have arrived. A **delta** is an ordered list of `upsert`/`remove` changes taking the agent from `from_version` to `to_version` with no gaps.
- **Which one:** on `hello` the agent states its `snapshot_version`. Equal → nothing. Behind and within change-log retention → a delta (several messages if > 500 changes). Otherwise, or after a `gap` ack (§4.6) or a `request_snapshot` command → a snapshot. Connected agents receive each new version as a delta, normally one version at a time.
- **Point membership** is part of each entry (`points`), so the agent needs no separate policy table. Point definitions arrive in `update_config` (§4.6).

### 3.3 Size estimate (2 000-unit site)

| Item | Count | JSON bytes each | Total |
|---|---|---|---|
| Resident plates (≈ 1.5 vehicles per unit; `all_vehicle` resolves to 2–3 gate ids) | 3 000 | ≈ 170 | ≈ 0.5 MB |
| Active visitor credentials at a time (pre-registered, recurring, walk-ins) | ≈ 300 | ≈ 330 (64-char hash, windows) | ≈ 0.1 MB |
| **Snapshot** | | | **≈ 0.6 MB raw, ≈ 80–100 KB compressed** (permessage-deflate on the socket, gzip on long-poll) |
| Delta, one visitor | 1 | | < 1 KB |
| Daily churn | ≈ 100–200 changes | | < 0.1 MB/day |
| Agent local store | | | < 5 MB incl. indexes |

Upper bound the agent must handle: **20 000 entries / 8 MB** per snapshot; beyond that the server refuses (not expected for a residential site).

### 3.4 Local evaluation (agent clock and timezone)

`snapshot` and `update_config` carry `site.timezone` (IANA); the agent embeds `time/tzdata` and uses its **own clock** (NTP expected; §4.9). Decision for a presentation `(point, kind, value)` at instant *t*:

1. Normalize the value (plates §7.4; QR: parse `obk1.<token>` → `sha256` hex).
2. Look up entries by `(kind, value)`; none → `deny / unknown`. Entries whose `points` lack the point → `deny / wrong_point`.
3. For each candidate: `t < valid_from` or `t > valid_to` → outside window; `max_uses` reached by the **local** use count → `used_up`.
4. `recurring`: convert *t* to site time; the entry matches if (a) today's ISO weekday ∈ `days` and local time ∈ `[start, end]`, or (b) when `start > end` (overnight), yesterday's weekday ∈ `days` and local time `< end`. Times are wall-clock in the site timezone, so DST shifts follow the wall clock. Boundaries inclusive at the minute.
5. First matching entry wins → `allow / match`; otherwise the most specific denial seen (`outside_window` over `wrong_point` over `used_up`).
6. The shared fixtures (§10) pin these rules, including a DST transition and an overnight window.

---

## 4. Agent ↔ server protocol (v1)

Minimal and explicit. JSON is UTF-8, `snake_case`, timestamps RFC 3339 UTC with milliseconds. Message schemas are normative in **Appendix B** and extracted verbatim to `api/agent-protocol/v1/*.schema.json` (Apache-2.0, so the agent repo may vendor them). The three plain-HTTP endpoints (enrol, poll, rotate) are also in `api/openapi.yaml` under tag `agent`, `x-stability: preview` until 0.3.0 ships.

### 4.1 Credentials

| Credential | Where used | Lifetime |
|---|---|---|
| **Enrolment token** `obk_enr_<random>` | once, at enrolment | 15 min, single use, created by the site manager in the web UI (`POST /v1/orgs/{org}/sites/{site}/agents` → agent `pending` + token shown once) |
| **Agent token** `obk_agt_<agent_id>.<256-bit secret>` | `Authorization: Bearer` on the WebSocket upgrade and on poll/rotate | until revoked or rotated; hash only on the server; scoped to **one site** — it resolves to `(org, site, agent)` and nothing in the protocol names another site |

### 4.2 Enrolment

`POST /v1/orgs/{org}/sites/{site}/agents:enrol` (unauthenticated; the one-time token is the credential; 10/min per IP)

```json
// request
{ "enrolment_token": "obk_enr_…", "hostname": "pi-gate", "platform": "linux/arm64", "agent_version": "0.3.0" }
// 200
{ "agent_id": "…", "agent_token": "obk_agt_…", "org_id": "…", "site_id": "…",
  "server_time": "…", "ws_url": "wss://host/v1/agent/ws", "poll_url": "https://host/v1/agent/poll",
  "protocol_versions": [1] }
```

Unknown, expired or used token → `404` with one identical body (no probing). The agent writes the token to its state directory (mode 600) **before** reporting success to the operator. Re-enrolling needs a new token (the manager's "re-enrol" also invalidates the old agent token).

### 4.3 Transport

- **Primary:** one outbound WebSocket `wss://…/v1/agent/ws`, subprotocol `openbekci.agent.v1`, `Authorization: Bearer <agent_token>`, permessage-deflate. Text frames, **one JSON envelope per frame**, max 1 MiB (snapshot parts and event batches are sized to fit). Server ping every 30 s, 60 s pong deadline.
- **Fallback:** HTTPS long-poll. `POST /v1/agent/poll` with `{ "messages": [envelope…], "wait_seconds": 25 }` returns `{ "messages": [envelope…] }` as soon as the server has something or after the wait. Same envelopes, semantics and auth. The agent starts in WebSocket mode, drops to long-poll after 3 consecutive upgrade failures that look like a proxy (non-101 answer, reset before handshake), and **retries WebSocket every 10 minutes**. A long-poll *session* (until 60 s of silence) begins with `hello`.
- **Reconnect/backoff:** exponential 1 s → 60 s cap, factor 2, ±20 % jitter; reset after a connection has been healthy for 60 s. `401` is **not** retried (§4.11); `429`/`503` honour `Retry-After`.
- **Versions:** the agent lists `protocol_versions` in `hello`; the server picks the highest it supports (agents from the previous two minors, infra §11) and states it in its `hello`; no common version → close 4426 and "update required" in the site's access settings.

### 4.4 Envelope

```json
{ "v": 1, "type": "event", "id": "uuidv7", "ts": "2026-11-07T10:00:00.123Z", "payload": { } }
```

`id` is a fresh UUIDv7 per message (referenced by `ack.ref`); `ts` is the sender's clock. Unknown `type` → the receiver replies `ack` with `status: "unsupported"` and continues (forward-compatible).

### 4.5 Message types

| `type` | Direction | Purpose |
|---|---|---|
| `hello` | agent → server, then server → agent | Session start. Agent: identity, versions, `snapshot_version`, `config_version`, driver status. Server: chosen version, `server_time`, heartbeat interval, `rotate_by`, current `snapshot_version` |
| `snapshot` | server → agent | Full allow-list at `version`, in `part`/`parts` |
| `delta` | server → agent | Ordered changes `from_version → to_version` |
| `event` | agent → server | Batch (1–100) of access events; idempotent by each event's UUIDv7 |
| `ack` | both | Acknowledges a message by `ref` with a `status` and type-specific `detail` |
| `command` | server → agent | `open_point`, `revoke_credential`, `request_snapshot`, `update_config`, `revoked` |
| `heartbeat` | agent → server | Every 30 s: queue depth, snapshot version, driver health, uptime, clock |

```
agent                                   server
  │── hello ────────────────────────────▶│  authenticate, update agent row
  │◀──────────────────────────── hello ──│  version, server_time
  │◀────────── command update_config ────│  if config_version differs
  │◀────────── snapshot | delta ─────────│  from the agent's snapshot_version
  │── ack(ref=snapshot|delta) ──────────▶│
  │── event[ … queued while offline ] ──▶│
  │◀──────────────────── ack(accepted) ──│
  │── heartbeat (every 30 s) ───────────▶│
  │◀── delta (as credentials change) ────│
  │◀── command open_point ───────────────│
  │── ack(ref=command, done|failed) ────▶│  followed by an event (manual_open)
```

### 4.6 Message rules

- **`hello`** from the agent is always first. The server validates the token, replies with its `hello`, then sends what the agent is missing: `update_config` if `config_version` differs, snapshot/delta, then pending unexpired commands.
- **`snapshot`/`delta` ordering:** a delta applies only if `from_version` equals the agent's current version; `to_version` ≤ current → ack `duplicate`; `from_version` > current → ack `gap` with `detail.have_version`, and the server answers with a snapshot. The agent persists the new version **in the same transaction** as the changes and acks after commit, so a crash replays harmlessly.
- **`event`:** batches go in `occurred_at` order and stay in the agent queue until acked. The server inserts with `ON CONFLICT (org_id, id) DO NOTHING` and acks `{accepted, duplicates, rejected:[{id, code}]}`. Rejection codes: `unknown_point` (deleted or not this agent's; the agent **drops** it), `invalid` (schema/size; drop and log). Anything else → the agent retries later. At most 5 batches in flight.
- **`command.open_point`** `{point_id, credential_id?, visitor_entry_id?, actor, issued_at, expires_at}`: the agent **refuses a command whose `expires_at` has passed** (default TTL 15 s) so a gate never opens late. Result by `ack` with `status ∈ done | failed | expired | unknown_point | refused` and `detail.reason`. On `done` the agent emits an `event` (`manual_open`, `reason: guard`/`manager`, `command_id`). The agent remembers the last 1 000 command ids for 24 h; a re-delivered id is acked with the stored result and **not executed again**.
- **`command.revoke_credential`** `{credential_id}`: remove immediately; the same removal also arrives in the next delta, so repeating is harmless.
- **`command.request_snapshot`**: the agent acks and applies the next snapshot as a full replacement.
- **`command.update_config`** `{config_version, site{timezone}, points[{id,name,kind,direction,driver_ref,enabled}], settings{offline_grace_hours, plate_match, heartbeat_seconds, maintenance_hour}}` replaces the previous config entirely. A `driver_ref.instance` missing from the local file is reported in `heartbeat.drivers` as `missing`; that point cannot open (`driver_error`).
- **`command.revoked`**: see §4.11.
- **`heartbeat`** doubles as keep-alive; 90 s without one shows the agent offline in the UI (`agent_silent_minutes` governs notification only).
- **At-least-once, idempotent effects:** events by event id, commands by command id, deltas by version, snapshots by version. Nothing is lost silently: unacked events persist on disk (§6); unacked commands expire visibly.

### 4.7 Ack

`{ "ref": "<message id>", "status": "ok" | "duplicate" | "gap" | "done" | "failed" | "expired" | "refused" | "unknown_point" | "unsupported" | "error", "detail": { } }`. For `event` batches `detail` is `{accepted, duplicates, rejected}`.

### 4.8 Server-side hub

- One goroutine per connected agent; **one live session per agent** — a second connection replaces the first (old one closed with 4409).
- New allow-list versions reach the hub by an in-process notification from the projector; with several replicas each hub also polls `access_site_state.version` every 2 s. Commands are noticed the same way via `agent_command`. No dependency on `LISTEN/NOTIFY` (poolers, backend §5).
- Long-poll is served by the same logic: a poll first drains pending messages, else waits on the same notification.

### 4.9 Clock skew

- `hello` and `heartbeat` carry the agent's `ts`; the server's `hello` carries `server_time`. The agent computes `offset_ms = server − agent` (round-trip midpoint) and sends it in heartbeats and event batches (`clock_offset_ms`).
- **Agent decisions always use the agent's own clock** (windows carry slack; visitors ±30 min). If |offset| > 120 s the agent logs a warning and reports `clock_suspect` in heartbeats; it keeps working.
- **Stored `occurred_at`** = reported time + the batch's `clock_offset_ms` when present and |offset| ≤ 24 h, else as reported; never later than `synced_at`. The unadjusted value is kept in `raw_payload.reported_at` when it differs by more than 5 s.
- |offset| > 5 min raises the notification `access.agent_clock_skew` (at most once per 6 h).

### 4.10 Token rotation

`POST /v1/agent/token:rotate` with the current token → `{ "agent_token": "obk_agt_…" }`. The agent writes the new token to a temp file, fsyncs, renames over the old one, **then** uses it; the server keeps the old hash valid 10 minutes, so a crash between steps recovers. The server's `hello` carries `rotate_by` (default 180 days after issue); the agent rotates automatically within 14 days of it. A manager can force rotation (`rotate_now: true` in the next `hello`).

### 4.11 Revocation

Manager revokes in the UI → `agent.status = revoked`, hash cleared. A connected agent receives `command revoked` and the socket closes with 4401; any later attempt gets `401`. **Any `401` on an authenticated call means revoked:** the agent wipes allow-list, queue, config and token, logs once, and refuses to start until re-enrolled. While merely *offline* (server unreachable) it keeps working until `offline_grace_hours` (default 72 h, infra §11), then becomes **stopped** (§6.3). Queued unacked events are lost on wipe; server-side rows stay.

---

## 5. Guard console integration

### 5.1 "Open gate" command path

1. Guard presses *Open* on a point: `POST /v1/orgs/{org}/sites/{site}/access-points/{id}:open` (`Idempotency-Key` required; body `{visitor_entry_id?, credential_id?}`). Permission `access.open` (`guard`, `site_manager`, `org_admin`); attribution comes from the console's quick PIN switch, so the request carries that guard's identity (C2 defaults).
2. Server checks: point enabled, its agent `active` and **connected or seen within 90 s**; otherwise `409 agent_offline` immediately (a visible failure, not a spinner). Rate limits §8. It inserts an `agent_command` (TTL 15 s), wakes the hub, responds `202 { command_id }`.
3. The console polls `GET …/access/commands/{id}` every 500 ms for up to 10 s → `pending | delivered | done | failed | expired` plus `detail.reason`.
4. Agent → driver `Open` → ack → `event(manual_open)`; the server stamps `actor_user_id` from the command, never from an agent claim.
5. Agent up but driver failing: `failed / driver_error`, shown in red with the point name. **No automatic retry** of an open (a second pulse could cycle a gate on a vehicle).

### 5.2 Live "who just arrived"

`GET /v1/orgs/{org}/sites/{site}/access/events?after=<sync_seq>&limit=50` (permission `access.view_events`; the guard role sees the last 24 h only, in the minimized shape below). The console polls every 2 s with `sync_seq` as cursor.

```json
{ "sync_seq": 1042, "occurred_at": "…", "point": {"id":"…","name":"Main gate"},
  "decision": "allow", "reason": "match", "plate": "34ABC123",
  "who": { "kind": "resident|visitor|unknown", "unit_label": "A-12", "visitor_name": "…" } }
```

The guard sees a resident's unit door number, a visitor's name and unit, never other contacts or balances (S8, C2). `deny / unknown` rows are highlighted so the guard can step in. Latency gate → screen ≈ batch delay (≤ 1 s) + poll (≤ 2 s).

### 5.3 Visitor approval interplay (C2)

1. No QR: the guard enters name, plate and unit → `visitor_entry.approval_status = requested` → notification `visitor.approval_requested`.
2. The resident approves (portal / decision link): `approved`, `decided_by` set → the handler enqueues `access.project` with priority and wakes the hub (§4.8).
3. The projector upserts a single-use `plate` credential valid `now … now + slack` (and `visitor_qr` if a token exists) → one `allowlist_change` → `delta` to the agent in seconds. A gate camera can now open by itself; the guard may also press *Open* with `visitor_entry_id` so the event links to the visit.
4. Agent offline: the credential still exists server-side; the console shows agent state prominently and the guard opens by physical means.
5. Denial or expiry sets the credential `revoked`; a delta removes it.

QR scanning in the console stays a community operation (`POST …/visitor-entries:scan`, online validation against `visitor_entry`) and does not need the agent. A QR reader wired to the agent (future hardware) presents `kind: visitor_qr` and takes the offline path of §3.4.

---

## 6. Offline behaviour

### 6.1 Agent store: SQLite

**Choice: SQLite in WAL mode via `modernc.org/sqlite` (pure Go; the binary stays static and CGO-free).** The alternative, one JSON file rewritten atomically (temp + rename), was rejected because (a) the event queue is append-heavy and must survive power loss at a gate — rewriting a 0.6 MB file per event wears a Pi's SD card and adds latency; (b) applying a delta and recording its version must be one transaction; (c) lookups by `(kind, value)` and pruning by expiry need an index at 20 000 entries. Costs: a dependency and a few MB of binary size; accepted.

Tables: `meta(key, value)` (agent id, snapshot version, config version, last server contact), `entry(id pk, kind, value, json)` indexed on `(kind, value)`, `uses(entry_id, count)`, `queue(event_id pk, enqueued_at, json)`, `command_seen(id pk, result, at)`, `config(json)`. `entry.value` and queued plates are encrypted per row with **AES-256-GCM** under a random key in `state_dir/store.key` (mode 600), as infra §11 states. This protects a casually copied database, not a root attacker on the box; the real limits are the 72 h grace and that only plates and hashes are stored.

### 6.2 What continues offline

The agent keeps evaluating from the last snapshot plus deltas, drives gates for matches, and appends every decision (allow and deny) to `queue`. Windows are evaluated by the local clock, so an entry that **expires while offline** denies (`outside_window`) exactly as online; a credential **revoked while offline** keeps working until contact returns (bounded by the grace period) — the accepted trade-off of offline-first, stated in the admin UI. A visitor approved while offline cannot be admitted automatically; the guard opens by hand.

### 6.3 Grace period and stopped state

`offline_grace_hours` counts from the **last successful server contact** (any authenticated frame). After it the agent is **stopped**: it no longer opens for credentials (decisions are still logged as `deny / agent_stopped`) and keeps trying to reconnect. Contact resumes it; a fresh `hello` always follows with a delta or snapshot.

### 6.4 Reconciliation on reconnect

`hello` → config/snapshot/delta first (so the agent is current), then queued events in order, batch by batch, until empty; heartbeats run concurrently. Events keep their original UUIDv7 and `occurred_at`; duplicates are harmless. **Single-use visitors:** the agent enforces `max_uses` against its local count; the server counts allowed events and, at `uses ≥ max_uses`, sets the credential `revoked / used_up` and removes it by delta. Two *different* agents admitting the same single-use credential before syncing is accepted (a site normally has one agent; both events are logged).

### 6.5 Limits

| Limit | Value | On exceed |
|---|---|---|
| Queued events | 100 000 or 50 MB | drop oldest `deny` first, then oldest `allow`; count in `heartbeat.dropped_events`; log once per hour |
| Event batch | 100 events or 512 KiB | split |
| Snapshot | 20 000 entries / 8 MB | server refuses; admin sees a banner |
| Command TTL | 15 s default, 60 s max | `expired` |
| Last-contact grace | `offline_grace_hours` | stopped (§6.3) |
| Presentation dedupe | 10 s per `(point, value)` | camera re-reads of one plate don't re-pulse the barrier |

---

## 7. Drivers

### 7.1 Interface (`openbekci-agent/driver`)

```go
package driver

// Presentation is something a reader showed to a point.
type Presentation struct {
    Instance   string         // driver instance name from agent.yaml
    Channel    string         // lane/reader within the instance ("" = default)
    Kind       string         // "plate" | "visitor_qr" | "tag" | "pin"
    Value      string         // raw; the agent normalizes (plate) or hashes (qr/tag/pin)
    Confidence float64        // 0..1, 1 when not applicable
    At         time.Time      // reader's time if reliable, else receipt time
    RawRef     string         // opaque local reference, e.g. snapshot file name
    Detail     map[string]any // small, non-personal (lane, direction hint)
}

type Health struct {
    State  string // "ok" | "degraded" | "down" | "missing"
    Detail string // short human text, no personal data
}

type Driver interface {
    // Open pulses/opens the physical point; returns after the hardware
    // accepted the command or failed; honours ctx cancellation.
    Open(ctx context.Context, channel string) error
    // Subscribe streams presentations until ctx ends. Output-only drivers
    // (relays) return driver.ErrNoEvents immediately.
    Subscribe(ctx context.Context, out chan<- Presentation) error
    Health(ctx context.Context) Health
    Close() error
}

// Factory builds an instance from its section of agent.yaml.
type Factory func(name string, cfg map[string]any, log *slog.Logger) (Driver, error)

type Registration struct {
    Type   string
    New    Factory
    Schema string // JSON Schema of cfg
}
```

Registration is an explicit slice in `cmd/openbekci-agent/drivers.go` — `var Drivers = []driver.Registration{relay.Registration, platecam.Registration, mock.Registration}` — no `init()` side effects (same rule as packs). A driver instance is bound to a point by the point's `driver_ref {instance, channel}`. **Reading and opening are separate instances**: a plate camera and the relay that lifts the barrier are two instances, and an `allow` decision for a point is executed on the point's own instance (its `driver_ref`). The mapping from a camera lane to a point is the same `driver_ref` on the point (camera instance + lane channel) plus the relay for opening configured in the point's `open_ref` inside the agent's local file (§7.2), so a point may be fed by one instance and opened by another.

### 7.2 Local configuration (`/etc/openbekci-agent/agent.yaml`)

```yaml
server: https://bekci.example.org          # base URL
state_dir: /var/lib/openbekci-agent         # agent.token, store.key, store.db
ca_file: /etc/openbekci-agent/ca.pem        # optional extra CA (private CA, §8)
log_level: info
drivers:
  - name: gate-relay                        # instance names used by driver_ref.instance
    type: relay
    config: { … }                           # per-type schema, §7.3
  - name: gate-cam
    type: platecam
    config: { … }
points:                                     # optional local wiring for points fed by one instance and opened by another
  - point: "main-gate-in"                   # access_point.driver_ref.instance of the reader
    open: { instance: gate-relay, channel: "1" }
```

The server owns point definitions; the file declares hardware and, only where reader and opener differ, the pairing (keyed by the point's `driver_ref.instance` + `channel`). Without a `points` entry the opener is the point's own instance. `openbekci-agent config check` rejects unknown types and schema violations before start.

### 7.3 The 0.3 drivers

**(a) `relay` — generic dry contact** (output only)

| Field | Meaning |
|---|---|
| `mode` | `serial` / `http` / `gpio` |
| `pulse_ms` | contact-closed time, default 800 (100–10 000) |
| `min_interval_ms` | refuse a new open sooner than this, default 2 000 |
| `serial`: `port`, `baud`, `channels: {"1": {on: "A00101A2", off: "A00100A1"}}` | hex byte strings per channel; covers USB relay boards via their documented frames |
| `http`: `channels: {"1": {on_url, off_url, method, headers, timeout_ms}}` | e.g. Shelly Gen1 `http://ip/relay/0?turn=on&timer=1` (`off_url` omitted when the device self-resets), Gen2 `http://ip/rpc/Switch.Set?id=0&on=true&toggle_after=1`; private/LAN addresses only |
| `gpio`: `chip`, `channels: {"1": {line, active_low}}` | libgpiod character device on Raspberry Pi |

Sequence: `on`, wait `pulse_ms`, `off`. `off` is always attempted — also on ctx cancel and panic recovery — so a relay is never left latched. `Health` checks serial port presence, HTTP reachability or chip access.

**(b) `platecam` — plate-recognition camera interface** (input only; `Open` returns `ErrNotSupported`). A *transport* delivers camera messages; a *brand adapter* decodes them:

```go
type Adapter interface {
    Decode(req Message) ([]driver.Presentation, error) // push body or poll response
}
```

| Field | Meaning |
|---|---|
| `mode` | `push` (camera posts to the agent) / `poll` (agent queries the camera) |
| `adapter` | **`generic_json`** (configurable field paths) ships in 0.3; the **brand adapter is "brand TBD — the first real site's camera"**, added to the same package once site and model are known (infra §11) |
| `push.listen`, `push.path`, `push.secret` | `ip:port` on a **LAN interface only** (the agent refuses `0.0.0.0` unless `allow_any: true`); shared secret in a header or basic auth, compared in constant time |
| `poll.url`, `poll.interval_ms`, `poll.auth` | for cameras with a query API |
| `generic_json` | `plate_path`, `confidence_path?`, `time_path?`, `lane_path?` (dotted paths) |
| `confidence_min` | default 0.80; below it the read is logged `deny / unknown` with the detail and not acted on |
| `dedupe_s` | default 30: the same plate on the same channel is ignored (no event) in this window |
| `channel_map` | camera lane/direction value → `channel` string used in `driver_ref.channel` |

**(c) `mock`** — tests and demos; both directions. Config: `script` (file of `{after_ms, channel, kind, value}` presentations, optionally looping), `listen: 127.0.0.1:port` with `POST /present` for manual injection, `open_log` (file recording every `Open` with timestamp), `fail_open: bool`, `open_delay_ms`. Used by the end-to-end test (§10) and the demo compose stack.

### 7.4 Plate normalization

One function, identical in server and agent, pinned by `plate-normalization.json`:

1. Unicode NFKC, then map Turkish letters to their Latin base: `İ ı I → I`, `Ş ş → S`, `Ç ç → C`, `Ğ ğ → G`, `Ö ö → O`, `Ü ü → U`; then ASCII-uppercase.
2. Remove everything except `A–Z` and `0–9` (spaces, dashes, dots, underscores, other punctuation).
3. Empty after step 2 → invalid (no credential).
4. Examples: `34 ABC 123` → `34ABC123`; `34-abc-123` → `34ABC123`; `06 ş 4444` → `06S4444`; `34 ABC 12` → `34ABC12`.
5. **Format check is advisory and country-specific** (core has no `if country`): the web UI warns when a `TR` site's plate does not match `^(0[1-9]|[1-7][0-9]|8[01])[A-Z]{1,3}[0-9]{2,4}$`; the pack validator (0.4) takes over; foreign plates are accepted.
6. **Matching:** `plate_match = exact` (default) compares normalized strings. `tolerant` additionally accepts a presented plate at **Levenshtein distance 1 where the differing characters are a known OCR confusable pair** (`O/0`, `I/1`, `B/8`, `S/5`, `Z/2`, `G/6`) **and only if exactly one allow-list plate qualifies**; the event records `reason: match` with `detail.tolerant: true`. Off by default: a wrong opening is worse than a missed one.

---

## 8. Security

| Topic | Rule |
|---|---|
| Agent token storage | `state_dir/agent.token`, mode 600, owned by the service user; never logged; written by temp-file-and-rename (§4.10). The server keeps only a SHA-256 hash |
| TLS | HTTPS/WSS only; certificate verification always on; no `insecure` flag. For private CAs the agent loads `ca_file` in addition to system roots (infra §5 private-CA flow) |
| Network | **No inbound ports from outside the LAN.** Only the optional camera-push listener (LAN interface, shared secret) and the mock / `/metrics` listeners on localhost exist |
| Command authentication | Commands are accepted **only** on the authenticated, established socket or poll response; the agent has no endpoint that opens anything. The server authors a command only from an authenticated user request with `access.open`; the `actor` in the command is for the agent's log, the server stamps the real actor on the event itself |
| Command freshness | `expires_at` enforced by the agent; replayed ids executed once (§4.6) |
| Rate limits on `open` | per user 10/min; per point 6/min (a gate cycle takes ≥ 10 s); per site 60/min; excess → `429 rate_limited`. The relay driver also enforces `min_interval_ms` |
| Audit | Every `open` request writes an `audit_event` (`access.open_requested`: actor, point, command id, result); the resulting `access_event` carries `actor_user_id`. Agent create/revoke/rotate, point changes and allow-list rebuilds are audited; revoking an agent requires re-auth |
| Agent logs | Event, point, credential and command ids and driver states. **No plates, QR values or names** (plates are PII under C2 retention); plates are logged only with explicit `log_level: debug`, which prints a warning at start |
| Data minimization | Allow-list entries hold plates and hashes only; no unit, person or contact data reaches the agent; a stolen box yields ≤ 72 h of plates (infra §11) |
| Server side | Agent endpoints use agent-token middleware only; agent tokens never satisfy user authz and vice versa; every agent query is scoped by the token's `(org, site, agent)` (tested). Limits: enrol 10/min per IP, WebSocket upgrade 20/min per agent, poll 4/s per agent |
| Notifications (new types) | `access.agent_silent` (silent > `agent_silent_minutes`), `access.agent_recovered`, `access.agent_clock_skew`, `access.agent_stopped`, `access.driver_down`; to site managers by the O5 defaults; added to the vocabulary of backend §12 |

---

## 9. Packaging and operations

Summary of [infra §11](05-infra.md) plus module-specific points.

- **Artifact:** single static binary (`linux/arm64`, `linux/amd64`, `windows/amd64`), GoReleaser + SBOM + cosign, repository `openbekci-agent` (AGPL-3.0). Hardened systemd unit with a dedicated user in `dialout`/`gpio`; optional container image.
- **CLI:** `openbekci-agent enrol --server URL --token obk_enr_… [--config …]`, `run`, `config check`, `status` (version, snapshot version, queue depth, driver health, last contact), `test-open <instance:channel>` (drives the hardware locally, needs the service user), `rotate-token`, `version`.
- **Update policy:** opt-in auto-update at `maintenance_hour`, cosign-verified, previous binary kept; the server's agent list shows `version` and "update available". The server supports agents from the previous two minors; protocol changes within `v1` are additive.
- **Server-side visibility** (site access settings and platform admin): per agent `status`, `last_seen`, `version`, `snapshot_version` vs current, `queue_depth`, `drivers_status`, `clock_offset_ms`; per point last event time. Prometheus gauges when metrics are enabled: `openbekci_agent_last_seen_seconds`, `openbekci_agent_queue_depth`, `openbekci_agent_snapshot_lag`.
- **Agent `/metrics`:** optional, `127.0.0.1` only (queue depth, decisions by reason, driver errors, reconnects).

---

## 10. Test strategy

**Shared fixtures (the contract).** `server/testdata/access-protocol/` in the core repo, versioned with protocol `v1`; the agent repo pins a core tag and fetches the directory in CI (`task fixtures:sync`), failing if the tag changes without a lockfile bump.

```
server/testdata/access-protocol/
├─ messages/<type>/valid-*.json, invalid-*.json   validated against api/agent-protocol/v1 schemas
├─ plate-normalization.json                        [{input, normalized|null}] Turkish letters, dashes, empty
├─ evaluation/*.json                               {allow_list, tz, now, presentation, expect:{decision, reason, entry_id}}
│                                                   plain match, wrong_point, not-yet/expired, recurring weekday,
│                                                   overnight window, DST spring/fall, max_uses, tolerant plate
├─ scenarios/*.json                                scripted sessions [{send|expect|wait|clock, …}]
│                                                   enrol-and-sync, delta-gap, duplicate-event, command-expired,
│                                                   command-replay, revoke, token-rotate, longpoll-fallback
└─ README.md                                       step vocabulary and how to run
```

| Test | Backend (A) | Agent (B) |
|---|---|---|
| Schema conformance | every emitted message validates; every `invalid-*` is rejected | same for what the agent emits and accepts |
| Evaluation fixtures | a reference evaluator in `internal/core/access/evaltest` (tests only) must agree with the fixtures | the production evaluator must agree |
| Plate normalization | fixture file | fixture file |
| Scenarios | a scripted **fake agent** drives the real hub over a real WebSocket against Postgres | a **fake server** (scenario runner package in the agent repo) drives the real agent |
| Projection | table-driven: every §2.2 trigger → expected credential and `allowlist_change` rows; reconcile repairs injected drift; no bump when unchanged | — |
| Authz | agent token cannot read other sites; revoked/rotated tokens; `access.open` row in the authz matrix harness | — |
| Offline/reconnect | queued-event replay is idempotent; `occurred_at` clamp | process-level: cut the network mid-batch, kill the process mid-delta (SQLite recovery), expire a window while offline, exceed grace → stopped → resume, queue overflow policy, clock jump |
| End to end with `mock` | compose test `access-e2e` (owned by A, core repo): server + Postgres + real agent binary + `mock` driver scripted with a resident plate, an unknown plate and a visitor approval. Assertions: resident allowed and event synced; unknown denied and visible in the feed; walk-in approved → delta → mock plate allowed within 5 s; guard `open` → one `open_log` entry and a `manual_open` event with the guard id; network cut → plates still decided locally → reconnect → events reconcile | B runs the same test in agent CI against core `:edge` |

---

## 11. Work breakdown

Estimates in engineer-weeks (one engineer, tests included). The agent total matches infra §11; the backend access work is most of the 8 backend weeks of 0.3 (backend §20), with community features alongside, so the schedule needs either a second backend engineer for part of the phase or the community items to slip within 0.3.

### 11.1 Backend — A (`server/internal/core/access`, `api/`)

| # | Task | Weeks |
|---|---|---|
| A1 | Migrations (§2.1 tables, RLS, append-only grants, retention functions), sqlc queries, permissions `access.manage/open/view_events`, role-matrix rows | 0.5 |
| A2 | Access points and agents API: CRUD, enrolment, revoke, re-enrol, token rotate, agent-token middleware | 1 |
| A3 | Projector: `access.project`, `access.reconcile`, change log, version counter, snapshot builder, change-log retention | 1.5 |
| A4 | Hub: WebSocket + long-poll, hello/config/snapshot/delta/commands, heartbeat handling, one-session rule, clock handling | 1.5 |
| A5 | Event ingest (idempotent), feed endpoint, guard `open` and command status, rate limits, audit, `uses` accounting | 1 |
| A6 | Monitoring and notifications (`agent_silent`, clock skew, stopped, driver down), Prometheus gauges, platform admin lists | 0.5 |
| A7 | JSON Schemas in `api/agent-protocol/v1`, shared fixtures, reference evaluator, conformance and `access-e2e` tests | 1 |
| | **Total** (web admin and guard-console screens are in [02-web.md](02-web.md)) | **≈ 7** |

### 11.2 Agent — B (`openbekci-agent`)

| # | Task | Weeks |
|---|---|---|
| B1 | Repo, CLI, config loader/validator, logging, enrolment, token storage and rotation | 0.75 |
| B2 | Protocol client: WebSocket + long-poll fallback, backoff, hello/ack/heartbeat, schema validation | 1 |
| B3 | Store (SQLite, encryption), snapshot/delta application, event queue and reconcile, command dedupe, grace/stopped state | 1.25 |
| B4 | Evaluator (windows, recurring, DST, `max_uses`), plate normalization, dedupe, decision pipeline; fixture runners | 0.75 |
| B5 | Drivers: relay (serial/http/gpio), platecam (push/poll + `generic_json`), mock | 1.5 |
| B6 | Packaging: GoReleaser, systemd unit, container image, signed update, metrics, operator docs for `docs/ops/` | 0.75 |
| | **Total** | **≈ 6** |
| B7 | After the first site's hardware is known: brand adapter for its camera | ≈ 0.5–1 |

### 11.3 Sequencing

First, A7's schemas and initial fixtures (messages, plate normalization, evaluation) land; both sides start from them. A1–A4 and B1–B4 then run in parallel. Milestone one: the `enrol-and-sync` scenario passes on both sides. Milestone two: `access-e2e` with `mock`. The real camera adapter closes the phase.

### 11.4 Open points

- Whether the guard console should show a camera snapshot with each event (needs server-side image storage; deferred, `raw_ref` reserves the hook).
- Whether `visitor_qr` should be checkable per gate (C2 default: site-bound); entries already carry `points`, so the change is server-only.
- Whether tolerant plate matching should stay after field experience at the first site.

---

## Appendix A. Message examples

```json
{"v":1,"type":"hello","id":"0192…","ts":"2026-11-07T10:00:00.000Z","payload":{
  "from":"agent","agent_id":"…","agent_version":"0.3.0","platform":"linux/arm64",
  "protocol_versions":[1],"snapshot_version":1041,"config_version":7,
  "drivers":[{"instance":"gate-relay","state":"ok"}],"queue_depth":0,"clock_offset_ms":null}}

{"v":1,"type":"hello","id":"0192…","ts":"…","payload":{
  "from":"server","protocol_version":1,"server_time":"…","heartbeat_seconds":30,
  "config_version":7,"rotate_by":"2027-05-06T00:00:00Z","rotate_now":false,
  "site":{"id":"…","timezone":"Europe/Istanbul"},"snapshot_version":1042}}

{"v":1,"type":"delta","id":"…","ts":"…","payload":{"from_version":1041,"to_version":1042,"changes":[
  {"op":"upsert","entry":{"id":"…","kind":"plate","value":"34ABC123","points":["…"],
    "valid_from":"2026-11-07T10:00:00Z","valid_to":"2026-11-07T11:30:00Z","recurring":null,"max_uses":1}},
  {"op":"remove","credential_id":"…"}]}}

{"v":1,"type":"event","id":"…","ts":"…","payload":{"clock_offset_ms":-312,"events":[
  {"id":"0192…","point_id":"…","decision":"allow","reason":"match","credential_id":"…",
   "presented_kind":"plate","observed_plate":"34ABC123","occurred_at":"2026-11-07T10:01:12.481Z",
   "raw_ref":"snap/20261107-100112.jpg","detail":{"confidence":0.93,"lane":"in"}}]}}

{"v":1,"type":"ack","id":"…","ts":"…","payload":{"ref":"<event message id>","status":"ok",
  "detail":{"accepted":["0192…"],"duplicates":[],"rejected":[]}}}

{"v":1,"type":"command","id":"0192…","ts":"…","payload":{"command":"open_point","point_id":"…",
  "credential_id":null,"visitor_entry_id":"…","actor":{"user_id":"…","display":"Guard Ali"},
  "issued_at":"…","expires_at":"2026-11-07T10:02:30Z"}}

{"v":1,"type":"heartbeat","id":"…","ts":"…","payload":{"snapshot_version":1042,"queue_depth":3,
  "dropped_events":0,"uptime_s":86400,"clock_suspect":false,
  "drivers":[{"instance":"gate-cam","state":"ok","detail":""}]}}
```

## Appendix B. JSON Schemas (v1, draft 2020-12)

Shared definitions (`$defs`), referenced below as `#/$defs/…`:

```json
{ "$defs": {
  "uuid": { "type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$" },
  "ts":   { "type": "string", "format": "date-time" },
  "kind": { "type": "string", "examples": ["plate", "visitor_qr", "tag", "pin"] },
  "hhmm": { "type": "string", "pattern": "^([01][0-9]|2[0-3]):[0-5][0-9]$" },
  "entry": { "type": "object", "additionalProperties": false,
    "required": ["id", "kind", "value", "points", "valid_from"],
    "properties": {
      "id": {"$ref": "#/$defs/uuid"}, "kind": {"$ref": "#/$defs/kind"},
      "value": {"type": "string", "minLength": 1, "maxLength": 128},
      "points": {"type": "array", "items": {"$ref": "#/$defs/uuid"}, "minItems": 1},
      "valid_from": {"$ref": "#/$defs/ts"},
      "valid_to": {"oneOf": [{"$ref": "#/$defs/ts"}, {"type": "null"}]},
      "recurring": {"oneOf": [{"type": "null"}, {"type": "object", "additionalProperties": false,
        "required": ["days", "start", "end"],
        "properties": {
          "days": {"type": "array", "items": {"type": "integer", "minimum": 1, "maximum": 7}, "minItems": 1, "uniqueItems": true},
          "start": {"$ref": "#/$defs/hhmm"}, "end": {"$ref": "#/$defs/hhmm"}}}]},
      "max_uses": {"oneOf": [{"type": "integer", "minimum": 1}, {"type": "null"}]} } },
  "driver_state": { "type": "object", "required": ["instance", "state"],
    "properties": {"instance": {"type": "string"},
                   "state": {"enum": ["ok", "degraded", "down", "missing"]},
                   "detail": {"type": "string"}} },
  "driver_ref": { "type": "object", "required": ["instance"],
    "properties": {"instance": {"type": "string"}, "channel": {"type": "string"}} }
}}
```

Envelope (`envelope.schema.json`):

```json
{ "type": "object", "additionalProperties": false, "required": ["v", "type", "id", "ts", "payload"],
  "properties": { "v": {"const": 1}, "type": {"type": "string"}, "id": {"$ref": "#/$defs/uuid"},
                  "ts": {"$ref": "#/$defs/ts"}, "payload": {"type": "object"} } }
```

Payload schemas, one file per `type`; each is an object with the listed `required` properties and `additionalProperties: true` (forward compatibility), except where noted:

| Type | Required | Optional / notes |
|---|---|---|
| `hello` (agent) | `from` = "agent", `agent_id`, `agent_version`, `protocol_versions` (int[]), `snapshot_version` (int ≥ 0), `config_version` (int ≥ 0), `drivers` (driver_state[]), `queue_depth` (int ≥ 0) | `platform`, `clock_offset_ms` (int or null) |
| `hello` (server) | `from` = "server", `protocol_version`, `server_time`, `heartbeat_seconds` (10–120), `config_version`, `site` {id, timezone}, `snapshot_version` | `rotate_by`, `rotate_now` |
| `snapshot` | `version`, `part` (≥ 1), `parts` (≥ 1), `site` {timezone}, `entries` (entry[] ≤ 2 000) | parts of one version share `version`; the union of all parts replaces the store |
| `delta` | `from_version`, `to_version` (> from), `changes` (≤ 500), each `{op: "upsert", entry}` or `{op: "remove", credential_id}` (`oneOf`) | |
| `event` | `events` (1–100): each requires `id` (UUIDv7), `point_id`, `decision` (`allow`/`deny`/`manual_open`), `reason`, `occurred_at` | per event: `credential_id`, `presented_kind`, `observed_plate`, `command_id`, `visitor_entry_id`, `raw_ref` (≤ 256), `detail` (object ≤ 2 KB); batch: `clock_offset_ms` |
| `ack` | `ref` (uuid), `status` (§4.7) | `detail` (object) |
| `command` | `command` (`open_point` / `revoke_credential` / `request_snapshot` / `update_config` / `revoked`) and per-command fields, as a `oneOf` keyed on `command`: `open_point`: `point_id`, `issued_at`, `expires_at` (+ optional `credential_id`, `visitor_entry_id`, `actor` {user_id, display}); `revoke_credential`: `credential_id`; `update_config`: `config_version`, `site` {timezone}, `points` ({id, name, kind, direction, driver_ref, enabled}[]), `settings` {offline_grace_hours, plate_match, heartbeat_seconds, maintenance_hour}; `request_snapshot` and `revoked`: none | |
| `heartbeat` | `snapshot_version`, `queue_depth`, `drivers` (driver_state[]) | `dropped_events`, `uptime_s`, `clock_suspect`, `clock_offset_ms` |

HTTP bodies: enrol as §4.2; poll `{messages: [envelope], wait_seconds (0–30)}` → `{messages: [envelope]}`; rotate `{}` → `{agent_token}`. WebSocket close codes: 4401 revoked/unauthorized, 4409 replaced by a newer session, 4426 no common protocol version, 4429 rate limited.
