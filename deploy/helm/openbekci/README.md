# openbekci Helm chart

Server (`serve --no-worker`) and worker (`worker`) Deployments, a migration hook Job, Service, optional Ingress, HPA, PodDisruptionBudgets and ServiceMonitor. PostgreSQL is external; a bitnami sub-chart exists for evaluation only. Install steps: [`docs/ops/install.md`](../../../docs/ops/install.md).

```sh
helm dependency build        # once; the sub-chart is only used when postgresql.enabled=true
helm lint . --set config.BASE_URL=https://x --set secrets.existingSecret=s
```

## Values

| Key | Default | Meaning |
|---|---|---|
| `image.repository`, `image.tag`, `image.pullPolicy` | ghcr.io/openbekci/openbekci, chart `appVersion`, IfNotPresent | image |
| `imagePullSecrets` | `[]` | |
| `config` | `BASE_URL` (required), `TRUSTED_PROXIES`, `DATA_DIR` | each key becomes `OPENBEKCI_<KEY>` in a ConfigMap shared by server and worker |
| `otel` | `{}` | raw `OTEL_*` variables; tracing is off until `OTEL_EXPORTER_OTLP_ENDPOINT` is set |
| `secrets.existingSecret` | required | Secret holding every secret value |
| `secrets.keys` | `database-url`, `migrator-database-url`, `platform-database-url`, `secret-key`, `encryption-key`, `metrics-token` | key name in that Secret per setting; mounted under `/secrets` and read through `OPENBEKCI_<NAME>_FILE` |
| `metrics.enabled` | `false` | read `METRICS_TOKEN` and serve `/metrics` on the server |
| `extraEnv`, `extraEnvFrom` | `[]` | more environment (SMTP, SMS, S3, `PUSH_RELAYS`, ...) for all workloads |
| `postgresql.enabled` | `false` | bitnami PostgreSQL sub-chart, evaluation only; migration then runs post-install |
| `migrate.enabled` | `true` | pre-install/pre-upgrade hook Job running `migrate up` with the migrator URL only |
| `migrate.backoffLimit`, `activeDeadlineSeconds`, `resources` | 1, 900, `{}` | |
| `persistence.enabled`, `size`, `storageClass`, `existingClaim`, `accessMode` | false, 10Gi, "", "", ReadWriteOnce | local file storage; an emptyDir otherwise. Use S3 for more than one server replica |
| `server.replicas` | 2 | ignored when autoscaling is on |
| `server.resources` | 100m / 192Mi requests, 512Mi limit | |
| `server.autoscaling.*` | disabled; min 2, max 6, CPU 70 % | HPA |
| `server.pdb.enabled`, `minAvailable` | true, 1 | |
| `server.terminationGracePeriodSeconds` | 30 | the server drains within 20 s |
| `worker.replicas` | 1 | River elects a leader for periodic jobs |
| `worker.metricsPort` | 9090 | worker `/healthz` and `/metrics` |
| `worker.resources`, `worker.pdb.*` | 50m / 128Mi requests, 384Mi limit; PDB off | |
| `server.*` / `worker.*` | | also `nodeSelector`, `tolerations`, `affinity`, `podAnnotations` |
| `service.type`, `service.port` | ClusterIP, 80 | |
| `ingress.enabled`, `className`, `annotations`, `host` | false, "", {}, "" | |
| `ingress.tls.enabled`, `ingress.tls.secretName` | true, `openbekci-tls` | issue the certificate with cert-manager through `annotations` |
| `serviceMonitor.enabled`, `interval`, `labels` | false, 30s, {} | needs the Prometheus operator and `metrics.enabled` |
| `podSecurityContext`, `containerSecurityContext` | non-root 65532, read-only root, no capabilities | |

Probes: readiness `/readyz`, liveness and startup `/healthz` on the server; `/healthz` on the worker.

The server needs `OPENBEKCI_TRUSTED_PROXIES` to cover the ingress controller's pod network, or client addresses and rate limits will be wrong.
