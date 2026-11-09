# Observability

Logs, metrics and traces are all opt-in or local; nothing is sent anywhere unless you configure it.

## Logs

JSON on stdout. Records carry `request_id` and, when tracing is on, `trace_id` and `span_id`, so a slow request in Grafana links to its log lines (search Loki for the trace id). Emails and phone numbers are never logged.

## Metrics

- Server: `GET /metrics` with `Authorization: Bearer <OPENBEKCI_METRICS_TOKEN>`. Not served at all without a token. In Helm set `metrics.enabled=true` (the token comes from the Secret key `metrics-token`) and `serviceMonitor.enabled=true` if you use the Prometheus operator.
- Worker: `OPENBEKCI_METRICS_ADDR=:9090` serves `/healthz` and `/metrics` (same token) on a cluster-internal port; the Helm chart sets it. Job metrics exist only in the process that runs the jobs, so scrape the worker.

| Metric | Labels |
|---|---|
| `openbekci_http_requests_total`, `openbekci_http_request_duration_seconds` | method, route (pattern), status |
| `openbekci_job_duration_seconds` (count = runs, `outcome="error"` = failures) | kind, outcome |
| `openbekci_notification_deliveries_total` | channel, status |
| `openbekci_push_results_total` | outcome |
| `openbekci_webhook_deliveries_total` | result (succeeded, retry, failed) |
| `openbekci_checkout_transitions_total` | status |
| `openbekci_db_pool_{acquired,idle,total,max}_conns`, `_acquires_total`, `_empty_acquires_total`, `_acquire_seconds_total` | pool |

Plus the Go runtime and process collectors. Labels never carry organization or person ids.

Dashboard: import `deploy/observability/grafana-dashboard.json`. Alerts: load `deploy/observability/alert-rules.yaml` into Prometheus (or wrap it in a `PrometheusRule`). Both are examples; tune thresholds to your traffic.

## Traces

Off by default. Set the standard OpenTelemetry variables on the server and worker:

```
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318     # OTLP over HTTP; setting it turns tracing on
OTEL_SERVICE_NAME=openbekci                                 # optional
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.1
OTEL_SDK_DISABLED=true                                      # switches it off again
```

Spans: one per inbound HTTP request (named `METHOD route`), one per database transaction (`db.tx`), one per River job (`job <kind>`), webhook deliveries, and outbound HTTP calls to push relays, SMS gateways and webhook receivers. Outbound calls never send trace headers to third parties.

Not yet covered: the payment provider calls inside packs and SMTP sending have no spans of their own.
