package httpapi

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

func (x *effectsEnv) assessmentBody(key uuid.UUID, start string, total int) string {
	return fmt.Sprintf(`{"kind":"special_assessment","plan":{"name":"Roof repair","kind":"one_off","charge_type":"special_assessment","amount_mode":"total_allocated","allocation_key_id":%q,"scope":{"type":"site","id":null},"start":%q,"amount":{"amount":%d,"currency":"TRY"}}}`, key, start, total)
}

// A paused or archived plan takes no new rate: 409 with a field error on plan_id,
// for the preview too. A decision applied before the plan was paused stays applied.
func TestIntegrationDecisionEffectDuesRatePlanStatus(t *testing.T) {
	x := newEffectsEnv(t)
	plan := x.newDuesPlan("2026-01-01", 100000)
	dApplied := x.decision("Dues change before the pause")
	applied := fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2027-03-01","amount":{"amount":200000,"currency":"TRY"}}`, plan)
	x.apply(dApplied, applied, "dues-status-0001", 200, false)
	for i, status := range []string{"paused", "archived"} {
		x.w.Exec(t, `UPDATE charge_plan SET status = $3 WHERE org_id = $1 AND id = $2`, x.org.ID, plan, status)
		d := x.decision("Dues change on a " + status + " plan")
		body := fmt.Sprintf(`{"kind":"dues_rate","plan_id":%q,"from":"2028-0%d-01","amount":{"amount":300000,"currency":"TRY"}}`, plan, i+1)
		for _, dry := range []bool{false, true} {
			m := x.apply(d, body, fmt.Sprintf("dues-status-%d-%t-1", i, dry), 409, dry)
			errs, _ := m["errors"].([]any)
			if gstr(m, "code") != "conflict" || len(errs) != 1 || gstr(errs[0].(map[string]any), "field") != "plan_id" || gstr(errs[0].(map[string]any), "code") != "invalid_value" {
				t.Fatalf("%s plan, dry_run=%v: %v", status, dry, m)
			}
		}
		if rs := x.ratesOf(plan); len(rs) != 2 {
			t.Fatalf("a refused apply on a %s plan changed the rates: %v", status, rs)
		}
		if got := gmap(x.c.must(200, x.mgr, "GET", x.base("/decisions/"+d), ""), "effect"); got != nil {
			t.Fatalf("a refused apply left an effect: %v", got)
		}
		x.apply(dApplied, applied, fmt.Sprintf("dues-status-again-%d", i), 200, false)
	}
}

// Units without a weight in the allocation key are left out: no charge, and the
// shares of the others add up to the total exactly.
func TestIntegrationDecisionEffectSpecialAssessmentExcludedUnits(t *testing.T) {
	x := newEffectsEnv(t)
	key := id.New()
	x.w.Exec(t, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind) VALUES ($1, $2, $3, 'roof', 'Roof', 'table')`, key, x.org.ID, x.h.site)
	for _, w := range []struct {
		unit int
		raw  string
	}{{0, "1"}, {1, "2"}} {
		x.w.Exec(t, `INSERT INTO allocation_key_value (id, org_id, site_id, key_id, unit_id, value, raw) VALUES ($1, $2, $3, $4, $5, $6::numeric, $6)`,
			id.New(), x.org.ID, x.h.site, key, x.h.units[w.unit], w.raw)
	}
	d := x.decision("Roof repair for the two top floors")
	body := x.assessmentBody(key, "2026-12-15", 100001)
	pv := gmap(x.apply(d, body, "assess-excl-prev-1", 200, true), "preview")
	if lines, _ := pv["charges"].([]any); len(lines) != 2 {
		t.Fatalf("preview bills %d units, want 2: %v", len(lines), pv)
	}
	r := x.apply(d, body, "assess-excl-appl-1", 200, false)
	plan := uuid.MustParse(gstr(gmap(gmap(r, "decision"), "effect"), "record_id"))
	if n, total := x.query(`SELECT count(*), COALESCE(sum(amount), 0)::bigint FROM charge WHERE org_id = $1 AND plan_id = $2 AND unit_id = ANY($3)`, x.org.ID, plan, []uuid.UUID{x.h.units[0], x.h.units[1]}); n != 2 || total != 100001 {
		t.Fatalf("included units: %d charges totalling %d, want 2 totalling 100001", n, total)
	}
	if n, _ := x.query(`SELECT count(*), 0 FROM charge WHERE org_id = $1 AND plan_id = $2 AND unit_id = ANY($3)`, x.org.ID, plan, []uuid.UUID{x.h.units[2], x.h.units[3]}); n != 0 {
		t.Fatalf("%d charges on the excluded units", n)
	}
	if n, _ := x.query(`SELECT count(*), 0 FROM charge WHERE org_id = $1 AND plan_id = $2 AND amount = 33334`, x.org.ID, plan); n != 1 {
		t.Fatalf("the rounding remainder is not on exactly one share")
	}
}

// A unit nobody lives in is still billed: the charge sits on its owner side with
// no debtor person (the same as plan generation does), never skipped.
func TestIntegrationDecisionEffectSpecialAssessmentVacantUnit(t *testing.T) {
	x := newEffectsEnv(t)
	vacant := id.New()
	x.w.Exec(t, `INSERT INTO unit (id, org_id, site_id, block_id, door_number) SELECT $1, org_id, site_id, block_id, 'V-1' FROM unit WHERE org_id = $2 AND id = $3`, vacant, x.org.ID, x.h.units[0])
	d := x.decision("Roof repair, five units")
	r := x.apply(d, x.assessmentBody(x.keyID("equal"), "2026-12-15", 100000), "assess-vacant-001", 200, false)
	plan := uuid.MustParse(gstr(gmap(gmap(r, "decision"), "effect"), "record_id"))
	var n int
	var amount int64
	var side string
	var person *uuid.UUID
	err := x.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM charge WHERE org_id = $1 AND plan_id = $2`, x.org.ID, plan).Scan(&n); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(), `SELECT amount, debtor_kind, debtor_person_id FROM charge WHERE org_id = $1 AND plan_id = $2 AND unit_id = $3`, x.org.ID, plan, vacant).Scan(&amount, &side, &person)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 || amount != 20000 || side != "owner" || person != nil {
		t.Fatalf("%d charges; vacant unit: amount %d side %s person %v, want 5, 20000, owner, none", n, amount, side, person)
	}
}

// Two applies of one decision racing: the unique index on the rate's decision id
// makes the loser return the winner's plan, and one set of charges exists.
func TestIntegrationDecisionEffectSpecialAssessmentConcurrentApply(t *testing.T) {
	x := newEffectsEnv(t)
	key := x.keyID("equal")
	d := uuid.MustParse(x.decision("Roof repair, applied twice at once"))
	plan := map[string]any{"allocation_key_id": key.String(), "start": "2026-12-15", "amount": map[string]any{"amount": 100001, "currency": "TRY"}}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]uuid.UUID, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = x.db.InTenantTx(context.Background(), x.org.ID, func(tx pgx.Tx) error {
				res, err := x.bill.WithTx(tx).ApplySpecialAssessment(ledger.SystemContext(context.Background(), x.org.ID), x.org.ID, billing.SpecialAssessmentEffect{DecisionID: d, SiteID: x.h.site, Plan: plan}, false)
				results[i] = res.RecordID
				return err
			})
		}()
	}
	close(start)
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("apply %d: %v", i, errs[i])
		}
		if results[i] != results[0] || results[i] == uuid.Nil {
			t.Fatalf("applies returned different plans: %v", results)
		}
	}
	if n, total := x.query(`SELECT count(*), COALESCE(sum(amount), 0)::bigint FROM charge WHERE org_id = $1 AND site_id = $2 AND charge_type = 'special_assessment'`, x.org.ID, x.h.site); n != 4 || total != 100001 {
		t.Fatalf("%d charges totalling %d, want 4 totalling 100001", n, total)
	}
	if n, _ := x.query(`SELECT count(*), 0 FROM charge_plan WHERE org_id = $1 AND site_id = $2 AND charge_type = 'special_assessment'`, x.org.ID, x.h.site); n != 1 {
		t.Fatalf("%d assessment plans, want 1", n)
	}
}
