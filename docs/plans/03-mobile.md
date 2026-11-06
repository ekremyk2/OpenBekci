# OpenBekci — Mobile Plan

| | |
|---|---|
| Status | Draft v0.1 |
| Date | 2026-11-06 |
| Parent | [00-overview.md](00-overview.md) — shared contracts there bind this document. API details in [01-backend.md](01-backend.md) |

---

## 1. Goals and non-goals

**Goals**

- A Flutter app for Android and iOS that works against **any OpenBekci instance** — the project's hosted service or a self-hosted server — chosen at first launch.
- **Resident- and owner-first.** The app is the daily touchpoint for the people who live in the site: dues, tickets, announcements, bookings, visitors, votes. Management and accounting stay on the web.
- Push notifications that work for self-hosters without them owning store credentials (push relay, overview §6.5).
- Usable offline for reading and for the few writes that matter when the connection is bad (opening a ticket in the basement, registering a visitor at the gate).
- One codebase, build-time branding: forks and white-label builds change a config file, not Dart code.
- Same quality bars as the rest of the project: generated client from the OpenAPI spec, i18n with RTL from the first screen, accessibility, tests in CI.

**Non-goals (v1)**

- Admin/configuration screens (charge plans, chart of accounts, imports, pack settings) — web only.
- Flutter Web or desktop targets (the React app covers desktop; evaluate Flutter desktop for a guard kiosk later).
- Embedding payment card forms; providers' hosted checkout only.
- Full offline-first sync of every entity.

---

## 2. Users and the app's shape

| User | Mode in the app | Typical actions |
|---|---|---|
| Resident / tenant | default | See balance and dues, pay (if a provider is enabled), open and follow tickets, read announcements, book facilities, pre-register visitors |
| Owner | default + owner extras | Everything above for all owned units, vote in assemblies, view documents |
| Board member | default + oversight | Approve bookings, see site-wide tickets, meeting participation |
| Staff / technician | **work mode** (v1.1) | Assigned ticket queue, status updates with photos |
| Guard / concierge | **gate mode** (v1.1) | Expected visitors today, check-in/out, plate lookup, deliveries |
| Manager | limited | Quick actions only: record a cash payment, assign a ticket, publish an announcement (v1.2). Everything else links to the web app |

The app renders what the user's permissions allow (overview §6.4): the permission list and roles arrive with `GET /v1/me`, and navigation is built from them. There is no separate "manager app".

---

## 3. Technology stack

| Concern | Choice | Notes |
|---|---|---|
| Framework | Flutter stable channel, Dart 3.x; we track the latest stable and update within a month of each release | Pin via `.fvmrc` (fvm) so CI and contributors match |
| State management | **Riverpod** (`riverpod` + `riverpod_generator`, `riverpod_annotation`) | Compile-safe providers, easy testing via overrides |
| Routing | **go_router** with typed routes (`go_router_builder`) | Deep links, guards for auth/org selection, nested navigation |
| API client | Generated **Dart client** from `api/openapi.yaml` (`openapi-generator` `dart-dio` target, published as `clients/dart` in the monorepo; the app depends on it by path) | Spike at MB0: if generated models are too heavy, fall back to `openapi-generator` `dart` + `json_serializable`. ADR |
| HTTP | `dio` with interceptors: auth (bearer + refresh), request id, retry with backoff for idempotent calls, logging (dev only), `Accept-Language` | |
| Models | `freezed` + `json_serializable` for app-side models; generated client models stay as generated | |
| Secure storage | `flutter_secure_storage` (Keychain / Android Keystore) for refresh tokens and account list | |
| Local database | **drift** (SQLite) for cache and the outbound write queue | Typed queries, migrations, works on both platforms |
| Preferences | `shared_preferences` for non-sensitive UI prefs | |
| Push | Flavor-dependent: `firebase_messaging` (Android Play + iOS via APNs) and **UnifiedPush** (`unifiedpush` package) for the FOSS/F-Droid flavor | See §9 |
| Deep / universal links | `app_links`; Android App Links + iOS Universal Links for the hosted domain; custom scheme `openbekci://` for self-hosted callbacks | |
| Browser flows | `flutter_custom_tabs` (Chrome Custom Tabs / SFSafariViewController) for OIDC, e-Devlet, payment checkout | Never a `WebView` for credentials |
| Biometrics | `local_auth` for app lock | |
| Media | `image_picker`, `flutter_image_compress` (resize to ≤2000 px, strip EXIF location before upload), `file_picker`, `open_filex` | |
| PDF viewing | `pdfrx` (statements, minutes) | |
| QR | `mobile_scanner` for server/invitation QR codes | |
| i18n | `flutter_localizations` + ARB files via `flutter gen-l10n`; `intl` for dates/numbers/currency | RTL handled by `Directionality` from locale |
| Logging / crashes | `logger` with token scrubbing; optional Sentry (`sentry_flutter`) only in the hosted flavor and opt-in | |
| Lint | `very_good_analysis` (or `flutter_lints` + project rules) | |
| Testing | `flutter_test`, `mocktail`, golden tests (`alchemist`), `integration_test`, **patrol** for native-interaction e2e | |
| CI / release | GitHub Actions, `fastlane` (Play internal track, TestFlight), F-Droid metadata in `metadata/` | |

Deliberately avoided: BLoC (Riverpod is enough and simpler for contributors), GetX, hand-written API models, Firebase beyond messaging (no Analytics/Crashlytics in core), embedded WebViews for auth or payments.

---

## 4. Architecture

Feature-first, three layers inside each feature. Dependencies point inward: presentation → application → data; nothing in `features/` imports another feature's data layer (go through its providers).

```
mobile/
├─ lib/
│  ├─ main_dev.dart / main_staging.dart / main_prod.dart     entry points per flavor
│  ├─ app/
│  │  ├─ app.dart              MaterialApp.router, theme, locale, lifecycle
│  │  ├─ router.dart           go_router config + guards (needs account? needs org?)
│  │  ├─ theme/                Material 3 theme, brand tokens from app config
│  │  └─ config/               AppConfig loaded from assets/app_config.json (branding, default server, relay id, flavor)
│  ├─ core/
│  │  ├─ api/                  Dio setup, interceptors, error mapping (problem+json → AppError with `code`)
│  │  ├─ accounts/             Account model (server + user), AccountStore (secure storage), switching
│  │  ├─ auth/                 token flow, refresh single-flight, external IdP flow, invitation handling
│  │  ├─ instance/             discovery (.well-known), capability/version checks
│  │  ├─ db/                   drift database, per-account DB files, migrations
│  │  ├─ sync/                 cache policies, outbound write queue, connectivity
│  │  ├─ push/                 platform push abstraction, device registration, payload → route
│  │  ├─ l10n/                 generated localizations + helpers (money, dates in site tz)
│  │  ├─ permissions/          Permission enum (from generated client) + `can()` helpers
│  │  └─ widgets/              shared UI: empty states, error views, money text, avatar, skeletons
│  └─ features/
│     ├─ onboarding/           server entry, QR scan, account add
│     ├─ home/                 dashboard
│     ├─ statement/            charges, payments, balance, PDF
│     ├─ payments/             pay flow (provider checkout), payment history
│     ├─ tickets/
│     ├─ announcements/
│     ├─ documents/
│     ├─ bookings/
│     ├─ visitors/
│     ├─ notifications/
│     ├─ meetings/             (v1.1) agenda, attendance, online voting
│     ├─ gate/                 (v1.1) guard mode
│     ├─ work/                 (v1.1) staff ticket queue
│     └─ profile/              me, units, org/site switch, settings, app lock, consents
├─ packages/                   small internal packages if a layer gets large (e.g. `openbekci_ui`)
├─ assets/                     app_config.json, fonts, images
├─ android/ ios/               flavors: dev, staging, prod × play, foss (Android only)
├─ test/ integration_test/
└─ metadata/                   F-Droid / store listing sources
```

Per feature:

```
features/tickets/
├─ data/          TicketsRepository (API + drift cache), DTO mappers
├─ application/   providers/notifiers (list, detail, create form), use cases
└─ presentation/  screens, widgets
```

Error handling: every API error becomes `AppError(code, fieldErrors, retryable)`; a single `errorMessage(code)` maps codes to ARB strings. Unknown codes show a generic message with the code visible under "details", so support can work from screenshots.

---

## 5. Instance discovery and accounts

### 5.1 First launch

1. **Server screen**: a text field for the server address (prefilled with the hosted service in the official build; empty in white-label builds unless `app_config.json` sets a default), and a **Scan QR** button. Managers can show a QR in the web app that encodes `openbekci://connect?server=https://…&invite=<token>`.
2. The app normalizes the input (adds `https://`, strips paths) and fetches `GET https://<host>/.well-known/openbekci.json`:
   ```json
   {
     "instance_name": "Güneş Sitesi Yönetimi",
     "api_base_url": "https://site.example.com/v1",
     "server_version": "0.9.3",
     "api_versions": ["v1"],
     "min_app_version": { "android": "1.0.0", "ios": "1.0.0" },
     "auth_methods": ["password", "magic_link", "passkey", "oidc:google", "oidc:tr.edevlet"],
     "push": { "relay_id": "official", "unified_push": true },
     "packs": ["tr"],
     "branding": { "logo_url": "…", "primary_color": "#…" }
   }
   ```
3. Version gate: if the app is older than `min_app_version` → "update the app"; if the server lacks the app's required API version → "ask your manager to update the server". Both screens still allow sign-out/switch account.
4. **HTTPS is required.** Plain `http://` is accepted only for `localhost`/`10.0.2.2` in the `dev` flavor. Self-signed certificates are not special-cased: the user installs their CA on the device (documented); the app does no certificate pinning because servers are arbitrary.

### 5.2 Accounts

- An **account** = server URL + user. The app supports several at once (a manager with two companies, a person living in one site and owning a flat in another on a different server). Switching is in the profile tab; each account has its own token store entry and its own drift database file (`<account_id>.db`), so caches never mix.
- Within an account, the user may belong to several **organizations**; `GET /v1/me` lists them with roles and permissions. The app remembers the last org per account; a switcher appears only when there is more than one.
- Logout revokes the session server-side (`DELETE /v1/me/sessions/current`), wipes the account's secure-storage entry and deletes its database.

---

## 6. Authentication

- Token flow from backend §6: `POST /v1/auth/token` → access JWT (15 min) + rotating refresh token. Access token lives in memory only; the refresh token in secure storage.
- `AuthInterceptor`: attaches the bearer; on `401` runs a **single-flight refresh** (concurrent requests wait on the same future), retries once; if refresh fails with `invalid_grant` the account is marked signed-out and the UI returns to the sign-in screen for that account without losing other accounts.
- Sign-in methods, driven by `auth_methods` from discovery:
  - **Password** (+ TOTP step when the server answers `mfa_required` with a challenge id).
  - **Magic link**: the app requests it; the email's link is a universal/app link (`https://<hosted>/m/…`) or `openbekci://auth/magic?…` for self-hosted; the app exchanges the token for a session.
  - **Passkeys**: `passkeys` package with platform authenticators — **v1.1**, after the web has proven the server side.
  - **External IdPs** (OIDC, e-Devlet via pack): authorization code + PKCE in a Custom Tab / SFSafariViewController; callback via app link or custom scheme; the server completes the exchange and issues tokens. The app never sees provider credentials.
  - **Invitation**: `openbekci://invite?server=…&token=…` or the hosted universal link → prefilled account creation that links the `person`.
- **App lock**: optional biometric/PIN lock (`local_auth`) with a configurable grace period; mandatory lock can be required by org policy (a flag in `/v1/me`).
- Session list and remote revocation are in the profile tab (`GET /v1/me/sessions`).

---

## 7. Features by release

### 7.1 v1.0 (project milestone M5) — resident core

| Feature | Details |
|---|---|
| Home | Per selected org/site: current balance and next due charge (per unit, with a unit chip when the person has several), unread announcements, open tickets, upcoming bookings, today's expected visitors. One aggregate call (`GET /v1/orgs/{org}/me/dashboard`) |
| Statement | Charges, payments, late fees, running balance per unit and period; filter by period; download the PDF statement; share sheet |
| Pay | If the org has an enabled `PaymentProvider`: select charges/amount → server creates a checkout → opened in Custom Tab → return via app link → poll payment status. If no provider: show bank details (from site settings) and a "I've paid by transfer" note that creates a *pending* manual payment for the manager to confirm |
| Tickets | List (mine / my unit's; site-wide when permitted), detail with timeline, create with category, description, photos (compressed), optional location; comment; reopen. Resident sees only `public` comments |
| Announcements | List with unread state, detail (Markdown rendered), attachments, read receipt sent on open; pinned first |
| Documents | Folder tree with visibility already applied by the server; open PDFs inline; download with share |
| Bookings | Facility list with rules; calendar/slot picker; create request; cancel; see fee before confirming; status updates by push |
| Visitors | Pre-register a visitor (name, plate, time window), share a QR/code; history; my vehicles |
| Notifications | In-app center with read state; preferences per type → channels (mirrors web) |
| Profile | Me (name, contacts, locale, timezone display), my units, org/site switch, account switch, sessions, app lock, consents (texts from core or pack), data export request, about/licenses |

### 7.2 v1.1 (M6) — pack-dependent and governance

- Payments through the Turkish pack provider; e-Devlet sign-in; KVKK consent screens (texts from the API).
- **Meetings & voting**: upcoming assembly, agenda, proxy declaration, check-in QR, online ballots while a vote is open, results, minutes.
- **Work mode** for staff: assigned ticket queue, status changes, photo notes.
- **Gate mode** for guards: today's expected visitors, check-in/out, plate search, manual entry, deliveries; large touch targets, stays on while docked.
- Passkeys.

### 7.3 v1.2 — manager quick actions

- Record a cash/transfer payment (with `Idempotency-Key`), assign/triage tickets, approve/reject bookings, publish an announcement. Each is a single screen; everything deeper deep-links into the web app (`<server>/app/…` with the same session via a one-time login link).

Not planned for mobile: ledger views, charge plans, imports/exports, pack configuration, meeting setup.

---

## 8. Offline and caching

- **Read cache** in drift for every list/detail the home, statement, announcements, tickets, documents (metadata), bookings and visitors screens need. Policy: **stale-while-revalidate** — render from cache immediately, refresh in the background, show a subtle "updated …" / "offline" indicator. Lists use `updated_since` + `ETag`/`If-None-Match` (backend §8) to keep refreshes cheap.
- **Write queue** (`outbox` table in drift) for a deliberately small set: create ticket (with attachments queued as uploads), add ticket comment, register visitor, mark announcement read. Each queued write carries a client-generated `Idempotency-Key` and the resource `version` where relevant; the queue replays in order when connectivity returns; conflicts (`412`) surface as a "needs your attention" item rather than silent overwrite.
- Everything else (payments, bookings, votes) requires connectivity and says so.
- Cache limits: per account, trimmed by age; attachments cached on demand only. Cleared on logout and on "clear cache" in settings.
- Connectivity: `connectivity_plus` as a hint only; the real signal is request success.

---

## 9. Push notifications

- **Registration**: after sign-in and on token change, `POST /v1/me/devices { platform, push_kind: fcm | apns | unifiedpush, token, relay_id, app_version, locale }`. The server stores it (encrypted) and routes through the configured relay (or directly to the UnifiedPush endpoint, which is just an HTTPS URL). Unregister on logout.
- **Payload** (overview §6.5): content-free except routing data: `{ notification_id, org_id, type, collapse_key, badge }`. The app has localized generic titles per `type` (e.g. "New announcement", "Your ticket was updated") so a notification is readable without network. When online it fetches `GET /v1/me/notifications/{id}` to show the real title/body: on Android in the `onMessage` handler, on iOS in a **Notification Service Extension** (with a short timeout and the generic text as fallback). No personal data ever transits FCM/APNs/relay.
- **Tap** → route via go_router (`/orgs/:org/tickets/:id` …); if the account isn't the active one, switch first.
- **Flavors**: `play` uses FCM (Android) and APNs through `firebase_messaging` (iOS). `foss` (Android only, F-Droid) uses UnifiedPush with a fallback of periodic background fetch (`workmanager`) when no distributor is installed. The iOS build always uses APNs.
- **Relay identity**: `app_config.json` carries `relay_id`; the official build says `official`. A white-label build points to its own relay and its own FCM/APNs project. The infra plan specifies the relay protocol; this plan only requires that the server knows a relay by id.
- Android notification channels per type (announcements, tickets, money, visitors, meetings) so users can tune at OS level; iOS uses the in-app preferences.

---

## 10. Packs in the mobile app

The rule from the overview: packs should not need custom mobile UI. The app is data-driven where packs show up:

| Pack contribution | How the app handles it |
|---|---|
| Identity provider | Appears as a sign-in button from `auth_methods`; label/icon from the pack manifest via discovery; browser flow as in §6 |
| Payment provider | "Pay" exists when `GET /v1/orgs/{org}/payments/providers` is non-empty; checkout is a URL |
| Consent texts | Rendered Markdown with accept/decline; version tracked by the server |
| Document templates | Produce files; the app just opens them |
| Legal rules | Affect numbers (quorum, weights) returned by the API; the app displays them |
| Settings schemas | Not shown in mobile (admin is web) |

If a pack ever genuinely needs native UI (an NFC gate card, for example), it ships as a Dart package registered in a `packs.dart` file behind a feature flag from `/v1/me.enabled_packs` — the mobile counterpart of the server's compile-time packs. Not in v1.

---

## 11. UX, design, accessibility

- Material 3 with a brand seed color from `app_config.json`; light and dark themes; platform-adaptive bits where users expect them (iOS back swipe, Cupertino date pickers, share sheets).
- Bottom navigation: Home · Money · Tickets · More (Announcements, Documents, Bookings, Visitors, Notifications, Profile). Work/Gate modes replace the tab set when active.
- **RTL** verified on every screen via golden tests in `ar` pseudo-locale; icons that imply direction are mirrored.
- **Accessibility**: semantic labels on all actionable widgets, dynamic type up to 200 % without clipping, minimum 48 dp touch targets, contrast per WCAG AA, TalkBack/VoiceOver pass per release.
- Money is always rendered through one `MoneyText` widget using the site's currency and the user's locale; dates through `SiteDate` using the site's timezone — never `DateTime.now()` formatting directly.
- Tablet: responsive layouts (two-pane lists) without separate screens; not a release gate for 1.0.
- Empty, loading (skeleton), error and offline states are first-class components, not afterthoughts.

---

## 12. Internationalization

- ARB files under `lib/l10n/`, `en` as the template, `tr` complete before 1.0; Weblate project shared with web (different file formats, same workflow).
- Keys are namespaced per feature (`tickets_create_title`); the same enum/`code` strings from the API are mapped in one place (`core/l10n/codes.dart`) so a new error code is a one-line addition.
- Plurals and gendered/case forms through ICU messages in ARB.
- Locale resolution: user setting → device → `en`. The chosen locale is sent as `Accept-Language` so server-rendered PDFs match.

---

## 13. Security and privacy

- Tokens: refresh token only in secure storage; access token in memory; nothing in logs (interceptor scrubs `Authorization`, tokens in URLs, and personal fields).
- No certificate pinning (arbitrary servers); HTTPS enforced; TLS errors are never bypassable from the UI.
- App lock with biometrics; sensitive screens (statement, payments) can opt into `FLAG_SECURE`/screenshot prevention via a user setting (off by default — residents screenshot statements legitimately).
- Attachments: EXIF location stripped before upload; camera/photos permissions requested in context with a rationale.
- Push payloads carry no content (§9).
- Store compliance: iOS privacy manifest and "App Privacy" answers; Play Data Safety form; both derived from a `PRIVACY.md` kept in the repo. Paying dues is a real-world service, so Apple's in-app purchase rules do not apply; we still never collect card data in-app.
- No analytics SDKs in the core app. Crash reporting is opt-in and only in the hosted flavor.
- Dependencies reviewed with `dart pub outdated` monthly and Dependabot; `flutter pub deps` audit in CI.

---

## 14. Testing

| Layer | What | Tooling |
|---|---|---|
| Unit | Notifiers/providers with overridden repositories, mappers, money/date formatting, write-queue replay logic, auth refresh single-flight | `flutter_test`, `mocktail`, Riverpod overrides |
| Widget | Screens with fake providers: loading/empty/error/offline states, permission-gated UI | `flutter_test` |
| Golden | Key screens × {light, dark} × {ltr, rtl} × {normal, large text} | `alchemist`, fonts pinned |
| Contract | Generated client against the backend's spec examples (`prism` mock server in CI) for request shapes | `prism` |
| Integration | App against a **real backend** from `docker compose` with seeded data (the backend's golden 24-unit site): sign-in, dashboard, create ticket with photo, book a facility, register a visitor, offline queue replay | `integration_test` + `patrol` on Android emulator and iOS simulator in CI |
| Accessibility | Semantics checks in widget tests (`meetsGuideline` for tap targets, contrast, labels) | `flutter_test` a11y guidelines |

CI runs unit/widget/golden on every PR (< 8 min); integration nightly and on release branches.

---

## 15. Build, flavors and release

- **Flavors**: `dev` (local server allowed over http, debug tools), `staging`, `prod` × distribution `play` / `foss` (Android). iOS: `dev`, `staging`, `prod`.
- **App config** (`assets/app_config.json`, one per flavor/brand): app name, bundle ids are per-brand via `--dart-define-from-file`; default server URL (optional), `relay_id`, brand colors/logo, support URL, whether to show "hosted" sign-up. White-label = a new config file + icons + signing; documented in `docs/mobile/white-label.md`.
- Versioning: semver for the app, build number auto-incremented in CI; the app sends `X-OpenBekci-App: android/1.2.0 (123)`.
- `fastlane` lanes: `beta` (Play internal / TestFlight), `release`. Signing secrets only in CI.
- F-Droid: `foss` flavor builds reproducibly without Google services; metadata in `metadata/` (fastlane supply format).
- Release checklist: changelog, screenshots regenerated by `patrol` on reference devices, a11y pass, store forms unchanged or updated, `min_app_version` on the hosted instance bumped only after the stores finish rollout.

---

## 16. Backend requirements driven by this plan

These are recorded in the backend plan so the API side ships them in B5:

1. `GET /.well-known/openbekci.json` discovery document and `GET /v1/instance` (same data, authenticated variant with pack details).
2. `GET /v1/me` aggregate: user, organizations with roles and effective permissions, enabled packs, policies (app lock required), units via occupancies.
3. `GET /v1/orgs/{org}/me/dashboard` aggregate.
4. `updated_since` filter and `ETag`/`If-None-Match` on list endpoints the app caches.
5. `POST/DELETE /v1/me/devices` for push registration with `push_kind` and `relay_id`.
6. `GET /v1/me/notifications/{id}` cheap enough for the iOS Notification Service Extension (< 300 ms, minimal payload).
7. Image variants (`?variant=thumb|medium`) for ticket/announcement attachments.
8. A one-time "web login link" endpoint for deep-linking from the app into the web app with the current session.
9. Payment checkout API shape that is URL-based (`checkout_url`, `return_url` scheme) and a status poll endpoint.

---

## 17. Milestones

The app starts at B3 of the backend (tenancy, identity, money and tickets are stable by then) and lands 1.0 at project milestone M5.

| # | Name | Deliverables | Exit criteria |
|---|---|---|---|
| **MB0** | Skeleton (≈3 wks, during B3) | Project with flavors, `app_config.json`, generated Dart client pipeline in `task gen`, Dio + auth interceptor + refresh, instance discovery, multi-account store, go_router with guards, theme, ARB l10n (en/tr), drift setup, CI (analyze, test, golden) | Sign in to a dev server with password and magic link; account switch; RTL golden suite runs; ADR on generator choice |
| **MB1** | Resident core (≈6 wks, during B3–B4) | Home, statement + PDF, announcements, tickets with photos, documents, profile; read cache with SWR; write queue for tickets/read-receipts | Integration tests against compose backend pass; offline ticket creation replays |
| **MB2** | Operations + push (≈4 wks, needs B5 + relay) | Bookings, visitors, notifications center + preferences, push registration and handling (FCM/APNs/UnifiedPush), NSE on iOS, deep links from push | Push received end-to-end on both platforms through the relay; store internal tracks live |
| **MB3** | 1.0 release (≈3 wks, M5) | Payment flow (provider-agnostic, bank-details fallback), consents, data export request, a11y pass, store listings, F-Droid metadata, white-label docs | Public release on Play and App Store; `foss` build submitted to F-Droid |
| **MB4** | 1.1 (≈6 wks, M6) | TR pack flows (e-Devlet, provider), meetings & voting, work mode, gate mode, passkeys | Vote cast and counted end-to-end; guard check-in flow tested on a tablet |
| **MB5** | 1.2 (≈4 wks) | Manager quick actions, web deep-link login, tablet two-pane layouts | — |

---

## 18. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| API churn before B5 breaks the app during MB0–MB1 | Start only against modules that have passed their backend milestone; the generated client makes breakage compile-time; the spec's `x-stability` marks endpoints the app may use |
| Who runs the official push relay, cost and abuse control | Project owner decision (overview §11); UnifiedPush and white-label relays keep self-hosters independent either way |
| Firebase SDK in an open-source app | Confined to the `play`/iOS flavors; `foss` flavor has zero Google dependencies and is the one F-Droid builds |
| Content-free push makes notifications less informative when offline | Generic per-type titles; NSE/`onMessage` fetch when online; accept the trade-off for privacy and self-hosting |
| Generated Dart client quality (`dart-dio` + `built_value` is verbose) | MB0 spike; alternative generator; ADR |
| Self-signed certificates on small self-hosted servers | Documented CA install; recommend Let's Encrypt/Caddy in the infra plan rather than weakening TLS in the app |
| Phone-first users without email in Turkey | Depends on backend decision (backend §21); invitation links by SMS from the TR pack cover onboarding |
| Background fetch limits (iOS) for the `foss`-style fallback | iOS always has APNs; the fallback is Android-only |
| Store review of "payments" | Dues are services; no IAP; hosted checkout in a browser tab. Keep the "I've paid by transfer" flow clearly informational |
