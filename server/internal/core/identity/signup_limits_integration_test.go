package identity_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// A client that is limited by its address must not drain the join code's
// budget with the requests that were refused: other residents still get in.
func TestIntegrationSignUpRefusedRequestsDoNotDrainTheCodeBudget(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	lim := identity.Limits{SignUpCode: ratelimit.New(3, time.Hour), OTPSendIP: ratelimit.New(1, time.Hour)}
	svc := identity.NewService(d, identity.Config{
		InstanceName: "Test", BaseURL: "https://bekci.example.org", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SMS: &sms.Recorder{}, Limits: &lim, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	signUp := func(i int, ip string) error {
		return svc.RequestSignUp(ctx, identity.SignUpInput{Code: "org.secret", Phone: fmt.Sprintf("+9055500100%02d", i)}, identity.Meta{IP: ip})
	}
	if err := signUp(0, "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ { // the same address, refused by its own limit
		wantProblem(t, signUp(i, "198.51.100.1"), 429, problem.CodeRateLimited)
	}
	for i, ip := range []string{"198.51.100.2", "198.51.100.3"} { // two more residents still fit the code's budget of three
		if err := signUp(20+i, ip); err != nil {
			t.Fatalf("resident %d: %v", i, err)
		}
	}
	wantProblem(t, signUp(30, "198.51.100.4"), 429, problem.CodeRateLimited) // the code's budget is spent
	if err := svc.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}
