# Integrations: API keys and webhooks

Release 0.4. Two ways to connect your own tools to an OpenBekci instance: **API keys** let a script call the API without a person signing in, and **webhooks** make the instance call you when something happens. The endpoints are in the API reference under the `integrations` tag; this page is what you need to use them.

## API keys

An API key belongs to an organization and is created by one of its administrators (permission `apikeys.manage`).

```http
POST /v1/orgs/{org_id}/api-keys
{"name": "Accounting export", "scopes": ["payments.record", "reports.read"], "site_id": null, "expires_at": "2027-06-30T00:00:00Z"}
```

The answer contains `secret`, for example `obk_k3x9q7ab_Zr0...`. **It is shown exactly once**; only a hash is stored. Use it as a bearer token:

```sh
curl -H "Authorization: Bearer obk_k3x9q7ab_Zr0..." https://bekci.example.org/v1/orgs/$ORG/payments
```

- **Scopes** are permission names (the `Permission` enum of the API; the permission table is in `api/PERMISSIONS.md`). A key can only be given permissions its creator holds through a role in the organization, and never `apikeys.*`, `webhooks.manage` or permissions that belong to residents. A key can read webhook endpoints (`webhooks.read`) but never create, change, test or rotate them.
- **A key never exceeds its creator.** On every request its rights are the *intersection* of its scopes and the grants its creator holds at that moment. If the administrator loses the role, is removed or disabled, the key loses the rights with them, at once. A key holds no account rights, works in its own organization only, and can be restricted to a single site with `site_id`. It never gains more than its scopes: a role the creator receives later does not widen it. A key can call only operations that list a permission in the API reference: the ones that need nothing but a signed-in person (`/v1/me`, sessions, password, re-authentication, `GET /v1/instance`, accepting an invitation, unit claims) answer `403`. Idempotency keys (`Idempotency-Key`) belong to the key, not to the person who created it.
- **Re-authentication.** Creating, rotating and revoking keys needs a recent re-authentication of the administrator's session (`POST /v1/auth/reauth`, valid 10 minutes), otherwise `403 reauth_required`. A key itself can never manage keys, and cannot do anything else that asks for re-authentication.
- **Listing** (`GET /v1/orgs/{org_id}/api-keys`) shows the public prefix (`k3x9q7ab`), scopes, status, `last_used_at` (updated at most every five minutes) and expiry, never a secret.
- **Rotating** (`POST .../api-keys/{key_id}:rotate`) issues a new prefix and secret for the same key; the old token stops working immediately, so plan the swap. **Revoking** (`POST .../:revoke`) ends the key.
- **Errors.** A token that matches no key, or has a wrong secret, answers `401 unauthenticated`. A key that was revoked answers `401 api_key_revoked`, an expired one `401 api_key_expired` (stable problem codes; the answer is only given to a holder of the real secret). A key over its rate answers `429 rate_limited` with `Retry-After`; the default is 600 requests per minute per key (`OPENBEKCI_API_KEY_RATE_PER_MINUTE`).
- **Audit.** Everything done with a key is recorded with `actor_kind: api_key` and `actor_key_id` (the key id); `actor_user_id` is empty, so the trail never blames the administrator for what a script did. Key creation, rotation and revocation are recorded as the administrator's own actions.

## Webhooks

An endpoint is an `https` URL plus the event types it wants (permission `webhooks.manage`).

```http
POST /v1/orgs/{org_id}/webhook-endpoints
{"url": "https://hooks.example.org/openbekci", "events": ["payment.confirmed", "ticket.created"], "description": "Accounting"}
```

The answer contains `secret` (`whsec_...`), **shown once**: store it where your receiver can read it. It is kept sealed on the server and cannot be read back; to change it, [rotate it](#rotating-the-secret). `PATCH` changes the URL, events, description and `active` (with `If-Match`), `DELETE` removes the endpoint and its log. At most 20 endpoints per organization.

`POST .../webhook-endpoints/{endpoint_id}:test` queues a `ping` event (also to an inactive endpoint) so you can see your receiver work; `GET .../webhook-endpoints/{endpoint_id}/deliveries` is the delivery log, newest first: status (`pending`, `succeeded`, `failed`), attempts, the last HTTP status and error, and `next_retry_at` while a retry is waiting.

### What is sent

A `POST` with `Content-Type: application/json` and these headers:

| Header | Value |
|---|---|
| `OpenBekci-Signature` | `t=<unix seconds>,v1=<hex>`, see below |
| `OpenBekci-Delivery-Id` | The delivery's UUID. The same on every retry of one delivery; use it to de-duplicate. |
| `OpenBekci-Event` | The event type, for example `ticket.created`. |

The body carries **ids only**, never records. Read the details through the API with an API key.

```json
{
  "id": "01a1bcc3-4203-7c82-97df-2cf27dd58534",
  "type": "ticket.created",
  "version": 1,
  "org_id": "01a1bcc3-41fe-7339-b8ba-c53f876a12f4",
  "occurred_at": "2026-11-09T09:30:12.512Z",
  "ids": { "ticket_id": "01a1bcc3-431a-792f-88d3-25345cafaaf9", "site_id": "01a1bcc3-4377-7d1c-b7a3-7a1d0e7aa001" }
}
```

`id` identifies the event, `version` the schema of the payload (additive changes keep it). New keys may appear in `ids` and in the object; ignore what you do not know.

### Events

| Event | Emitted when | `ids` |
|---|---|---|
| `payment.recorded` | A payment is recorded (also through bank-statement matching) | `payment_id`, `site_id` |
| `payment.confirmed` | A declared payment is confirmed, or a payment provider confirms a card payment | `payment_id`, `site_id` |
| `charge.created` | A one-off charge is created (plan-generated charges are announced by `charges.generated`) | `charge_id`, `site_id` |
| `charges.generated` | A charge plan billed a period (one event per plan and period) | `plan_id`, `site_id`; plus `period` (for example `2026-11`) and `charge_ids` (the ids of the charges created) next to `ids` |
| `ticket.created` | A ticket is opened | `ticket_id`, `site_id` |
| `ticket.status_changed` | A ticket moves to another status | `ticket_id`, `site_id` |
| `announcement.published` | An announcement is published | `announcement_id`, `site_id` |
| `meeting.called` | A meeting is called | `meeting_id`, `site_id` |
| `decision.applied` | A decision's effects are applied | `decision_id`, `site_id` |
| `visitor.approved` | A visitor request is approved by the resident | `visitor_approval_request_id`, `visitor_entry_id` (when known), `site_id` |
| `delivery.received` | A parcel is logged at the gate | `delivery_id`, `unit_id`, `site_id` |
| `ping` | You asked for a test | `endpoint_id` |

Events are emitted in the transaction of the action that causes them: an event exists if and only if the change was saved. Only active endpoints that subscribe to the type receive it, once per event.

### Rotating the secret

`POST .../webhook-endpoints/{endpoint_id}:rotate-secret` issues a new secret and returns it **once**, like creation. The replaced secret does not stop at once: for **24 hours** (`previous_secret_expires_at` in the answer) every delivery is signed with both, as two `v1` entries, newest first:

```
OpenBekci-Signature: t=1731144612,v1=<hmac with the new secret>,v1=<hmac with the old secret>
```

A receiver that accepts any matching `v1` (the verifiers below do) keeps working while you deploy the new secret; when the window ends only the new `v1` is sent. Rotating again inside the window keeps just the secret that was current a moment ago as the overlap: the oldest one stops signing immediately. If a secret leaked, rotate twice to end the overlap at once. Rotation needs a signed-in administrator (`webhooks.manage`), not an API key, and is recorded in the audit trail as `webhook.rotate_secret` (never the secret).

### Verifying the signature

`v1` is the lower-case hex of `HMAC-SHA256(secret, t + "." + body)`: the key is the whole secret string (with the `whsec_` prefix) as UTF-8, the message is the timestamp from the header, a dot, and the **raw** request body bytes (verify before you parse or re-serialize it). Then:

1. Compare in constant time.
2. **Replay protection:** reject the request when `t` is more than **5 minutes** from your clock (in either direction). Every attempt, retries included, is signed with the time of that attempt. Within the window, remember `OpenBekci-Delivery-Id`s you have processed if handling the same delivery twice would hurt.
3. The header is a comma separated list; accept the request if any `v1` matches and ignore keys you do not know.

Go:

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const tolerance = 5 * time.Minute

// verify checks OpenBekci-Signature over the raw body.
func verify(secret, header string, body []byte, now time.Time) error {
	var ts string
	var tags []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		switch {
		case !ok:
		case k == "t":
			ts = v
		case k == "v1":
			tags = append(tags, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(tags) == 0 {
		return fmt.Errorf("malformed signature header")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("timestamp outside the tolerance (replay?)")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, tag := range tags {
		if got, err := hex.DecodeString(tag); err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return fmt.Errorf("signature mismatch")
}

func handler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || verify(secret, r.Header.Get("OpenBekci-Signature"), body, time.Now()) != nil {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		// r.Header.Get("OpenBekci-Event"), r.Header.Get("OpenBekci-Delivery-Id"); parse body, queue the work
		w.WriteHeader(http.StatusNoContent)
	}
}
```

Node (18+, no dependencies; give the middleware the raw body, for example `express.raw({ type: "application/json" })`):

```js
const crypto = require("node:crypto");

const TOLERANCE_SECONDS = 5 * 60;

// rawBody is a Buffer of the exact bytes received.
function verify(secret, header, rawBody, nowSeconds = Date.now() / 1000) {
  const parts = String(header || "").split(",").map((p) => p.trim().split("="));
  const ts = parts.find(([k]) => k === "t")?.[1];
  const tags = parts.filter(([k]) => k === "v1").map(([, v]) => v);
  if (!/^\d+$/.test(ts || "") || tags.length === 0) return false;
  if (Math.abs(nowSeconds - Number(ts)) > TOLERANCE_SECONDS) return false; // replay protection
  const want = crypto.createHmac("sha256", secret).update(`${ts}.`).update(rawBody).digest();
  return tags.some((tag) => {
    const got = Buffer.from(tag, "hex");
    return got.length === want.length && crypto.timingSafeEqual(got, want);
  });
}

// app.post("/openbekci", express.raw({ type: "application/json" }), (req, res) => {
//   if (!verify(process.env.OPENBEKCI_WEBHOOK_SECRET, req.get("OpenBekci-Signature"), req.body)) return res.sendStatus(401);
//   const event = JSON.parse(req.body);   // { id, type, version, org_id, occurred_at, ids }
//   res.sendStatus(204);
// });
```

### Delivery, retries and failure

- **At least once.** Answer any `2xx` to acknowledge, quickly (the request times out after **10 seconds**; read your own work off a queue). Anything else counts as a failed attempt: a non-2xx status, a timeout, a connection error, a certificate the system does not trust, and **any redirect** (3xx is never followed).
- **Backoff.** Failed attempts are retried after 30 seconds, then 1, 2, 4, 8, 16 and 32 minutes (doubling, capped at 4 hours): eight attempts by default, about an hour in all (`OPENBEKCI_WEBHOOK_MAX_ATTEMPTS`, 1 to 20). The log shows `next_retry_at`.
- **When the attempts are used up** the delivery is `failed`, the endpoint is marked `failing` (`failing`, `failing_since` in the API) and the organization's administrators get a `webhook.endpoint_failing` notification, once per streak. Events keep being delivered to a failing endpoint; the first success clears the mark. Deactivating (`active: false`) stops new events; changing the URL or re-activating clears the mark.
- **Order is not guaranteed**, and a retried delivery can arrive after a newer event. Use `occurred_at` and the ids, then read current state from the API.
- **What we send to**: only `https` URLs whose host resolves to public addresses. Loopback, private (RFC 1918, unique-local), link-local (including cloud metadata addresses), carrier-grade NAT, multicast and documentation ranges are refused when you create the endpoint **and** again when each request connects (the IPv6 forms that carry an IPv4 address, such as 6to4, Teredo, NAT64 and IPv4-mapped, are covered). Self-hosted instances with a receiver on their own network list the ranges in `OPENBEKCI_WEBHOOK_ALLOWED_CIDRS` (operator setting; for example `192.168.1.0/24`). At most 1 MiB of your response is read, and it is discarded.
- The delivery log keeps the last status and error only (`http_status: 500`, `request_failed: ...`, `forbidden_target`, `redirect_not_followed`), never response bodies.

Why the signature is built this way: [ADR-0002](adr/0002-webhook-signing.md).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `OPENBEKCI_API_KEY_RATE_PER_MINUTE` | 600 | Requests per minute per API key (a burst of ten seconds' worth is allowed); negative removes the limit. |
| `OPENBEKCI_WEBHOOK_MAX_ATTEMPTS` | 8 | Delivery attempts per (event, endpoint), 1 to 20. |
| `OPENBEKCI_WEBHOOK_ALLOWED_CIDRS` | empty | Comma separated CIDRs or IPs that webhooks may reach although they are private, loopback or link-local. |

Webhook secrets are sealed with `OPENBEKCI_ENCRYPTION_KEY`; it is part of the backup set like the other keys.

## Data handling

API keys, webhook endpoints and the delivery log belong to the organization, not to a site: the **site export archive excludes them** (a key is a credential of the old organization and is bound to its administrators; the new organization creates its own). They hold no personal data of residents (delivery bodies are ids only), so person erasure (`privacy_erase`) does not touch them; a key whose creator is erased or removed stops working at once because it never exceeds its creator's grants, and can be revoked in the list.
