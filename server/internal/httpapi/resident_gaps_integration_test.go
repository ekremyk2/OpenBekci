package httpapi

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/dashboardmoney"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/latefee"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// residentWorld is one site with an owner, a tenant and a household member of the
// same unit (each with an account), a resident of another unit, and the money module
// wired into the home dashboard, as the server wires it.
type residentWorld struct {
	p                                 *pb
	f                                 authztest.SiteFix
	ownerTok, tenantTok, memberTok    string
	otherTok                          string
	owner, tenant, member             uuid.UUID
	memberOcc                         uuid.UUID
	ownerUser, tenantUser, memberUser uuid.UUID
	tz                                string
	ch                                ledger.Chart
	today                             time.Time // the site's civil today, midnight UTC
}

func newResidentWorld(t *testing.T) *residentWorld {
	t.Helper()
	p := newPB(t)
	p.e.ops.dashboards.SetMoney(dashboardmoney.New(p.e.deps.Money.Reports))
	w := &residentWorld{p: p, f: p.w.NewSite(t, p.org)}
	w.ch = seedChart(p.w, t, p.org, w.f.Site)
	mk := func(person uuid.UUID) (string, uuid.UUID) {
		u, err := p.e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: id.New().String() + "@example.org"})
		if err != nil {
			t.Fatal(err)
		}
		p.w.Exec(t, `UPDATE person SET user_id = $2 WHERE id = $1`, person, u.ID)
		return tokenOf(t, p.e, u.ID), u.ID
	}
	w.owner, w.tenant, w.member = p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org), p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, w.f, w.f.Unit, w.owner, "owner")
	p.w.NewOccupancy(t, w.f, w.f.Unit, w.tenant, "tenant")
	w.memberOcc = p.w.NewOccupancy(t, w.f, w.f.Unit, w.member, "household") // owner side, no money access yet
	w.ownerTok, w.ownerUser = mk(w.owner)
	w.tenantTok, w.tenantUser = mk(w.tenant)
	w.memberTok, w.memberUser = mk(w.member)
	other := p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, w.f, p.w.NewUnit(t, w.f), other, "owner")
	w.otherTok, _ = mk(other)
	err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT timezone FROM site WHERE id = $1`, w.f.Site).Scan(&w.tz)
	})
	if err != nil {
		t.Fatal(err)
	}
	day, _ := ledger.ParseDate(ledger.Today(w.tz, time.Now()))
	w.today = day
	return w
}

func (w *residentWorld) date(daysAgo int) string {
	return w.today.AddDate(0, 0, -daysAgo).Format(ledger.DateLayout)
}

// charge posts a one-off charge to a unit side as the manager.
func (w *residentWorld) charge(side string, amount int64, due string) {
	w.p.t.Helper()
	body := fmt.Sprintf(`{"site_id":"%s","unit_id":"%s","charge_type":"dues","debtor_kind":"%s","amount":{"amount":%d,"currency":"TRY"},"due_date":"%s","description":"test dues"}`, w.f.Site, w.f.Unit, side, amount, due)
	w.p.must(w.p.call("POST", w.p.base()+"/charges", body, w.p.adm, moneyIdem()...), 201)
}

func (w *residentWorld) statement(side, query, tok string) resp {
	return w.p.call("GET", w.p.base()+"/units/"+w.f.Unit.String()+"/statements/"+side+query, "", tok)
}

func amountOf(m map[string]any, k string) int64 {
	mm, _ := m[k].(map[string]any)
	f, _ := mm["amount"].(float64)
	return int64(f)
}

// Bug 1: a resident reads the statement and the PDF of their own unit through the
// occupancy right; the owner both sides, the tenant the tenant side, a household
// member only with may_see_money, everybody else gets 403.
func TestIntegrationResidentReadsOwnStatement(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	w.charge("owner", 50000, w.date(10))
	w.charge("tenant", 30000, w.date(10))

	for _, side := range []string{"owner", "tenant"} {
		r := p.must(w.statement(side, "", w.ownerTok), 200)
		if side == "owner" && amountOf(r.Body, "total_due") < 50000 {
			t.Fatalf("owner statement: %s", r.Raw)
		}
		p.must(w.statement(side, "?format=pdf", w.ownerTok), 200)
	}
	p.must(w.statement("tenant", "", w.tenantTok), 200)
	p.must(w.statement("tenant", "?format=pdf", w.tenantTok), 200)
	p.must(w.statement("owner", "", w.tenantTok), 403)
	p.must(w.statement("owner", "?format=pdf", w.tenantTok), 403)

	// a household member needs the money access the owner grants
	p.must(w.statement("owner", "", w.memberTok), 403)
	p.must(w.statement("tenant", "", w.memberTok), 403)
	p.must(p.call("PUT", p.base()+"/occupancies/"+w.memberOcc.String()+"/money-access", `{"may_see_money":true}`, w.ownerTok), 200)
	p.must(w.statement("owner", "", w.memberTok), 200)
	p.must(w.statement("owner", "?format=pdf", w.memberTok), 200)
	p.must(w.statement("tenant", "", w.memberTok), 403)

	// a resident of another unit, and an unknown unit
	p.must(w.statement("owner", "", w.otherTok), 403)
	p.must(w.statement("tenant", "?format=pdf", w.otherTok), 403)
	p.must(p.call("GET", p.base()+"/units/"+id.New().String()+"/statements/owner", "", w.ownerTok), 403)

	// the manager is unchanged
	p.must(w.statement("owner", "", p.adm), 200)
}

// Bug 2 and 3: the owner's home dashboard lists both sides of the unit, the tenant's
// only the tenant side, and the late fee shown is the one accrued to today (39 days
// overdue at the default 5 % a month) as the calculator computes it, with no booking job run.
func TestIntegrationResidentDashboardSidesAndAccruedFee(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	const principal = 100000
	due := w.date(39)
	w.charge("owner", principal, due)
	w.charge("tenant", principal, due)

	// what the calculator says for 39 days late at 5 %, from the site's own policy
	dueDay, _ := ledger.ParseDate(due)
	loc := ledger.Location(w.tz)
	dueLocal := time.Date(dueDay.Year(), dueDay.Month(), dueDay.Day(), 0, 0, 0, 0, loc)
	todayLocal := time.Date(w.today.Year(), w.today.Month(), w.today.Day(), 0, 0, 0, 0, loc)
	pol := latefee.Policy{Currency: "TRY", RateBPSPerMonth: 500, Simple: true}
	acc, err := latefee.Accrue(latefee.Constant(pol, time.Date(1970, 1, 1, 0, 0, 0, 0, loc)),
		[]latefee.OpenCharge{{ID: uuid.New(), Kind: latefee.KindPrincipal, DueDate: dueLocal, Principal: money.New(principal, "TRY")}}, todayLocal, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := acc.Total.Amount
	if want <= 0 {
		t.Fatalf("the calculator accrued %d on a charge 39 days late", want)
	}

	sidesOf := func(tok string) map[string]map[string]any {
		out := map[string]map[string]any{}
		sides, _ := p.must(p.call("GET", p.base()+"/me/dashboard", "", tok), 200).Body["sides"].([]any)
		for _, x := range sides {
			s, _ := x.(map[string]any)
			out[str(s, "debtor_kind")] = s
		}
		return out
	}
	owner := sidesOf(w.ownerTok)
	if len(owner) != 2 || owner["owner"] == nil || owner["tenant"] == nil {
		t.Fatalf("the owner sees %d sides: %v", len(owner), owner)
	}
	for kind, s := range owner {
		if got := amountOf(s, "accrued_late_fee"); got != want {
			t.Fatalf("%s side accrued_late_fee %d, calculator %d", kind, got, want)
		}
		if got := amountOf(s, "total"); got != principal+want {
			t.Fatalf("%s side total %d, want %d", kind, got, principal+want)
		}
	}
	tenant := sidesOf(w.tenantTok)
	if len(tenant) != 1 || tenant["tenant"] == nil {
		t.Fatalf("the tenant sees %v", tenant)
	}
	if got := amountOf(tenant["tenant"], "accrued_late_fee"); got != want {
		t.Fatalf("tenant accrued_late_fee %d, calculator %d", got, want)
	}
	// a household member sees no money until the owner allows it, then the owner side only
	if got := sidesOf(w.memberTok); len(got) != 0 {
		t.Fatalf("household member without access sees %v", got)
	}
	p.must(p.call("PUT", p.base()+"/occupancies/"+w.memberOcc.String()+"/money-access", `{"may_see_money":true}`, w.ownerTok), 200)
	if got := sidesOf(w.memberTok); len(got) != 1 || got["owner"] == nil {
		t.Fatalf("household member with access sees %v", got)
	}

	// the statement shows the same accrued fee as its last line
	st := p.must(w.statement("owner", "", w.ownerTok), 200)
	if got := amountOf(st.Body, "accrued_late_fee"); got != want {
		t.Fatalf("statement accrued_late_fee %d, calculator %d", got, want)
	}
	// the accrued line is told apart (kind fee, accrued true), is the last one and is counted once
	lines, _ := st.Body["lines"].([]any)
	last, _ := lines[len(lines)-1].(map[string]any)
	if str(last, "kind") != "fee" || last["accrued"] != true || amountOf(last, "late_fee") != want || amountOf(st.Body, "total_due") != principal+want {
		t.Fatalf("accrued line: %v (total_due %d)", last, amountOf(st.Body, "total_due"))
	}
	for _, l := range lines[:len(lines)-1] {
		if m, _ := l.(map[string]any); m["accrued"] == true {
			t.Fatalf("more than one accrued line: %v", lines)
		}
	}
}

// A site whose late fee policy is the automatic default charges dues that fell due
// before the site was set up too (the default applies from the beginning).
func TestIntegrationDefaultLateFeePolicyCoversEarlierDues(t *testing.T) {
	p := newPB(t)
	site := p.must(p.call("POST", p.base()+"/sites", `{"name":"Gul Sitesi","timezone":"Europe/Istanbul","currency":"TRY"}`, p.adm), 201).Body
	sid := str(site, "id")
	if n := p.qInt(`SELECT count(*) FROM late_fee_policy WHERE site_id = $1 AND is_default AND from_on = DATE '1970-01-01' AND rate_bps_per_month = 500 AND grace_days = 0`, uuid.MustParse(sid)); n != 1 {
		t.Fatalf("the default policy of a new site does not reach back to the start (%d rows)", n)
	}
}

// Bug 7: declared (pending_declared) payments are statement lines of their own kind;
// they move no balance, and the PDF and the CSV still render.
func TestIntegrationStatementShowsDeclaredPayments(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	w.charge("tenant", 40000, w.date(20))
	before := p.must(w.statement("tenant", "", w.tenantTok), 200)
	balance := amountOf(before.Body, "closing_balance")

	body := fmt.Sprintf(`{"site_id":"%s","amount":{"amount":15000,"currency":"TRY"},"paid_on":"%s","reference":"EFT 1","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"tenant"}}`, w.f.Site, w.date(2), w.f.Unit)
	pay := p.must(p.call("POST", p.base()+"/payments:declare", body, w.tenantTok, moneyIdem()...), 201)

	after := p.must(w.statement("tenant", "", w.tenantTok), 200)
	if got := amountOf(after.Body, "closing_balance"); got != balance {
		t.Fatalf("a declared payment moved the balance: %d -> %d", balance, got)
	}
	var found map[string]any
	lines, _ := after.Body["lines"].([]any)
	for _, l := range lines {
		if m, _ := l.(map[string]any); str(m, "kind") == "declared_payment" {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("no declared_payment line: %s", after.Raw)
	}
	if str(found, "ref_id") != str(pay.Body, "id") || str(found, "ref_type") != "payment" || str(found, "description") != "EFT 1" || amountOf(found, "declared_amount") != 15000 {
		t.Fatalf("declared line: %v", found)
	}
	if amountOf(found, "debit") != 0 || amountOf(found, "credit") != 0 || amountOf(found, "principal") != 0 || amountOf(found, "late_fee") != 0 {
		t.Fatalf("declared line carries money: %v", found)
	}
	if got := amountOf(found, "balance"); got <= 0 {
		t.Fatalf("declared line balance %d", got)
	}
	// the owner (who sees the whole unit) has none on the owner side
	for _, l := range func() []any {
		r := p.must(w.statement("owner", "", w.ownerTok), 200)
		ls, _ := r.Body["lines"].([]any)
		return ls
	}() {
		if m, _ := l.(map[string]any); str(m, "kind") == "declared_payment" {
			t.Fatalf("the tenant's declared payment shows on the owner side: %v", m)
		}
	}
	p.must(w.statement("tenant", "?format=pdf", w.tenantTok), 200)
	p.must(w.statement("tenant", "?format=csv", w.tenantTok), 200)

	// once confirmed it is an ordinary payment line, no longer a declared one
	p.must(p.call("POST", p.base()+"/payments/"+str(pay.Body, "id")+":confirm", fmt.Sprintf(`{"asset_account_id":"%s"}`, w.ch.Account(ledger.SubBank)), p.adm, moneyIdem()...), 200)
	done := p.must(w.statement("tenant", "", w.tenantTok), 200)
	for _, l := range func() []any { ls, _ := done.Body["lines"].([]any); return ls }() {
		if m, _ := l.(map[string]any); str(m, "kind") == "declared_payment" {
			t.Fatalf("a confirmed payment is still a declared line: %v", m)
		}
	}
}

// Bug 4: a resident chooses private or common; a private ticket must be about their own unit.
func TestIntegrationResidentChoosesTicketVisibility(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	cats := items(p.must(p.call("GET", p.base()+"/ticket-categories?site_id="+w.f.Site.String(), "", w.ownerTok), 200))
	if len(cats) == 0 {
		t.Fatal("a resident sees no ticket categories on a fresh site")
	}
	var plumbing, elevator string
	for _, c := range cats {
		switch str(c, "default_visibility") {
		case "private":
			plumbing = str(c, "id")
		case "common":
			elevator = str(c, "id")
		}
	}
	create := func(tok, cat, vis, unit string) resp {
		body := fmt.Sprintf(`{"site_id":"%s","category_id":"%s","title":"Leak","description":"water","visibility":"%s"`, w.f.Site, cat, vis)
		if unit != "" {
			body += fmt.Sprintf(`,"unit_id":"%s"`, unit)
		}
		return p.call("POST", p.base()+"/tickets", body+"}", tok)
	}
	// the category default is private, the resident asks for common, and the other way round
	if v := str(p.must(create(w.ownerTok, plumbing, "common", ""), 201).Body, "visibility"); v != "common" {
		t.Fatalf("common asked for in a private category: %s", v)
	}
	if v := str(p.must(create(w.tenantTok, elevator, "private", ""), 201).Body, "visibility"); v != "private" {
		t.Fatalf("private asked for in a common category: %s", v)
	}
	if v := str(p.must(create(w.tenantTok, plumbing, "private", w.f.Unit.String()), 201).Body, "visibility"); v != "private" {
		t.Fatalf("private on the own unit: %s", v)
	}
	// without a choice the category's default applies
	r := p.must(p.call("POST", p.base()+"/tickets", fmt.Sprintf(`{"site_id":"%s","category_id":"%s","title":"Light","description":"out"}`, w.f.Site, elevator), w.ownerTok), 201)
	if v := str(r.Body, "visibility"); v != "common" {
		t.Fatalf("category default: %s", v)
	}
	// a private ticket for a unit the resident does not occupy is refused, a common one too when it names it
	otherUnit := p.w.NewUnit(t, w.f)
	p.must(create(w.ownerTok, plumbing, "private", otherUnit.String()), 403)
	p.must(create(w.ownerTok, elevator, "common", otherUnit.String()), 403)
	// a resident with no unit of their own cannot open a private ticket (no unit to attach it to)
	p.must(create(w.otherTok, plumbing, "private", w.f.Unit.String()), 403)
	// the managers keep full control
	if v := str(p.must(create(p.adm, plumbing, "common", ""), 201).Body, "visibility"); v != "common" {
		t.Fatalf("manager common: %s", v)
	}
	// the tenant's common ticket is visible to the other residents of the site, the private one is not
	common := str(p.must(create(w.tenantTok, plumbing, "common", ""), 201).Body, "id")
	private := str(p.must(create(w.tenantTok, plumbing, "private", ""), 201).Body, "id")
	p.must(p.call("GET", p.base()+"/tickets/"+common, "", w.otherTok), 200)
	p.must(p.call("GET", p.base()+"/tickets/"+private, "", w.otherTok), 404)
}

// Bug 5: the default ticket categories exist for a resident on a fresh site, and for
// a site made through the API, without anybody having read them as a manager.
func TestIntegrationDefaultTicketCategoriesForResidents(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	if n := p.qInt(`SELECT count(*) FROM ticket_category WHERE site_id = $1`, w.f.Site); n != 0 {
		t.Fatalf("precondition: %d categories before any read", n)
	}
	cats := items(p.must(p.call("GET", p.base()+"/ticket-categories?site_id="+w.f.Site.String(), "", w.tenantTok), 200))
	names := []string{}
	for _, c := range cats {
		names = append(names, str(c, "name"))
	}
	if len(cats) < 5 {
		t.Fatalf("a resident sees %v", names)
	}

	// a site created through the API has them from the start (the site hook seeds them)
	p.e.tenSvc.WithSiteHook(tickets.SiteSeeder{})
	site := p.must(p.call("POST", p.base()+"/sites", `{"name":"Yeni Site","timezone":"Europe/Istanbul","currency":"TRY"}`, p.adm), 201).Body
	if n := p.qInt(`SELECT count(*) FROM ticket_category WHERE site_id = $1`, uuid.MustParse(str(site, "id"))); n != len(cats) {
		t.Fatalf("a new site has %d categories, want %d", n, len(cats))
	}
}

// Bug 2: /v1/me shows the occupancy-derived rights apart from the roles, and the
// profile can be changed with If-Match.
func TestIntegrationMeShowsOccupantRightsAndProfileUpdate(t *testing.T) {
	w := newResidentWorld(t)
	p := w.p
	me := p.must(p.call("GET", "/v1/me", "", w.tenantTok), 200).Body
	orgs, _ := me["organizations"].([]any)
	var org map[string]any
	for _, o := range orgs {
		if m, _ := o.(map[string]any); str(m, "id") == p.org.ID.String() {
			org = m
		}
	}
	if org == nil {
		t.Fatalf("the organization is missing: %v", me)
	}
	if roles, _ := org["roles"].([]any); len(roles) != 0 {
		t.Fatalf("a resident holds roles: %v", roles)
	}
	if perms, _ := org["permissions"].([]any); len(perms) != 0 {
		t.Fatalf("a resident holds role permissions: %v", perms)
	}
	occ, _ := org["occupant_permissions"].([]any)
	got := []string{}
	for _, g := range occ {
		m, _ := g.(map[string]any)
		got = append(got, str(m, "permission"))
		if sids, _ := m["site_ids"].([]any); len(sids) != 1 || sids[0] != w.f.Site.String() {
			t.Fatalf("occupant permission %v is not limited to the site", m)
		}
	}
	for _, want := range []string{"statements.read_own", "tickets.open"} {
		if !slices.Contains(got, want) {
			t.Fatalf("occupant_permissions %v lacks %s", got, want)
		}
	}
	if slices.Contains(got, "tickets.manage") || slices.Contains(got, "money.read") {
		t.Fatalf("occupant_permissions carries staff rights: %v", got)
	}
	// a manager keeps role permissions and has no occupant ones
	adm := p.must(p.call("GET", "/v1/me", "", p.adm), 200).Body
	for _, o := range adm["organizations"].([]any) {
		m, _ := o.(map[string]any)
		if str(m, "id") == p.org.ID.String() {
			if perms, _ := m["permissions"].([]any); len(perms) == 0 {
				t.Fatalf("the admin has no permissions: %v", m)
			}
			if occ, _ := m["occupant_permissions"].([]any); len(occ) != 0 {
				t.Fatalf("the admin has occupant permissions: %v", occ)
			}
		}
	}

	// PATCH /v1/me/profile: display name and locale, If-Match on user.version
	user, _ := me["user"].(map[string]any)
	ver := num(user, "version")
	if ver < 1 {
		t.Fatalf("user.version: %v", user)
	}
	p.must(p.call("PATCH", "/v1/me/profile", `{"display_name":"Ayse H.","locale":"en"}`, w.tenantTok), 422) // If-Match is required
	r := p.must(p.call("PATCH", "/v1/me/profile", `{"display_name":"  Ayse H.  ","locale":"en"}`, w.tenantTok, "If-Match", fmt.Sprintf(`"%d"`, ver)), 200)
	if str(r.Body, "display_name") != "Ayse H." || str(r.Body, "locale") != "en" || num(r.Body, "version") != ver+1 {
		t.Fatalf("updated profile: %s", r.Raw)
	}
	p.must(p.call("PATCH", "/v1/me/profile", `{"locale":"tr"}`, w.tenantTok, "If-Match", fmt.Sprintf(`"%d"`, ver)), 409) // stale
	p.must(p.call("PATCH", "/v1/me/profile", `{"locale":"not a locale"}`, w.tenantTok, "If-Match", fmt.Sprintf(`"%d"`, ver+1)), 422)
	p.must(p.call("PATCH", "/v1/me/profile", `{}`, w.tenantTok, "If-Match", fmt.Sprintf(`"%d"`, ver+1)), 422)
	p.must(p.call("PATCH", "/v1/me/profile", `{"locale":"tr"}`, "", "If-Match", `"1"`), 401)
	again := p.must(p.call("GET", "/v1/me", "", w.tenantTok), 200).Body["user"].(map[string]any)
	if str(again, "display_name") != "Ayse H." || str(again, "locale") != "en" || num(again, "version") != ver+1 {
		t.Fatalf("GET /v1/me after the update: %v", again)
	}
	// an empty name clears it again: the linked person's name comes back
	cleared := p.must(p.call("PATCH", "/v1/me/profile", `{"display_name":""}`, w.tenantTok, "If-Match", fmt.Sprintf(`"%d"`, ver+1)), 200)
	if str(cleared.Body, "display_name") == "" || str(cleared.Body, "display_name") == "Ayse H." {
		t.Fatalf("cleared display name: %s", cleared.Raw)
	}
}
