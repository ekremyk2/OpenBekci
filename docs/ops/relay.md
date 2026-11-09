# Push relay operations

The push relay holds the FCM/APNs credentials of the store app and forwards content-free notifications for enrolled servers. This page covers both sides: a server that uses a relay, and the person who runs one. The relay's own service lives in the `openbekci-relay` repository.

## Server side

- `OPENBEKCI_PUSH_RELAYS` is a comma separated list of `relay_id=url;server_id;secret_file[;app_id]`. Relays other than `log` must be https. The secret file is mode 600 (in Kubernetes: a Secret key mounted as a file, via `extraEnv` and an extra volume).
- Enrol once: `openbekci admin push enrol --help`; it writes the secret file and prints the entry to add. Enrolment fetches `/.well-known/openbekci.json` from your public `BASE_URL`, so the server must be reachable from the relay at that moment. LAN-only sites use the manual path with an invite code from the relay operator.
- Check: the discovery document (`/.well-known/openbekci.json`) lists the enrolled ids under `push.relay_ids`.
- Metrics: `openbekci_push_results_total{outcome}`. `invalid_token` is normal (stale devices are removed); a rising `error`, `no_route` or `rejected` share means the relay is unreachable, disabled for your server (a 403 `server_disabled` makes jobs retry instead of dropping pushes) or misconfigured. The alert `OpenBekciPushErrors` watches it.
- When the relay is down the app falls back to periodic fetch of the unread endpoint; nothing is lost, delivery is just late. UnifiedPush endpoints bypass the relay.

## Running a relay

- Two small replicas behind a load balancer, managed PostgreSQL (enrolments, nonces, quotas), FCM service account and APNs `.p8` key from a secrets manager.
- `RELAY_ENROLLMENT=invite` (default): issue invite codes with `relay admin invites create`.
- Kill switch per server: `relay admin servers disable <server_id>` (and `enable`, `list`). Use it for abuse or a compromised server secret; the affected server's jobs then retry until it is enabled.
- Rotate a server's secret by re-enrolling it with a new invite; update the secret file and restart the server.
- Alert on error ratio per provider and per server, and on `/healthz`. Request logs are content-free and kept 7 days.
- Back up PostgreSQL like any database ([backup.md](backup.md)); the credentials come from your secrets manager.
