package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// account gives a person of the world a login and returns its token.
func (w *residentWorld) account(t *testing.T, person uuid.UUID) string {
	t.Helper()
	u, err := w.p.e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: id.New().String() + "@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	w.p.w.Exec(t, `UPDATE person SET user_id = $2 WHERE id = $1`, person, u.ID)
	return tokenOf(t, w.p.e, u.ID)
}

// The statement fallback (a resident has no units.read) is open to the occupants of the
// unit and nobody else, in every format: a resident of another unit, a household member
// without may_see_money, and a former occupant for what lies outside their period or on a
// side they never held. A unit of another organization or a made-up one answers like before.
func TestIntegrationStatementFallbackLimits(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	w.charge("owner", 50000, w.date(10))
	w.charge("tenant", 30000, w.date(10))

	// a former tenant of the unit: lived there until 100 days ago
	w.p.w.Exec(t, `UPDATE occupancy SET valid_to = $2::date WHERE id IN (SELECT id FROM occupancy WHERE unit_id = $1 AND kind = 'tenant')`, w.f.Unit, w.date(100))

	for _, q := range []string{"", "?format=csv", "?format=pdf"} {
		// other unit's resident, household member without money access, unknown unit
		p.must(w.statement("owner", q, w.otherTok), 403)
		p.must(w.statement("tenant", q, w.otherTok), 403)
		p.must(w.statement("owner", q, w.memberTok), 403)
		p.must(w.statement("tenant", q, w.memberTok), 403)
		p.must(p.call("GET", p.base()+"/units/"+id.New().String()+"/statements/owner"+q, "", w.otherTok), 403)
		// the former tenant never held the owner side
		p.must(w.statement("owner", q, w.tenantTok), 403)
		// ... and reads the tenant side only inside the period
		r := p.call("GET", p.base()+"/units/"+w.f.Unit.String()+"/statements/tenant"+q, "", w.tenantTok)
		if r.Code == 403 {
			continue // statements.read_own is not granted to former occupants (review note): no data at all
		}
		if r.Code != 200 {
			t.Fatalf("former tenant, format %q: %d %s", q, r.Code, r.Raw)
		}
		if q == "" {
			if lines, _ := r.Body["lines"].([]any); len(lines) != 0 {
				t.Fatalf("a former tenant sees what lies after their period: %s", r.Raw)
			}
			if amountOf(r.Body, "total_due") != 0 || amountOf(r.Body, "accrued_late_fee") != 0 {
				t.Fatalf("a former tenant is shown the debt after their period: %s", r.Raw)
			}
		} else if strings.Contains(r.Raw, "test dues") {
			t.Fatalf("former tenant, format %q shows a charge after their period", q)
		}
	}

	// a unit of another organization: the resident of this one has no way to tell it exists
	otherOrg := p.w.OrgB
	of := p.w.NewSite(t, otherOrg)
	for _, q := range []string{"", "?format=csv"} {
		a := p.call("GET", p.base()+"/units/"+of.Unit.String()+"/statements/owner"+q, "", w.ownerTok)
		b := p.call("GET", p.base()+"/units/"+id.New().String()+"/statements/owner"+q, "", w.ownerTok)
		if a.Code != 403 || b.Code != 403 || str(a.Body, "detail") != str(b.Body, "detail") || str(a.Body, "code") != str(b.Body, "code") {
			t.Fatalf("a unit of another org answers %d %s, a made-up one %d %s", a.Code, a.Raw, b.Code, b.Raw)
		}
		// and through the other organization's path: not a member there
		c := p.call("GET", "/v1/orgs/"+otherOrg.ID.String()+"/units/"+of.Unit.String()+"/statements/owner"+q, "", w.ownerTok)
		d := p.call("GET", "/v1/orgs/"+otherOrg.ID.String()+"/units/"+id.New().String()+"/statements/owner"+q, "", w.ownerTok)
		if c.Code != d.Code || str(c.Body, "detail") != str(d.Body, "detail") || str(c.Body, "code") != str(d.Body, "code") {
			t.Fatalf("unit of another org by its org path: %d %s; made-up: %d %s", c.Code, c.Raw, d.Code, d.Raw)
		}
	}
}

// The late fee the dashboard shows (booked fee still open plus the accrued, unbooked one) is the
// statement's own figure for the same day, also when the site's zone is not UTC and part of the fee
// is already booked.
func TestIntegrationDashboardFeeEqualsStatementAcrossZoneAndBooking(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	const zone = "Pacific/Kiritimati" // UTC+14: the civil day is often not the UTC day
	p.w.Exec(t, `UPDATE site SET timezone = $2 WHERE id = $1`, w.f.Site, zone)
	w.tz = zone
	w.today = civilToday(t, zone)
	const principal = 100000
	w.charge("owner", principal, w.date(75))
	// a waiver books the fee of the closed months first, so the figure is part booked, part accrued
	body := fmt.Sprintf(`{"site_id":"%s","amount":{"amount":100,"currency":"TRY"},"reason":"goodwill","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"owner"}}`, w.f.Site, w.f.Unit)
	p.must(p.call("POST", p.base()+"/late-fee-waivers", body, p.adm, moneyIdem()...), 201)

	st := p.must(w.statement("owner", "", w.ownerTok), 200)
	totalDue := amountOf(st.Body, "total_due")
	feeOfStatement := totalDue - principal
	if feeOfStatement <= 0 {
		t.Fatalf("no fee on a charge 75 days late: %s", st.Raw)
	}
	var side map[string]any
	sides, _ := p.must(p.call("GET", p.base()+"/me/dashboard", "", w.ownerTok), 200).Body["sides"].([]any)
	for _, x := range sides {
		if s, _ := x.(map[string]any); str(s, "debtor_kind") == "owner" {
			side = s
		}
	}
	if side == nil {
		t.Fatalf("no owner side on the dashboard: %v", sides)
	}
	if got := amountOf(side, "accrued_late_fee"); got != feeOfStatement {
		t.Fatalf("dashboard fee %d, statement fee %d (total_due %d)", got, feeOfStatement, totalDue)
	}
	if got := amountOf(side, "total"); got != totalDue {
		t.Fatalf("dashboard total %d, statement total_due %d", got, totalDue)
	}
}

// A site made while a seeder fails is not made at all: the hooks run in the creating
// transaction and the first error rolls the site back with everything the hooks wrote.
func TestIntegrationSiteHookFailureRollsBackTheSite(t *testing.T) {
	p := newPB(t)
	p.e.tenSvc.WithSiteHook(failingSiteHook{})
	before := p.qInt(`SELECT count(*) FROM site WHERE org_id = $1`, p.org.ID)
	r := p.call("POST", p.base()+"/sites", `{"name":"Hook Fail","timezone":"Europe/Istanbul","currency":"TRY"}`, p.adm)
	if r.Code < 500 {
		t.Fatalf("a failing site hook answered %d %s", r.Code, r.Raw)
	}
	if after := p.qInt(`SELECT count(*) FROM site WHERE org_id = $1`, p.org.ID); after != before {
		t.Fatalf("the site survived its failing hook (%d -> %d sites)", before, after)
	}
	if n := p.qInt(`SELECT count(*) FROM site WHERE name = 'Hook Fail'`); n != 0 {
		t.Fatalf("%d half-made sites", n)
	}
}

type failingSiteHook struct{}

func (failingSiteHook) SiteCreated(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return fmt.Errorf("seeder down")
}

var _ tenancy.SiteHook = failingSiteHook{}

// The profile name follows the sign-up rules (plain text) and the locale is one the server has texts for.
func TestIntegrationProfileNameAndLocaleRules(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	user, _ := p.must(p.call("GET", "/v1/me", "", w.tenantTok), 200).Body["user"].(map[string]any)
	ver := num(user, "version")
	patch := func(body string) resp {
		return p.call("PATCH", "/v1/me/profile", body, w.tenantTok, "If-Match", fmt.Sprintf(`"%d"`, ver))
	}
	for _, bad := range []string{
		`{"display_name":"Ayse‮evil"}`,  // bidi override
		`{"display_name":"Ay\u0000se"}`, // control
		`{"display_name":"Ay​se"}`,      // zero width
		`{"locale":"fr"}`,
		`{"locale":"tr-TR"}`,
	} {
		p.must(patch(bad), 422)
	}
	r := p.must(patch(`{"display_name":"  Ayse   Yilmaz  "}`), 200)
	if str(r.Body, "display_name") != "Ayse Yilmaz" {
		t.Fatalf("display name not normalized: %s", r.Raw)
	}
}

// civilToday is the site calendar day now, as midnight UTC.
func civilToday(t *testing.T, zone string) time.Time {
	t.Helper()
	d, err := ledger.ParseDate(ledger.Today(zone, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
