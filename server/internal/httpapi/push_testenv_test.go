package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/devices"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/push"
)

var (
	testRelaySecret = []byte("relay-secret-for-tests-0123456789")
	testDeviceKey   = []byte("0123456789abcdef0123456789abcdef-device-key")
)

func newTestDevices(t *testing.T, d *db.DB) *devices.Service {
	t.Helper()
	s, err := devices.NewService(d, devices.Config{EncryptionKey: testDeviceKey, AllowPrivateEndpoints: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// relayCall is one request the fake relay received.
type relayCall struct {
	Auth string
	Body []byte
}

// fakeRelay stands in for both the push relay (POST /v1/send) and a
// UnifiedPush distributor (POST /up/...), recording what arrives.
type fakeRelay struct {
	srv *httptest.Server

	mu       sync.Mutex
	calls    []relayCall
	upBodies [][]byte
	// invalid lists the tokens the relay reports as invalid_token.
	invalid map[string]bool
	// rejected tokens get a non-retryable error, flaky ones a retryable error.
	rejected, flaky map[string]bool
	nonces          map[string]bool
	// status is the HTTP status of /v1/send (0 means 200); upStatus that of /up/.
	status, upStatus int
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	f := &fakeRelay{invalid: map[string]bool{}, rejected: map[string]bool{}, flaky: map[string]bool{}, nonces: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/send", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, relayCall{Auth: r.Header.Get("Authorization"), Body: body})
		status, invalid := f.status, f.invalid
		f.mu.Unlock()
		fail := func(code int, c, msg string) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": c, "message": msg}})
		}
		// the relay's authentication (docs/PROTOCOL.md section 1), written out independently of the client
		if !f.authentic(r.Header.Get("Authorization"), body) {
			fail(401, "unauthorized", "bad signature")
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || len(req.Messages) == 0 || len(req.Messages) > 500 {
			fail(400, "bad_request", "malformed")
			return
		}
		type res struct {
			Index     int    `json:"index"`
			Status    string `json:"status"`
			Code      string `json:"code,omitempty"`
			Retryable bool   `json:"retryable,omitempty"`
		}
		out := struct {
			Results []res `json:"results"`
		}{}
		for i, m := range req.Messages {
			token, _ := m["token"].(string)
			data, _ := m["data"].(map[string]any)
			allowed := map[string]bool{"instance_id": true, "org_id": true, "notification_id": true, "type": true, "collapse_key": true, "badge": true}
			bad := false
			for k := range data {
				bad = bad || !allowed[k]
			}
			switch {
			case bad:
				out.Results = append(out.Results, res{Index: i, Status: "error", Code: "data_not_allowed"})
			case invalid[token]:
				out.Results = append(out.Results, res{Index: i, Status: "invalid_token"})
			case f.rejected[token]:
				out.Results = append(out.Results, res{Index: i, Status: "error", Code: "wrong_app"})
			case f.flaky[token]:
				out.Results = append(out.Results, res{Index: i, Status: "error", Code: "provider_unavailable", Retryable: true})
			default:
				out.Results = append(out.Results, res{Index: i, Status: "ok"})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/up/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.upBodies = append(f.upBodies, body)
		status := f.upStatus
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// router is the production Router pointed at the fake: the relay `official`
// and direct UnifiedPush posts to the fake's /up/ endpoint.
func (f *fakeRelay) router() *push.Router {
	return push.NewRouter(push.Options{
		Relays:       []push.RelayConfig{{ID: "official", URL: f.srv.URL, ServerID: "srv-test", Secret: testRelaySecret, AppID: "org.test.app"}},
		AllowPrivate: true,
	})
}

func (f *fakeRelay) endpoint(name string) string { return f.srv.URL + "/up/" + name }

func (f *fakeRelay) take() ([]relayCall, [][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, u := f.calls, f.upBodies
	f.calls, f.upBodies = nil, nil
	return c, u
}

// authentic verifies `Relay-HMAC <server_id>:<ts>:<nonce>:<mac>`: ts within five
// minutes, a nonce never seen before, mac = hex HMAC-SHA256(secret, ts "." nonce
// "." sha256hex(body)).
func (f *fakeRelay) authentic(h string, body []byte) bool {
	parts := strings.Split(strings.TrimPrefix(h, "Relay-HMAC "), ":")
	if !strings.HasPrefix(h, "Relay-HMAC ") || len(parts) != 4 || parts[0] != "srv-test" {
		return false
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > 5*time.Minute {
		return false
	}
	if n := len(parts[2]); n < 16 || n > 64 || strings.Trim(parts[2], "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return false
	}
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, testRelaySecret)
	m.Write([]byte(parts[1] + "." + parts[2] + "." + hex.EncodeToString(sum[:])))
	if !hmac.Equal([]byte(hex.EncodeToString(m.Sum(nil))), []byte(parts[3])) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nonces[parts[2]] {
		return false
	}
	f.nonces[parts[2]] = true
	return true
}

func (f *fakeRelay) set(fn func(*fakeRelay)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}
