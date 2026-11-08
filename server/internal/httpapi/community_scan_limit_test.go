package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// A guard session may scan 30 times a minute; the 31st is 429 with Retry-After.
// Every scan that matched nothing is audited with the console, never the text.
func TestIntegrationCommunityScanRateLimitAndAudit(t *testing.T) {
	c := newCommEnv(t)
	secret := "obk1.zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	scan := func(text string) int {
		return c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, text)).Code
	}
	for i := range 28 {
		if got := scan(secret); got != 200 {
			t.Fatalf("scan %d = %d", i, got)
		}
	}
	if got := scan("garbage"); got != 200 { // unparseable text is audited too
		t.Fatalf("scan of garbage = %d", got)
	}
	if got := scan(secret); got != 200 {
		t.Fatalf("30th scan = %d", got)
	}
	rec := c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, secret))
	c.problemCode(rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}

	var failed, withConsole, leaked int
	err := c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE diff ? 'console_id'), count(*) FILTER (WHERE diff::text LIKE '%zzzz%' OR diff::text LIKE '%garbage%')
			FROM audit_event WHERE org_id = $1 AND action = 'gate.scan_failed'`, c.org.ID).Scan(&failed, &withConsole, &leaked)
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed != 30 || withConsole != 30 || leaked != 0 {
		t.Fatalf("audit of failed scans: rows=%d with console=%d with scanned text=%d, want 30/30/0", failed, withConsole, leaked)
	}
}
