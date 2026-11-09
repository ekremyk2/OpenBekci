# Push relay

Mobile push needs Firebase (FCM) and Apple (APNs) credentials that belong to whoever publishes the store app, and a self-hosted server cannot hold them. The **push relay** holds them and forwards **content-free** notifications on behalf of every enrolled server. Source and full reference: the `openbekci-relay` repository.

## What the relay sees

Device tokens, `org_id`, `instance_id`, notification ids and notification types. **Never** titles, bodies, names, phone numbers, e-mail addresses or payment details: the server sends only `instance_id`, `org_id`, `notification_id`, `type`, `collapse_key` and `badge`, the relay rejects any other key, and the app fetches the details from its own server after the push arrives. The relay stores no device registry (tokens are forwarded, never kept) and its logs are content-free.

Written down plainly: because the relay holds the store credentials it can push to any device registered through any enrolled server. A compromised relay could send spam or correlate activity per organization; it could not read notifications.

If the relay is unreachable, disabled, or your server is not enrolled, the app falls back to periodic fetch. iOS has no other path; Android can also use UnifiedPush (a plain HTTPS endpoint the server posts to directly, bypassing the relay).

## Use the official relay

The official relay serves the store builds of the app. Enrollment is by invite code from the operator.

1. Ask for an invite (see the relay policy page).
2. On your server (it must be running and reachable over HTTPS by the relay):

    ```sh
    openbekci admin push enrol --relay https://push.openbekci.org --app-id org.openbekci.app --invite CODE
    ```

3. The command publishes a short-lived challenge in your discovery document, the relay verifies it, and the command writes the secret file (mode 600) and prints the entry to add to `OPENBEKCI_PUSH_RELAYS`, in the form `relay-id=url;server_id;secret_file;app_id`. Restart the server with it.

A **LAN-only** server cannot be reached by the relay. Use `--manual` with an invite the operator marked manual (the server gets a lower default quota), or run a relay of your own inside the VPN.

## Run your own relay

For a white-label app. One static binary or the Docker image, SQLite for enrollments, nonces and quotas, a TLS terminator in front.

```sh
cp deploy/relay.example.env deploy/relay.env     # edit
mkdir deploy/secrets                             # fcm-service-account.json, AuthKey_XXXX.p8
docker compose -f deploy/compose.yaml up -d
```

Configuration is `RELAY_*` environment variables (every secret also has a `_FILE` form). Key settings: `RELAY_ENROLLMENT` (`invite` by default; `open` or `closed`), `RELAY_APP_n_ID` with the FCM and APNs credentials per app, `RELAY_TRUSTED_PROXIES` (your proxy's CIDR), `RELAY_METRICS_TOKEN`.

Issue an invite and manage servers:

```sh
openbekci-relay admin invites create --note "Sunset Residences" --ttl 168h
openbekci-relay admin servers list
openbekci-relay admin servers disable srv_...    # kill switch, effective on the next request
```

Operating notes: run a single replica per SQLite file; back the file up like a credential store (losing it means servers re-enroll); monitor `/healthz` and the Prometheus `/metrics` (alert on provider and per-server error ratio); rotate the Firebase or APNs key by replacing the secret and restarting.
