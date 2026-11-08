package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// effectsEnv is a governance + money environment: the billing-backed decision
// effects are installed, and a site has four owner units and accepted decisions.
type effectsEnv struct {
	*authzEnv
	c    gcall
	mgr  authztest.Actor
	org  authztest.Org
	h    heldFix
	bill *billing.Service
	n    int
}

func newEffectsEnv(t *testing.T) *effectsEnv {
	t.Helper()
	e := newAuthzEnv(t)
	bill := e.deps.Money.Billing
	e.gov.SetEffects(billing.GovernanceEffects{Billing: bill})
	x := &effectsEnv{authzEnv: e, c: gcall{t, e}, mgr: e.w.Actor("site_manager"), org: e.w.OrgA, bill: bill}
	x.h = seedHeld(t, e.w, x.org)
	return x
}

func (x *effectsEnv) base(s string) string { return orgPath(x.org, s) }

// decision inserts an accepted decision of the site and returns its id.
func (x *effectsEnv) decision(text string) string {
	x.t.Helper()
	x.n++
	did := id.New()
	x.w.Exec(x.t, `INSERT INTO decision (id, org_id, site_id, meeting_id, sequence_no, year, text, outcome) VALUES ($1, $2, $3, $4, $5, 2026, $6, 'accepted')`,
		did, x.org.ID, x.h.site, x.h.meeting, x.n, text)
	return did.String()
}

func (x *effectsEnv) apply(did, body, key string, code int, dry bool) map[string]any {
	x.t.Helper()
	q := ""
	if dry {
		q = "?dry_run=true"
	}
	return x.c.must(code, x.mgr, "POST", x.base("/decisions/"+did+":apply"+q), body, "Idempotency-Key", key)
}

func (x *effectsEnv) problemOf(did, body, key string, code int, want string) map[string]any {
	x.t.Helper()
	m := x.apply(did, body, key, code, false)
	if gstr(m, "code") != want {
		x.t.Fatalf("problem = %v, want code %s", m, want)
	}
	return m
}

func (x *effectsEnv) ratesOf(plan string) []map[string]any {
	x.t.Helper()
	return litems(x.c.must(200, x.mgr, "GET", x.base("/charge-plans/"+plan+"/rates"), ""))
}

func (x *effectsEnv) query(sql string, args ...any) (n int, sum int64) {
	x.t.Helper()
	err := x.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql, args...).Scan(&n, &sum)
	})
	if err != nil {
		x.t.Fatal(err)
	}
	return
}

func (x *effectsEnv) newDuesPlan(start string, amount int) string {
	x.t.Helper()
	body := fmt.Sprintf(`{"site_id":%q,"name":"Aidat","kind":"recurring","charge_type":"dues","amount_mode":"per_unit","allocation_key_id":%q,"amount":{"amount":%d,"currency":"TRY"},"scope":{"type":"site","id":null},"start":%q}`,
		x.h.site, x.keyID("equal"), amount, start)
	return gstr(x.c.must(201, x.mgr, "POST", x.base("/charge-plans"), body), "id")
}

func (x *effectsEnv) keyID(code string) uuid.UUID {
	x.t.Helper()
	var k uuid.UUID
	err := x.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT id FROM allocation_key WHERE org_id = $1 AND site_id = $2 AND code = $3`, x.org.ID, x.h.site, code).Scan(&k)
	})
	if err != nil {
		x.t.Fatal(err)
	}
	return k
}

// A dues-rate decision appends a rate from its date (the open rate ends the
// day before), previews without writing, applies once, and is refused behind
// the lock date and behind a period the plan already billed.
func TestIntegrationDecisionEffectDuesRate(t *testing.T) {
	x := newEffectsEnv(t)
	plan := x.newDuesPlan("2026-01-01", 100000)
	d1 := x.decision("Dues become 2,000 TRY from March 2027")
	body := fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2027-03-01","amount":{"amount":200000,"currency":"TRY"}}`, plan)

	// preview: the computed change, nothing written
	pv := x.apply(d1, body, "dues-preview-0001", 200, true)
	p := gmap(pv, "preview")
	if pv["dry_run"] != true || gstr(p, "closes_previous_on") != "2027-02-28" || gstr(p, "from") != "2027-03-01" || gnum(gmap(p, "previous_amount"), "amount") != 100000 || gnum(gmap(p, "amount"), "amount") != 200000 {
		t.Fatalf("preview = %v", pv)
	}
	if rs := x.ratesOf(plan); len(rs) != 1 || rs[0]["to"] != nil {
		t.Fatalf("a preview changed the rates: %v", rs)
	}

	// apply: the new period starts on the date, linked to the decision
	r := x.apply(d1, body, "dues-apply-00001", 200, false)
	eff := gmap(gmap(r, "decision"), "effect")
	if gstr(eff, "kind") != "dues_rate" || gstr(eff, "record_type") != "charge_plan_rate" {
		t.Fatalf("effect = %v", eff)
	}
	rs := x.ratesOf(plan)
	if len(rs) != 2 || gstr(rs[0], "to") != "2027-02-28" || gstr(rs[1], "from") != "2027-03-01" || rs[1]["to"] != nil || gstr(rs[1], "decision_id") != d1 || gstr(rs[1], "id") != gstr(eff, "record_id") {
		t.Fatalf("rates after apply = %v", rs)
	}

	// applying again creates nothing, under a new key or the same one
	x.apply(d1, body, "dues-apply-00002", 200, false)
	if rs := x.ratesOf(plan); len(rs) != 2 {
		t.Fatalf("re-apply added a rate: %v", rs)
	}
	// the service level is idempotent too (the governance record is not the only guard)
	err := x.db.InTenantTx(context.Background(), x.org.ID, func(tx pgx.Tx) error {
		res, err := x.bill.WithTx(tx).ApplyDuesRate(context.Background(), x.org.ID, billing.DuesRateEffect{DecisionID: uuid.MustParse(d1), SiteID: x.h.site, PlanID: uuid.MustParse(plan), From: "2027-03-01", Amount: money.New(200000, "TRY")}, false)
		if err == nil && res.RecordID.String() != gstr(eff, "record_id") {
			err = fmt.Errorf("record %s, want %s", res.RecordID, gstr(eff, "record_id"))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rs := x.ratesOf(plan); len(rs) != 2 {
		t.Fatalf("service re-apply added a rate: %v", rs)
	}

	// a date that overlaps the schedule (before the last rate began) is refused
	d2 := x.decision("Dues back to 1,500 from January 2027")
	x.problemOf(d2, fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2027-01-01","amount":{"amount":150000,"currency":"TRY"}}`, plan), "dues-overlap-0001", 409, "conflict")

	// validation: a plan that is not a dues plan, a foreign currency
	d3 := x.decision("Dues in euro")
	x.problemOf(d3, fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2028-01-01","amount":{"amount":150000,"currency":"EUR"}}`, plan), "dues-currency-001", 422, "validation_failed")
	x.problemOf(d3, fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2028-01-01","amount":{"amount":150000,"currency":"TRY"}}`, uuid.NewString()), "dues-noplan-0001", 422, "validation_failed")

	// the lock date: a rate cannot start on or before it
	x.w.Exec(t, `UPDATE site SET lock_date = '2026-06-30' WHERE org_id = $1 AND id = $2`, x.org.ID, x.h.site)
	plan2 := x.newDuesPlan("2026-01-01", 100000)
	d4 := x.decision("Dues change, backdated")
	body4 := fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2026-06-30","amount":{"amount":200000,"currency":"TRY"}}`, plan2)
	x.problemOf(d4, body4, "dues-locked-0001", 409, "locked_period")
	if m := x.apply(d4, body4, "dues-locked-0002", 409, true); gstr(m, "code") != "locked_period" {
		t.Fatalf("the preview must refuse too: %v", m)
	}
	if rs := x.ratesOf(plan2); len(rs) != 1 {
		t.Fatalf("a refused apply changed the rates: %v", rs)
	}
	if got := gmap(x.c.must(200, x.mgr, "GET", x.base("/decisions/"+d4), ""), "effect"); got != nil {
		t.Fatalf("a refused apply left an effect: %v", got)
	}
}

// A period the plan already billed blocks a rate that would start at or before it.
func TestIntegrationDecisionEffectDuesRateBookedPeriod(t *testing.T) {
	x := newEffectsEnv(t)
	plan := x.newDuesPlan("2025-01-01", 100000)
	n, err := x.bill.GenerateCharges(ledger.SystemContext(context.Background(), x.org.ID), x.org.ID, x.h.site)
	if err != nil || n == 0 {
		t.Fatalf("generated %d charges: %v", n, err)
	}
	d := x.decision("Dues change from mid 2025")
	m := x.problemOf(d, fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2025-06-01","amount":{"amount":200000,"currency":"TRY"}}`, plan), "dues-booked-0001", 409, "conflict")
	errs, _ := m["errors"].([]any)
	if len(errs) != 1 || gstr(errs[0].(map[string]any), "code") != "before_booked_period" {
		t.Fatalf("problem = %v, want a before_booked_period field error", m)
	}
	if rs := x.ratesOf(plan); len(rs) != 1 {
		t.Fatalf("rates = %v", rs)
	}
	// a rate starting the day after the last billed due date is allowed: the rate in
	// force on a period's due date prices it, so billed periods keep their charges
	// and the new rate prices the next period on
	var lastDue string
	err = x.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT (max(due_date) + 1)::text FROM charge WHERE org_id = $1 AND plan_id = $2`, x.org.ID, uuid.MustParse(plan)).Scan(&lastDue)
	})
	if err != nil {
		t.Fatal(err)
	}
	d2 := x.decision("Dues change from the next period")
	x.apply(d2, fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":%q,"amount":{"amount":200000,"currency":"TRY"}}`, plan, lastDue), "dues-booked-0002", 200, false)
	if n, maxAmount := x.query(`SELECT count(*), COALESCE(max(amount), 0)::bigint FROM charge WHERE org_id = $1 AND plan_id = $2`, x.org.ID, uuid.MustParse(plan)); n == 0 || maxAmount != 100000 {
		t.Fatalf("billed charges after the rate change: %d, largest %d, want the old 100000", n, maxAmount)
	}
}

// A special assessment decision bills every unit a share of the total by the
// allocation key: previewed first, then one charge per unit, once per decision.
func TestIntegrationDecisionEffectSpecialAssessment(t *testing.T) {
	x := newEffectsEnv(t)
	key := x.keyID("equal")
	d := x.decision("Roof repair: 1,000.01 TRY split equally, due 15 December")
	assess := func(start string, total int) string {
		return fmt.Sprintf(`{"kind":"special_assessment","plan":{"name":"Roof repair","kind":"one_off","charge_type":"special_assessment","amount_mode":"total_allocated","allocation_key_id":%q,"scope":{"type":"site","id":null},"start":%q,"amount":{"amount":%d,"currency":"TRY"}}}`, key, start, total)
	}
	body := assess("2026-12-15", 100001)

	pv := gmap(x.apply(d, body, "assess-preview-01", 200, true), "preview")
	lines, _ := pv["charges"].([]any)
	var sum int
	for _, l := range lines {
		sum += gnum(gmap(l.(map[string]any), "amount"), "amount")
	}
	if len(lines) != 4 || sum != 100001 || gnum(gmap(pv, "total"), "amount") != 100001 || gstr(pv, "due_date") != "2026-12-15" || gstr(pv, "kind") != "special_assessment" {
		t.Fatalf("preview = %v", pv)
	}
	if n, _ := x.query(`SELECT count(*), 0 FROM charge WHERE org_id = $1 AND site_id = $2 AND charge_type = 'special_assessment'`, x.org.ID, x.h.site); n != 0 {
		t.Fatalf("a preview created %d charges", n)
	}

	r := x.apply(d, body, "assess-apply-0001", 200, false)
	eff := gmap(gmap(r, "decision"), "effect")
	if gstr(eff, "kind") != "special_assessment" || gstr(eff, "record_type") != "charge_plan" {
		t.Fatalf("effect = %v", eff)
	}
	count := func() (int, int64) {
		return x.query(`SELECT count(*), COALESCE(sum(amount), 0)::bigint FROM charge WHERE org_id = $1 AND plan_id = $2 AND charge_type = 'special_assessment' AND kind = 'principal' AND due_date = '2026-12-15' AND cancel_entry_id IS NULL`,
			x.org.ID, uuid.MustParse(gstr(eff, "record_id")))
	}
	if n, total := count(); n != 4 || total != 100001 {
		t.Fatalf("charges = %d totalling %d, want 4 totalling 100001", n, total)
	}
	if n, _ := x.query(`SELECT count(DISTINCT unit_id), 0 FROM charge WHERE org_id = $1 AND plan_id = $2`, x.org.ID, uuid.MustParse(gstr(eff, "record_id"))); n != 4 {
		t.Fatalf("charges cover %d units, want 4", n)
	}

	// re-applying: same record, no second set of charges
	r2 := x.apply(d, body, "assess-apply-0002", 200, false)
	if gstr(gmap(gmap(r2, "decision"), "effect"), "record_id") != gstr(eff, "record_id") {
		t.Fatalf("re-apply = %v", r2)
	}
	err := x.db.InTenantTx(context.Background(), x.org.ID, func(tx pgx.Tx) error {
		res, err := x.bill.WithTx(tx).ApplySpecialAssessment(context.Background(), x.org.ID, billing.SpecialAssessmentEffect{DecisionID: uuid.MustParse(d), SiteID: x.h.site, Plan: map[string]any{
			"allocation_key_id": key.String(), "start": "2026-12-15", "amount": map[string]any{"amount": 100001, "currency": "TRY"}}}, false)
		if err == nil && res.RecordID.String() != gstr(eff, "record_id") {
			err = fmt.Errorf("record %s, want %s", res.RecordID, gstr(eff, "record_id"))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, total := count(); n != 4 || total != 100001 {
		t.Fatalf("after re-applying: %d charges totalling %d", n, total)
	}

	// validation and the lock date refuse without leaving anything behind
	d2 := x.decision("Another assessment")
	x.problemOf(d2, `{"kind":"special_assessment","plan":{"start":"2026-12-15","amount":{"amount":5000,"currency":"TRY"}}}`, "assess-nokey-0001", 422, "validation_failed")
	x.w.Exec(t, `UPDATE site SET lock_date = '2026-12-31' WHERE org_id = $1 AND id = $2`, x.org.ID, x.h.site)
	x.problemOf(d2, assess("2026-12-20", 5000), "assess-locked-001", 409, "locked_period")
	if m := x.apply(d2, assess("2026-12-20", 5000), "assess-locked-002", 409, true); gstr(m, "code") != "locked_period" {
		t.Fatalf("the preview must refuse too: %v", m)
	}
	if n, _ := x.query(`SELECT count(*), 0 FROM charge_plan WHERE org_id = $1 AND site_id = $2 AND charge_type = 'special_assessment'`, x.org.ID, x.h.site); n != 1 {
		t.Fatalf("%d assessment plans, want only the first", n)
	}
}
