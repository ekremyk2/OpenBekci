# Backup

A backup is three things, taken in this order: the **database**, the **files**, the **secrets**.

| Part | What | Why |
|---|---|---|
| Database | `pg_dump -Fc` of `openbekci` | all business data |
| Files | the `data` volume (`DATA_DIR/files`) or the S3 bucket | uploaded documents and generated PDFs |
| Secrets | `OPENBEKCI_SECRET_KEY`, `OPENBEKCI_ENCRYPTION_KEY`, the `.env` file or the Kubernetes Secret, push relay secret files | without `ENCRYPTION_KEY` national ids, device tokens and provider credentials cannot be decrypted |

Dump first, then files: a file uploaded in between yields a file row without a blob after restore; the server tolerates that (the download answers `not_found`).

**The backup contains the keys. Encrypt every off-site copy** (for example `rclone crypt`, `age`, or an encrypted bucket) and keep a second copy of the two keys somewhere that is not the same host.

## Compose: nightly example

`/usr/local/bin/openbekci-backup.sh` (run by cron or a systemd timer at 03:00):

```sh
#!/bin/sh
set -eu
cd /opt/openbekci/deploy/compose
out=/var/backups/openbekci/$(date +%F)
umask 077
mkdir -p "$out"
docker compose exec -T postgres pg_dump -U postgres -Fc openbekci > "$out/db.dump"
docker run --rm -v compose_data:/data:ro -v "$out":/out alpine tar czf /out/files.tgz -C /data .
cp .env "$out/env"
# keep 14 days locally; ship an encrypted copy off-site
find /var/backups/openbekci -mindepth 1 -maxdepth 1 -mtime +14 -exec rm -rf {} +
age -r "$AGE_RECIPIENT" -o "$out.tar.age" -- "$out" 2>/dev/null || tar cf - -C "$out" . | age -r "$AGE_RECIPIENT" > "$out.tar.age"
rclone copy "$out.tar.age" offsite:openbekci/
```

The volume name is `<compose project>_data` (`docker volume ls`). Cron line: `0 3 * * * /usr/local/bin/openbekci-backup.sh`.

## Kubernetes

- Database: managed PostgreSQL snapshots/PITR, or a CronJob running `pg_dump -Fc "$URL" | rclone rcat ...` with the superuser or a read-only role.
- Files: S3 versioning, or snapshot the PVC (`persistence.enabled`).
- Secrets: `kubectl get secret openbekci -o yaml` into your secrets manager or an encrypted repository (sealed-secrets, SOPS). Never into plain git.

## Check that it works

A "last backup" marker for the platform dashboard (`openbekci admin backup-marker`) is planned but does not exist yet; until it does, alert on the age of the newest object off-site (for example a Prometheus textfile or your storage provider's monitor) and do the [restore drill](restore.md) every quarter. A backup that was never restored is not a backup.
