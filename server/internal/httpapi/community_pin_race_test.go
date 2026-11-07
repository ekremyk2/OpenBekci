package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// 40 parallel wrong PINs against one guard: the limit is 5 failures, so at most
// 5 may be evaluated (401), and the rest are refused (429) without being checked.
func TestIntegrationCommunityPINAttemptsAreSerialized(t *testing.T) {
	c := newCommEnv(t)
	guard := c.w.Actor("guard")
	c.want(c.req(c.manager, "PUT", c.path("/persons/"+guard.PersonID.String()+"/gate-pin"), `{"pin":"2468"}`), 204)

	const n = 40
	codes := make([]int, n)
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = c.req(c.guard, "POST", c.path("/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":"1357"}`, c.f.Site, guard.PersonID))
			codes[i] = recs[i].Code
		}()
	}
	wg.Wait()
	counts := map[int]int{}
	for _, code := range codes {
		counts[code]++
	}
	if counts[401] > 5 || counts[401]+counts[429] != n {
		t.Fatalf("outcomes = %v, want at most 5 evaluated (401) and the rest 429", counts)
	}
	var failures int
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM gate_pin_attempt WHERE person_id = $1 AND NOT ok`, guard.PersonID).Scan(&failures)
	})
	if failures > 5 {
		t.Fatalf("recorded failures = %d, want at most 5", failures)
	}
	// the right PIN is refused while the lock holds
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":"2468"}`, c.f.Site, guard.PersonID)), 429, "rate_limited")
}
