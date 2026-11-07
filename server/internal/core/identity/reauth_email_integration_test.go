package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func lastMail(t *testing.T, e *env) mail.Message {
	t.Helper()
	m, ok := e.mail.Last()
	if !ok {
		t.Fatal("no mail was sent")
	}
	return m
}

func TestIntegrationReauthEmailCodeExpiryAndNewerCode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "mailonly@example.org"})
	ws, _ := e.svc.IssueWebSession(ctx, u.ID, meta)
	p, _ := e.svc.Authenticate(ctx, identity.KindWebCookie, ws.Token)

	if err := e.svc.RequestReauthEmailCode(ctx, p, meta); err != nil {
		t.Fatal(err)
	}
	first := sixDigits.FindString(lastMail(t, e).Body)
	e.clk.Advance(2 * time.Minute)
	if err := e.svc.RequestReauthEmailCode(ctx, p, meta); err != nil {
		t.Fatal(err)
	}
	second := sixDigits.FindString(lastMail(t, e).Body)
	// a newer code voids the older one
	if first != second {
		_, err := e.svc.Reauth(ctx, p, identity.ReauthEmailCode, first, meta)
		wantProblem(t, err, 401, problem.CodeInvalidGrant)
	}
	// it expires after the OTP lifetime
	e.clk.Advance(identity.DefaultOTPTTL + time.Second)
	_, err := e.svc.Reauth(ctx, p, identity.ReauthEmailCode, second, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	if err := e.svc.RequestReauthEmailCode(ctx, p, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reauth(ctx, p, identity.ReauthEmailCode, sixDigits.FindString(lastMail(t, e).Body), meta); err != nil {
		t.Fatalf("fresh code: %v", err)
	}
}
