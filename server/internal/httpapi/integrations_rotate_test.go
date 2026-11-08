package httpapi

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/integrations"
)

// Rotating a webhook secret returns the new one once; deliveries then carry
// both signatures for 24 hours and only the new one afterwards.
func TestIntegrationWebhookSecretRotation(t *testing.T) {
	p := newPB(t)
	rcv := newReceiver(t)
	ep, oldSecret := p.newEndpoint(rcv.url(), `["ticket.created"]`)
	epID := str(ep, "id")
	ping := func() (sig string, body []byte) {
		t.Helper()
		before, nreq := len(p.e.jobRec.jobs()), len(rcv.requests())
		p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
		for _, err := range p.deliverQueued(before, 1, 8) {
			if err != nil {
				t.Fatalf("deliver: %v", err)
			}
		}
		reqs := rcv.requests()
		if len(reqs) != nreq+1 {
			t.Fatalf("receiver got %d requests, want %d", len(reqs), nreq+1)
		}
		last := reqs[len(reqs)-1]
		return last.header.Get("OpenBekci-Signature"), last.body
	}
	tags := regexp.MustCompile(`v1=[0-9a-f]{64}`)

	sig, body := ping()
	if n := len(tags.FindAllString(sig, -1)); n != 1 {
		t.Fatalf("before a rotation the header = %q", sig)
	}

	// rotating: a new secret, once; the endpoint resource never shows it
	rot := p.must(p.call("POST", p.hooksURL("/"+epID+":rotate-secret"), "", p.adm), 200)
	newSecret := str(rot.Body, "secret")
	if !strings.HasPrefix(newSecret, "whsec_") || newSecret == oldSecret || str(rot.Body, "previous_secret_expires_at") == "" {
		t.Fatalf("rotated = %s", rot.Raw)
	}
	until, err := time.Parse(time.RFC3339, str(rot.Body, "previous_secret_expires_at"))
	if err != nil || until.Before(time.Now().Add(23*time.Hour)) || until.After(time.Now().Add(25*time.Hour)) {
		t.Errorf("previous_secret_expires_at = %v (%v)", until, err)
	}
	for _, raw := range []string{p.must(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 200).Raw, p.must(p.call("GET", p.hooksURL(""), "", p.adm), 200).Raw} {
		if strings.Contains(raw, newSecret) || strings.Contains(raw, oldSecret) || strings.Contains(raw, "secret") {
			t.Errorf("a secret leaked into a read: %s", raw)
		}
	}

	// during the overlap: v1=<new>,v1=<old>, and each secret verifies on its own
	sig, body = ping()
	if n := len(tags.FindAllString(sig, -1)); n != 2 {
		t.Fatalf("during the overlap the header = %q", sig)
	}
	for name, secret := range map[string]string{"new": newSecret, "old": oldSecret} {
		if err := integrations.VerifySignature(secret, sig, body, time.Now(), integrations.SignatureTolerance); err != nil {
			t.Errorf("the %s secret does not verify: %v", name, err)
		}
	}
	if err := integrations.VerifySignature("whsec_other", sig, body, time.Now(), integrations.SignatureTolerance); err == nil {
		t.Error("a wrong secret verified")
	}

	// after the window only the new secret signs
	p.w.Exec(t, `UPDATE webhook_endpoint SET secret_previous_expires_at = now() - interval '1 minute' WHERE id = $1`, epID)
	sig, body = ping()
	if n := len(tags.FindAllString(sig, -1)); n != 1 {
		t.Fatalf("after the overlap the header = %q", sig)
	}
	if integrations.VerifySignature(oldSecret, sig, body, time.Now(), integrations.SignatureTolerance) == nil {
		t.Error("the old secret still verifies after the overlap")
	}
	if err := integrations.VerifySignature(newSecret, sig, body, time.Now(), integrations.SignatureTolerance); err != nil {
		t.Errorf("the new secret does not verify: %v", err)
	}

	// a second rotation inside a window keeps only the secret that was current
	rot2 := p.must(p.call("POST", p.hooksURL("/"+epID+":rotate-secret"), "", p.adm), 200)
	third := str(rot2.Body, "secret")
	sig, body = ping()
	if integrations.VerifySignature(oldSecret, sig, body, time.Now(), integrations.SignatureTolerance) == nil {
		t.Error("the oldest secret still signs after a second rotation")
	}
	for _, s := range []string{newSecret, third} {
		if err := integrations.VerifySignature(s, sig, body, time.Now(), integrations.SignatureTolerance); err != nil {
			t.Errorf("secret %s... does not verify: %v", s[:10], err)
		}
	}

	// the audit trail records it, never the secret
	rows := items(p.must(p.call("GET", "/v1/orgs/"+p.org.ID.String()+"/audit?action=webhook.rotate_secret", "", p.adm), 200))
	if len(rows) != 2 || strings.Contains(fmt.Sprint(rows), "whsec_") {
		t.Errorf("audit rows = %v", rows)
	}

	// unknown endpoint, and a key cannot rotate
	p.problemCode(p.call("POST", p.hooksURL("/"+p.w.OrgB.ID.String()+":rotate-secret"), "", p.adm), 404, "not_found")
	tok := p.forgeKey("forge002", `ARRAY['webhooks.manage']`)
	p.problemCode(p.call("POST", p.hooksURL("/"+epID+":rotate-secret"), "", tok), 403, "insufficient_permission")
}
