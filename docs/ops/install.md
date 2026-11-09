# Install

Two supported ways: Docker Compose (one host) and the Helm chart (Kubernetes). Both run the same image, `ghcr.io/openbekci/openbekci`.

## Docker Compose

Needs a host with ports 80 and 443 open and a DNS name pointing at it.

```sh
cd deploy/compose
cp .env.example .env          # fill in every REQUIRED value
openssl rand -base64 48       # once each for SECRET_KEY and ENCRYPTION_KEY (they must differ)
openssl rand -hex 24          # DB_PASSWORD and DB_SUPERUSER_PASSWORD
docker compose up -d
docker compose logs -f openbekci   # "http server listening"
curl -fsS https://$OPENBEKCI_DOMAIN/readyz
```

Compose starts PostgreSQL (the init scripts create the three roles), the server with `OPENBEKCI_AUTO_MIGRATE=true`, and Caddy, which obtains the TLS certificate. Then create the first administrator:

```sh
docker compose exec openbekci openbekci admin create-platform-admin --help
```

Optional: set `OPENBEKCI_METRICS_TOKEN` in `.env` to serve `/metrics` (see [observability](observability.md)); `OTEL_EXPORTER_OTLP_ENDPOINT` turns tracing on.

Back up before you rely on it: [backup.md](backup.md).

## Helm

Prerequisites: Kubernetes 1.27+, an ingress controller (cert-manager for TLS), a PostgreSQL 16+ database.

1. Create the three application roles and the database once (run `deploy/compose/init/01-roles.sql` as a superuser, with real passwords).
2. Create the Secret. Every key is optional to rename through `secrets.keys`:

   ```sh
   kubectl create secret generic openbekci \
     --from-literal=database-url='postgres://openbekci_app:PW@db:5432/openbekci' \
     --from-literal=migrator-database-url='postgres://openbekci_migrator:PW@db:5432/openbekci' \
     --from-literal=platform-database-url='postgres://openbekci_platform:PW@db:5432/openbekci' \
     --from-literal=secret-key="$(openssl rand -base64 48)" \
     --from-literal=encryption-key="$(openssl rand -base64 48)"
   ```

   Keep a copy of `secret-key` and `encryption-key` outside the cluster: losing the encryption key makes encrypted data unrecoverable ([backup.md](backup.md)).
3. Install:

   ```sh
   cd deploy/helm/openbekci && helm dependency build   # fetches the optional PostgreSQL sub-chart
   helm install bekci . -n bekci --create-namespace \
     --set config.BASE_URL=https://bekci.example.org \
     --set secrets.existingSecret=openbekci \
     --set ingress.enabled=true --set ingress.host=bekci.example.org \
     --set ingress.className=nginx \
     --set ingress.annotations."cert-manager\.io/cluster-issuer"=letsencrypt
   ```

   The pre-install hook Job runs `openbekci migrate up` first; the server and worker pods become ready when `/readyz` reports current migrations.

Evaluation without an external database: `--set postgresql.enabled=true` installs the bitnami sub-chart and runs the migration Job after it (post-install). You still create the roles and put matching URLs in the Secret. Never use this for real data.

More than one server replica needs shared file storage: set `config.STORAGE_DRIVER=s3` and the `STORAGE_S3_*` settings through `extraEnv`/`extraEnvFrom`. With the default local storage use one replica and `persistence.enabled=true`.

All values are documented in `deploy/helm/openbekci/README.md`.
