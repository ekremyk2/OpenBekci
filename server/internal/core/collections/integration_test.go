package collections_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/collections"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/moneytest"
)

type notifier struct {
	mu   sync.Mutex
	list []ledger.Notification
}

func (n *notifier) Enqueue(_ context.Context, _ pgx.Tx, e ledger.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, e)
	return nil
}

func (n *notifier) count(typ string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, e := range n.list {
		if e.Type == typ {
			c++
		}
	}
	return c
}

type world struct {
	e      *moneytest.Env
	bill   *billing.Service
	col    *collections.Service
	note   *notifier
	a1, a2 uuid.UUID
	p1     uuid.UUID
}

func newWorld(t *testing.T) *world {
	e := moneytest.New(t)
	w := &world{e: e, note: &notifier{}}
	w.bill = billing.NewService(e.DB, tenancy.NewService(e.DB), e.Clock.Now)
	w.col = collections.NewService(e.DB, w.bill, e.Clock.Now, w.note)
	w.a1, w.a2 = e.Unit("A-1", 600), e.Unit("A-2", 400)
	w.p1 = e.Person("Ayşe Yılmaz")
	e.Occupy(w.a1, w.p1, "owner", "2025-01-01", "")
	e.Occupy(w.a2, e.Person("Mehmet Kaya"), "owner", "2025-01-01", "")
	if _, err := w.bill.SetLateFeePolicy(e.Ctx, e.OrgID, e.SiteID, billing.PolicyRow{From: "2026-01-01", RateBPSPerMonth: 500}); err != nil {
		t.Fatal(err)
	}
	amount := int64(100000)
	if _, err := w.bill.CreatePlan(e.Ctx, e.OrgID, billing.PlanInput{SiteID: e.SiteID, Name: "Aidat", DueDay: 5, Start: "2026-01-01", AmountMode: "total_allocated", AllocationKey: ledger.Ptr("arsa_payi"), Amount: &amount}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) at(t *testing.T, day string) {
	t.Helper()
	w.e.Clock.Set(day)
	if _, err := w.bill.GenerateCharges(w.e.Ctx, w.e.OrgID, w.e.SiteID); err != nil {
		t.Fatal(err)
	}
}

func (w *world) stage(t *testing.T, name, kind string, days int, ch ...string) collections.Stage {
	t.Helper()
	st, err := w.col.CreateStage(w.e.Ctx, w.e.OrgID, collections.StageInput{SiteID: w.e.SiteID, Name: name, Kind: kind, DaysAfterDue: days, Channels: ch})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (w *world) events(t *testing.T) []collections.Event {
	t.Helper()
	ev, _, err := w.col.ListEvents(w.e.Ctx, w.e.OrgID, w.e.SiteID, collections.EventFilter{}, ledger.PageParams{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func inflate(t *testing.T, b []byte) string {
	t.Helper()
	var all strings.Builder
	rest := b
	for {
		i := bytes.Index(rest, []byte("stream\n"))
		if i < 0 {
			break
		}
		rest = rest[i+len("stream\n"):]
		j := bytes.Index(rest, []byte("\nendstream"))
		if j < 0 {
			break
		}
		if r, err := zlib.NewReader(bytes.NewReader(rest[:j])); err == nil {
			d, _ := io.ReadAll(r)
			if len(d) < 100000 {
				all.Write(d)
			}
		}
		rest = rest[j+len("\nendstream"):]
	}
	return all.String()
}

func TestIntegrationStagesDedupeEscalateAndFreeze(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	w.stage(t, "Hatırlatma 1", "reminder", 3, "in_app", "email")
	s2 := w.stage(t, "Hatırlatma 2", "reminder", 30, "sms")
	s3 := w.stage(t, "İhtarname", "ihtarname", 60, "letter")

	// day 4 after the January due date: stage 1 fires once per debtor and channel
	w.at(t, "2026-01-09")
	n, err := w.col.RunStages(ctx, e.OrgID, e.SiteID)
	if err != nil || n != 4 { // 2 debtors x 2 channels
		t.Fatalf("first run %d %v", n, err)
	}
	if n, _ := w.col.RunStages(ctx, e.OrgID, e.SiteID); n != 0 {
		t.Errorf("second run created %d events", n)
	}
	if got := w.note.count(ledger.NotifyDunningReminder); got != 4 {
		t.Errorf("dunning.reminder jobs %d", got)
	}
	var p map[string]any
	_ = json.Unmarshal(w.note.list[0].Payload, &p)
	if p["stage"] != "Hatırlatma 1" || p["channel"] == nil || p["amount_due"] == nil {
		t.Errorf("payload %v", p)
	}

	// 36 days: stage 2 escalates (not stage 1 again)
	w.at(t, "2026-02-10")
	if n, err := w.col.RunStages(ctx, e.OrgID, e.SiteID); err != nil || n != 2 {
		t.Fatalf("escalation %d %v", n, err)
	}
	if n, _ := w.col.RunStages(ctx, e.OrgID, e.SiteID); n != 0 {
		t.Errorf("repeat created %d", n)
	}

	// the owner of A-1 goes to a lawyer: reminders and late fees freeze
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	cs, err := w.col.CreateCase(ctx, e.OrgID, collections.CaseInput{SiteID: e.SiteID, Debtor: a1, OpenedOn: "2026-02-10", Lawyer: ledger.Ptr("Av. Demir"), FileNo: ledger.Ptr("2026/55")})
	if err != nil || cs.Status != "open" || cs.DebtorPersonID == nil || *cs.DebtorPersonID != w.p1 {
		t.Fatalf("case %+v %v", cs, err)
	}
	if _, err := w.col.CreateCase(ctx, e.OrgID, collections.CaseInput{SiteID: e.SiteID, Debtor: a1, OpenedOn: "2026-02-11"}); err == nil {
		t.Error("a second open case for the debtor must conflict")
	}
	before, _ := w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a1, "2026-02-10")
	frozen, _ := w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a1, "2026-04-20")
	if !frozen.Frozen || frozen.Accrued.Amount != before.Accrued.Amount {
		t.Errorf("late fee not frozen: before %d, later %d, frozen=%v", before.Accrued.Amount, frozen.Accrued.Amount, frozen.Frozen)
	}
	a2 := billing.Debtor{UnitID: &w.a2, Kind: "owner"}
	if other, _ := w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a2, "2026-04-20"); other.Frozen {
		t.Error("the other unit must not be frozen")
	}

	// 64+ days: the ihtarname goes to A-2 only; the letter is stored on the event
	w.at(t, "2026-03-10")
	if n, err := w.col.RunStages(ctx, e.OrgID, e.SiteID); err != nil || n != 1 {
		t.Fatalf("ihtarname run %d %v", n, err)
	}
	var letterEvent *collections.Event
	for _, ev := range w.events(t) {
		if ev.StageID == s3.ID {
			ev := ev
			letterEvent = &ev
		}
		if ev.StageID == s3.ID && *ev.UnitID != w.a2 {
			t.Errorf("frozen debtor got the ihtarname: %+v", ev)
		}
	}
	if letterEvent == nil || letterEvent.Channel != "letter" || !letterEvent.HasLetter {
		t.Fatalf("letter event %+v", letterEvent)
	}
	pdfBytes, err := w.col.Letter(ctx, e.OrgID, collections.LetterInput{EventID: &letterEvent.ID})
	if err != nil || !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatalf("letter %v", err)
	}
	s := inflate(t, pdfBytes)
	for _, u := range []string{"011F", "015F", "0130"} { // ğ ş İ
		if !strings.Contains(s, "<"+u+">") {
			t.Errorf("letter lacks U+%s", u)
		}
	}
	if !strings.Contains(s, "ActualText <FEFF20BA>") {
		t.Error("letter lacks the lira sign")
	}
	// a letter on demand for a debtor
	if b, err := w.col.Letter(ctx, e.OrgID, collections.LetterInput{SiteID: e.SiteID, Debtor: &a1, AsOf: "2026-03-10"}); err != nil || len(b) < 1000 {
		t.Errorf("debtor letter %d %v", len(b), err)
	}

	// manual run skips the frozen debtor and counts it
	in := collections.RunInput{SiteID: e.SiteID, StageID: s2.ID, Debtors: []billing.Debtor{a1, a2}, Channels: []string{"email", "sms"}}
	pv, err := w.col.PreviewRun(ctx, e.OrgID, in)
	if err != nil || pv.RecipientCount != 1 || pv.SkippedFrozen != 1 || pv.SMSCount != 1 {
		t.Fatalf("preview %+v %v", pv, err)
	}
	run, err := w.col.CreateRun(ctx, e.OrgID, in)
	if err != nil || run.EventCount != 2 || run.SMSCount != 1 {
		t.Fatalf("run %+v %v", run, err)
	}

	// closing the case ends the freeze and charges the legal costs to the side
	cost := money.New(25000, "TRY")
	closed, err := w.col.CloseCase(ctx, e.OrgID, cs.ID, &cost, "settled")
	if err != nil || closed.Status != "closed" || closed.CostsCharged.Amount != 25000 || closed.ClosedOn == nil {
		t.Fatalf("close %+v %v", closed, err)
	}
	if n := e.Int(`SELECT count(*) FROM charge WHERE org_id=$1 AND unit_id=$2 AND charge_type='legal_cost' AND amount=25000 AND debtor_kind='owner'`, e.OrgID, w.a1); n != 1 {
		t.Errorf("legal cost charges %d", n)
	}
	if n := e.Int(`SELECT count(*) FROM late_fee_freeze WHERE org_id=$1 AND unit_id=$2 AND to_on IS NOT NULL`, e.OrgID, w.a1); n != 1 {
		t.Errorf("freeze not ended: %d", n)
	}
	if _, err := w.col.CloseCase(ctx, e.OrgID, cs.ID, nil, ""); err == nil {
		t.Error("closing twice must conflict")
	}
	if _, err := w.col.UpdateCase(ctx, e.OrgID, cs.ID, 1, collections.CaseUpdate{}); err == nil {
		t.Error("stale version must be refused")
	}
}

func TestIntegrationDebtBreakdown(t *testing.T) {
	w := newWorld(t)
	e, ctx := w.e, w.e.Ctx
	w.at(t, "2026-03-12")
	a1 := billing.Debtor{UnitID: &w.a1, Kind: "owner"}
	b, err := w.col.Breakdown(ctx, e.OrgID, e.SiteID, a1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Lines) != 3 || b.TotalPrincipal.Amount != 180000 || b.Lines[0].Principal.Amount != 60000 || *b.Lines[0].Period != "2026-01" {
		t.Fatalf("breakdown %+v", b)
	}
	acc, _ := w.bill.AccrueLateFee(ctx, e.OrgID, e.SiteID, a1, "2026-03-12")
	if b.TotalLateFee.Amount != acc.Expected.Amount || b.TotalLateFee.Amount <= 0 {
		t.Errorf("late fee %d vs expected %d", b.TotalLateFee.Amount, acc.Expected.Amount)
	}
	var sum int64
	for _, l := range b.Lines {
		sum += l.LateFee.Amount
	}
	if sum != b.TotalLateFee.Amount || b.Lines[0].LateFee.Amount < b.Lines[2].LateFee.Amount {
		t.Errorf("fee spread %+v", b.Lines)
	}
	// as of an earlier date only the January charge is due
	early, err := w.col.Breakdown(ctx, e.OrgID, e.SiteID, a1, "2026-01-20")
	if err != nil || len(early.Lines) != 1 {
		t.Fatalf("early %+v %v", early, err)
	}
	if csv, err := b.CSV(); err != nil || !strings.Contains(string(csv), "2026-01,2026-01-05,600.00") {
		t.Errorf("csv %q %v", csv, err)
	}
	if x, err := b.XLSX(); err != nil || !bytes.HasPrefix(x, []byte("PK")) {
		t.Errorf("xlsx %v", err)
	}
	if p, err := b.PDF("Borç dökümü"); err != nil || !bytes.HasPrefix(p, []byte("%PDF")) {
		t.Errorf("pdf %v", err)
	}
	if got := collections.FormatTL(123450); got != "1.234,50 ₺" {
		t.Errorf("FormatTL %q", got)
	}
	// person mode: no debt of its own
	pb, err := w.col.Breakdown(ctx, e.OrgID, e.SiteID, billing.Debtor{PersonID: &w.p1}, "")
	if err != nil || len(pb.Lines) != 0 {
		t.Errorf("person %+v %v", pb, err)
	}
}
