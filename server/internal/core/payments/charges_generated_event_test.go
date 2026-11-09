package payments_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
)

// Generating a plan's charges emits one charges.generated per billed period,
// in the generating transaction, carrying ids only.
func TestIntegrationChargesGeneratedEvent(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx

	var mu sync.Mutex
	queued := 0
	svc, err := integrations.NewService(e.DB, integrations.Config{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), KeyRatePerMinute: -1})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetInsert(func(_ context.Context, _ pgx.Tx, _ river.JobArgs, _ *river.InsertOpts) error {
		mu.Lock()
		defer mu.Unlock()
		queued++
		return nil
	})
	t.Cleanup(audit.SetSink(svc.EmitFromAudit))
	endpoint := uuid.New()
	e.Exec(`INSERT INTO webhook_endpoint (id, org_id, url, secret_sealed, events) VALUES ($1, $2, 'https://93.184.216.34/hook', decode('00', 'hex'), ARRAY['charges.generated'])`, endpoint, e.OrgID)

	amount := int64(100000)
	plan, err := w.bill.CreatePlan(ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &amount})
	if err != nil {
		t.Fatal(err)
	}
	e.Clock.Set("2026-03-10")
	if n, err := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); err != nil || n != 6 {
		t.Fatalf("generated %d, %v", n, err)
	}
	mu.Lock()
	got := queued
	mu.Unlock()
	if got != 3 {
		t.Fatalf("queued %d deliveries, want one per period (3)", got)
	}
	var bodies [][]byte
	err = e.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT body FROM webhook_delivery WHERE endpoint_id = $1 ORDER BY body->>'period'`, endpoint)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			bodies = append(bodies, raw)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	periods := []string{}
	for _, raw := range bodies {
		var ev struct {
			Type      string               `json:"type"`
			IDs       map[string]uuid.UUID `json:"ids"`
			Period    string               `json:"period"`
			ChargeIDs []uuid.UUID          `json:"charge_ids"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != "charges.generated" || ev.IDs["plan_id"] != plan.ID || ev.IDs["site_id"] != e.SiteID || len(ev.IDs) != 2 || len(ev.ChargeIDs) != 2 {
			t.Errorf("event = %s", raw)
		}
		if strings.Contains(string(raw), "Aidat") {
			t.Error("the payload carries record data")
		}
		periods = append(periods, ev.Period)
	}
	if strings.Join(periods, ",") != "2026-01,2026-02,2026-03" {
		t.Errorf("periods = %v", periods)
	}
	// nothing new to bill: nothing emitted
	if n, _ := w.bill.GenerateCharges(ctx, e.OrgID, e.SiteID); n != 0 {
		t.Fatalf("second run generated %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if queued != 3 {
		t.Errorf("an idempotent run queued more: %d", queued)
	}
}
