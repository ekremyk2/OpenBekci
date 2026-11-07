package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/problem"
)

func csrfReq(method, origin, xrw string) *http.Request {
	r := httptest.NewRequest(method, "/v1/me/sessions/current", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if xrw != "" {
		r.Header.Set("X-Requested-With", xrw)
	}
	return r
}

func TestCheckCSRF(t *testing.T) {
	allowed := []string{"https://bekci.example.org", "http://192.168.1.10:8484"}
	for _, c := range []struct {
		name         string
		origin, xrw  string
		wantRejected bool
	}{
		{"base url", "https://bekci.example.org", "openbekci-web", false},
		{"extra origin", "http://192.168.1.10:8484", "openbekci-web", false},
		{"case-insensitive host", "https://BEKCI.example.org", "openbekci-web", false},
		{"no origin", "", "openbekci-web", true},
		{"foreign origin", "https://evil.example.com", "openbekci-web", true},
		{"origin null", "null", "openbekci-web", true},
		{"wrong scheme", "http://bekci.example.org", "openbekci-web", true},
		{"wrong port", "https://bekci.example.org:8443", "openbekci-web", true},
		{"suffix trick", "https://bekci.example.org.evil.com", "openbekci-web", true},
		{"no header", "https://bekci.example.org", "", true},
		{"wrong header", "https://bekci.example.org", "XMLHttpRequest", true},
	} {
		err := CheckCSRF(csrfReq(http.MethodPost, c.origin, c.xrw), allowed)
		if c.wantRejected {
			if err == nil || err.Code != problem.CodeCSRFFailed || err.Status != http.StatusForbidden {
				t.Errorf("%s: err = %v, want 403 csrf_failed", c.name, err)
			}
		} else if err != nil {
			t.Errorf("%s: rejected: %v", c.name, err)
		}
	}
}

func TestSafeMethods(t *testing.T) {
	for m, safe := range map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "POST": false, "DELETE": false, "PUT": false, "PATCH": false} {
		if safeMethod(m) != safe {
			t.Errorf("safeMethod(%s) = %v", m, !safe)
		}
	}
}

func TestAuthCookieFlags(t *testing.T) {
	c := authCookie("tok", time.Now().Add(time.Hour), false)
	if c.Name != "obk_session" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie = %+v", c)
	}
	if authCookie("tok", time.Now().Add(time.Hour), true).Secure {
		t.Error("insecure-dev cookie is Secure")
	}
	if cc := clearedCookie(false); cc.MaxAge >= 0 || cc.Value != "" {
		t.Errorf("clear cookie = %+v", cc)
	}
}
