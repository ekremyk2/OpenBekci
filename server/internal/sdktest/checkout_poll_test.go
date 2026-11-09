package sdktest_test

import (
	"sync"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/checkout"
)

// The status poll asks the provider at most once per interval per checkout, however many polls arrive at once:
// the throttle is a claim in the database.
func TestCheckoutStatusPollIsThrottledInTheDatabase(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	c := e.create("owner")
	f := e.withFlaky()

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.chk.Get(e.Ctx(), e.Env.OrgID, c.ID); err != nil {
				t.Errorf("poll: %v", err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 3; i++ {
		e.get(c)
	}
	if f.status != 1 {
		t.Fatalf("the provider was asked %d times by 15 polls, want 1", f.status)
	}
	// a second service instance (another process) shares the claim through the database
	e2 := checkout.NewService(checkout.Config{DB: e.Env.DB, Platform: e.Env.DB, Payments: e.Payments, Providers: f, BaseURL: "https://app.example.test"})
	if _, err := e2.Get(e.Ctx(), e.Env.OrgID, c.ID); err != nil || f.status != 1 {
		t.Fatalf("a second process asked the provider (%d): %v", f.status, err)
	}
	// once the interval has passed the poll goes through again
	e.Env.Exec(`UPDATE checkout SET last_polled_at = now() - interval '10 seconds' WHERE id = $1`, c.ID)
	e.get(c)
	if f.status != 2 {
		t.Errorf("after the interval the provider was asked %d times, want 2", f.status)
	}
}
