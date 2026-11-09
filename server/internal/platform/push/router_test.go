package push

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func msg(kind, token, relay string) Message {
	return Message{Kind: kind, Token: token, RelayID: relay,
		Data: Payload{InstanceID: "i", OrgID: "o", NotificationID: "n", Type: "ticket.updated", CollapseKey: "k", Badge: 2}}
}

func TestRelaySignsAndSplitsResults(t *testing.T) {
	var auth string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/v1/send" || r.Method != "POST" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"results":[{"status":"ok"},{"status":"invalid_token"},{"status":"rate_limited"},{"status":"error","code":"provider_unavailable","retryable":true}]}`)
	}))
	defer srv.Close()
	r := NewRouter(Options{Relays: []RelayConfig{{ID: "official", URL: srv.URL, ServerID: "s1", Secret: []byte("secret")}}})
	res, err := r.Send(context.Background(), []Message{msg(KindFCM, "a", "official"), msg(KindAPNs, "b", "official"), msg(KindFCM, "c", "official"), msg(KindFCM, "d", "official")})
	if err != nil {
		t.Fatal(err)
	}
	want := []Outcome{OK, InvalidToken, RateLimited, Error}
	for i, w := range want {
		if res[i].Outcome != w {
			t.Errorf("result %d = %s, want %s", i, res[i].Outcome, w)
		}
	}
	parts := strings.Split(strings.TrimPrefix(auth, "Relay-HMAC "), ":")
	if len(parts) != 4 || RelayAuth("s1", []byte("secret"), parts[1], parts[2], body) != auth {
		t.Errorf("bad signature %q", auth)
	}
	var req struct {
		Messages []struct {
			AppID string         `json:"app_id"`
			Data  map[string]any `json:"data"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) != 4 || req.Messages[0].AppID != DefaultAppID || len(req.Messages[0].Data) != 6 {
		t.Errorf("body = %s (%v)", body, err)
	}
}

func TestRelayFailureModes(t *testing.T) {
	status := 200
	reply := `{"results":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	defer srv.Close()
	r := NewRouter(Options{Relays: []RelayConfig{{ID: "r", URL: srv.URL, ServerID: "s", Secret: []byte("x")}}})
	one := []Message{msg(KindFCM, "a", "r")}
	for _, c := range []struct {
		status int
		reply  string
		want   Outcome
	}{{429, "", RateLimited}, {503, "", Error}, {401, `{"error":{"code":"stale_timestamp"}}`, Error}, {403, `{"error":{"code":"server_disabled"}}`, Error}, {401, `{"error":{"code":"unauthorized"}}`, Rejected}, {400, "", Rejected}, {200, `not json`, Error}, {200, `{"results":[]}`, Error}} {
		status, reply = c.status, c.reply
		res, _ := r.Send(context.Background(), one)
		if res[0].Outcome != c.want || res[0].Retryable() != (c.want != Rejected) {
			t.Errorf("status %d reply %q: %+v, want %s (retryable)", c.status, c.reply, res[0], c.want)
		}
	}
	// no relay for the id, and an unknown kind: not retryable
	res, _ := r.Send(context.Background(), []Message{msg(KindFCM, "a", "missing"), msg("sms", "a", "r")})
	if res[0].Outcome != NoRoute || res[0].Retryable() || res[1].Outcome != InvalidToken {
		t.Errorf("results = %+v", res)
	}
	// unreachable relay
	srv.Close()
	if res, _ := r.Send(context.Background(), one); res[0].Outcome != Error {
		t.Errorf("unreachable relay: %+v", res[0])
	}
}

func TestUnifiedPushGuardsTheEndpoint(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
	}))
	defer srv.Close()
	// default: plain http and loopback are refused, nothing is sent
	strict := NewRouter(Options{})
	res, _ := strict.Send(context.Background(), []Message{msg(KindUnifiedPush, srv.URL+"/x", "")})
	if res[0].Outcome != InvalidToken || got != nil {
		t.Fatalf("strict router: %+v, body %q", res[0], got)
	}
	// https to a loopback address is refused at connect time
	tls := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached a loopback endpoint") }))
	defer tls.Close()
	if res, _ := strict.Send(context.Background(), []Message{msg(KindUnifiedPush, tls.URL, "")}); res[0].Outcome != InvalidToken {
		t.Fatalf("loopback https: %+v", res[0])
	}
	lax := NewRouter(Options{AllowPrivate: true})
	res, _ = lax.Send(context.Background(), []Message{msg(KindUnifiedPush, srv.URL+"/x", "")})
	if res[0].Outcome != OK {
		t.Fatalf("lax router: %+v", res[0])
	}
	var p map[string]any
	if err := json.Unmarshal(got, &p); err != nil || len(p) != 6 {
		t.Fatalf("body %s (%v)", got, err)
	}
}

func TestLogSender(t *testing.T) {
	res, err := Log{}.Send(context.Background(), []Message{msg(KindFCM, "a", "dev")})
	if err != nil || res[0].Outcome != OK {
		t.Fatalf("%+v %v", res, err)
	}
	r := NewRouter(Options{Relays: []RelayConfig{{ID: "dev", URL: "log"}}})
	if res, _ := r.Send(context.Background(), []Message{msg(KindAPNs, "a", "dev")}); res[0].Outcome != OK {
		t.Fatalf("log relay: %+v", res[0])
	}
}

func TestGenericTitleKey(t *testing.T) {
	if got := GenericTitleKey("visitor.approval_requested"); got != "push.visitor_approval_requested" {
		t.Fatal(got)
	}
}

func TestBlockedNet(t *testing.T) {
	for _, a := range []string{"100.64.0.1", "100.127.255.254", "198.18.0.1", "192.0.0.8", "240.0.0.1", "64:ff9b::a00:1"} {
		if !blockedNet(net.ParseIP(a)) {
			t.Errorf("%s should be blocked", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		if blockedNet(net.ParseIP(a)) {
			t.Errorf("%s should be allowed", a)
		}
	}
}
