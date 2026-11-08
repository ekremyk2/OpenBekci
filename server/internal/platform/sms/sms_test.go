package sms

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func netgsmServer(t *testing.T, status int, body string, check func(*http.Request, string)) *Netgsm {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if check != nil {
			check(r, string(b))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Netgsm{Usercode: "user", Password: "pw", MsgHeader: "OPENBEKCI", BaseURL: srv.URL}
}

func TestNetgsmSendOK(t *testing.T) {
	g := netgsmServer(t, 200, `{"code":"00","jobid":"12345","description":"queued"}`, func(r *http.Request, body string) {
		if u, p, ok := r.BasicAuth(); !ok || u != "user" || p != "pw" {
			t.Errorf("basic auth = %q %q %v", u, p, ok)
		}
		for _, want := range []string{`"msgheader":"OPENBEKCI"`, `"no":"905321234567"`, `"msg":"Kod: 123456"`, `"encoding":"TR"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body %s lacks %s", body, want)
			}
		}
	})
	id, err := g.Send(context.Background(), "+905321234567", "Kod: 123456")
	if err != nil || id != "12345" {
		t.Fatalf("Send = %q, %v", id, err)
	}
}

func TestNetgsmErrorMapping(t *testing.T) {
	for code, want := range map[string]error{
		"30": ErrCredentials, "40": ErrSender, "80": ErrQuota, "20": ErrRejected, "70": ErrRejected, "99": ErrRejected,
	} {
		g := netgsmServer(t, 200, `{"code":"`+code+`","description":"x"}`, nil)
		if _, err := g.Send(context.Background(), "+905321234567", "x"); !errors.Is(err, want) {
			t.Errorf("code %s: err = %v, want %v", code, err, want)
		}
	}
	g := netgsmServer(t, 503, `oops`, nil)
	if _, err := g.Send(context.Background(), "+905321234567", "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("503: err = %v", err)
	}
	g = netgsmServer(t, 401, `nope`, nil)
	if _, err := g.Send(context.Background(), "+905321234567", "x"); !errors.Is(err, ErrCredentials) {
		t.Errorf("401: err = %v", err)
	}
}

func TestRecorder(t *testing.T) {
	var r Recorder
	if _, ok := r.Last(); ok {
		t.Fatal("empty recorder has a last message")
	}
	if _, err := r.Send(context.Background(), "+90", "hello"); err != nil {
		t.Fatal(err)
	}
	if m, _ := r.Last(); m.To != "+90" || m.Text != "hello" {
		t.Errorf("last = %+v", m)
	}
}
