# Upgrade

Order is always: **backup, migrate, roll**. Read the release notes first; pre-1.0 minors may change configuration, and the server refuses to start on removed settings with a message saying which.

Migrations are expand/contract, so version N keeps working against the schema of N+1 while a rolling upgrade overlaps.

## Compose

```sh
cd deploy/compose
/usr/local/bin/openbekci-backup.sh        # see backup.md
docker compose pull
docker compose up -d                       # the server migrates on start (AUTO_MIGRATE=true)
docker compose logs --tail=50 openbekci
curl -fsS https://$OPENBEKCI_DOMAIN/readyz
```

To migrate explicitly instead, set `OPENBEKCI_AUTO_MIGRATE=false` and run `docker compose run --rm openbekci openbekci migrate up` before `up -d`.

## Helm

```sh
# 1. backup (database snapshot, files, secrets)
# 2. upgrade: the pre-upgrade hook Job runs `migrate up` and must succeed before pods roll
helm upgrade bekci deploy/helm/openbekci -n bekci --reuse-values --set image.tag=0.5.0
kubectl -n bekci rollout status deploy/bekci-server deploy/bekci-worker
kubectl -n bekci logs job/bekci-migrate-<revision>   # if the hook failed
```

If the migration Job fails the release stays on the old version; fix the cause and run the upgrade again. The hook Job is removed after it succeeds.

Skipping versions is supported across minors within a major; to cross a major, upgrade through the last minor of the previous major.

## Rollback

- Before the migration ran, or when the release notes say the migrations are reversible: previous image (`helm rollback`, or the old tag in Compose) plus `openbekci migrate down` once per migration to undo.
- Otherwise: restore the pre-upgrade backup ([restore.md](restore.md)). That is why step 1 is not optional.

## After

Check the Grafana dashboard for error ratio and job failures for an hour (see [observability](observability.md)).
