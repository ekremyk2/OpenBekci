# Install with Docker Compose

The reference self-host: OpenBekci, PostgreSQL 17 and Caddy (automatic HTTPS) on one machine. A 2 vCPU / 2 GB VPS serves a 500-unit organization comfortably.

You need a public name that resolves to the machine and ports 80 and 443 reachable (Caddy obtains the certificate). Sites that cannot have that: see [No public certificate](#no-public-certificate).

## Install

From the repository, in `deploy/compose/`:

```sh
cp .env.example .env
```

Fill in every value marked REQUIRED in `.env`:

| Variable | What |
|---|---|
| `OPENBEKCI_DOMAIN` | The public host name Caddy serves. |
| `OPENBEKCI_BASE_URL` | The public URL, `https://...`. Used for cookies, links and origin checks. |
| `OPENBEKCI_EXTRA_ORIGINS` | Optional. Another accepted origin, for example a LAN name next to the public domain. |
| `OPENBEKCI_SECRET_KEY` | Signing and session key. Generate with `openssl rand -base64 48`. |
| `OPENBEKCI_ENCRYPTION_KEY` | Encrypts national ids, device tokens and provider credentials. Generate the same way. **Back it up with the database; if it is lost, that data cannot be recovered.** |
| `OPENBEKCI_DB_PASSWORD` | Password of the three application database roles. Letters, digits and `. _ ~ -` only (it goes into database URLs); `openssl rand -hex 24` works. |
| `OPENBEKCI_DB_SUPERUSER_PASSWORD` | PostgreSQL superuser, used only to bootstrap. |

The server refuses to start with placeholder or short keys. Then:

```sh
docker compose up -d
docker compose exec openbekci openbekci admin create-platform-admin --email you@example.org
docker compose exec openbekci openbekci admin create-org --slug my-site --name "My Site" --country TR --admin-email manager@example.org
```

`create-org` prints a one-time sign-in link for the organization's first administrator. `GET /readyz` answers when the server is ready.

What runs: `openbekci` (migrates on start, then serves with the job worker embedded), `postgres` and `caddy`. The compose network has a pinned subnet and the server trusts `X-Forwarded-*` only from `OPENBEKCI_TRUSTED_PROXIES`.

!!! warning
    The shipped compose file does not yet include a backup service. Set up backups before you put real data in: see [Backup, restore, upgrade](../operate/backup-restore-upgrade.md).

## Sign-in codes and mail

Residents sign in by SMS code, e-mail link or password. Set `OPENBEKCI_SMS_PROVIDER` (`netgsm`; a pack such as the Turkish one can add providers per organization) and `OPENBEKCI_MAIL_PROVIDER` or `OPENBEKCI_SMTP_*` to make those work; with none set, requesting a code answers `503 sms_unavailable` / `mail_unavailable`. Sends are budgeted per recipient and per instance so a runaway client cannot run up your SMS bill: `OPENBEKCI_SEND_DAILY_BUDGET` (10 per number per day), `OPENBEKCI_SMS_HOURLY_BUDGET` (120), `OPENBEKCI_MAIL_HOURLY_BUDGET` (300); a negative value removes a limit. Messages over budget are dropped and listed by `openbekci admin delivery-log --failed`.

## No public certificate

The mobile app and the on-site agent require valid HTTPS. For a site behind CGNAT or without public 80/443 use Caddy with a DNS-01 challenge, or a tunnel (Cloudflare Tunnel, Tailscale Serve). For a LAN-only site use a private CA (Caddy's `tls internal` or `step-ca`) and install the root certificate on the guard PC, the agent box and residents' phones. A LAN-only site cannot be reached by the official push relay: see [Push relay](../operate/relay.md).

## Bare binary

The same binary runs under systemd with a distro PostgreSQL and Caddy or nginx in front: `openbekci migrate up`, then `openbekci serve`, configuration through `OPENBEKCI_*` environment variables (or `--config file.yaml`). Secrets also accept `_FILE` variants.
