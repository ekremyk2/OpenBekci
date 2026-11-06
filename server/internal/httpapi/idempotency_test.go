package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
)

const idemKey = "Idempotency-Key"

func newIdemAPI(t *testing.T) *testAPI {
	t.Helper()
	return newTestAPI(t, idempotency.NewPG(dbtest.New(t)))
}

func TestIntegrationIdempotencyReplay(t *testing.T) {
	ta := newIdemAPI(t)
	body := `{"name":"a"}`

	first := ta.doAuth("POST", "/v1/things", body, idemKey, "key-00000001")
	if first.Code != 201 || first.Body.String() != `{"id":1}` {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	if first.Header().Get("Idempotent-Replayed") != "" {
		t.Error("first response marked as replay")
	}

	// Same key and request: stored response, handler not run again.
	again := ta.doAuth("POST", "/v1/things", body, idemKey, "key-00000001")
	if again.Code != 201 || again.Body.String() != `{"id":1}` || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %s %v", again.Code, again.Body, again.Header())
	}
	if n := ta.creates.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
	// Allow-listed headers come back, per-request ones do not.
	if got := again.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("replay Content-Type = %q", got)
	}
	if got := again.Header().Get("Location"); got != "/v1/things/1" {
		t.Errorf("replay Location = %q", got)
	}
	if got := again.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("replay carries Set-Cookie %q", got)
	}

	// Same key, different request.
	decodeProblem(t, ta.doAuth("POST", "/v1/things", `{"name":"b"}`, idemKey, "key-00000001"), 422, "idempotency_mismatch")

	// Another key runs the handler.
	if rec := ta.doAuth("POST", "/v1/things", body, idemKey, "key-00000002"); rec.Body.String() != `{"id":2}` {
		t.Fatalf("second key: %s", rec.Body)
	}
}

func TestIntegrationIdempotencyScopedByPrincipal(t *testing.T) {
	ta := newIdemAPI(t)
	body := `{"name":"a"}`
	a := ta.do("POST", "/v1/things", body, "Authorization", "Bearer ok", idemKey, "shared-key-1")
	b := ta.do("POST", "/v1/things", body, "Authorization", "Bearer other", idemKey, "shared-key-1")
	if a.Code != 201 || b.Code != 201 || a.Body.String() == b.Body.String() || b.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("principals share a key: %d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	if n := ta.creates.Load(); n != 2 {
		t.Fatalf("handler ran %d times, want 2", n)
	}
	// A different body under the other principal's key is not a mismatch either.
	if rec := ta.do("POST", "/v1/things", `{"name":"zz"}`, "Authorization", "Bearer other", idemKey, "shared-key-1"); rec.Code != 422 {
		t.Fatalf("own key, other body: %d %s", rec.Code, rec.Body)
	}
}

// Anonymous callers have no stored keys: required operations want a login,
// optional ones ignore the header.
func TestIntegrationIdempotencyAnonymous(t *testing.T) {
	ta := newIdemAPI(t)
	decodeProblem(t, ta.do("POST", "/v1/ping", "", idemKey, "anon-key-001"), 401, "unauthenticated")

	a := ta.do("POST", "/v1/things-optional", "", idemKey, "opt-00000001")
	b := ta.do("POST", "/v1/things-optional", "", idemKey, "opt-00000001")
	if a.Body.String() == b.Body.String() || b.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("anonymous callers share replays: %s vs %s", a.Body, b.Body)
	}
	// Signed in, the same optional operation does replay.
	c := ta.doAuth("POST", "/v1/things-optional", "", idemKey, "opt-00000002")
	d := ta.doAuth("POST", "/v1/things-optional", "", idemKey, "opt-00000002")
	if c.Body.String() != d.Body.String() || d.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("authenticated optional replay: %s vs %s", c.Body, d.Body)
	}
	// Optional without a key: no idempotency, handler runs each time.
	e := ta.doAuth("POST", "/v1/things-optional", "")
	f := ta.doAuth("POST", "/v1/things-optional", "")
	if e.Body.String() == f.Body.String() {
		t.Fatalf("no key must not dedupe: %s %s", e.Body, f.Body)
	}
}

func TestIntegrationIdempotencyBodilessOperation(t *testing.T) {
	ta := newIdemAPI(t)
	a := ta.doAuth("POST", "/v1/ping", "", idemKey, "ping-key-01")
	b := ta.doAuth("POST", "/v1/ping", "", idemKey, "ping-key-01")
	if a.Code != 200 || b.Code != 200 || a.Body.String() != b.Body.String() || b.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("bodiless: %d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	if n := ta.creates.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

// A client error from the handler is a real outcome: it is stored and replayed.
func TestIntegrationIdempotencyReplaysClientErrors(t *testing.T) {
	ta := newIdemAPI(t)
	a := ta.doAuth("POST", "/v1/reject", "", idemKey, "reject-key-1")
	decodeProblem(t, a, 409, "conflict")
	b := ta.doAuth("POST", "/v1/reject", "", idemKey, "reject-key-1")
	decodeProblem(t, b, 409, "conflict")
	if b.Header().Get("Idempotent-Replayed") != "true" || b.Body.String() != a.Body.String() {
		t.Fatalf("4xx not replayed: %v %s", b.Header(), b.Body)
	}
	if n := ta.creates.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

// A handler panic and a handler 5xx both release the key and store nothing:
// the retry runs the handler again.
func TestIntegrationIdempotencyReleasesKeyOnPanicAndServerError(t *testing.T) {
	ta := newIdemAPI(t)
	for _, tc := range []struct{ path, key string }{
		{"/v1/boom", "boom-key-001"},
		{"/v1/fail", "fail-key-001"},
	} {
		before := ta.creates.Load()
		first := ta.doAuth("POST", tc.path, "", idemKey, tc.key)
		decodeProblem(t, first, 500, "internal_error")
		second := ta.doAuth("POST", tc.path, "", idemKey, tc.key)
		decodeProblem(t, second, 500, "internal_error")
		if second.Header().Get("Idempotent-Replayed") != "" {
			t.Errorf("%s: a 5xx was replayed", tc.path)
		}
		if got := ta.creates.Load() - before; got != 2 {
			t.Errorf("%s: handler ran %d times, want 2 (key released after failure)", tc.path, got)
		}
	}
}

func TestIntegrationIdempotencyInProgressConflict(t *testing.T) {
	ta := newIdemAPI(t)
	ta.gate = make(chan struct{})
	body := `{"name":"a"}`

	var wg sync.WaitGroup
	var first *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		first = ta.doAuth("POST", "/v1/things", body, idemKey, "key-concurrent")
	}()
	// Wait until the first request holds the key.
	deadline := time.Now().Add(5 * time.Second)
	for ta.creates.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first request never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	decodeProblem(t, ta.doAuth("POST", "/v1/things", body, idemKey, "key-concurrent"), 409, "conflict")

	close(ta.gate)
	wg.Wait()
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	if again := ta.doAuth("POST", "/v1/things", body, idemKey, "key-concurrent"); again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("after completion: %d %s", again.Code, again.Body)
	}
	if n := ta.creates.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

// Many identical requests released together: the insert race has exactly one
// winner, and every other request is a 409 or a replay.
func TestIntegrationIdempotencyConcurrentRace(t *testing.T) {
	ta := newIdemAPI(t)
	ta.delay = 150 * time.Millisecond
	const n = 20

	var ready, done sync.WaitGroup
	start := make(chan struct{})
	results := make([]*httptest.ResponseRecorder, n)
	ready.Add(n)
	done.Add(n)
	for i := range n {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			results[i] = ta.doAuth("POST", "/v1/things", `{"name":"a"}`, idemKey, "race-key-001")
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	var created, replayed, conflicts int
	for _, rec := range results {
		switch {
		case rec.Code == http.StatusCreated && rec.Header().Get("Idempotent-Replayed") == "true":
			replayed++
		case rec.Code == http.StatusCreated:
			created++
		case rec.Code == http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected response: %d %s", rec.Code, rec.Body)
		}
	}
	if created != 1 || created+replayed+conflicts != n {
		t.Fatalf("created=%d replayed=%d conflicts=%d, want exactly one creation", created, replayed, conflicts)
	}
	if got := ta.creates.Load(); got != 1 {
		t.Fatalf("handler ran %d times", got)
	}
}
