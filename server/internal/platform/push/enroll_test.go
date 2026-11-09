package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTwoStepEnrollment(t *testing.T) {
	var begin, complete map[string]any
	failComplete := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/enrollments/begin":
			_ = json.Unmarshal(b, &begin)
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"enrollment_id":"enr_1","enrollment_token":"tok","challenge":"c0123456789abcdef","expires_at":"2026-01-01T00:00:00Z"}`)
		case "/v1/enrollments/complete":
			_ = json.Unmarshal(b, &complete)
			if failComplete {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"error":{"code":"challenge_mismatch","message":"no match"}}`)
				return
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"server_id":"srv_1","server_secret":"obk_rs_abc","verified":true}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	bg, err := Begin(ctx, srv.URL, BeginRequest{AppID: "org.openbekci.app", InviteCode: "obk_inv_x"})
	if err != nil || bg.Challenge != "c0123456789abcdef" || bg.EnrollmentToken != "tok" {
		t.Fatalf("%+v %v", bg, err)
	}
	if begin["app_id"] != "org.openbekci.app" || begin["invite_code"] != "obk_inv_x" || len(begin) != 2 {
		t.Errorf("begin body = %v", begin)
	}
	en, err := Complete(ctx, srv.URL, CompleteRequest{EnrollmentID: bg.EnrollmentID, EnrollmentToken: bg.EnrollmentToken, BaseURL: "https://bekci.example.org"})
	if err != nil || en.ServerID != "srv_1" || en.ServerSecret != "obk_rs_abc" || !en.Verified {
		t.Fatalf("%+v %v", en, err)
	}
	if complete["enrollment_id"] != "enr_1" || complete["enrollment_token"] != "tok" || complete["base_url"] != "https://bekci.example.org" || len(complete) != 3 {
		t.Errorf("complete body = %v", complete)
	}
	failComplete = true
	if _, err := Complete(ctx, srv.URL, CompleteRequest{EnrollmentID: "x", EnrollmentToken: "y", BaseURL: "z"}); err == nil || !strings.Contains(err.Error(), "challenge_mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestRelayRateLimitedWholeBatchIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited"}}`)
	}))
	defer srv.Close()
	r := NewRouter(Options{Relays: []RelayConfig{{ID: "r", URL: srv.URL, ServerID: "s", Secret: []byte("x")}}})
	res, _ := r.Send(context.Background(), []Message{msg(KindFCM, "a", "r")})
	if res[0].Outcome != RateLimited || !res[0].Retryable() || !strings.Contains(res[0].Detail, "7") {
		t.Fatalf("%+v", res[0])
	}
}
