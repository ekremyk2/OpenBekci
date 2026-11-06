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

## Open decisions

Recorded in the [overview, §2.2](docs/plans/00-overview.md#22-assumed--confirm-or-change): deployment model, license (assumed AGPL-3.0 core / Apache-2.0 SDK, spec and clients — no `LICENSE` file is committed until confirmed), first country pack, module path.

## Contributing

Not open for contributions yet. Watch this repository for the M0 milestone.
