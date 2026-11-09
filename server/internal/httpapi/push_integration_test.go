package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/push"
)

// scalarOf runs a query as the platform role (no RLS) and returns its one value.
func scalarOf[T any](t *testing.T, e *opsEnv, sql string, args ...any) T {
	t.Helper()
	var v T
	err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql, args...).Scan(&v)
	})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

type deviceJSON struct {
	ID         uuid.UUID `json:"id"`
	PushKind   string    `json:"push_kind"`
	RelayID    *string   `json:"relay_id"`
	AppVersion *string   `json:"app_version"`
	Locale     *string   `json:"locale"`
	Enabled    bool      `json:"enabled"`
}

func (e *opsEnv) register(t *testing.T, token, body string) (deviceJSON, string) {
	t.Helper()
	rec := e.call(t, token, "POST", "/v1/me/devices", body)
	mustStatus(t, rec, 200)
	return jsonOf[deviceJSON](t, rec), rec.Body.String()
}

func TestIntegrationDeviceRegistrationEncryptsAndReRegisters(t *testing.T) {
	e := newOpsEnv(t)
	s := newD2Site(e.w, e.w.OrgA, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	bob := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Bob")

	d1, raw := e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-token-ann-1","relay_id":"official","app_version":"1.0.0","locale":"tr"}`)
	if strings.Contains(raw, "fcm-token-ann-1") || strings.Contains(raw, "token") {
		t.Fatalf("the response must not carry the token: %s", raw)
	}
	if d1.PushKind != "fcm" || d1.RelayID == nil || *d1.RelayID != "official" || !d1.Enabled {
		t.Fatalf("device = %+v", d1)
	}
	// stored sealed, not in clear
	sealed := scalarOf[[]byte](t, e, `SELECT token_enc FROM device WHERE id = $1`, d1.ID)
	if bytes.Contains(sealed, []byte("fcm-token-ann-1")) || !bytes.HasPrefix(sealed, []byte("k1:")) {
		t.Fatalf("token is not sealed: %q", sealed)
	}
	targets, err := e.devices.ForUsers(context.Background(), []uuid.UUID{ann.user})
	if err != nil || len(targets) != 1 || targets[0].Token != "fcm-token-ann-1" || targets[0].RelayID != "official" {
		t.Fatalf("opened targets = %+v, %v", targets, err)
	}

	// registering the same token again updates the row
	d2, _ := e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-token-ann-1","relay_id":"official","app_version":"1.1.0","locale":"en"}`)
	if d2.ID != d1.ID || d2.AppVersion == nil || *d2.AppVersion != "1.1.0" || d2.Locale == nil || *d2.Locale != "en" {
		t.Fatalf("re-register = %+v, first %+v", d2, d1)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM device WHERE user_id = $1`, ann.user); n != 1 {
		t.Fatalf("%d rows for one (user, token)", n)
	}
	// a disabled device is enabled again by registering it
	e.w.Exec(t, `UPDATE device SET disabled_at = now(), disabled_reason = 'invalid_token' WHERE id = $1`, d1.ID)
	if d3, _ := e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-token-ann-1","relay_id":"official"}`); !d3.Enabled {
		t.Fatalf("re-registered device is still disabled: %+v", d3)
	}
	// another user may hold the same token: one row per (user, token)
	d4, _ := e.register(t, bob.token, `{"push_kind":"fcm","token":"fcm-token-ann-1","relay_id":"official"}`)
	if d4.ID == d1.ID {
		t.Fatal("two users share a device row")
	}
	// a second token of the same user is a second device
	e.register(t, ann.token, `{"push_kind":"apns","token":"apns-token-ann","relay_id":"official"}`)
	if n := scalarOf[int](t, e, `SELECT count(*) FROM device WHERE user_id = $1`, ann.user); n != 2 {
		t.Fatalf("%d devices, want 2", n)
	}

	// validation
	for name, body := range map[string]string{
		"unknown kind":          `{"push_kind":"sms","token":"x","relay_id":"official"}`,
		"fcm needs a relay":     `{"push_kind":"fcm","token":"x"}`,
		"bad relay id":          `{"push_kind":"fcm","token":"x","relay_id":"has space"}`,
		"unifiedpush not a url": `{"push_kind":"unifiedpush","token":"not a url"}`,
		"unifiedpush no host":   `{"push_kind":"unifiedpush","token":"https:///x"}`,
		"empty token":           `{"push_kind":"fcm","token":"","relay_id":"official"}`,
		"bad locale":            `{"push_kind":"fcm","token":"x","relay_id":"official","locale":"not a locale!"}`,
	} {
		if rec := e.call(t, ann.token, "POST", "/v1/me/devices", body); rec.Code != 422 {
			t.Errorf("%s: status %d: %s", name, rec.Code, rec.Body)
		}
	}
	mustStatus(t, e.call(t, "", "POST", "/v1/me/devices", `{"push_kind":"fcm","token":"x","relay_id":"official"}`), 401)

	// delete: only the owner's
	mustStatus(t, e.call(t, bob.token, "DELETE", "/v1/me/devices/"+d1.ID.String(), ""), 404)
	mustStatus(t, e.call(t, ann.token, "DELETE", "/v1/me/devices/"+d1.ID.String(), ""), 204)
	mustStatus(t, e.call(t, ann.token, "DELETE", "/v1/me/devices/"+d1.ID.String(), ""), 404)
	if n := scalarOf[int](t, e, `SELECT count(*) FROM platform_audit_event WHERE action LIKE 'device.%'`); n < 5 {
		t.Fatalf("%d device audit events", n)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM platform_audit_event WHERE action LIKE 'device.%' AND diff::text LIKE '%token%'`); n != 0 {
		t.Fatal("a device audit event mentions the token")
	}
}

func TestIntegrationSignOutDeletesTheDevicesOfThatSession(t *testing.T) {
	e := newOpsEnv(t)
	s := newD2Site(e.w, e.w.OrgA, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	second := e.w.Token(t, authztest.Actor{UserID: ann.user}) // another session of the same user

	e.register(t, ann.token, `{"push_kind":"fcm","token":"tok-session-1","relay_id":"official"}`)
	e.register(t, second, `{"push_kind":"fcm","token":"tok-session-2","relay_id":"official"}`)
	count := func() int { return scalarOf[int](t, e, `SELECT count(*) FROM device WHERE user_id = $1`, ann.user) }
	if count() != 2 {
		t.Fatalf("%d devices, want 2", count())
	}
	mustStatus(t, e.call(t, ann.token, "DELETE", "/v1/me/sessions/current", ""), 204)
	if count() != 1 {
		t.Fatalf("after signing out the first session: %d devices, want 1 (the other session's)", count())
	}
	if tok := scalarOf[string](t, e, `SELECT 'x' FROM device WHERE user_id = $1 AND session_family_id IS NOT NULL`, ann.user); tok == "" {
		t.Fatal("the other session's device is gone")
	}
	mustStatus(t, e.call(t, second, "DELETE", "/v1/me/sessions", ""), 204)
	if count() != 0 {
		t.Fatalf("after signing out everywhere: %d devices", count())
	}
}

// pushPayloadKeys is the allowlist of backend section 12.
var pushPayloadKeys = []string{"badge", "collapse_key", "instance_id", "notification_id", "org_id", "type"}

func keysOf(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

type relayBody struct {
	Messages []map[string]any `json:"messages"`
}

func TestIntegrationPushThroughRelayAndUnifiedPush(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-ann","relay_id":"official"}`)
	e.register(t, ann.token, `{"push_kind":"apns","token":"apns-ann","relay_id":"official"}`)
	e.register(t, ann.token, fmt.Sprintf(`{"push_kind":"unifiedpush","token":%q}`, e.relay.endpoint("ann")))
	// a person without a device gets no push
	cy := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Cy")

	job := func(key string) jobs.NotifyArgs {
		return jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: key,
			Recipients: []jobs.Recipient{{PersonID: &ann.person}, {PersonID: &cy.person}},
			Payload:    map[string]any{"title": "Leaking secret-title pipe", "status": "Done secret-status"}}
	}
	e.deliver(t, job("ticket:1"))

	calls, ups := e.relay.take()
	if len(calls) != 1 {
		t.Fatalf("%d relay requests, want 1 (one batch for both devices of the relay)", len(calls))
	}
	// authenticated like the infra plan: Relay-HMAC <server_id>:<ts>:<nonce>:<hmac>
	parts := strings.Split(strings.TrimPrefix(calls[0].Auth, "Relay-HMAC "), ":")
	if len(parts) != 4 || parts[0] != "srv-test" {
		t.Fatalf("authorization = %q", calls[0].Auth)
	}
	if want := push.RelayAuth("srv-test", testRelaySecret, parts[1], parts[2], calls[0].Body); want != calls[0].Auth {
		t.Fatalf("hmac mismatch:\n got %s\nwant %s", calls[0].Auth, want)
	}
	var rb relayBody
	if err := json.Unmarshal(calls[0].Body, &rb); err != nil || len(rb.Messages) != 2 {
		t.Fatalf("relay body: %s (%v)", calls[0].Body, err)
	}
	notificationID := scalarOf[uuid.UUID](t, e, `SELECT id FROM notification WHERE org_id = $1 AND person_id = $2`, org.ID, ann.person)
	instanceID := scalarOf[uuid.UUID](t, e, `SELECT instance_id FROM instance_setting`)
	for _, m := range rb.Messages {
		kind := m["push_kind"].(string)
		topKeys := []string{"app_id", "data", "priority", "push_kind", "token", "ttl_seconds"}
		if kind == "apns" {
			topKeys = []string{"apns", "app_id", "data", "priority", "push_kind", "token", "ttl_seconds"}
		}
		if got := keysOf(m); strings.Join(got, ",") != strings.Join(topKeys, ",") {
			t.Errorf("%s message keys = %v, want %v", kind, got, topKeys)
		}
		if m["app_id"] != "org.test.app" || (kind == "fcm" && m["token"] != "fcm-ann") || (kind == "apns" && m["token"] != "apns-ann") {
			t.Errorf("message = %v", m)
		}
		data := m["data"].(map[string]any)
		if got := keysOf(data); strings.Join(got, ",") != strings.Join(pushPayloadKeys, ",") {
			t.Errorf("data keys = %v, want exactly %v", got, pushPayloadKeys)
		}
		if data["org_id"] != org.ID.String() || data["notification_id"] != notificationID.String() || data["type"] != "ticket.updated" ||
			data["collapse_key"] != "ticket:1" || data["badge"] != float64(1) || data["instance_id"] != instanceID.String() {
			t.Errorf("data = %v", data)
		}
		if kind == "apns" {
			apns := m["apns"].(map[string]any)
			if apns["mutable_content"] != true || apns["generic_title_key"] != "push.ticket_updated" {
				t.Errorf("apns = %v", apns)
			}
		}
	}
	// content-free: no title, status, name or address anywhere in the relay or endpoint traffic
	for _, b := range append([][]byte{calls[0].Body}, ups...) {
		for _, secret := range []string{"secret-title", "secret-status", "Ann", "res.example.test", "+90555"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Errorf("personal data %q in a push request: %s", secret, b)
			}
		}
	}
	// UnifiedPush: the same payload, posted straight to the endpoint
	if len(ups) != 1 {
		t.Fatalf("%d endpoint posts, want 1", len(ups))
	}
	var up map[string]any
	if err := json.Unmarshal(ups[0], &up); err != nil || strings.Join(keysOf(up), ",") != strings.Join(pushPayloadKeys, ",") || up["notification_id"] != notificationID.String() {
		t.Fatalf("unifiedpush body %s (%v)", ups[0], err)
	}
	// delivery records: push sent for Ann, none for Cy
	if st := scalarOf[string](t, e, `SELECT d.status FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id
		WHERE n.person_id = $1 AND d.channel = 'push'`, ann.person); st != "sent" {
		t.Fatalf("push delivery status = %s", st)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id
		WHERE n.person_id = $1 AND d.channel = 'push'`, cy.person); n != 0 {
		t.Fatalf("a person without a device has %d push deliveries", n)
	}

	// the badge is the unread count; a second notification makes it 2
	e.deliver(t, job("ticket:2"))
	calls, _ = e.relay.take()
	if len(calls) != 1 || !bytes.Contains(calls[0].Body, []byte(`"badge":2`)) {
		t.Fatalf("second push: %d calls, %s", len(calls), func() string {
			if len(calls) > 0 {
				return string(calls[0].Body)
			}
			return ""
		}())
	}
	// running the same job again sends nothing twice
	e.deliver(t, job("ticket:2"))
	if calls, ups = e.relay.take(); len(calls) != 0 || len(ups) != 0 {
		t.Fatalf("a repeated job pushed again: %d relay, %d endpoint", len(calls), len(ups))
	}
}

func TestIntegrationPushDeadTokenDisablesTheDevice(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	dead, _ := e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-dead","relay_id":"official"}`)
	e.relay.set(func(f *fakeRelay) { f.invalid["fcm-dead"] = true })

	job := func(key string) jobs.NotifyArgs {
		return jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: key,
			Recipients: []jobs.Recipient{{PersonID: &ann.person}}, Payload: map[string]any{"title": "t", "status": "s"}}
	}
	e.deliver(t, job("dead:1")) // the only device is invalid: skipped, not retried
	if calls, _ := e.relay.take(); len(calls) != 1 {
		t.Fatalf("%d relay requests", len(calls))
	}
	if !scalarOf[bool](t, e, `SELECT disabled_at IS NOT NULL FROM device WHERE id = $1`, dead.ID) {
		t.Fatal("the invalid device is not disabled")
	}
	if reason := scalarOf[string](t, e, `SELECT disabled_reason FROM device WHERE id = $1`, dead.ID); reason != "invalid_token" {
		t.Fatalf("reason = %q", reason)
	}
	if st := scalarOf[string](t, e, `SELECT d.status FROM notification_delivery d WHERE d.channel = 'push' AND d.org_id = $1`, org.ID); st != "skipped" {
		t.Fatalf("delivery status = %s, want skipped", st)
	}
	// later notifications do not reach the relay for that device
	e.deliver(t, job("dead:2"))
	if calls, _ := e.relay.take(); len(calls) != 0 {
		t.Fatalf("a disabled device was pushed to: %d requests", len(calls))
	}
	// registering the token again enables it
	if d, _ := e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-dead","relay_id":"official"}`); !d.Enabled {
		t.Fatal("not enabled again")
	}
}

func TestIntegrationPushRetriesAreLeftToTheJobAndUnknownRelaysSkip(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-ann","relay_id":"official"}`)
	job := jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: "retry:1",
		Recipients: []jobs.Recipient{{PersonID: &ann.person}}, Payload: map[string]any{"title": "t", "status": "s"}}

	e.relay.set(func(f *fakeRelay) { f.status = 503 })
	if err := e.ops.notifications.Deliver(context.Background(), job, 1); err == nil {
		t.Fatal("a relay outage must fail the job so River retries it")
	}
	if st := scalarOf[string](t, e, `SELECT status FROM notification_delivery WHERE channel = 'push' AND org_id = $1`, org.ID); st != "failed" {
		t.Fatalf("delivery status = %s, want failed", st)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM notification WHERE org_id = $1 AND person_id = $2`, org.ID, ann.person); n != 1 {
		t.Fatalf("%d notifications", n)
	}
	e.relay.set(func(f *fakeRelay) { f.status = 0 })
	e.relay.take()
	if err := e.ops.notifications.Deliver(context.Background(), job, 2); err != nil {
		t.Fatal(err)
	}
	if calls, _ := e.relay.take(); len(calls) != 1 {
		t.Fatalf("%d relay requests on the retry", len(calls))
	}
	if st := scalarOf[string](t, e, `SELECT status FROM notification_delivery WHERE channel = 'push' AND org_id = $1`, org.ID); st != "sent" {
		t.Fatalf("delivery status after retry = %s", st)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM notification WHERE org_id = $1 AND person_id = $2`, org.ID, ann.person); n != 1 {
		t.Fatalf("the retry duplicated the notification: %d", n)
	}

	// a relay id this server has no entry for: skipped, no request, no retry
	bob := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Bob")
	e.register(t, bob.token, `{"push_kind":"fcm","token":"fcm-bob","relay_id":"somewhere-else"}`)
	job2 := jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: "retry:2",
		Recipients: []jobs.Recipient{{PersonID: &bob.person}}, Payload: map[string]any{"title": "t", "status": "s"}}
	e.deliver(t, job2)
	if calls, _ := e.relay.take(); len(calls) != 0 {
		t.Fatalf("a request went to a relay that is not configured: %d", len(calls))
	}
	if why := scalarOf[string](t, e, `SELECT d.last_error FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id
		WHERE n.person_id = $1 AND d.channel = 'push'`, bob.person); why != "no_relay" {
		t.Fatalf("reason = %q", why)
	}

	// a failing UnifiedPush endpoint is retried too; a gone one (410) disables the device
	cy := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Cy")
	up, _ := e.register(t, cy.token, fmt.Sprintf(`{"push_kind":"unifiedpush","token":%q}`, e.relay.endpoint("cy")))
	e.relay.set(func(f *fakeRelay) { f.upStatus = 500 })
	job3 := jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: "retry:3",
		Recipients: []jobs.Recipient{{PersonID: &cy.person}}, Payload: map[string]any{"title": "t", "status": "s"}}
	if err := e.ops.notifications.Deliver(context.Background(), job3, 1); err == nil {
		t.Fatal("an endpoint failure must fail the job")
	}
	e.relay.set(func(f *fakeRelay) { f.upStatus = 410 })
	if err := e.ops.notifications.Deliver(context.Background(), job3, 2); err != nil {
		t.Fatal(err)
	}
	if !scalarOf[bool](t, e, `SELECT disabled_at IS NOT NULL FROM device WHERE id = $1`, up.ID) {
		t.Fatal("a gone endpoint (410) did not disable the device")
	}
}

func TestIntegrationPushRelayRetryableAndPermanentErrors(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	e.register(t, ann.token, `{"push_kind":"fcm","token":"fcm-flaky","relay_id":"official"}`)
	job := jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: "err:1",
		Recipients: []jobs.Recipient{{PersonID: &ann.person}}, Payload: map[string]any{"title": "t", "status": "s"}}
	e.relay.set(func(f *fakeRelay) { f.flaky["fcm-flaky"] = true })
	if err := e.ops.notifications.Deliver(context.Background(), job, 1); err == nil {
		t.Fatal("a retryable relay error must fail the job")
	}
	// the same message later refused for good: dropped, device kept, no retry
	e.relay.set(func(f *fakeRelay) { delete(f.flaky, "fcm-flaky"); f.rejected["fcm-flaky"] = true })
	e.deliver(t, job)
	if why := scalarOf[string](t, e, `SELECT last_error FROM notification_delivery WHERE channel = 'push' AND org_id = $1`, org.ID); why != "rejected" {
		t.Fatalf("reason = %q", why)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM device WHERE disabled_at IS NULL AND user_id = $1`, ann.user); n != 1 {
		t.Fatal("a non-retryable relay error must not disable the device")
	}
}

// An APNs sandbox/production mix-up on the relay makes every token invalid: the
// devices must survive, loudly.
func TestIntegrationPushInvalidTokenSurgeDoesNotPruneDevices(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	var rcpts []jobs.Recipient
	for i := 0; i < 12; i++ {
		r := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", fmt.Sprintf("Res%d", i))
		tok := fmt.Sprintf("apns-surge-%d", i)
		e.register(t, r.token, fmt.Sprintf(`{"push_kind":"apns","token":%q,"relay_id":"official"}`, tok))
		e.relay.set(func(f *fakeRelay) { f.invalid[tok] = true })
		p := r.person
		rcpts = append(rcpts, jobs.Recipient{PersonID: &p})
	}
	e.deliver(t, jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "announcement.published", Key: "surge:1", Recipients: rcpts,
		Channels: []string{"in_app", "push"}, Payload: map[string]any{"title": "t"}})
	if n := scalarOf[int](t, e, `SELECT count(*) FROM device WHERE disabled_at IS NOT NULL`); n != 0 {
		t.Fatalf("%d devices disabled during an invalid_token surge", n)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM notification_delivery WHERE channel = 'push' AND last_error = 'invalid_token_surge'`); n != 12 {
		t.Fatalf("%d push deliveries marked as a surge, want 12", n)
	}
	if n := scalarOf[int](t, e, `SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'push.invalid_token_surge'`, org.ID); n != 1 {
		t.Fatalf("%d surge audit events, want 1", n)
	}
}

func TestIntegrationDiscoveryServesThePendingRelayChallenge(t *testing.T) {
	e := newOpsEnv(t)
	ch := instance.PGChallenges{DB: e.db}
	read := func() *string {
		rec := e.call(t, "", "GET", "/.well-known/openbekci.json", "")
		mustStatus(t, rec, 200)
		return jsonOf[struct {
			RelayEnrollment struct {
				Challenge *string `json:"challenge"`
			} `json:"relay_enrollment"`
		}](t, rec).RelayEnrollment.Challenge
	}
	if read() != nil {
		t.Fatal("a challenge without an enrollment")
	}
	// the discovery handler is built from deps.Instance: wire the challenge source like the server does
	e.deps.Instance.WithChallenges(ch)
	if err := ch.Set(context.Background(), "challenge-0123456789abcdef", 600); err != nil {
		t.Fatal(err)
	}
	if got := read(); got == nil || *got != "challenge-0123456789abcdef" {
		t.Fatalf("challenge = %v", got)
	}
	if err := ch.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if read() != nil {
		t.Fatal("the challenge outlived the enrollment")
	}
	if err := ch.Set(context.Background(), "expired-challenge-0123456", -1); err != nil {
		t.Fatal(err)
	}
	if read() != nil {
		t.Fatal("an expired challenge is served")
	}
}

func TestIntegrationGetNotification(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	bob := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Bob")
	e.deliver(t, jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "ticket.updated", Key: "get:1",
		Recipients: []jobs.Recipient{{PersonID: &ann.person}}, Payload: map[string]any{"title": "Leak", "status": "Done"}})
	nid := scalarOf[uuid.UUID](t, e, `SELECT id FROM notification WHERE org_id = $1 AND person_id = $2`, org.ID, ann.person)

	rec := e.call(t, ann.token, "GET", orgURL(org, "/me/notifications/"+nid.String()), "")
	mustStatus(t, rec, 200)
	n := jsonOf[struct {
		ID      uuid.UUID `json:"id"`
		Type    string    `json:"type"`
		ReadAt  *string   `json:"read_at"`
		Payload struct {
			Title string `json:"title"`
		} `json:"payload"`
	}](t, rec)
	if n.ID != nid || n.Type != "ticket.updated" || n.ReadAt != nil || n.Payload.Title != "Leak" {
		t.Fatalf("notification = %s", rec.Body)
	}
	mustStatus(t, e.call(t, bob.token, "GET", orgURL(org, "/me/notifications/"+nid.String()), ""), 404)
	mustStatus(t, e.call(t, ann.token, "GET", orgURL(org, "/me/notifications/"+uuid.NewString()), ""), 404)
	mustStatus(t, e.call(t, "", "GET", orgURL(org, "/me/notifications/"+nid.String()), ""), 401)
	// the sibling routes still work
	mustStatus(t, e.call(t, ann.token, "GET", orgURL(org, "/me/notifications/unread-count"), ""), 200)
	mustStatus(t, e.call(t, ann.token, "POST", orgURL(org, "/me/notifications/"+nid.String()+":read"), ""), 200)
}
