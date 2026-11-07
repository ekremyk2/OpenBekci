package identity_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func fromIP(n int) identity.Meta { return identity.Meta{IP: fmt.Sprintf("198.51.100.%d", n)} }

// One address that guesses is locked out of the account after five failures,
// but the owner, from another address, is not: an attacker cannot lock the
// owner out from a single address.
func TestIntegrationLockoutIsPerAddressFirst(t *testing.T) {
	e := newEnv(t, openIPLimits)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Email: "owner@example.org", Password: "correct horse battery"})

	for range 5 {
		_, err := e.svc.PasswordSignIn(ctx, "owner@example.org", "wrong password!!", fromIP(1))
		wantProblem(t, err, 401, problem.CodeInvalidGrant)
	}
	_, err := e.svc.PasswordSignIn(ctx, "owner@example.org", "correct horse battery", fromIP(1))
	wantProblem(t, err, 429, problem.CodeRateLimited)
	// the owner is not affected
	if _, err := e.svc.PasswordSignIn(ctx, "owner@example.org", "correct horse battery", fromIP(2)); err != nil {
		t.Fatalf("owner from another address: %v", err)
	}
}

// Failures from many addresses add up to an account-wide lock at the higher
// threshold (five times the per-address one), and not before.
func TestIntegrationLockoutAccountWideThreshold(t *testing.T) {
	e := newEnv(t, openIPLimits)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Email: "wide@example.org", Password: "correct horse battery"})

	spread := func(failures int) {
		t.Helper()
		for i := range failures {
			// five failures per address, never enough to lock one address
			_, err := e.svc.PasswordSignIn(ctx, "wide@example.org", "wrong password!!", fromIP(10+i/5))
			wantProblem(t, err, 401, problem.CodeInvalidGrant)
		}
	}
	spread(24)
	if _, err := e.svc.PasswordSignIn(ctx, "wide@example.org", "correct horse battery", fromIP(99)); err != nil {
		t.Fatalf("24 failures from five addresses locked the account: %v", err)
	}
	e.clk.Advance(time.Second) // the success resets the counters
	spread(25)
	_, err := e.svc.PasswordSignIn(ctx, "wide@example.org", "correct horse battery", fromIP(99))
	pe := wantProblemPE(t, err, 429, problem.CodeRateLimited)
	if pe.RetryAfter <= 0 || pe.RetryAfter > 30*time.Second {
		t.Errorf("retry after = %v", pe.RetryAfter)
	}
	// and it ends
	e.clk.Advance(31 * time.Second)
	if _, err := e.svc.PasswordSignIn(ctx, "wide@example.org", "correct horse battery", fromIP(99)); err != nil {
		t.Fatalf("after the account-wide lock: %v", err)
	}
}

func TestIntegrationSendDailyAndHourlyBudgets(t *testing.T) {
	e := newEnv(t, func(c *identity.Config) { c.SendDailyBudget, c.SMSHourlyBudget = 2, 3 })
	ctx := context.Background()
	for _, p := range []string{"+905321110001", "+905321110002", "+905321110003", "+905321110004"} {
		e.user(t, identity.CreateUserParams{Phone: p})
	}
	// daily: two per identifier, whatever the short-term buckets say; unknown numbers count too
	for range 2 {
		if err := e.requestOTP(ctx, "+905321110001", fromIP(1)); err != nil {
			t.Fatal(err)
		}
		e.clk.Advance(3 * time.Minute)
	}
	pe := wantProblemPE(t, e.requestOTP(ctx, "+905321110001", fromIP(1)), 429, problem.CodeRateLimited)
	if pe.RetryAfter < time.Hour {
		t.Errorf("daily retry after = %v, want hours", pe.RetryAfter)
	}
	for range 2 {
		if err := e.requestOTP(ctx, "+905559990000", fromIP(1)); err != nil {
			t.Fatal(err)
		}
	}
	wantProblem(t, e.requestOTP(ctx, "+905559990000", fromIP(1)), 429, problem.CodeRateLimited)
	e.clk.Advance(13 * time.Hour)
	if err := e.requestOTP(ctx, "+905321110001", fromIP(1)); err != nil {
		t.Fatalf("after the day: %v", err)
	}

	// hourly, per instance: three sends in the hour (two were spent above in the first 6 min...)
	e2 := newEnv(t, func(c *identity.Config) { c.SMSHourlyBudget = 2 })
	for _, p := range []string{"+905321110001", "+905321110002", "+905321110003"} {
		e2.user(t, identity.CreateUserParams{Phone: p})
		if err := e2.requestOTP(ctx, p, fromIP(2)); err != nil {
			t.Fatal(err) // the requester is never told
		}
	}
	if n := len(e2.sms.Messages()); n != 2 {
		t.Errorf("sent %d SMS, want the 2 of the hourly budget", n)
	}
	ds, err := e2.svc.RecentDeliveries(ctx, 10, true)
	if err != nil || len(ds) != 1 || ds[0].Status != "dropped" || ds[0].Channel != "sms" {
		t.Fatalf("delivery log = %+v, %v", ds, err)
	}
	// unknown numbers do not use the instance budget up
	e3 := newEnv(t, func(c *identity.Config) { c.SMSHourlyBudget = 1 })
	for i := range 3 {
		if err := e3.requestOTP(ctx, fmt.Sprintf("+90555999000%d", i), fromIP(3)); err != nil {
			t.Fatal(err)
		}
	}
	e3.user(t, identity.CreateUserParams{Phone: "+905321110001"})
	if err := e3.requestOTP(ctx, "+905321110001", fromIP(3)); err != nil || len(e3.sms.Messages()) != 1 {
		t.Fatalf("real send after unknown requests: %v, %d sent", err, len(e3.sms.Messages()))
	}
}
