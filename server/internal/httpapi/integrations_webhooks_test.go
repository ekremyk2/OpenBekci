package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// receiver is a fake webhook receiver: a TLS server on 127.0.0.1 that records
// what it is sent and answers with a configurable status.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []recvReq
	status atomic.Int32
	// redirect, when set, answers 302 to it
	redirect atomic.Value
	bigBody  atomic.Bool
}

type recvReq struct {
	header http.Header
	body   []byte
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	r.status.Store(200)
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.reqs = append(r.reqs, recvReq{header: req.Header.Clone(), body: b})
		r.mu.Unlock()
		if to, _ := r.redirect.Load().(string); to != "" {
			http.Redirect(w, req, to, http.StatusFound)
			return
		}
		w.WriteHeader(int(r.status.Load()))
		if r.bigBody.Load() {
			chunk := []byte(strings.Repeat("x", 1<<16))
			for i := 0; i < 80; i++ { // 5 MiB: far beyond what the sender reads
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}
	}))
	testRoots.AddCert(r.srv.Certificate())
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) url() string { return r.srv.URL + "/hook" }

func (r *receiver) requests() []recvReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recvReq(nil), r.reqs...)
}

func (p *pb) hooksURL(more string) string {
	return "/v1/orgs/" + p.org.ID.String() + "/webhook-endpoints" + more
}

// newEndpoint creates an endpoint as the org admin and returns its resource and secret.
func (p *pb) newEndpoint(url string, events string) (map[string]any, string) {
	p.t.Helper()
	r := p.must(p.call("POST", p.hooksURL(""), fmt.Sprintf(`{"url":%q,"events":%s}`, url, events), p.adm), 201)
	ep, _ := r.Body["endpoint"].(map[string]any)
	secret := str(r.Body, "secret")
	if ep == nil || !strings.HasPrefix(secret, "whsec_") {
		p.t.Fatalf("created endpoint = %s", r.Raw)
	}
	return ep, secret
}

// pending runs the delivery jobs the service queued since from, once each, as attempt.
func (p *pb) deliverQueued(from, attempt, maxAttempts int) []error {
	p.t.Helper()
	var errs []error
	for _, a := range p.e.jobRec.jobs()[from:] {
		errs = append(errs, p.e.integ.Deliver(context.Background(), a, attempt, maxAttempts))
	}
	return errs
}

func (p *pb) deliveries(endpoint string) []map[string]any {
	p.t.Helper()
	return items(p.must(p.call("GET", p.hooksURL("/"+endpoint+"/deliveries"), "", p.adm), 200))
}

func TestIntegrationWebhookEndpointCRUD(t *testing.T) {
	p := newPB(t)
	rcv := newReceiver(t)

	// the target is validated: https only, no internal addresses, no credentials
	for url, want := range map[string]string{
		"http://93.184.216.34/h":          "https_required",
		"https://10.0.0.5/h":              "forbidden_target",
		"https://169.254.169.254/latest":  "forbidden_target",
		"https://[::1]/h":                 "forbidden_target",
		"https://192.168.1.10:8443/h":     "forbidden_target",
		"https://user:pw@93.184.216.34/h": "invalid_url",
		"https://93.184.216.34/h#frag":    "invalid_url",
		"not a url":                       "invalid_url",
		"https:///nohost":                 "invalid_url",
		"https://93.184.216.34:99999/h":   "invalid_url",
	} {
		r := p.must(p.call("POST", p.hooksURL(""), fmt.Sprintf(`{"url":%q,"events":["ticket.created"]}`, url), p.adm), 422)
		if !strings.Contains(r.Raw, `"field":"url"`) || !strings.Contains(r.Raw, want) {
			t.Errorf("url %q: %s, want %s", url, r.Raw, want)
		}
	}
	for events, field := range map[string]string{`[]`: "events", `["nope.event"]`: "events", `["ping"]`: "events", `["ticket.created","ticket.created"]`: "events"} {
		r := p.must(p.call("POST", p.hooksURL(""), fmt.Sprintf(`{"url":%q,"events":%s}`, rcv.url(), events), p.adm), 422)
		if !strings.Contains(r.Raw, `"field":"`+field+`"`) {
			t.Errorf("events %s: %s", events, r.Raw)
		}
	}

	ep, secret := p.newEndpoint(rcv.url(), `["ticket.created","payment.confirmed"]`)
	epID := str(ep, "id")
	if ep["active"] != true || ep["failing"] != false || num(ep, "version") != 1 || str(ep, "url") != rcv.url() {
		t.Errorf("endpoint = %+v", ep)
	}
	got := p.must(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 200)
	if strings.Contains(got.Raw, secret) || strings.Contains(got.Raw, "secret") {
		t.Errorf("the secret is in the endpoint resource: %s", got.Raw)
	}
	if n := len(items(p.must(p.call("GET", p.hooksURL(""), "", p.adm), 200))); n != 1 {
		t.Errorf("list has %d endpoints", n)
	}

	// the secret is stored sealed, not in clear
	var sealed []byte
	if err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT secret_sealed FROM webhook_endpoint WHERE id = $1`, epID).Scan(&sealed)
	}); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || strings.Contains(string(sealed), secret) {
		t.Error("the signing secret is not sealed")
	}

	// update: If-Match, validation, null description, deactivate
	p.problemCode(p.call("PATCH", p.hooksURL("/"+epID), `{"active":false}`, p.adm, "If-Match", `"7"`), 409, "version_conflict")
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"url":"http://93.184.216.34/h"}`, p.adm, "If-Match", `"1"`), 422)
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"events":["ping"]}`, p.adm, "If-Match", `"1"`), 422)
	up := p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"active":false,"description":"billing sync","events":["payment.confirmed"]}`, p.adm, "If-Match", `"1"`), 200)
	if up.Body["active"] != false || str(up.Body, "description") != "billing sync" || num(up.Body, "version") != 2 || len(up.Body["events"].([]any)) != 1 {
		t.Errorf("updated = %s", up.Raw)
	}
	up = p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"description":null}`, p.adm, "If-Match", `"2"`), 200)
	if up.Body["description"] != nil {
		t.Errorf("description = %v", up.Body["description"])
	}

	// the audit trail has the endpoint's host, never the secret
	audRows := items(p.must(p.call("GET", "/v1/orgs/"+p.org.ID.String()+"/audit?target_type=webhook_endpoint", "", p.adm), 200))
	if len(audRows) < 3 || strings.Contains(fmt.Sprint(audRows), secret) {
		t.Errorf("webhook audit = %v", audRows)
	}

	p.must(p.call("DELETE", p.hooksURL("/"+epID), "", p.adm), 204)
	p.problemCode(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 404, "not_found")
	p.problemCode(p.call("DELETE", p.hooksURL("/"+epID), "", p.adm), 404, "not_found")
	p.problemCode(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 404, "not_found")
	p.problemCode(p.call("GET", p.hooksURL("/"+epID+"/deliveries"), "", p.adm), 404, "not_found")
}

// A delivery is a signed POST with ids only, headers as documented, and a log entry.
func TestIntegrationWebhookDeliverySignature(t *testing.T) {
	p := newPB(t)
	rcv := newReceiver(t)
	ep, secret := p.newEndpoint(rcv.url(), `["ticket.created"]`)
	epID := str(ep, "id")

	// test sends a ping, also to an inactive endpoint
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"active":false}`, p.adm, "If-Match", `"1"`), 200)
	test := p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
	if str(test.Body, "status") != "pending" || str(test.Body, "event_type") != "ping" || num(test.Body, "attempts") != 0 {
		t.Errorf("test delivery = %s", test.Raw)
	}
	for _, err := range p.deliverQueued(0, 1, 8) {
		if err != nil {
			t.Fatalf("deliver: %v", err)
		}
	}
	reqs := rcv.requests()
	if len(reqs) != 1 {
		t.Fatalf("receiver got %d requests", len(reqs))
	}
	h, body := reqs[0].header, reqs[0].body

	// header format: t=<unix>,v1=<hex hmac-sha256(secret, t + "." + body)>
	sig := h.Get("OpenBekci-Signature")
	m := regexp.MustCompile(`^t=(\d{10}),v1=([0-9a-f]{64})$`).FindStringSubmatch(sig)
	if m == nil {
		t.Fatalf("OpenBekci-Signature = %q", sig)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(m[1] + "." + string(body)))
	if want := hex.EncodeToString(mac.Sum(nil)); m[2] != want {
		t.Errorf("v1 = %s, want %s", m[2], want)
	}
	if err := integrations.VerifySignature(secret, sig, body, time.Now(), integrations.SignatureTolerance); err != nil {
		t.Errorf("VerifySignature: %v", err)
	}
	if integrations.VerifySignature("whsec_other", sig, body, time.Now(), integrations.SignatureTolerance) == nil {
		t.Error("a wrong secret verified")
	}
	if integrations.VerifySignature(secret, sig, append(append([]byte(nil), body...), ' '), time.Now(), integrations.SignatureTolerance) == nil {
		t.Error("a changed body verified")
	}
	if integrations.VerifySignature(secret, sig, body, time.Now().Add(6*time.Minute), integrations.SignatureTolerance) == nil {
		t.Error("a signature older than five minutes verified (replay)")
	}
	if h.Get("OpenBekci-Event") != "ping" || h.Get("OpenBekci-Delivery-Id") != str(test.Body, "id") || h.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", h)
	}

	// the body is the core event: ids only
	var ev map[string]any
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(ev))
	for k := range ev {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "id,ids,occurred_at,org_id,type,version" || ev["type"] != "ping" || ev["version"] != float64(1) || ev["org_id"] != p.org.ID.String() {
		t.Errorf("body = %s", body)
	}
	if ids, _ := ev["ids"].(map[string]any); len(ids) != 1 || ids["endpoint_id"] != epID {
		t.Errorf("ids = %v", ev["ids"])
	}
	if _, err := time.Parse(time.RFC3339, str(ev, "occurred_at")); err != nil {
		t.Errorf("occurred_at = %v", ev["occurred_at"])
	}

	// the log
	log := p.deliveries(epID)
	if len(log) != 1 || str(log[0], "status") != "succeeded" || num(log[0], "attempts") != 1 || num(log[0], "response_code") != 200 || log[0]["delivered_at"] == nil || log[0]["next_retry_at"] != nil {
		t.Errorf("log = %v", log)
	}

	// a huge response body does not hold the delivery up and still counts as success
	rcv.bigBody.Store(true)
	before := len(p.e.jobRec.jobs())
	p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
	start := time.Now()
	for _, err := range p.deliverQueued(before, 1, 8) {
		if err != nil {
			t.Fatalf("deliver with a big response: %v", err)
		}
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("delivery with a 5 MiB response took %s", time.Since(start))
	}
}

// Failed attempts back off exponentially; the delivery is abandoned after the
// bound, which marks the endpoint failing and tells the admins once; a later
// success clears the mark.
func TestIntegrationWebhookRetryAndFailing(t *testing.T) {
	p := newPB(t)
	rcv := newReceiver(t)
	ep, _ := p.newEndpoint(rcv.url(), `["ticket.created"]`)
	epID := str(ep, "id")
	ping := func() int {
		before := len(p.e.jobRec.jobs())
		p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
		return before
	}

	// 500 on the first attempt: pending with a retry time ~30 s away; then success
	rcv.status.Store(500)
	from := ping()
	errs := p.deliverQueued(from, 1, 3)
	if len(errs) != 1 || errs[0] == nil {
		t.Fatalf("a failed attempt must return an error for River to retry: %v", errs)
	}
	log := p.deliveries(epID)
	if str(log[0], "status") != "pending" || num(log[0], "attempts") != 1 || num(log[0], "response_code") != 500 || str(log[0], "last_error") != "http_status: 500" {
		t.Fatalf("after attempt 1: %v", log[0])
	}
	retry, err := time.Parse(time.RFC3339, str(log[0], "next_retry_at"))
	if err != nil || retry.Before(time.Now().Add(20*time.Second)) || retry.After(time.Now().Add(40*time.Second)) {
		t.Errorf("next_retry_at = %v (%v), want about 30 s ahead", log[0]["next_retry_at"], err)
	}
	rcv.status.Store(200)
	if errs := p.deliverQueued(from, 2, 3); errs[0] != nil {
		t.Fatalf("attempt 2: %v", errs[0])
	}
	log = p.deliveries(epID)
	if str(log[0], "status") != "succeeded" || num(log[0], "attempts") != 2 || log[0]["next_retry_at"] != nil {
		t.Errorf("after attempt 2: %v", log[0])
	}
	// the delivery id is the same on every attempt
	r := rcv.requests()
	if r[0].header.Get("OpenBekci-Delivery-Id") != r[1].header.Get("OpenBekci-Delivery-Id") {
		t.Error("the delivery id changed between attempts")
	}

	// used up: failed, endpoint failing, admins told once
	rcv.status.Store(503)
	from = ping()
	if errs := p.deliverQueued(from, 3, 3); errs[0] == nil {
		t.Error("the last failed attempt returned no error")
	}
	log = p.deliveries(epID)
	if str(log[0], "status") != "failed" || num(log[0], "attempts") != 3 || log[0]["next_retry_at"] != nil {
		t.Errorf("after the last attempt: %v", log[0])
	}
	got := p.must(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 200)
	if got.Body["failing"] != true || got.Body["failing_since"] == nil || num(got.Body, "consecutive_failures") != 1 {
		t.Errorf("endpoint = %s", got.Raw)
	}
	notes := p.e.notes.byTemplate("webhook.endpoint_failing")
	if len(notes) != 1 || notes[0].OrgID != p.org.ID || len(notes[0].Recipients) == 0 || notes[0].Payload["host"] == "" {
		t.Fatalf("notifications = %+v", notes)
	}
	admin := p.w.Actor("org_admin").PersonID
	found := false
	for _, rc := range notes[0].Recipients {
		found = found || (rc.PersonID != nil && *rc.PersonID == admin)
	}
	if !found {
		t.Errorf("the org admin is not a recipient: %+v", notes[0].Recipients)
	}
	// a second abandoned delivery in the same streak does not tell them again
	from = ping()
	_ = p.deliverQueued(from, 3, 3)
	if n := len(p.e.notes.byTemplate("webhook.endpoint_failing")); n != 1 {
		t.Errorf("%d failing notifications, want 1 per streak", n)
	}
	got = p.must(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 200)
	if num(got.Body, "consecutive_failures") != 2 {
		t.Errorf("consecutive_failures = %v", got.Body["consecutive_failures"])
	}
	// a success clears it
	rcv.status.Store(200)
	from = ping()
	_ = p.deliverQueued(from, 1, 3)
	got = p.must(p.call("GET", p.hooksURL("/"+epID), "", p.adm), 200)
	if got.Body["failing"] != false || num(got.Body, "consecutive_failures") != 0 {
		t.Errorf("after a success: %s", got.Raw)
	}
	// the log pages newest first
	all := p.deliveries(epID)
	if len(all) != 4 {
		t.Fatalf("log has %d entries", len(all))
	}
	page := p.must(p.call("GET", p.hooksURL("/"+epID+"/deliveries?limit=3"), "", p.adm), 200)
	if len(items(page)) != 3 || page.Body["next_cursor"] == nil || str(items(page)[0], "id") != str(all[0], "id") {
		t.Fatalf("page 1 = %s", page.Raw)
	}
	page2 := p.must(p.call("GET", p.hooksURL("/"+epID+"/deliveries?limit=3&cursor="+str(page.Body, "next_cursor")), "", p.adm), 200)
	if len(items(page2)) != 1 || str(items(page2)[0], "id") != str(all[3], "id") || page2.Body["next_cursor"] != nil {
		t.Errorf("page 2 = %s", page2.Raw)
	}
	if n := len(items(p.must(p.call("GET", p.hooksURL("/"+epID+"/deliveries?status=failed"), "", p.adm), 200))); n != 2 {
		t.Errorf("failed deliveries = %d", n)
	}
	p.must(p.call("GET", p.hooksURL("/"+epID+"/deliveries?status=bogus"), "", p.adm), 422)
}

// Delivery-time guards: an address that turned internal after creation is not
// connected to, a redirect is not followed, and a deleted endpoint drops its jobs.
func TestIntegrationWebhookSSRFGuardAtDelivery(t *testing.T) {
	p := newPB(t)
	rcv, elsewhere := newReceiver(t), newReceiver(t)
	ep, _ := p.newEndpoint(rcv.url(), `["ticket.created"]`)
	epID := str(ep, "id")
	ping := func() int {
		before := len(p.e.jobRec.jobs())
		p.must(p.call("POST", p.hooksURL("/"+epID+":test"), "", p.adm), 202)
		return before
	}

	// the loopback allowance of the test environment does not cover link-local metadata endpoints
	p.w.Exec(t, `UPDATE webhook_endpoint SET url = 'https://169.254.169.254/latest/meta-data' WHERE id = $1`, epID)
	from := ping()
	if errs := p.deliverQueued(from, 1, 5); errs[0] == nil {
		t.Fatal("a delivery to a link-local address succeeded")
	}
	log := p.deliveries(epID)
	if !strings.HasPrefix(str(log[0], "last_error"), "forbidden_target") || str(log[0], "status") != "pending" {
		t.Errorf("log = %v", log[0])
	}

	// a redirect to anywhere (here an allowed receiver) is a failed delivery, and is not followed
	p.w.Exec(t, `UPDATE webhook_endpoint SET url = $2 WHERE id = $1`, epID, rcv.url())
	rcv.redirect.Store(elsewhere.url())
	from = ping()
	if errs := p.deliverQueued(from, 1, 5); errs[0] == nil {
		t.Fatal("a redirect counted as success")
	}
	if n := len(elsewhere.requests()); n != 0 {
		t.Errorf("the redirect was followed (%d requests)", n)
	}
	log = p.deliveries(epID)
	if num(log[0], "response_code") != 302 || !strings.HasPrefix(str(log[0], "last_error"), "redirect_not_followed") {
		t.Errorf("log = %v", log[0])
	}

	// a job whose endpoint was deleted is dropped without an error
	from = ping()
	p.must(p.call("DELETE", p.hooksURL("/"+epID), "", p.adm), 204)
	if errs := p.deliverQueued(from, 1, 5); errs[0] != nil {
		t.Errorf("a delivery of a deleted endpoint returned %v", errs[0])
	}
}

// Audited domain actions emit their webhook event in the action's transaction,
// to the active endpoints that subscribe, once per (event, endpoint).
func TestIntegrationWebhookEventEmission(t *testing.T) {
	p := newPB(t)
	rcv := newReceiver(t)
	restore := audit.SetSink(p.e.integ.EmitFromAudit)
	t.Cleanup(restore)
	ctx := context.Background()

	ep, _ := p.newEndpoint(rcv.url(), `["ticket.created","visitor.approved"]`)
	epID := str(ep, "id")
	s := newD2Site(p.w, p.org, t)

	jobs0 := len(p.e.jobRec.jobs())
	tk := p.must(p.call("POST", "/v1/orgs/"+p.org.ID.String()+"/tickets",
		fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Broken lamp","description":"entrance","visibility":"common"}`, s.Site, s.Category), p.adm), 201)
	jobs := p.e.jobRec.jobs()[jobs0:]
	if len(jobs) != 1 || jobs[0].EndpointID.String() != epID || jobs[0].OrgID != p.org.ID {
		t.Fatalf("jobs after creating a ticket = %+v", jobs)
	}
	log := p.deliveries(epID)
	if len(log) != 1 || str(log[0], "event_type") != "ticket.created" {
		t.Fatalf("log = %v", log)
	}
	for _, err := range p.deliverQueued(jobs0, 1, 8) {
		if err != nil {
			t.Fatal(err)
		}
	}
	var ev struct {
		Type string               `json:"type"`
		IDs  map[string]uuid.UUID `json:"ids"`
	}
	if err := json.Unmarshal(rcv.requests()[0].body, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "ticket.created" || ev.IDs["ticket_id"].String() != str(tk.Body, "id") || ev.IDs["site_id"] != s.Site || len(ev.IDs) != 2 {
		t.Errorf("event = %+v, ticket %s", ev, tk.Raw)
	}
	if strings.Contains(string(rcv.requests()[0].body), "Broken lamp") {
		t.Error("the payload carries record data")
	}

	// not subscribed: nothing is queued for a status change
	jobs1 := len(p.e.jobRec.jobs())
	ticket := s.ticket(p.w, t)
	trans := func() {
		p.must(p.call("POST", "/v1/orgs/"+p.org.ID.String()+"/tickets/"+ticket.String()+":transition", fmt.Sprintf(`{"status_id":%q,"comment":"fixed"}`, s.Done), p.adm), 200)
	}
	trans()
	if n := len(p.e.jobRec.jobs()) - jobs1; n != 0 {
		t.Errorf("an unsubscribed event queued %d jobs", n)
	}
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"events":["ticket.status_changed"]}`, p.adm, "If-Match", `"1"`), 200)
	p.w.Exec(t, `UPDATE ticket SET status_id = $2 WHERE id = $1`, ticket, s.Open)
	trans()
	if n := len(p.e.jobRec.jobs()) - jobs1; n != 1 {
		t.Errorf("a subscribed ticket.status_changed queued %d jobs", n)
	}

	// an inactive endpoint is skipped
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"events":["ticket.created","visitor.approved"],"active":false}`, p.adm, "If-Match", `"2"`), 200)
	jobs2 := len(p.e.jobRec.jobs())
	p.must(p.call("POST", "/v1/orgs/"+p.org.ID.String()+"/tickets",
		fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Second","description":"x","visibility":"common"}`, s.Site, s.Category), p.adm), 201)
	if n := len(p.e.jobRec.jobs()) - jobs2; n != 0 {
		t.Errorf("an inactive endpoint got %d jobs", n)
	}
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"active":true}`, p.adm, "If-Match", `"3"`), 200)

	// dedupe: the same event twice is one delivery per endpoint
	evt := integrations.Event{ID: id.New(), Type: integrations.EventDecisionApplied, Version: 1, OrgID: p.org.ID, OccurredAt: time.Now(), IDs: map[string]uuid.UUID{"decision_id": id.New()}}
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"events":["decision.applied"]}`, p.adm, "If-Match", `"4"`), 200)
	jobs3 := len(p.e.jobRec.jobs())
	for i := 0; i < 2; i++ {
		if err := p.e.db.InTenantTx(ctx, p.org.ID, func(tx pgx.Tx) error { return p.e.integ.Emit(ctx, tx, evt) }); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(p.e.jobRec.jobs()) - jobs3; n != 1 {
		t.Errorf("the same event queued %d jobs, want 1", n)
	}

	// visitor.approved only for an approval; unmapped actions emit nothing
	p.must(p.call("PATCH", p.hooksURL("/"+epID), `{"events":["visitor.approved"]}`, p.adm, "If-Match", `"5"`), 200)
	record := func(action, status string) {
		t.Helper()
		err := p.e.db.InTenantTx(ctx, p.org.ID, func(tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Event{OrgID: p.org.ID, SiteID: &s.Site, Action: action, TargetType: "visitor_approval_request", TargetID: id.New(),
				Diff: audit.Changes{"status": {After: json.RawMessage(`"` + status + `"`)}}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	jobs4 := len(p.e.jobRec.jobs())
	record("visitor.decide", "denied")
	record("visitor.update", "approved")
	if n := len(p.e.jobRec.jobs()) - jobs4; n != 0 {
		t.Errorf("a denial or an unmapped action queued %d jobs", n)
	}
	record("visitor.decide", "approved")
	if n := len(p.e.jobRec.jobs()) - jobs4; n != 1 {
		t.Errorf("an approval queued %d jobs", n)
	}

	// the event exists if and only if the action commits

	rows := func() (n int) {
		_ = p.e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery WHERE endpoint_id = $1`, epID).Scan(&n)
		})
		return n
	}
	before := rows()
	_ = p.e.db.InTenantTx(ctx, p.org.ID, func(tx pgx.Tx) error {
		if err := audit.Record(ctx, tx, audit.Event{OrgID: p.org.ID, SiteID: &s.Site, Action: "visitor.decide", TargetType: "visitor_approval_request", TargetID: id.New(),
			Diff: audit.Changes{"status": {After: json.RawMessage(`"approved"`)}}}); err != nil {
			return err
		}
		return fmt.Errorf("the action failed after it was recorded")
	})
	if rows() != before {
		t.Error("a rolled-back action left a delivery behind")
	}
}
