package httpapi

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

func (e *opsEnv) deliver(t *testing.T, a jobs.NotifyArgs) {
	t.Helper()
	if err := e.ops.notifications.Deliver(context.Background(), a, 1); err != nil {
		t.Fatal(err)
	}
}

// O5: sign-in codes and invitations go to the phone when there is one and to
// the email otherwise, never both; statements and payment decisions go in-app
// and by email, never by SMS; dues reminders keep the SMS.
func TestIntegrationDefaultChannelsFollowO5(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	withPhone := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	noPhone := id.New()
	e.w.Exec(t, `INSERT INTO person (id, org_id, display_name, contact) VALUES ($1, $2, 'Nophone', '{"email":"nophone@res.example.test"}')`, noPhone, org.ID)
	rcpt := func(p uuid.UUID) []jobs.Recipient { return []jobs.Recipient{{PersonID: &p}} }
	var seenMail, seenSMS int
	counts := func() (int, int) { // messages sent since the last call
		m, sm := len(e.ops.mail.Messages()), len(e.ops.sms.Messages())
		dm, ds := m-seenMail, sm-seenSMS
		seenMail, seenSMS = m, sm
		return dm, ds
	}

	base := func(typ, key string, p uuid.UUID) jobs.NotifyArgs {
		return jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: typ, Key: key, Recipients: rcpt(p),
			Payload: map[string]any{"code": "123456", "minutes": 10, "inviter": "Manager", "link": "https://x.test/i", "amount": "TRY 5", "unit": "A-1", "unit_door": "1"}}
	}
	e.deliver(t, base("auth.sign_in_code", "signin:1", withPhone.person))
	if m, sm := counts(); m != 0 || sm != 1 {
		t.Fatalf("sign-in code to a person with a phone: %d emails, %d SMS", m, sm)
	}
	e.deliver(t, base("auth.sign_in_code", "signin:2", noPhone))
	if m, sm := counts(); m != 1 || sm != 0 {
		t.Fatalf("sign-in code to a person without a phone: %d emails, %d SMS", m, sm)
	}
	e.deliver(t, base("invitation.sent", "invite:1", withPhone.person))
	if m, sm := counts(); m != 0 || sm != 1 {
		t.Fatalf("invitation: %d emails, %d SMS", m, sm)
	}
	for _, typ := range []string{"statement.sent", "payment.confirmed", "payment.rejected"} {
		e.deliver(t, base(typ, typ+":1", withPhone.person))
		if m, sm := counts(); m != 1 || sm != 0 {
			t.Fatalf("%s: %d emails, %d SMS; want email only", typ, m, sm)
		}
	}
	e.deliver(t, base("charge.due_soon", "due:1", withPhone.person))
	if m, sm := counts(); m != 0 || sm != 1 {
		t.Fatalf("dues reminder: %d emails, %d SMS; want SMS only", m, sm)
	}
}

// Two workers running the same event at once send each message once.
func TestIntegrationConcurrentDeliveriesSendOnce(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	var people []jobs.Recipient
	for i := 0; i < 5; i++ {
		r := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Res")
		p := r.person
		people = append(people, jobs.Recipient{PersonID: &p})
	}
	a := jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "charge.due_soon", Key: "due:race", Recipients: people,
		Payload: map[string]any{"amount": "TRY 5", "unit": "A-1", "due_on": "2026-12-01"}}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.ops.notifications.Deliver(context.Background(), a, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(e.ops.sms.Messages()); n != 5 {
		t.Fatalf("%d SMS sent for 5 recipients", n)
	}
	// the same key under another type is another event, not a duplicate; the
	// same event again is a no-op
	b := a
	b.Type = "charge.overdue"
	e.deliver(t, b)
	e.deliver(t, a)
	e.deliver(t, b)
	if n := len(e.ops.sms.Messages()); n != 10 {
		t.Fatalf("%d SMS sent, want 10 (5 per type)", n)
	}
}
