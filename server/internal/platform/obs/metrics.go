// Package obs holds the process's observability: Prometheus metrics and
// OpenTelemetry tracing (infra plan, section 8). Metrics live in package
// variables on a private registry so any package can count without wiring;
// tracing is a no-op until an OTLP endpoint is configured.
package obs

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var reg = prometheus.NewRegistry()

var (
	httpRequests = promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Name: "openbekci_http_requests_total", Help: "HTTP requests by method, route pattern and status.",
	}, []string{"method", "route", "status"})
	httpDuration = promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
		Name: "openbekci_http_request_duration_seconds", Help: "HTTP request latency by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	jobDuration = promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
		Name: "openbekci_job_duration_seconds", Help: "River job run time by kind and outcome (ok, error).",
		Buckets: []float64{.01, .05, .1, .5, 1, 5, 15, 60, 300},
	}, []string{"kind", "outcome"})
	notifications = promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Name: "openbekci_notification_deliveries_total", Help: "Notification delivery outcomes by channel and status (sent, failed, skipped).",
	}, []string{"channel", "status"})
	pushResults = promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Name: "openbekci_push_results_total", Help: "Push message outcomes (ok, invalid_token, rate_limited, error, no_route, rejected).",
	}, []string{"outcome"})
	webhookResults = promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Name: "openbekci_webhook_deliveries_total", Help: "Outbound webhook attempts by result (succeeded, retry, failed).",
	}, []string{"result"})
	checkoutStates = promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Name: "openbekci_checkout_transitions_total", Help: "Online checkouts reaching a state (paid, failed, expired, refunded).",
	}, []string{"status"})
)

func init() {
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), newPoolCollector())
}

// Notification counts one notification delivery outcome.
func Notification(channel, status string) { notifications.WithLabelValues(channel, status).Inc() }

// Push counts one push message outcome.
func Push(outcome string) { pushResults.WithLabelValues(outcome).Inc() }

// Webhook counts one outbound webhook attempt.
func Webhook(result string) { webhookResults.WithLabelValues(result).Inc() }

// Checkout counts n checkouts reaching status.
func Checkout(status string, n int64) { checkoutStates.WithLabelValues(status).Add(float64(n)) }

// Job records one worked River job.
func Job(kind string, d time.Duration, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	jobDuration.WithLabelValues(kind, outcome).Observe(d.Seconds())
}

// HTTP records every request by its chi route pattern (never the raw path:
// ids would explode the label set) and names the request's span after it.
func HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		httpRequests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
		httpDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		if sp := trace.SpanFromContext(r.Context()); sp.IsRecording() {
			sp.SetName(r.Method + " " + route)
			sp.SetAttributes(attribute.String("http.route", route))
		}
	})
}

// Handler serves /metrics; a non-empty token is required as a bearer token (constant-time compare).
func Handler(token string) http.Handler {
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != "" && subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// pools is what the DB pool collector reports; the app swaps it in once the pools are open.
var pools atomic.Pointer[map[string]*pgxpool.Pool]

// SetPools tells the DB pool metrics which pools to read (name -> pool).
func SetPools(p map[string]*pgxpool.Pool) { pools.Store(&p) }

type poolCollector struct {
	gauges                  map[string]*prometheus.Desc
	acquires, waits, waitDu *prometheus.Desc
}

func newPoolCollector() *poolCollector {
	l := []string{"pool"}
	c := &poolCollector{gauges: map[string]*prometheus.Desc{}}
	for _, k := range []string{"acquired", "idle", "total", "max"} {
		c.gauges[k] = prometheus.NewDesc("openbekci_db_pool_"+k+"_conns", "Connections "+k+" in the pool.", l, nil)
	}
	c.acquires = prometheus.NewDesc("openbekci_db_pool_acquires_total", "Connection acquires.", l, nil)
	c.waits = prometheus.NewDesc("openbekci_db_pool_empty_acquires_total", "Acquires that had to wait for a connection.", l, nil)
	c.waitDu = prometheus.NewDesc("openbekci_db_pool_acquire_seconds_total", "Total time spent acquiring connections.", l, nil)
	return c
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range c.gauges {
		ch <- d
	}
	ch <- c.acquires
	ch <- c.waits
	ch <- c.waitDu
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	m := pools.Load()
	if m == nil {
		return
	}
	for name, p := range *m {
		if p == nil {
			continue
		}
		s := p.Stat()
		for k, v := range map[string]float64{"acquired": float64(s.AcquiredConns()), "idle": float64(s.IdleConns()), "total": float64(s.TotalConns()), "max": float64(s.MaxConns())} {
			ch <- prometheus.MustNewConstMetric(c.gauges[k], prometheus.GaugeValue, v, name)
		}
		ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(s.AcquireCount()), name)
		ch <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, float64(s.EmptyAcquireCount()), name)
		ch <- prometheus.MustNewConstMetric(c.waitDu, prometheus.CounterValue, s.AcquireDuration().Seconds(), name)
	}
}
