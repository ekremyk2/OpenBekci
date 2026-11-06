# OpenBekci — Mobile Plan

| | |
|---|---|
| Status | **Draft v0.2** — revised on 2026-11-06 against the [plan review](06-review.md) (§6, §7, §8 and the annex) and [`docs/decisions/`](../decisions/) |
| Parent | [00-overview.md](00-overview.md) — decisions and shared contracts there bind this document. API and domain details come from [01-backend.md](01-backend.md); product behaviour from the decision files (M = money, S = setup & people, O = operations, G = governance, C = community) |
| Repository | **`github.com/ekremyk2/openbekci-mobile`** — its own repository (decision D8), licensed **MPL-2.0** (D1). It consumes a **pinned OpenAPI spec version** and the **Dart client published from the core repository's releases**; it never builds against the core working tree |
| Release | Ships with core release **0.4 "Mobile & integrations"**. The app has its own version line: **mobile 1.0 is the first store release**, published alongside core 0.4 |

---

## 1. Goals and non-goals

**Goals**

- A Flutter app for Android and iOS that works against **any OpenBekci instance** — the hosted service or a self-hosted server — chosen at first launch through the discovery document (overview §6.8).
- **Resident- and owner-first.** The app is the daily touchpoint for the people who live in the site: statement and payment, tickets, announcements, documents, bookings, visitors, parcels, assembly polls and check-in. Management, accounting and configuration stay on the web.
- **Phone-first sign-in**: SMS one-time code is the primary method (D5); password, magic link, invitation links and the site QR with unit claim complete the set.
- Push notifications that work for self-hosters without them owning store credentials: content-free payloads through the push relay (overview §6.5), with graceful degradation to periodic fetch.
- Same quality bars as the rest of the project: generated client from the spec, i18n from the first screen, accessibility, tests in CI, contract tests against supported server versions.

**Non-goals (first store release)**

- Admin and configuration screens (dues plans, chart of accounts, imports, workflows, pack settings) — web only.
- Flutter Web or desktop targets; the React app covers them (the guard console is a web screen, C2).
- Embedding card forms; payment providers' hosted checkout only.
- Offline-first sync or an offline write queue (§8).
- Multi-account, white-label tooling, F-Droid/UnifiedPush flavor, iOS Notification Service Extension, staff "work mode", guard "gate mode" — all **later** (§7.3).

---

## 2. Users and the app's shape

| User | Rights come from | Typical actions |
|---|---|---|
| **Owner** (every co-owner) | occupancy `owner` | Whole unit: both debtor sides of the statement, pay, tickets, announcements, documents incl. the unit's private folder, bookings, visitors, parcels, assembly polls, QR check-in, decisions and minutes |
| **Tenant** | occupancy `tenant` | Tenant side of the statement (M1), pay, tickets, announcements, bookings, visitors, parcels, decisions marked "concerns residents" |
| **Household member** | occupancy `household` | Tickets, announcements, bookings, visitors, parcels; money only when `may_see_money` is granted (S3) |
| **Former resident** | expired occupancy | Read-only: statements and receipts from their own period, nothing after (S7) |
| **Board member / manager / staff** | membership | Use the same resident screens for their own units. Management, the guard console and staff queues are web (0.3); a staff work mode and a gate mode are later |

Rights derive from occupancies and memberships exactly as backend §7 states; `GET /v1/me` delivers organizations, occupancies (units with sides), roles and effective permissions, and navigation is built from them. The server enforces; the app hides. There is no separate "manager app".

---

## 3. Technology stack

| Concern | Choice | Notes |
|---|---|---|
| Framework | Flutter stable, Dart 3.x, pinned with `.fvmrc` | Upgrade deliberately per release; no promise to track every stable |
| State | **Riverpod** (`riverpod_annotation`, `riverpod_generator`) | Compile-safe providers, testing by overrides |
| Routing | **go_router** with typed routes (`go_router_builder`) | Deep links, auth/org guards, nested navigation |
| API client | **Dart client published from core releases** (`openapi-generator`, `dart-dio` target), generated from the **pinned spec version** in the core CI; the app depends on the published package or release asset | Generator configured with **`enumUnknownDefaultCase: true`** (open enums, overview §6.2) and validated against the **3.0.3-compatible subset**. Spike in phase A: if `dart-dio`/`built_value` models are too heavy, switch to `dart` + `json_serializable`; ADR |
| HTTP | `dio` with interceptors: bearer + single-flight refresh, request id, `Accept-Language`, `Idempotency-Key` for `x-idempotent` operations, retry with backoff for idempotent calls, log scrubbing | |
| App models | `freezed` + `json_serializable` for view models; generated models stay generated | |
| Secure storage | `flutter_secure_storage` (Keychain / Android Keystore) for the refresh token | |
| Local database | **drift** (SQLite) as the **read cache** | No write queue in the first release (§8) |
| Preferences | `shared_preferences` | Non-sensitive UI prefs, drafts |
| Push | `firebase_messaging` (FCM on Android, APNs on iOS) through the official relay | UnifiedPush flavor later (§9) |
| Links | `app_links`; Android App Links + iOS Universal Links for the hosted domain; custom scheme `openbekci://` for self-hosted servers | Formats from backend §6 (§6 below) |
| Browser flows | `flutter_custom_tabs` (Custom Tabs / `SFSafariViewController`) for payment checkout | Never a `WebView` for credentials or payment |
| Biometrics | `local_auth` for the optional app lock | |
| Media | `image_picker`, `flutter_image_compress` (≤ 2000 px, EXIF location stripped client-side; the server strips again), `file_picker`, `open_filex` | Uploads always go **through the server** (`POST /v1/orgs/{org}/files`, backend §13) |
| PDF | `pdfrx` (statements, receipts, minutes) | |
| QR | `mobile_scanner` (site QR, invitation QR); `qr_flutter` to render visitor and check-in QRs | |
| i18n | `flutter_localizations` + ARB via `flutter gen-l10n`; `intl` for dates/numbers/currency | |
| Logging | `logger` with token scrubbing; crash reporting opt-in, hosted flavor only | No analytics SDKs |
| Lint | `very_good_analysis` | |
| Testing | `flutter_test`, `mocktail`, `alchemist` goldens, `integration_test` + `patrol` | |
| CI / release | GitHub Actions in the mobile repo; `fastlane` (Play internal track, TestFlight); tags **`v*`** | |

Deliberately avoided: BLoC, GetX, hand-written API models, JWT handling of any kind (tokens are opaque strings), Firebase beyond messaging, embedded WebViews for auth or payments, certificate pinning (servers are arbitrary).

---

## 4. Architecture

Feature-first, three layers per feature; dependencies point inward (presentation → application → data) and features reach each other only through providers.

```
openbekci-mobile/
├─ lib/
│  ├─ main_dev.dart / main_prod.dart        entry points per flavor
│  ├─ app/
│  │  ├─ app.dart              MaterialApp.router, theme, locale, lifecycle
│  │  ├─ router.dart           go_router config + guards (signed in? org chosen?)
│  │  ├─ theme/                Material 3 theme, brand tokens from app config
│  │  └─ config/               AppConfig from assets/app_config.json (name, default server, relay_id, brand, flavor)
│  ├─ core/
│  │  ├─ api/                  Dio setup, interceptors, problem+json → AppError(code, fieldErrors, retryable)
│  │  ├─ account/              the single account (server URL + user), secure storage, sign-out
│  │  ├─ auth/                 token flow, refresh single-flight, sign-in methods, link handling
│  │  ├─ instance/             discovery, version gate, instance id
│  │  ├─ db/                   drift read cache, migrations
│  │  ├─ cache/                stale-while-revalidate policies, connectivity hint
│  │  ├─ push/                 device registration, payload → route, generic titles, periodic-fetch fallback
│  │  ├─ l10n/                 generated localizations; money, site-timezone dates; `codes.dart`
│  │  ├─ permissions/          Permission enum from the generated client + `can()` helpers
│  │  └─ widgets/              empty/error/offline states, MoneyText, SiteDate, skeletons, avatar
│  └─ features/
│     ├─ onboarding/           server entry, site QR, invitation, sign-in methods, unit claim
│     ├─ home/                 dashboard
│     ├─ statement/            charges, payments, late fee, running balance per side, PDF
│     ├─ payments/             checkout flow, declared transfer, payment history
│     ├─ tickets/  announcements/  documents/
│     ├─ bookings/  visitors/  parcels/
│     ├─ governance/           upcoming assembly, polls, check-in QR, decisions, minutes
│     ├─ notifications/        in-app center, mutes
│     └─ profile/              me, my units, org switch, sessions, app lock, directory opt-in, gate contact phone, consents
├─ assets/                     app_config.json, fonts, images
├─ android/ ios/               flavors: dev, prod
├─ test/ integration_test/
├─ tool/                       scripts: fetch pinned spec + client, screenshots
└─ fastlane/                   lanes and store metadata
```

Per feature: `data/` (repository = API + drift cache, mappers), `application/` (providers, use cases), `presentation/` (screens, widgets).

Error handling: every API error becomes `AppError`; `errorMessage(code)` maps the backend's problem codes (backend §8) to ARB strings; unknown codes show a generic message with the code under "details". Enum values the app does not know fall into the generated client's unknown case and render as their raw string, never as a crash — this is what lets an old store build survive an additive server release.

---

## 5. Instance discovery and the account

### 5.1 First launch

1. **Server screen**: a text field for the server address (prefilled with the hosted service in the official build; `app_config.json` may set another default) and **Scan QR**. The web app's "Connect the mobile app" page shows a QR with the server URL and, optionally, an invitation (web plan §12); invitation links use the formats in backend §6.
2. The app normalizes the input (adds `https://`, strips paths) and fetches **`GET /.well-known/openbekci.json`**. The document's schema is normative in **backend §8** and is not restated here; the app uses `api_base_url`, `api_versions`, `server_version`, `min_app_version`, `auth_methods`, `push`, `branding`, and the instance identifier (§16).
3. **Version gate**: app older than `min_app_version` → "update the app"; server outside the supported range (§15) → "ask your manager to update the server". Both screens still allow sign-out.
4. **HTTPS is required.** Plain `http://` only for `localhost` / `10.0.2.2` in the `dev` flavor. No pinning. **The app trusts user-installed CAs** (Android `network_security_config` with `<certificates src="user" />`; iOS trusts profile-installed roots with full trust enabled) so self-hosted sites behind CGNAT can use the private-CA path documented in the infra plan §5.

### 5.2 The account

- The first release holds **one account** (server URL + user). Within it the user may belong to **several organizations**; `GET /v1/me` lists them with roles, permissions and occupancies. The app remembers the last organization; a switcher appears only when there is more than one. Site context follows the user's occupancies (a person with units in two sites of the same organization gets a site chip).
- Sign-out revokes the session server-side (`DELETE /v1/me/sessions/current`), unregisters the device (§9), wipes secure storage and deletes the cache database.
- Multi-account is **later** (§7.3); the cache is keyed by instance id from day one so adding it does not require a migration.

---

## 6. Authentication

Token flow from backend §6: `POST /v1/auth/token` → **opaque** access token (15 min, in memory only) + **rotating refresh token** (60 days, reuse detection; in secure storage). **No JWT**; the app never inspects a token.

- `AuthInterceptor` attaches the bearer; on `401` runs a **single-flight refresh** (concurrent requests await the same future) and retries once; on `invalid_grant` the account is signed out and the UI returns to sign-in. `403 reauth_required` opens a re-authentication sheet (OTP or password, `POST /v1/auth/reauth`) and replays the request.
- **Sign-in methods** are rendered from `auth_methods` in the discovery document, in this order of prominence:
  - **SMS one-time code** (`sms_otp`) — primary for phone-first residents (D5): phone number → 6-digit code; the app offers SMS autofill (Android SMS Retriever / iOS one-time-code keyboard).
  - **Password** (`password`) with email or phone.
  - **Magic link** (`magic_link`): the app requests it with `client=mobile`; the server emits `openbekci://…` (or the hosted universal link) and the app exchanges the token for a session.
  - **Invitation links** (S4: manager, site-code, household): one format, defined in backend §6 (`{BASE_URL}/invite/{token}`, or the mobile scheme when requested with `client=mobile`). Opening one in the app prefills server and invitation, creates or links the account and lands on the unit.
  - **Site QR sign-up with unit claim** (S4): scan the site's QR → sign up (SMS code) → choose block/unit and claimed role → "waiting for the manager's approval" state until the `unit_claim` is approved.
  - **Passkeys, TOTP, OIDC**: later, after the backend schedules them; the app shows unknown `auth_methods` ids as unavailable rather than failing.
- **App lock**: optional biometric/PIN lock with a grace period, a user setting (no org policy in the first release).
- Sessions list and remote revocation in the profile tab (`GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}`).

---

## 7. Features

### 7.1 Mobile 1.0 (with core 0.4) — resident core

| Feature | Details | Decision |
|---|---|---|
| Home | Per organization: balance and next due charge per unit side the user may see, unread announcements, open tickets, upcoming bookings, today's expected visitors and uncollected parcels, the next assembly. One call: `GET /v1/orgs/{org}/me/dashboard` | — |
| Statement | Per **debtor side**: the owner sees the whole unit (owner and tenant sides), the tenant sees the tenant side, a household member only with `may_see_money`. Charges, payments, crystallized fees and the **late fee accrued to today shown continuously**; running balance; period filter; statement PDF via share sheet. Former residents see their own period only | M1, M4, S3, S7 |
| Pay | Two paths, chosen by what the server offers. **Provider checkout** (0.4, first provider): select charges or amount → server creates a checkout → `checkout_url` opens in a Custom Tab → return by `return_url` → status poll until settled; the receipt arrives as a notification. **Bank transfer**: site bank details (IBAN, holder) with copy buttons and an **"I've paid by transfer"** declaration (amount, date, optional reference) that creates a `pending_declared` payment for the manager to confirm; shown as "declared, awaiting confirmation" on the statement. Never card data in-app | M5 |
| Tickets | Mine and my unit's, plus **common** tickets of the site/block; detail with status history and public comments; create with category, description, up to 10 photos, optional unit; comment; **"+1"** on common tickets with the count visible. Statuses are **per-site workflows**: the app renders the status name and uses the `open_like` / `closed_like` semantic for grouping, never a hard-coded list | O1, O3 |
| Announcements | One-way list with unread state and pinned first; detail with Markdown and attachments; the **read receipt is sent when opened** in the app; no comments or reactions | O4 |
| Documents | Site folders with visibility already applied by the server; the **unit's private folder**; **generated documents** (statements, receipts, certificates, minutes) appear where the server filed them; open PDFs inline; share | O8 |
| Bookings | Facility list with its **rules** (hours, max duration, lead time, quota); slot picker; **fee preview** before confirming; instant confirmation; cancel (refund rule shown); my bookings; `Idempotency-Key` required | C1 |
| Visitors | **Pre-register** (name, plate, time window); the app renders the server-issued `qr_token` as a QR and **shares it** (image + text) through the share sheet; **recurring visitors** with a long-lived QR, revocable; history. **Unknown visitor at the gate**: the `visitor.approval_requested` notification opens an approve / deny screen (notification action buttons where the platform allows); the expiry countdown is shown; after expiry the guard calls the **gate contact phone** the user set in the profile | C2 |
| Parcels | `delivery.received` notification; list of uncollected and recent parcels per unit | C3 |
| Governance (0.2 backend) | Upcoming assembly with agenda and call letter; **pre-meeting non-binding polls** (one answer per unit, owners only); the owner's **check-in QR** for the attendance desk; decisions with outcome and minutes PDF; tenants see only decisions marked "concerns residents". No online binding vote | G1, G2, G4 |
| Notifications | In-app center with read state; mutes per non-essential type (sign-in codes and invitations cannot be muted); the Android channel per `type` and the iOS in-app preference are the same setting | O5 |
| Profile | Me (name, contacts, locale), my units and sides, organization switch, sessions, app lock, **directory opt-in** per site (S8), **gate contact phone** (C2), consents (texts from the server, Turkish texts from the 0.4 pack), about and licenses | S8, C2 |

### 7.2 Deep links into the web app

Anything deeper than the app offers (a manager assigning a ticket, the guard console, settings) opens the web app with the current session through the **web-login link** (backend §8, 0.4): the app requests a one-time token and opens `{BASE_URL}/login/link/{token}?next=/orgs/{org_id}/…`. Web routes are the `/orgs/$orgId/…` tree of the web plan; there is no `/app/…` path.

### 7.3 Later (after mobile 1.0; each a separate, estimated piece)

| Item | One-line scope |
|---|---|
| **Multi-account** | Several (server, user) pairs with per-account caches and a switcher; the push payload's `instance_id` already routes for it |
| **Persistent offline write queue** | Durable outbox for tickets, comments and visitor registrations with client-supplied UUIDv7 ids accepted API-wide — a deliberate API contract, not a client trick (review §6) |
| **Delta sync** | `updated_since` with tombstones and ETags once the backend adds them; until then first-page refetch |
| **iOS Notification Service Extension** | ≈ 2 weeks of native Swift: a shared keychain access group for the token, fetch of the real title within the time budget, localization; until then generic per-type titles |
| **`foss` flavor / UnifiedPush** | Android build without Google services, `unifiedpush` distributor with periodic-fetch fallback, F-Droid metadata; the design stays as in §9 |
| **White-label tooling** | Scripted rebrand (icons, bundle ids, signing, relay) from `app_config.json`; the config file itself exists from day one |
| **Staff work mode** | Assigned ticket queue, status changes with photos, large targets |
| **Guard gate mode** | Phone-native version of the 0.3 web guard console: scan the guest QR, expected visitors, parcels |
| **Manager quick actions** | Record a cash payment, assign a ticket, publish an announcement |
| **Passkeys / OIDC**, data export and erasure requests (1.0 privacy endpoints), tablet two-pane layouts | When the backend ships them |

---

## 8. Offline and caching

- **Read cache** in drift for everything the home, statement, announcements, tickets, documents (metadata), bookings, visitors, parcels and governance screens need. Policy: **stale-while-revalidate** — render from cache immediately, refresh in the background, show "updated …" / "offline" subtly. Refresh means **refetching the first page(s)** of each list; there is no `updated_since` or ETag in v1 (overview §6.2) and lists are small.
- **Writes are online-only with retry-on-failure.** A failed create keeps its draft (text, chosen photos, the generated `Idempotency-Key`) in memory and `shared_preferences` until it is sent or discarded; retrying reuses the same key so a timeout cannot double-create. `412 version_conflict` reloads the record and asks the user to redo the change. Payments, bookings and poll answers say plainly that they need a connection.
- Cache limits per account, trimmed by age; attachments cached on demand; cleared on sign-out and from settings.
- `connectivity_plus` is a hint only; the real signal is request success.

---

## 9. Push notifications

- **Registration**: after sign-in and on token change, `POST /v1/me/devices` with the `device` fields of backend §4.12: `push_kind` (`fcm` | `apns`), `token`, `relay_id` (from `app_config.json`; `official` in the store build), `app_version`, `locale`. `DELETE` on sign-out. The server stores the token encrypted and sends through the relay configured for that `relay_id` (`PUSH_RELAYS`, backend §17).
- **Payload** (backend §12): content-free — `{ instance_id, org_id, notification_id, type, collapse_key, badge }`. `instance_id` lets the app verify the message belongs to its account and will route between accounts once multi-account exists. The **relay protocol lives in the infra plan §4**; this plan only requires that the relay's `data` allowlist carries exactly these keys.
- **Display**: the app ships **generic per-type titles** in ARB for every value of the notification `type` vocabulary (backend §12: `announcement.published`, `charge.due_soon`, `charge.overdue`, `payment.receipt`, `ticket.updated`, `visitor.approval_requested`, `delivery.received`, `meeting.called`, …), so a notification is readable without network. Android: `onMessage` fetches `GET /v1/me/notifications/{id}` and replaces the text when online. iOS: generic title only in the first release (NSE later, §7.3). Unknown types fall back to "New notification".
- **Tap** → go_router route from `type` + ids (`/orgs/:org/tickets/:id`, `/orgs/:org/visitors/requests/:id`, …).
- **Android notification channels** are keyed by the backend's `type` vocabulary grouped per area (announcements, money, tickets, visitors and parcels, assembly), so users can tune at OS level; iOS uses the in-app mutes.
- **UnifiedPush (design note, built later)**: a `foss` Android flavor registers with `push_kind: unifiedpush` and the distributor's endpoint URL as `token`; the server POSTs the same content-free payload directly to that URL (backend §12), bypassing the relay; without a distributor installed the flavor uses the periodic-fetch path below. iOS always uses APNs.
- **Degradation**: the relay is a **shared dependency** (overview §6.5). When registration fails, the instance has no relay, or no push arrives for a configured period, the app falls back to **periodic fetch** of the unread-count endpoint (background fetch on both platforms, best effort) and shows a "notifications may be delayed" hint in the center. No personal data ever transits FCM, APNs or the relay.

---

## 10. Packs in the mobile app

Rule from overview §6.7: **nothing a pack does requires client code**. Where packs show up, the app is data-driven:

| Pack contribution | How the app handles it |
|---|---|
| Payment provider | "Pay by card" exists when the checkout endpoint reports an enabled provider; checkout is a URL |
| SMS provider | Invisible; OTP arrives the same way |
| Consent texts | Rendered Markdown with accept/decline; versions tracked by the server |
| Document templates | Produce files; the app opens them |
| Legal presets | Change numbers and rules the API returns (fees, quorum); the app displays them |
| Labels for enums and codes | `GET /v1/orgs/{org}/codes?locale=` (0.1) supplies pack labels; the app's own ARB covers core values |
| Settings schemas | Not shown in mobile (admin is web) |

If a pack ever needs native UI (an NFC credential, for example), it would ship as a Dart package behind a feature flag from `/v1/instance` — post-1.0, as pack UI bundles are.

---

## 11. UX, design, accessibility

- Material 3 with a brand seed color from `app_config.json` (instance branding from discovery tints the server screen and header); light and dark; platform-adaptive bits (iOS back swipe, Cupertino pickers, share sheets).
- Bottom navigation: **Home · Money · Tickets · More** (Announcements, Documents, Bookings, Visitors & parcels, Assembly, Notifications, Profile). Money is hidden entirely for people without money rights.
- Money through one `MoneyText` (site currency, user locale); dates through `SiteDate` (site timezone); never `DateTime.now()` formatting directly.
- **Accessibility**: semantic labels on every actionable widget, dynamic type to 200 % without clipping, 48 dp targets, WCAG AA contrast, TalkBack/VoiceOver pass per release.
- **RTL**: logical layout from day one (`EdgeInsetsDirectional`, `Directionality`); goldens in an `ar` pseudo-locale; mirrored directional icons. Not a release gate for 1.0 (overview §6.6).
- Empty, loading (skeleton), error and offline states are shared components. Tablet layouts are later.

---

## 12. Internationalization

- ARB under `lib/l10n/`, `en` template, `tr` complete before 1.0; translation platform shared with the core repo when one exists (overview §10).
- Keys namespaced per feature; API `code`/enum strings mapped in one place (`core/l10n/codes.dart`); ICU plurals.
- **Locale chain: user setting → device locale → organization default → `en`** (overview §6.6; `tr-TR → tr → en`). The chosen locale is sent as `Accept-Language` and stored on the device record so server-rendered PDFs, SMS and push text match.

---

## 13. Security and privacy

- Refresh token only in secure storage; access token in memory; the log interceptor scrubs `Authorization`, tokens in URLs and personal fields.
- HTTPS enforced; no certificate pinning; TLS errors are never bypassable from the UI.
- App lock with biometrics; `FLAG_SECURE` / screenshot prevention on money screens as an opt-in setting (off by default — residents screenshot statements legitimately).
- Attachments: EXIF location stripped before upload (the server strips again); permissions requested in context.
- Push payloads carry no content (§9); the relay sees device tokens and ids, which the privacy notice states.
- Store compliance: iOS privacy manifest and "App Privacy" answers, Play Data Safety form, both derived from `PRIVACY.md` in the repo. Dues are a real-world service: Apple's in-app purchase rules do not apply; no card data in-app.
- No analytics SDKs; crash reporting opt-in and only in the hosted build. Dependencies audited monthly (`dart pub outdated`, Dependabot).

---

## 14. Testing

| Layer | What | Tooling |
|---|---|---|
| Unit | Providers with overridden repositories, mappers, money/date formatting, refresh single-flight, retry with reused `Idempotency-Key`, push payload → route, generic title lookup incl. unknown types | `flutter_test`, `mocktail` |
| Widget | Screens with fake providers: loading/empty/error/offline, permission- and side-gated UI (owner vs tenant vs household), unknown enum values rendered safely | `flutter_test` |
| Golden | Key screens × {light, dark} × {ltr, rtl} × {normal, large text} | `alchemist`, fonts pinned |
| Contract | Generated client against the **pinned spec** (`prism` mock) for request shapes; the **integration suite against the previous two server minor releases** (N-1, N-2 container images) so the N-2 support promise (§15) is tested, not assumed | `prism`, compose |
| Integration | App against a real backend from the core repo's compose with the golden site: sign-in by SMS code (stub gateway), dashboard, statement per side, declared transfer, create ticket with photo, "+1", book with fee preview, register and share a visitor, approve a visitor request from a notification, poll answer | `integration_test` + `patrol`, Android emulator + iOS simulator |
| Accessibility | `meetsGuideline` checks for tap targets, contrast, labels | `flutter_test` |

PR CI: unit/widget/golden < 8 min. Nightly: integration against the current server and the N-1/N-2 matrix.

---

## 15. Build, flavors, release and stores

- **Flavors**: `dev` (http on localhost, debug tools) and `prod`. The `foss` distribution flavor is later (§7.3).
- **App config** (`assets/app_config.json`): app name, default server URL (optional), `relay_id`, brand colors/logo, support URL. Bundle ids and signing per build via `--dart-define-from-file`. The file exists from day one; the rebrand tooling is later.
- **Spec pinning**: `tool/fetch_api.sh` downloads the spec and Dart client for the version recorded in `api_version.lock` from the core repo's release assets; bumping the lock is an ordinary PR with regenerated code and a changelog note of the server minimum.
- **Server support window**: a given app release supports servers from its pinned minor **down to N-2 minors** (built against 0.4 → works with 0.4, 0.3, 0.2 for the endpoints they have; features whose endpoints are missing hide themselves). The app only depends on endpoints marked `x-stability: stable`.
- **Versioning**: semver for the app; build number auto-incremented in CI; tags **`v*` in the mobile repository** (no `mobile-v*`). The app identifies itself by `User-Agent: OpenBekci-Mobile/<version> (<platform>; <build>)` and by `app_version` on the device record.
- `fastlane` lanes `beta` (Play internal / TestFlight) and `release`; signing secrets only in CI.
- **Store submission dependencies**: a **reviewer-accessible demo instance** with a seeded site and documented test credentials is required before the first submission (owner, overview §11). If the hosted instance ever offers **Google OIDC**, Apple requires **Sign in with Apple** on the same screen — a dependency on the sign-in method list, noted now so it is not discovered at review.
- Release checklist: changelog, screenshots regenerated by `patrol`, a11y pass, store privacy forms re-checked against `PRIVACY.md`, N-1/N-2 matrix green, `min_app_version` on the hosted instance bumped only after store rollout completes.

---

## 16. Backend requirements driven by this plan

What the app needs, with the core release each ships in (backend §8):

| Requirement | Core release |
|---|---|
| Discovery document `GET /.well-known/openbekci.json` and `GET /v1/instance`; an **instance identifier** the app can match against `instance_id` in push payloads | 0.1 (identifier: confirm it is exposed by `/v1/instance`) |
| `GET /v1/me`: organizations, roles, effective permissions, occupancies with sides and `may_see_money`, policies | 0.1 |
| `GET /v1/orgs/{org}/me/dashboard` aggregate | 0.1 |
| `GET /v1/orgs/{org}/codes?locale=` for enum/error labels incl. pack labels | 0.1 |
| Image variants `?variant=thumb\|medium` for attachments | 0.1 |
| Mobile link formats with `client=mobile` (magic link, invitation, reset) | 0.1 |
| Statement per unit side and per person; late fee accrued to date in the statement response | 0.1 |
| Declared bank-transfer payment (`pending_declared`) creatable by the payer | 0.1 |
| Visitor approval request endpoints answerable by the resident; `qr_token` for visitors and the per-person meeting check-in token | 0.3 / 0.2 |
| `POST/DELETE /v1/me/devices` with `push_kind`, `token`, `relay_id`, `app_version`, `locale`; push payload with `instance_id` | 0.4 |
| **Web-login link** endpoint and the web route `/login/link/$token` | 0.4 |
| Checkout `checkout_url` / `return_url` and a status poll endpoint; the mobile return URL is `openbekci://pay/return` | 0.4 |
| `GET /v1/me/notifications/{id}` cheap enough to call from a push handler | 0.1 (endpoint) |

Not required in v1: `updated_since`, ETags, client-supplied ids (all later, §7.3).

---

## 17. Phases

Work starts **after core 0.3 is stable**; phases A and B run against a 0.3 server (every endpoint they need exists by then), phases C and D run in parallel with the backend's 0.4 phase, which they depend on. ≈ 20 engineer-weeks to the first store release for one Flutter engineer; the relay (≈ 4 weeks) is counted in the infra plan.

| Phase | Weeks | Deliverables | Exit criteria |
|---|---|---|---|
| **A — Foundation** | 4 | Repository, flavors, `app_config.json`, pinned spec + published Dart client wiring (generator spike, open enums, ADR), Dio + auth interceptor + refresh, discovery and version gate, all 0.1 sign-in methods incl. site QR + unit claim, go_router with guards, theme, ARB en/tr, drift read cache, CI (analyze, test, golden) | Sign in to a dev server by SMS code, password, magic link and invitation; unit claim reaches "waiting for approval"; RTL golden suite runs |
| **B — Resident core** | 7 | Home, statement per side with continuous late fee and PDF, declared transfer, tickets (workflow-generic statuses, private/common, +1, photos), announcements with read receipts, documents incl. unit folder and generated files, notifications center and mutes, profile (directory opt-in, gate contact phone, sessions, app lock, consents) | Integration suite passes against the compose backend with the golden site; owner/tenant/household goldens prove side visibility |
| **C — Community, governance, push** | 5 | Bookings with rules and fee preview, visitors (QR share, recurring, approve/deny from notification), parcels, governance (polls, check-in QR, decisions, minutes), device registration, push handling with generic titles, Android channels, periodic-fetch degradation | Push received end to end on both platforms through the relay; visitor approval answered from a notification; store internal tracks live |
| **D — Pay and store release** | 4 | Provider checkout + status poll, web-login link, N-1/N-2 contract matrix, a11y pass, store listings and privacy forms, demo instance for reviewers, release automation | **Mobile 1.0 on Play and App Store** alongside core 0.4 |

---

## 18. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| API churn between the pinned spec and the 0.4 server | The app pins a spec version and only uses `x-stability: stable` endpoints; the generated client turns breakage into compile errors; the N-1/N-2 matrix catches runtime drift |
| Who runs the official relay, its cost and abuse control | Owner decision (overview §11); the app degrades to periodic fetch, and white-label relays remain possible by `relay_id` |
| Relay is a shared point of failure or store ban | Accepted and documented; per-server kill switch on the relay side (infra plan); periodic fetch keeps the app usable |
| Content-free push is less informative offline | Generic per-type titles; Android fetches on receipt; iOS NSE later |
| Generated Dart client quality and open-enum support in `openapi-generator` | Phase A spike on the pinned spec; `enumUnknownDefaultCase`; ADR on the generator |
| Phone-first users without email | SMS OTP is the primary method from 0.1 (D5); invitations go by SMS when a phone exists (O5) |
| Self-signed certificates on small self-hosted servers | Documented CA install; the infra plan pushes Let's Encrypt via Caddy rather than weakening TLS in the app |
| Store review of payments and of a sign-in-only app | Dues are services, no IAP, hosted checkout in a browser tab; reviewer demo instance with seeded data; Sign in with Apple only becomes mandatory if Google OIDC is offered |
| Retry-on-failure without a durable queue loses a draft if the app is killed | Drafts persist in `shared_preferences` with their `Idempotency-Key`; the durable queue is a later, estimated piece with its own API contract |
| `instance_id` is defined for the push payload (backend §12) but not yet in the discovery schema (backend §8) | Listed in §16 as a requirement; resolve in the backend spec before the devices endpoint ships |
| Background fetch limits on iOS make the degradation path best-effort | Acceptable: the in-app center always shows the truth on open; the hint tells the user push is delayed |
