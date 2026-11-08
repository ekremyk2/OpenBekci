package integrations

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/audit"
)

func TestSignFormatAndVerify(t *testing.T) {
	secret := "whsec_test-secret"
	body := []byte(`{"type":"ping"}`)
	now := time.Unix(1_800_000_000, 0)
	sig := Sign(secret, now, body)
	if !regexp.MustCompile(`^t=1800000000,v1=[0-9a-f]{64}$`).MatchString(sig) {
		t.Fatalf("signature = %q", sig)
	}
	if sig != Sign(secret, now, body) {
		t.Error("signing is not deterministic")
	}
	if Sign(secret, now.Add(time.Second), body) == sig || Sign("whsec_other", now, body) == sig || Sign(secret, now, []byte(`{}`)) == sig {
		t.Error("the signature does not depend on time, secret and body")
	}
	for name, tc := range map[string]struct {
		secret, header string
		body           []byte
		at             time.Time
		ok             bool
	}{
		"valid":                 {secret, sig, body, now, true},
		"within tolerance":      {secret, sig, body, now.Add(4*time.Minute + 59*time.Second), true},
		"clock ahead":           {secret, sig, body, now.Add(-4 * time.Minute), true},
		"replayed":              {secret, sig, body, now.Add(5*time.Minute + time.Second), false},
		"from the future":       {secret, sig, body, now.Add(-6 * time.Minute), false},
		"wrong secret":          {"whsec_other", sig, body, now, false},
		"tampered body":         {secret, sig, []byte(`{"type":"pong"}`), now, false},
		"missing v1":            {secret, "t=1800000000", body, now, false},
		"missing t":             {secret, strings.Replace(sig, "t=1800000000,", "", 1), body, now, false},
		"garbage":               {secret, "nonsense", body, now, false},
		"empty":                 {secret, "", body, now, false},
		"two tags, one valid":   {secret, "t=1800000000,v1=" + strings.Repeat("0", 64) + "," + strings.SplitN(sig, ",", 2)[1], body, now, true},
		"altered timestamp":     {secret, "t=1800000001," + strings.SplitN(sig, ",", 2)[1], body, now, false},
		"uppercase hex differs": {secret, strings.ToUpper(sig), body, now, false},
	} {
		err := VerifySignature(tc.secret, tc.header, tc.body, tc.at, SignatureTolerance)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func TestRetryDelayIsExponentialAndCapped(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute}
	for i, w := range want {
		if got := RetryDelay(i + 1); got != w {
			t.Errorf("RetryDelay(%d) = %s, want %s", i+1, got, w)
		}
	}
	if RetryDelay(0) != 30*time.Second || RetryDelay(100) != 4*time.Hour {
		t.Errorf("edges: %s, %s", RetryDelay(0), RetryDelay(100))
	}
}

func TestAddrAllowed(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.216.34": true, "8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "127.8.8.8": false, "::1": false, "0.0.0.0": false, "::": false,
		"10.0.0.1": false, "172.16.0.1": false, "172.31.255.255": false, "192.168.0.1": false,
		"169.254.169.254": false, "fe80::1": false, "fc00::1": false, "fd12:3456::1": false,
		"100.64.0.1": false, "224.0.0.1": false, "ff02::1": false, "255.255.255.255": false, "240.0.0.1": false,
		"192.0.2.1": false, "198.51.100.1": false, "203.0.113.1": false, "198.18.0.1": false, "2001:db8::1": false,
		"::ffff:127.0.0.1": false, "::ffff:10.1.2.3": false, "::ffff:8.8.8.8": true,
		"64:ff9b::7f00:1": false, "64:ff9b::808:808": true,
		// IPv6 forms that carry an IPv4 address: 6to4 of 127.0.0.1 and 8.8.8.8, Teredo, IPv4-compatible, SIIT, local-use NAT64
		"2002:7f00:1::1": false, "2002:808:808::1": false, "2001:0:4136:e378:8000:63bf:3fff:fdd2": false, "::7f00:1": false, "::a00:1": false,
		"::ffff:0:7f00:1": false, "64:ff9b:1::7f00:1": false, "192.88.99.1": false, "5f00::1": false,
		"172.32.0.1": true, "11.0.0.1": true,
	} {
		if got := AddrAllowed(netip.MustParseAddr(addr), nil); got != want {
			t.Errorf("AddrAllowed(%s) = %v, want %v", addr, got, want)
		}
	}
	lan := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	if !AddrAllowed(netip.MustParseAddr("192.168.1.9"), lan) || AddrAllowed(netip.MustParseAddr("192.168.2.9"), lan) || AddrAllowed(netip.MustParseAddr("10.0.0.1"), lan) {
		t.Error("the operator allow-list does not behave as a precise exception")
	}
}

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []netip.Addr
	for _, s := range ips {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func TestValidateTarget(t *testing.T) {
	r := fakeResolver{
		"hooks.example.org": {"93.184.216.34"},
		"internal.example":  {"10.1.2.3"},
		"mixed.example":     {"93.184.216.34", "127.0.0.1"},
		"dual.example.org":  {"93.184.216.34", "2606:4700:4700::1111"},
		"metadata.example":  {"169.254.169.254"},
	}
	for raw, want := range map[string]string{
		"https://hooks.example.org/path?x=1":   "",
		"https://hooks.example.org:8443/":      "",
		"https://dual.example.org/":            "",
		"https://93.184.216.34/h":              "",
		"https://internal.example/":            "forbidden_target",
		"https://mixed.example/":               "forbidden_target", // one internal answer is enough to refuse
		"https://metadata.example/":            "forbidden_target",
		"https://127.0.0.1/":                   "forbidden_target",
		"https://localhost/":                   "unresolvable",
		"https://[::1]:8443/":                  "forbidden_target",
		"https://nxdomain.example/":            "unresolvable",
		"http://hooks.example.org/":            "https_required",
		"ftp://hooks.example.org/":             "https_required",
		"https://u:p@hooks.example.org/":       "invalid_url",
		"https://hooks.example.org/#f":         "invalid_url",
		"https://hooks.example.org:0/":         "invalid_url",
		"https://hooks.example.org:70000/":     "invalid_url",
		"//hooks.example.org/":                 "https_required",
		"":                                     "invalid_url",
		"https://":                             "invalid_url",
		"https://" + strings.Repeat("a", 2000): "too_long",
	} {
		_, got := ValidateTarget(context.Background(), r, raw, nil)
		if got != want {
			t.Errorf("ValidateTarget(%.60q) = %q, want %q", raw, got, want)
		}
	}
	if _, got := ValidateTarget(context.Background(), r, "https://internal.example/", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}); got != "" {
		t.Errorf("an operator-allowed range was refused: %q", got)
	}
}

// The guarded client checks the address it connects to: a name that resolves
// to loopback is refused, redirects are not followed, and proxies are ignored.
func TestGuardedClient(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	get := func(c *http.Client, url string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		return c.Do(req)
	}

	// strict: the loopback receiver is refused before any byte is sent
	strict := newGuardedClient(fakeResolver{}, nil, cfg)
	if _, err := get(strict, srv.URL); !errors.Is(err, ErrForbiddenTarget) {
		t.Errorf("loopback literal: err = %v", err)
	}
	// DNS rebinding: a harmless-looking name that answers with a private address
	rebind := newGuardedClient(fakeResolver{"evil.example.org": {"127.0.0.1"}}, nil, cfg)
	port := srv.URL[strings.LastIndex(srv.URL, ":"):]
	if _, err := get(rebind, "https://evil.example.org"+port+"/"); !errors.Is(err, ErrForbiddenTarget) {
		t.Errorf("rebinding name: err = %v", err)
	}
	if hits != 0 {
		t.Errorf("the receiver was contacted %d times", hits)
	}

	// allowed by the operator: reachable, redirect not followed
	lan := newGuardedClient(fakeResolver{}, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, cfg)
	resp, err := get(lan, srv.URL+"/ok")
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("allowed receiver: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	resp, err = get(lan, srv.URL+"/redirect")
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	if hits != 2 {
		t.Errorf("hits = %d, want 2 (the redirect target must not be requested)", hits)
	}
	if lan.Timeout != DeliveryTimeout {
		t.Errorf("timeout = %s", lan.Timeout)
	}
	// the client does not use proxies from the environment
	if tr, ok := lan.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Error("the transport may use an environment proxy")
	}
	// a certificate the system does not trust is refused
	untrusted := newGuardedClient(fakeResolver{}, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, nil)
	if _, err := get(untrusted, srv.URL); err == nil {
		t.Error("a self-signed receiver was accepted")
	}
}

// fromAudit maps audited domain actions to the event vocabulary.
func TestFromAuditMapping(t *testing.T) {
	org, target, site := uuid.New(), uuid.New(), uuid.New()
	for action, tc := range map[string]struct {
		targetType, event, idKey string
	}{
		"payment.record":        {"payment", EventPaymentRecorded, "payment_id"},
		"payment.confirm":       {"payment", EventPaymentConfirmed, "payment_id"},
		"charge.create":         {"charge", EventChargeCreated, "charge_id"},
		"tickets.create":        {"ticket", EventTicketCreated, "ticket_id"},
		"tickets.transition":    {"ticket", EventTicketStatusChanged, "ticket_id"},
		"announcements.publish": {"announcement", EventAnnouncementPublish, "announcement_id"},
		"meeting.call":          {"meeting", EventMeetingCalled, "meeting_id"},
		"decision.apply":        {"decision", EventDecisionApplied, "decision_id"},
		"delivery.receive":      {"delivery", EventDeliveryReceived, "delivery_id"},
	} {
		ev, ok, err := fromAudit(context.Background(), nil, audit.Event{ID: uuid.New(), OrgID: org, SiteID: &site, Action: action, TargetType: tc.targetType, TargetID: target})
		if err != nil || !ok || ev.Type != tc.event || ev.OrgID != org || ev.Version != 1 || ev.IDs[tc.idKey] != target || ev.IDs["site_id"] != site || len(ev.IDs) != 2 {
			t.Errorf("%s -> %+v ok=%v err=%v", action, ev, ok, err)
		}
	}
	for _, action := range []string{"tickets.update", "tickets.comment", "payment.declare", "payment.reject", "charge.cancel", "announcements.create", "meeting.update", "decision.update", "visitor.update", "org.update", "webhook.create", "apikey.create"} {
		if _, ok, _ := fromAudit(context.Background(), nil, audit.Event{OrgID: org, Action: action, TargetType: "x", TargetID: target}); ok {
			t.Errorf("%s emits an event", action)
		}
	}
	// the delivery event also names the unit, from the audit diff
	unit := uuid.New()
	ev, _, _ := fromAudit(context.Background(), nil, audit.Event{OrgID: org, Action: "delivery.receive", TargetType: "delivery", TargetID: target,
		Diff: audit.Changes{"unit_id": {After: []byte(`"` + unit.String() + `"`)}}})
	if ev.IDs["unit_id"] != unit {
		t.Errorf("ids = %v", ev.IDs)
	}
}

func TestEventTypesAndValidation(t *testing.T) {
	want := []string{"payment.recorded", "payment.confirmed", "charge.created", "ticket.created", "ticket.status_changed", "announcement.published", "meeting.called", "decision.applied", "visitor.approved", "delivery.received"}
	if !slices.Equal(EventTypes(), want) {
		t.Errorf("EventTypes = %v", EventTypes())
	}
	if !validEventTypes([]string{"ticket.created", "payment.confirmed"}) || validEventTypes(nil) || validEventTypes([]string{"ping"}) ||
		validEventTypes([]string{"ticket.created", "ticket.created"}) || validEventTypes([]string{"nope"}) {
		t.Error("event type validation")
	}
}

func TestKeyTokenShape(t *testing.T) {
	prefix, token, hash, err := newKeyToken()
	if err != nil || len(hash) != 32 {
		t.Fatalf("%v %d", err, len(hash))
	}
	if !regexp.MustCompile(`^obk_[a-z0-9]{8}_[A-Za-z0-9_-]{43}$`).MatchString(token) || !strings.HasPrefix(token, "obk_"+prefix+"_") {
		t.Errorf("token = %q", token)
	}
	p, s, ok := splitKeyToken(token)
	if !ok || p != prefix || s != strings.TrimPrefix(token, "obk_"+prefix+"_") {
		t.Errorf("split = %q %q %v", p, s, ok)
	}
	for _, bad := range []string{"", "obk_", "obk_short_x", "obk_ABCDEFGH_" + strings.Repeat("x", 43), "obk_abcdefgh-" + strings.Repeat("x", 43), "xyz_abcdefgh_" + strings.Repeat("x", 43), "obk_abcdefgh_" + strings.Repeat("x", 5)} {
		if _, _, ok := splitKeyToken(bad); ok {
			t.Errorf("%q parsed as a key", bad)
		}
	}
}
