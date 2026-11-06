# OpenBekci

Open-source residential community management: apartment buildings, gated sites, housing estates and mixed-use complexes. One system for residents, owners, boards, managers and staff — dues and a real ledger, tickets, announcements, documents, assemblies and voting, bookings, visitors.

**Status:** planning. No code yet; the plans below are the current state of the project.

## Shape

| Layer | Choice |
|---|---|
| Backend | Go, modular monolith, one binary with the web app embedded, PostgreSQL as the only required dependency |
| Web | React + Vite + TanStack Router/Query, shadcn/ui, Lingui (RTL from day one) |
| Mobile | Flutter, in its own repository ([openbekci-mobile](https://github.com/ekremyk2/openbekci-mobile)); connects to any instance, push via a relay |
| API | OpenAPI 3.1, contract-first; Go server and TypeScript/Dart clients are generated |
| Country packs | Compile-time Go modules behind a public SDK; the core contains no country logic. First pack: Turkey |

## Plans

| Document | Covers |
|---|---|
| [Overview](docs/plans/00-overview.md) | Decisions D1–D8, product scope by area with release tags, architecture, repositories, shared contracts, releases 0.1 → 1.0 |
| [Backend](docs/plans/01-backend.md) | Stack, modules, domain model (from the decision files), multi-tenancy and site portability, auth, API, money rules, pack SDK (0.4), jobs, privacy, testing, phases per release |
| [Web](docs/plans/02-web.md) | Routes for management, portal and platform, data layer, permissions in the UI, feature areas per release, design system, i18n, testing |
| [Mobile](docs/plans/03-mobile.md) | The Flutter app (own repository, ships with core 0.4): discovery, auth, features, push via relay, store release |
| [Packs](docs/plans/04-packs.md) | Pack SDK (0.4), extension points, authoring, the Turkish pack |
| [Infrastructure](docs/plans/05-infra.md) | Compose/systemd/Helm targets, backups with secrets, upgrades, push relay, on-site access agent, observability, release engineering |
| [Review](docs/plans/06-review.md) | The critical review that drove v0.2 of the plans (applied; kept for the reasoning) |

## Decisions

The blocking decisions (deployment model, data ownership, license, first-release scope, phone-first sign-in, gate control) were made on 2026-11-06 and are recorded in the [overview, §2.2](docs/plans/00-overview.md). Feature areas are being decided one at a time in plain language under [docs/decisions/](docs/decisions/) — [Money](docs/decisions/money.md), [Setup & people](docs/decisions/setup-people.md), [Daily operations](docs/decisions/daily-operations.md), [Governance](docs/decisions/governance.md), [Community](docs/decisions/community.md) — all five areas are decided. A decision file wins over conflicting text in the plans until the plans are revised. Licensing is per directory — see [LICENSING.md](LICENSING.md); the pack-linking exception is a draft pending legal review.

## Contributing

Not open for contributions yet. Watch this repository for the M0 milestone.
