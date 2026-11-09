# Restore and restore drill

Run this on a scratch host at least once a quarter, and for real after a loss. The drill proves that the secrets in the backup decrypt the data.

## Steps (Compose)

1. **Secrets.** Put the backed-up `.env` into `deploy/compose/.env`. The keys must be the ones that were in use when the dump was taken.
2. **Database.** Start only PostgreSQL, then load the dump:

   ```sh
   docker compose up -d postgres
   docker compose exec -T postgres createdb -U postgres openbekci   # skipped if the init script made it
   docker compose exec -T postgres pg_restore -U postgres -d openbekci --clean --if-exists --no-owner --role=openbekci_migrator < db.dump
   ```

   The init scripts already created the three roles; `--no-owner --role` keeps ownership right.
3. **Files.**

   ```sh
   docker run --rm -v compose_data:/data -v "$PWD":/in alpine sh -c 'cd /data && tar xzf /in/files.tgz && chown -R 65532:65532 /data'
   ```
4. **Migrations.** `docker compose run --rm openbekci openbekci migrate status`. If the dump is older than the image, `migrate up` brings it forward; if it is newer, use the image version that took the dump.
5. **Start.** `docker compose up -d`, then `curl -fsS https://$DOMAIN/readyz`.
6. **Smoke test (the drill's pass condition).** Sign in as an administrator, open a person with a national id (shown masked: that read decrypts the field), open a recent document (a file blob), and look at the newest ledger entry. If the national id cannot be read, the `ENCRYPTION_KEY` is wrong: stop, find the right key, do not "fix" data.

Dangling file rows (uploaded after the dump but before the file copy) answer `not_found`; they are not an error.

## Steps (Kubernetes)

Restore the database into a fresh PostgreSQL, recreate the Secret from the backup, restore files (PVC snapshot or bucket version), then `helm install` (or `helm upgrade`) with the same values. The migration hook Job checks the schema.

## Record the drill

Write down the date, the backup restored, the time it took, and anything that surprised you. The time it took is your real recovery time.
