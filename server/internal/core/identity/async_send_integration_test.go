package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// requestOTP asks for a code and waits for the background send to finish.
func (e *env) requestOTP(ctx context.Context, phone string, m identity.Meta) error {
	err := e.svc.RequestOTP(ctx, phone, m)
	e.drain()
	return err
}

// requestLink asks for a magic link and waits for the background send.
func (e *env) requestLink(ctx context.Context, email string, c identity.Client, m identity.Meta) error {
	err := e.svc.RequestMagicLink(ctx, email, c, m)
	e.drain()
	return err
}

func (e *env) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := e.svc.Drain(ctx); err != nil {
		panic(err)
	}
}

// gate lets a send block until released, so a test can observe that the
// request has already been answered while the send is still pending.
type gate struct {
	sms.Recorder
	release chan struct{}
}

func (g *gate) Send(ctx context.Context, to, text string) (string, error) {
	<-g.release
	return g.Recorder.Send(ctx, to, text)
}

type slowMail struct {
	mail.Recorder
	release chan struct{}
}

func (m *slowMail) Send(ctx context.Context, msg mail.Message) error {
	<-m.release
	return m.Recorder.Send(ctx, msg)
}

// The answer to a code or link request must not depend on whether the account
// exists: same result, and the caller does not wait for the lookup or the
// gateway, so neither a slow gateway nor an outage tells known from unknown.
func TestIntegrationRequestsAnswerImmediatelyAndIdentically(t *testing.T) {
	g := &gate{release: make(chan struct{})}
	sm := &slowMail{release: make(chan struct{})}
	e := newEnv(t, func(c *identity.Config) { c.SMS, c.Mail = g, sm })
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Email: "known@example.org", Phone: "+905321234567"})

	// every send blocks on the gateway until released
	done := make(chan error, 4)
	go func() {
		done <- e.svc.RequestOTP(ctx, "+905321234567", identity.Meta{IP: "198.51.100.1"})
		done <- e.svc.RequestOTP(ctx, "+905559998877", identity.Meta{IP: "198.51.100.2"})
		done <- e.svc.RequestMagicLink(ctx, "known@example.org", identity.ClientWeb, identity.Meta{IP: "198.51.100.3"})
		done <- e.svc.RequestMagicLink(ctx, "unknown@example.org", identity.ClientWeb, identity.Meta{IP: "198.51.100.4"})
	}()
	for i := range 4 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("request %d: %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("request %d waited for the gateway", i)
		}
	}
	close(g.release)
	close(sm.release)
	e.drain()
	if n := len(g.Messages()); n != 1 {
		t.Errorf("sms sent = %d, want 1 (known number only)", n)
	}
	if n := len(sm.Messages()); n != 1 {
		t.Errorf("mails sent = %d, want 1 (known address only)", n)
	}
}

func TestIntegrationMailFailureIsNotReportedButLogged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Email: "known@example.org"})
	e.mail.Err = errors.New("smtp: connection refused")

	if err := e.requestLink(ctx, "known@example.org", identity.ClientWeb, meta); err != nil {
		t.Fatalf("known address: %v", err)
	}
	if err := e.requestLink(ctx, "unknown@example.org", identity.ClientWeb, meta); err != nil {
		t.Fatalf("unknown address: %v", err)
	}
	ds, err := e.svc.RecentDeliveries(ctx, 10, true)
	if err != nil || len(ds) != 1 {
		t.Fatalf("delivery log = %+v, %v", ds, err)
	}
	if d := ds[0]; d.Status != "failed" || d.Channel != "email" || d.Purpose != "magic_link" || d.Target != "k***@example.org" {
		t.Errorf("entry = %+v", d)
	}
	// the other limits still apply and still do not depend on the account
	wantProblem(t, e.svc.RequestMagicLink(ctx, "", identity.ClientWeb, meta), 422, problem.CodeValidationFailed)
}
