package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// openIPLimits lifts the per-address limiter so that a test can hammer the
// per-account lockout from one address.
func openIPLimits(cfg *identity.Config) {
	l := identity.DefaultLimits(time.Now)
	l.AuthIP = ratelimit.New(10000, time.Millisecond)
	cfg.Limits = l
}

func TestIntegrationLockoutIsAtomicUnderParallelGuesses(t *testing.T) {
	e := newEnv(t, openIPLimits)
	ctx := context.Background()
	e.user(t, identity.CreateUserParams{Email: "race@example.org", Phone: "0532 000 11 22", Password: "correct horse battery"})

	var mu sync.Mutex
	evaluated, locked, other := 0, 0, 0
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// email and phone attempts share one counter
			ident := "race@example.org"
			if i%2 == 1 {
				ident = "+905320001122"
			}
			_, err := e.svc.PasswordSignIn(ctx, ident, "wrong password!!", meta)
			var pe *problem.Error
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.As(err, &pe) && pe.Code == problem.CodeInvalidGrant:
				evaluated++
			case errors.As(err, &pe) && pe.Code == problem.CodeRateLimited:
				locked++
			default:
				other++
			}
		}()
	}
	wg.Wait()
	if other != 0 || evaluated > 5 || evaluated+locked != 40 {
		t.Fatalf("evaluated %d, locked %d, other %d; want at most 5 evaluated (the threshold) of 40", evaluated, locked, other)
	}
	if _, err := e.svc.PasswordSignIn(ctx, "race@example.org", "correct horse battery", meta); err == nil {
		t.Fatal("the right password was accepted while locked")
	}
}
