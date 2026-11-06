package httpapi

import (
	"errors"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// A request for a code or link answers 202 with an empty body for a known and
// an unknown identifier alike, also when the gateway is down.
func TestIntegrationHTTPRequestsDoNotRevealAccounts(t *testing.T) {
	e := newIDEnv(t, true, true)
	e.user(identity.CreateUserParams{Email: "known@example.org", Phone: "+905321234567"})
	e.sms.Err = sms.ErrUnavailable
	e.mail.Err = errors.New("smtp: down")

	for _, c := range []struct{ path, known, unknown string }{
		{"/v1/auth/otp", `{"phone":"+905321234567"}`, `{"phone":"+905559998877"}`},
		{"/v1/auth/magic-link", `{"email":"known@example.org"}`, `{"email":"unknown@example.org"}`},
		{"/v1/auth/session", `{"method":"sms_otp","identifier":"+905321234567"}`, `{"method":"sms_otp","identifier":"+905559998877"}`},
		{"/v1/auth/session", `{"method":"magic_link","identifier":"known@example.org"}`, `{"method":"magic_link","identifier":"unknown@example.org"}`},
	} {
		k := e.do("POST", c.path, c.known, false)
		u := e.do("POST", c.path, c.unknown, false)
		if k.Code != 202 || u.Code != 202 || k.Body.String() != u.Body.String() {
			t.Errorf("%s: known %d %q, unknown %d %q", c.path, k.Code, k.Body, u.Code, u.Body)
		}
	}
	ds, err := e.svc.RecentDeliveries(t.Context(), 20, true)
	if err != nil || len(ds) != 4 {
		t.Errorf("delivery log has %d problems (%v), want the 4 failed sends of known accounts", len(ds), err)
	}
}
