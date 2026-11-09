# Backup, restore, upgrade

## What is state

Three things, and **every backup must contain all three**:

1. the **PostgreSQL database**;
2. the **file store**: `DATA_DIR/files`, or the S3 bucket;
3. the **secrets**: `OPENBEKCI_SECRET_KEY`, `OPENBEKCI_ENCRYPTION_KEY`, your `.env` or secret files, and relay secret files.

Losing `OPENBEKCI_ENCRYPTION_KEY` makes national ids, device tokens and provider credentials unrecoverable. Because backups contain the keys, **off-site copies must be encrypted** (for example `rclone crypt`).

## Backup

Order: dump the database first, then copy the files, then the secrets. A file uploaded between the dump and the file copy leaves a file record without a blob after a restore; the server tolerates that (the download answers `not_found`).

```sh
pg_dump -Fc "$OPENBEKCI_MIGRATOR_DATABASE_URL" > openbekci-$(date +%F).dump
tar czf openbekci-files-$(date +%F).tgz -C /path/to/data files        # skip with S3 storage
cp .env openbekci-env-$(date +%F)                                       # the secrets
```

Run it from cron or a systemd timer, keep several generations (for example 7 daily, 4 weekly, 6 monthly) and copy them off the machine, encrypted. The shipped compose file does not yet include a backup container, so schedule this yourself.

Moving one site rather than the whole instance: `openbekci admin export-site` and `import-site` move a site between a hosted and a self-hosted instance in both directions. API keys, webhook endpoints and the delivery log belong to the organization and are not part of a site export.

Application-level retention (erasure requests) does not purge historic backups; they age out.

## Restore

1. Put the secrets back (`.env`) **first**: the same encryption key is required.
2. Create an empty database with the three roles (the compose init script does this) and restore the dump: `pg_restore --clean --if-exists -d <database> openbekci.dump`.
3. Unpack the files into `DATA_DIR`.
4. `openbekci migrate status`, then start the server.
5. Smoke test: sign in, open a unit's statement, open an uploaded document, and check that a masked national id displays (that proves the encryption key is right).

A backup that has never been restored is not a backup: do step 1 to 5 on a spare machine before you need it.

## Upgrade

- Versions are semantic. `latest` moves only on stable releases. Before 1.0, a minor version may change configuration; read the release notes' breaking-changes heading. The server refuses to start on unknown or removed settings with a clear message.
- **Take a backup first.**
- Compose: `docker compose pull && docker compose up -d`. The server migrates the database on start (`OPENBEKCI_AUTO_MIGRATE=true` in the compose default), then serves.
- Bare binary: replace the binary, `openbekci migrate up`, restart.
- Skipping versions is supported across minors within a major. A major version must go through the last minor of the previous major; `migrate` says so if you try otherwise.
- **Rollback**: the previous image plus `openbekci migrate down` works only for migrations flagged reversible; otherwise restore the pre-upgrade backup.
- Packs are compiled into the binary and migrate with it.
- Mobile app compatibility: the server states a minimum app version in its discovery document (`/.well-known/openbekci.json`); the app supports the previous two server minor versions. The release notes state the range.
