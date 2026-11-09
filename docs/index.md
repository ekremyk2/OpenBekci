# OpenBekci

OpenBekci is open-source management software for residential communities: apartment buildings, gated sites, housing estates and mixed-use complexes. One system for residents, owners, boards, managers and staff: dues and a real ledger, tickets, announcements, documents, assemblies and voting, bookings, visitors and gate control.

## How it fits together

| Piece | What it is |
|---|---|
| **Server** | One Go binary: API, web app and job worker. PostgreSQL is the only required dependency. |
| **Web app** | For managers, boards, guards and residents. Served by the server. |
| **Mobile app** | The resident app (Flutter), in its own repository. Connects to any instance. See [Mobile](mobile.md). |
| **Push relay** | A small service that holds the store push credentials so self-hosted servers can send push. See [Push relay](operate/relay.md). |
| **On-site agent** | A small program on the site LAN that opens gates and doors, and keeps deciding when the internet is down. See [On-site agent](operate/agent.md). |
| **Packs** | Country modules compiled into the server. The core has no country logic; the first pack is Turkey. See [Packs](packs/authoring.md). |

## Where to start

- **Running it yourself:** [Install with Docker Compose](install/compose.md), then [backup, restore and upgrade](operate/backup-restore-upgrade.md).
- **Managing a site:** the [Use](use/setup-people.md) pages explain each area in plain language.
- **Connecting your own tools:** [API keys and webhooks](integrations.md) and the [API reference](integrate/reference.html).
- **Adding a country:** the [pack authoring guide](packs/authoring.md).
- **Contributing:** [Contributing, licensing and legal](contributing.md).

## Status

OpenBekci is pre-1.0. Minor versions may contain breaking configuration changes; the release notes list them under a fixed heading. The server refuses to start on unknown or removed settings with a clear message.
