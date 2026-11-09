# Install with Helm

!!! note
    The Helm chart is a 1.0 deliverable and is **not published yet**. This page describes what it will contain so you can plan; until then use [Docker Compose](compose.md) or the bare binary.

The chart (`oci://ghcr.io/openbekci/charts/openbekci`, source in `deploy/helm/openbekci/`) is for sites that already run Kubernetes and want several API replicas:

- **api**: `openbekci serve --no-worker`, with a horizontal pod autoscaler.
- **worker**: `openbekci worker`; periodic jobs use leader election, so more than one replica is safe.
- **migration job**: runs `migrate up` before each upgrade.
- **external PostgreSQL and S3-compatible storage are expected.** An in-cluster PostgreSQL sub-chart is for evaluation only.
- Ingress with cert-manager, a PodDisruptionBudget, a NetworkPolicy and a ServiceMonitor; readiness from `/readyz`, liveness from `/healthz`, graceful shutdown within 30 seconds.
- All configuration through a `Secret` and a `ConfigMap`, mapped to the same `OPENBEKCI_*` variables as every other install. The chart's `appVersion` pins a server version.

The same operating rules apply as on compose: back up the database, the file store **and the secrets**, and read the [upgrade notes](../operate/backup-restore-upgrade.md) before changing versions. With a transaction-mode connection pooler (PgBouncer 1.21 or newer) the server detects the pooler and configures itself for it, and logs what it chose.
