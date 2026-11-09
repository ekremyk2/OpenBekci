package httpapi

import (
	"fmt"
	"strings"
	"testing"
)

// A manager's own late fee policy starts a new period and keeps the default one before it: a due that is
// already overdue keeps the fee the default earned up to the new policy's start.
func TestIntegrationOwnLateFeePolicyKeepsOverdueDuesAccruing(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	w.charge("owner", 100000, w.date(39))
	before := p.must(w.statement("owner", "", w.ownerTok), 200)
	fee := amountOf(before.Body, "accrued_late_fee")
	if fee <= 0 {
		t.Fatalf("no fee on a due 39 days late: %s", before.Raw)
	}
	url := p.base() + "/late-fee-policy?site_id=" + w.f.Site.String()
	pol := p.must(p.call("GET", url, "", p.adm), 200)
	// a much lower rate from today: the 39 overdue days stay at the default's rate
	p.must(p.call("PUT", url, `{"grace_days":0,"rate_pct_per_month":1}`, p.adm, "If-Match", fmt.Sprintf(`"%d"`, num(pol.Body, "version"))), 200)
	after := p.must(w.statement("owner", "", w.ownerTok), 200)
	if got := amountOf(after.Body, "accrued_late_fee"); got != fee {
		t.Fatalf("the fee of the overdue days changed from %d to %d when the policy was replaced", fee, got)
	}
	if n := p.qInt(`SELECT count(*) FROM late_fee_policy WHERE site_id = $1`, w.f.Site); n != 2 {
		t.Fatalf("%d policy rows, want the default and the new one", n)
	}
	if n := p.qInt(`SELECT count(*) FROM late_fee_policy WHERE site_id = $1 AND is_default`, w.f.Site); n != 1 {
		t.Fatalf("the default row is gone")
	}
	// the ceiling still applies: a rate above the schema's bound is refused
	pol = p.must(p.call("GET", url, "", p.adm), 200)
	p.must(p.call("PUT", url, `{"grace_days":0,"rate_pct_per_month":150}`, p.adm, "If-Match", fmt.Sprintf(`"%d"`, num(pol.Body, "version"))), 422)
}

// A former occupant (S7) reads the statement of their own side for the period of their occupancy
// and nothing dated after valid_to, in JSON, CSV and PDF; the other resident reads stay closed.
func TestIntegrationFormerOccupantReadsOnlyOwnPeriod(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	// the tenant moved out 30 days ago; one due fell inside the stay, one after it
	p.w.Exec(t, `UPDATE occupancy SET valid_to = $2::date WHERE unit_id = $1 AND kind = 'tenant'`, w.f.Unit, w.date(30))
	w.charge("tenant", 40000, w.date(60))
	w.charge("tenant", 30000, w.date(10))

	st := p.must(w.statement("tenant", "", w.tenantTok), 200)
	lines, _ := st.Body["lines"].([]any)
	var charges int
	for _, l := range lines {
		m, _ := l.(map[string]any)
		if str(m, "date") > w.date(30) {
			t.Fatalf("a line after valid_to: %v", m)
		}
		if str(m, "kind") == "charge" {
			charges++
			if amountOf(m, "principal") != 40000 {
				t.Fatalf("unexpected charge line %v", m)
			}
		}
	}
	if charges != 1 {
		t.Fatalf("former tenant sees %d charges, want the one inside the stay: %s", charges, st.Raw)
	}
	if got := amountOf(st.Body, "closing_balance"); got < 40000 || got >= 70000 {
		t.Fatalf("closing balance %d includes what lies after the stay: %s", got, st.Raw)
	}
	csv := p.must(w.statement("tenant", "?format=csv", w.tenantTok), 200)
	if strings.Contains(csv.Raw, w.date(10)) {
		t.Fatalf("csv shows the due after valid_to:\n%s", csv.Raw)
	}
	if !strings.Contains(csv.Raw, w.date(60)) {
		t.Fatalf("csv lacks the due inside the stay:\n%s", csv.Raw)
	}
	p.must(w.statement("tenant", "?format=pdf", w.tenantTok), 200)

	// nothing on the owner side, and no other resident read of the unit
	p.must(w.statement("owner", "", w.tenantTok), 403)
	p.must(w.statement("owner", "?format=csv", w.tenantTok), 403)
	p.must(p.call("GET", p.base()+"/charges?site_id="+w.f.Site.String()+"&unit_id="+w.f.Unit.String()+"&debtor_kind=tenant", "", w.tenantTok), 403)
	p.must(p.call("GET", p.base()+"/payments?site_id="+w.f.Site.String()+"&unit_id="+w.f.Unit.String()+"&debtor_kind=tenant", "", w.tenantTok), 403)
}
