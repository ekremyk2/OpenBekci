package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/dashboardmoney"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// "Current" occupancy on the dashboard is judged on the site's civil day, not the database's: in a zone
// ahead of UTC (or behind it) an occupancy that starts or ends "today" there counts as it does there.
func TestIntegrationDashboardSidesUseSiteCivilDay(t *testing.T) {
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago"} { // UTC+14 and UTC-11: one of them is on another date than UTC
		t.Run(zone, func(t *testing.T) {
			w := newResidentWorld(t)
			p := w.p
			p.w.Exec(t, `UPDATE site SET timezone = $2 WHERE id = $1`, w.f.Site, zone)
			today := civilToday(t, zone).Format("2006-01-02")
			utc := time.Now().UTC().Format("2006-01-02")
			figures := dashboardmoney.New(p.e.deps.Money.Reports)
			// the figures themselves (the request gate judges the day on the organization's zone)
			count := func(person uuid.UUID) int {
				var n int
				err := p.e.db.InTenantTx(context.Background(), p.org.ID, func(tx pgx.Tx) error {
					sides, err := figures.MySides(context.Background(), tx, p.org.ID, person, nil)
					n = len(sides)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			// an occupancy that starts on a date is current from that date on, one that ends on a date no longer on it,
			// both measured on the site's civil day (today) and not on the UTC or database date (utc)
			for _, c := range []struct {
				name       string
				from, to   string
				wantSeeing bool
			}{
				{"starts civil today", today, "", true},
				{"ended civil today", "2020-01-01", today, false},
				{"starts utc date", utc, "", utc <= today},
				{"ends utc date", "2020-01-01", utc, utc > today},
			} {
				person, unit := p.w.NewPerson(t, p.org), p.w.NewUnit(t, w.f)
				p.w.Exec(t, `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from, valid_to) VALUES ($1, $2, $3, $4, $5, 'owner', 'owner', $6::date, NULLIF($7, '')::date)`,
					id.New(), p.org.ID, w.f.Site, unit, person, c.from, c.to)
				if n := count(person); (n == 1) != c.wantSeeing {
					t.Fatalf("%s in %s (civil %s, utc %s): %d sides, want seeing=%v", c.name, zone, today, utc, n, c.wantSeeing)
				}
			}
		})
	}
}

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
