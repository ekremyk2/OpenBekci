package obs

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var enabled atomic.Bool

// SetupTracing turns tracing on when OTEL_EXPORTER_OTLP_ENDPOINT (or the
// _TRACES_ variant) is set and OTEL_SDK_DISABLED is not true. Everything else
// (headers, OTEL_SERVICE_NAME, sampler) is the SDK's own environment handling.
// Disabled, it installs nothing and the returned shutdown does nothing, so a
// span costs one no-op call.
func SetupTracing(ctx context.Context, getenv func(string) string, version string) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if strings.EqualFold(getenv("OTEL_SDK_DISABLED"), "true") ||
		(getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "") {
		return noop, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "openbekci"), attribute.String("service.version", version)),
		resource.WithFromEnv())
	if err != nil {
		return noop, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	enabled.Store(true)
	return func(ctx context.Context) error { enabled.Store(false); return tp.Shutdown(ctx) }, nil
}

// Enabled reports whether SetupTracing installed a provider.
func Enabled() bool { return enabled.Load() }

// Start opens a span; close it with End.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("openbekci").Start(ctx, name, trace.WithAttributes(attrs...))
}

// End ends sp, marking it failed when err is not nil.
func End(sp trace.Span, err error) {
	if err != nil {
		sp.RecordError(err)
		sp.SetStatus(codes.Error, err.Error())
	}
	sp.End()
}

// WrapHTTP traces inbound requests when tracing is on; otherwise it returns h.
func WrapHTTP(h http.Handler) http.Handler {
	if !Enabled() {
		return h
	}
	return otelhttp.NewHandler(h, "http", otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }))
}

// Transport traces outbound calls (webhooks, push relays, SMS). It never
// injects trace headers: the receivers are third parties.
func Transport(rt http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(rt,
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return "HTTP " + r.Method }))
}
