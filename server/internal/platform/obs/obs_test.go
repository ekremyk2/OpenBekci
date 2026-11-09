package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetricsRegisteredOnce(t *testing.T) {
	Notification("email", "sent")
	Push("ok")
	Webhook("failed")
	Checkout("paid", 1)
	Job("k", time.Millisecond, nil)
	mfs, err := reg.Gather() // duplicate or inconsistent descriptors fail here
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, mf := range mfs {
		if seen[mf.GetName()] {
			t.Errorf("duplicate metric %s", mf.GetName())
		}
		seen[mf.GetName()] = true
	}
	for _, n := range []string{"openbekci_notification_deliveries_total", "openbekci_push_results_total", "openbekci_webhook_deliveries_total", "openbekci_checkout_transitions_total", "openbekci_job_duration_seconds"} {
		if !seen[n] {
			t.Errorf("%s missing", n)
		}
	}
}

func TestHandlerToken(t *testing.T) {
	h := Handler("s3cret")
	for auth, want := range map[string]int{"": 401, "Bearer nope": 401, "Bearer s3cret": 200} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("auth %q: %d, want %d", auth, rec.Code, want)
		}
	}
}

func TestTracingOffIsNoop(t *testing.T) {
	shutdown, err := SetupTracing(context.Background(), func(string) string { return "" }, "t")
	if err != nil || Enabled() {
		t.Fatalf("err=%v enabled=%v", err, Enabled())
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, sp := Start(context.Background(), "x")
	defer sp.End()
	if sp.SpanContext().IsValid() || sp.IsRecording() {
		t.Fatal("span should be a no-op")
	}
}
