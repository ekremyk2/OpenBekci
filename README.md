# OpenBekci

Open-source residential community management: apartment buildings, gated sites, housing estates and mixed-use complexes. One system for residents, owners, boards, managers and staff — dues and a real ledger, tickets, announcements, documents, assemblies and voting, bookings, visitors.

**Status:** planning. No code yet; the plans below are the current state of the project.

## Shape

| Layer | Choice |
|---|---|
| Backend | Go, modular monolith, one binary with the web app embedded, PostgreSQL as the only required dependency |
| Web | React + Vite + TanStack Router/Query, shadcn/ui, Lingui (RTL from day one) |
| Mobile | Flutter, connects to any instance (hosted or self-hosted), push via a relay |
| API | OpenAPI 3.1, contract-first; Go server and TypeScript/Dart clients are generated |
| Country packs | Compile-time Go modules behind a public SDK; the core contains no country logic. First pack: Turkey |

## Plans

| Document | Covers |
|---|---|
| [Overview](docs/plans/00-overview.md) | Decisions, product scope, architecture, repository layout, shared contracts, milestones M0–M7 |
| [Backend](docs/plans/01-backend.md) | Stack, modules, domain model, multi-tenancy (RLS), auth, API, ledger rules, pack SDK, jobs, security, testing, milestones B0–B7 |
| [Web](docs/plans/02-web.md) | Routes, data layer, permissions in the UI, feature areas, design system, i18n, schema-driven pack forms, milestones W0–W6 |
| [Mobile](docs/plans/03-mobile.md) | Instance discovery, accounts, auth, features by release, offline, push, release/white-label, milestones MB0–MB5 |
| [Packs](docs/plans/04-packs.md) | Extension points, SDK lifecycle, authoring, governance, the Turkish pack, milestones P0–P4 |
| [Infrastructure](docs/plans/05-infra.md) | Deployment targets, push relay, backups, upgrades, observability, release engineering, milestones I0–I5 |
| [Review](docs/plans/06-review.md) | Critical review of plans 00–05: blocking decisions, money-model gaps, mechanisms that don't work as written, sequencing bugs, a re-baselined 0.1 scope and an action plan. **Read this before the plans; they have not yet been revised against it.** |

## Decisions

The blocking decisions (deployment model, data ownership, license, first-release scope, phone-first sign-in, gate control) were made on 2026-11-06 and are recorded in the [overview, §2.2](docs/plans/00-overview.md). Feature areas are being decided one at a time in plain language under [docs/decisions/](docs/decisions/) — [Money](docs/decisions/money.md), [Setup & people](docs/decisions/setup-people.md), [Daily operations](docs/decisions/daily-operations.md), [Governance](docs/decisions/governance.md), [Community](docs/decisions/community.md) — all five areas are decided. A decision file wins over conflicting text in the plans until the plans are revised. Licensing is per directory — see [LICENSING.md](LICENSING.md); the pack-linking exception is a draft pending legal review.

## Contributing

Not open for contributions yet. Watch this repository for the M0 milestone.
