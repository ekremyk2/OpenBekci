package httpapi

import (
	"fmt"
	"testing"
)

// The day before the new rate is a civil date of the site: in a time zone west
// of UTC a rate that lasted a single day closes on that same day, not before it began.
func TestIntegrationDecisionEffectDuesRateSiteTimezone(t *testing.T) {
	x := newEffectsEnv(t)
	x.w.Exec(t, `UPDATE site SET timezone = 'America/Los_Angeles' WHERE org_id = $1 AND id = $2`, x.org.ID, x.h.site)
	plan := x.newDuesPlan("2026-01-01", 100000)
	d := x.decision("Dues change one day after the plan began")
	body := fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2026-01-02","amount":{"amount":200000,"currency":"TRY"}}`, plan)
	x.apply(d, body, "dues-tz-0000001", 200, false)
	rs := x.ratesOf(plan)
	if len(rs) != 2 || gstr(rs[0], "from") != "2026-01-01" || gstr(rs[0], "to") != "2026-01-01" || gstr(rs[1], "from") != "2026-01-02" || rs[1]["to"] != nil {
		t.Fatalf("rates = %v", rs)
	}
}
