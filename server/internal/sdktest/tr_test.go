package sdktest_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/collections"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/moneydocs"
	"github.com/openbekci/openbekci/server/internal/core/packdocs"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// The Turkish pack end to end: enablement and presets, the generic defaults of
// a site without the pack, every document template through the real core
// renderers, validators at the core's validation points, reference data, the
// consent texts and the labels.

func newTR(t *testing.T, opts ...sdktest.Option) *sdktest.Harness {
	t.Helper()
	h := sdktest.New(t, tr.New(), opts...)
	h.Exec(`UPDATE organization SET default_locale = 'tr' WHERE id = $1`, h.Env.OrgID)
	return h
}

func siteSettings(t *testing.T, h *sdktest.Harness, site uuid.UUID) map[string]any {
	t.Helper()
	var raw []byte
	h.Env.Query([]any{&raw}, `SELECT settings FROM site WHERE org_id = $1 AND id = $2`, h.Env.OrgID, site)
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func obj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	o, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("site setting %q is %v", key, m[key])
	}
	return o
}

func TestTRPackFillsTheSitePresetsOnEnable(t *testing.T) {
	h := newTR(t)
	site := h.Env.SiteID
	s := siteSettings(t, h, site)
	if s["legal_preset"] != "tr" {
		t.Fatalf("legal_preset = %v", s["legal_preset"])
	}
	q := obj(t, s, "quorum")
	if obj(t, q, "first_call")["basis"] != "units_and_share" || obj(t, q, "second_call")["basis"] != "none" {
		t.Errorf("quorum %v", q)
	}
	th := obj(t, s, "thresholds")
	if obj(t, obj(t, th, "by_topic"), "plan_amendment")["denominator"] != float64(5) {
		t.Errorf("thresholds %v", th)
	}
	if obj(t, s, "vote_weight")["proxy_small_building_unit_limit"] != float64(40) || obj(t, s, "schedule")["min_notice_days"] != float64(15) || obj(t, s, "transfer_rule")["debt_on_move_out"] != "stays_with_person" {
		t.Errorf("settings %v", s)
	}
	if lf := obj(t, s, "late_fee_policy"); lf["monthly_rate_ceiling_bps"] != float64(500) || lf["default_monthly_rate_bps"] != float64(500) || lf["pro_rata"] != true {
		t.Errorf("late fee %v", lf)
	}
	defs, err := h.Host.SiteDefaults(h.Ctx(), h.Env.OrgID, site)
	if err != nil || len(defs) != 7 || defs["quorum"].Pack != "tr" {
		t.Fatalf("pack defaults %v %v", defs, err)
	}
	// governance reads them back as the Law 634 rule set of the plan
	g := governance.FromSiteSettings(s)
	if g.Quorum.FirstCallByCountPct != 50 || g.Quorum.FirstCallBySharePct != 50 || g.RuleFor("plan_amendment").Rule != governance.RuleFourFifths || g.ProxyCapSmallBuildingMax != 2 {
		t.Errorf("governance settings %+v", g)
	}
}

func TestTRPackNamesTheChartInTurkish(t *testing.T) {
	h := newTR(t)
	var name string
	h.Env.Query([]any{&name}, `SELECT name FROM account WHERE org_id = $1 AND site_id = $2 AND code = '4000'`, h.Env.OrgID, h.Env.SiteID)
	if name != "Aidat gelirleri" {
		t.Fatalf("account 4000 is %q, want its Turkish name (the chart is created when the pack is enabled, names only)", name)
	}
	var typ string
	h.Env.Query([]any{&typ}, `SELECT type FROM account WHERE org_id = $1 AND site_id = $2 AND code = '4000'`, h.Env.OrgID, h.Env.SiteID)
	if typ != "income" {
		t.Fatalf("the structure is the core's: type %s", typ)
	}
}

func TestTRPackAppliesToNewSitesAndTheDefaultLateFeeRow(t *testing.T) {
	h := newTR(t)
	site, err := h.Tenancy.CreateSite(h.Ctx(), h.Env.OrgID, doc(map[string]any{"name": "Yeni Site"}))
	if err != nil {
		t.Fatal(err)
	}
	if site.Settings["legal_preset"] != "tr" || site.Settings["quorum"] == nil || site.Settings["late_fee_policy"] == nil {
		t.Fatalf("a site created in an organization with the pack gets the presets: %v", site.Settings)
	}
	if n := h.Int(`SELECT count(*) FROM late_fee_policy WHERE org_id = $1 AND site_id = $2 AND is_default AND rate_bps_per_month = 500 AND grace_days = 0`, h.Env.OrgID, site.ID); n != 1 {
		t.Fatalf("default late fee rows: %d", n)
	}
}

func TestTRSiteCanLowerTheLateFeeButNeverRaiseIt(t *testing.T) {
	h := newTR(t)
	ctx := h.Ctx()
	if _, err := h.Billing.SetLateFeePolicy(ctx, h.Env.OrgID, h.Env.SiteID, billing.PolicyRow{From: "2027-01-01", RateBPSPerMonth: 600}); err == nil {
		t.Fatal("6 % a month is above the 5 % ceiling of the Turkish preset")
	}
	if _, err := h.Billing.SetLateFeePolicy(ctx, h.Env.OrgID, h.Env.SiteID, billing.PolicyRow{From: "2027-01-01", RateBPSPerMonth: 300, GraceDays: 5}); err != nil {
		t.Fatalf("3 %% with 5 days of grace is allowed: %v", err)
	}
}

func TestSiteWithoutThePackGetsGenericDefaultsAndCanSetTheSameRulesByHand(t *testing.T) {
	h := newTR(t, sdktest.WithoutEnable())
	site := h.Env.SiteID
	s := siteSettings(t, h, site)
	if s["quorum"] != nil || s["legal_preset"] != nil {
		t.Fatalf("a site without the pack has no preset settings: %v", s)
	}
	ctx := h.Ctx()
	// the late fee: no ceiling, any rate up to the sanity bound
	if _, err := h.Billing.SetLateFeePolicy(ctx, h.Env.OrgID, site, billing.PolicyRow{From: "2027-01-01", RateBPSPerMonth: 900}); err != nil {
		t.Fatalf("without a pack there is no ceiling: %v", err)
	}
	// governance: the generic defaults, then the Law 634 values by hand
	gov := governance.NewService(h.Env.DB, h.Env.DB, governance.Config{Now: h.Env.Clock.Now})
	view, err := gov.GetSettings(ctx, h.Env.OrgID, site)
	if err != nil {
		t.Fatal(err)
	}
	if view.Preset != nil || view.Settings.Quorum.FirstCallBySharePct != 0 || view.Settings.RuleFor("budget").Rule != governance.RuleSimpleMajorityPresent {
		t.Fatalf("generic settings: %+v", view)
	}
	byHand := view.Settings
	byHand.Quorum = governance.QuorumRule{FirstCallByCountPct: 50, FirstCallBySharePct: 50}
	byHand.MaxVotesPerPersonPct, byHand.ProxyCapPct, byHand.ProxyCapSmallBuildingMax, byHand.SmallBuildingUnitLimit = 100.0/3, 5, 2, 40
	for i := range byHand.Thresholds {
		if byHand.Thresholds[i].Topic == "plan_amendment" {
			byHand.Thresholds[i].Rule = governance.RuleFourFifths
		}
	}
	got, err := gov.PutSettings(ctx, h.Env.OrgID, site, view.Version, byHand, "")
	if err != nil {
		t.Fatalf("the Law 634 values can be entered by hand: %v", err)
	}
	if got.Settings.RuleFor("plan_amendment").Rule != governance.RuleFourFifths || got.Settings.SmallBuildingUnitLimit != 40 {
		t.Fatalf("%+v", got.Settings)
	}
	// enabling the pack later fills only what the site has not changed: the quorum etc. are site settings, governance rows are the site's own
	h.Enable(nil)
	if siteSettings(t, h, site)["quorum"] == nil {
		t.Fatal("enabling the pack fills the unchanged site settings")
	}
}

func TestTRDeviationsFromTheLawDefaultNeedAReason(t *testing.T) {
	h := newTR(t)
	gov := governance.NewService(h.Env.DB, h.Env.DB, governance.Config{Now: h.Env.Clock.Now})
	view, err := gov.GetSettings(h.Ctx(), h.Env.OrgID, h.Env.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Preset == nil || *view.Preset != "tr" || view.Settings.CallMinDays != 15 {
		t.Fatalf("the Turkish site starts from the pack's rules: %+v", view)
	}
	weaker := view.Settings
	weaker.CallMinDays = 7
	if _, err := gov.PutSettings(h.Ctx(), h.Env.OrgID, h.Env.SiteID, view.Version, weaker, ""); err == nil {
		t.Fatal("a notice shorter than the law's default needs a reason")
	}
	if _, err := gov.PutSettings(h.Ctx(), h.Env.OrgID, h.Env.SiteID, view.Version, weaker, "yönetim planı m. 12: 7 gün"); err != nil {
		t.Fatal(err)
	}
}

// spy delegates to the pack host and keeps what each kind rendered.
type spy struct {
	r    doctpl.Resolver
	mu   sync.Mutex
	docs map[string]string
}

func (s *spy) Render(ctx context.Context, org uuid.UUID, kind, locale string, data map[string]any) (doctpl.Doc, bool, error) {
	d, ok, err := s.r.Render(ctx, org, kind, locale, data)
	if ok && err == nil {
		s.mu.Lock()
		if s.docs == nil {
			s.docs = map[string]string{}
		}
		s.docs[kind] = string(d.Body)
		s.mu.Unlock()
	}
	return d, ok, err
}

func (s *spy) has(t *testing.T, kind string, want ...string) {
	t.Helper()
	s.mu.Lock()
	body, ok := s.docs[kind]
	s.mu.Unlock()
	if !ok {
		t.Errorf("%s: no pack template rendered it", kind)
		return
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q:\n%s", kind, w, body)
		}
	}
}

func isPDF(b []byte) bool { return len(b) > 1000 && strings.HasPrefix(string(b), "%PDF-") }

// doc builds the request document the core services take from JSON-shaped values.
func doc(m map[string]any) tenancy.Doc {
	out := tenancy.Doc{}
	for k, v := range m {
		raw, _ := json.Marshal(v)
		out[k] = raw
	}
	return out
}

// The golden site: a Turkish site with two owners, a called meeting, a payment
// and a debt. Every template of the pack renders through the real renderer of
// the core and produces a PDF.
func TestTRGoldenSiteRendersEveryTemplate(t *testing.T) {
	h := newTR(t)
	ctx, org, site := h.Ctx(), h.Env.OrgID, h.Env.SiteID
	sp := &spy{r: h.Host}

	// two owners
	u1, u2 := h.Env.Unit("A-1", 500), h.Env.Unit("A-2", 500)
	p1, p2 := h.Env.Person("Ayşe Yılmaz"), h.Env.Person("Mehmet Çelik")
	for _, o := range []struct{ u, p uuid.UUID }{{u1, p1}, {u2, p2}} {
		h.Exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, share_pct, valid_from) VALUES (gen_random_uuid(), $1, $2, $3, $4, 'owner', 'owner', 100, '2020-01-01')`, org, site, o.u, o.p)
	}

	// governance: call, personal letter, attendance, minutes and the pack-only documents
	gov := governance.NewService(h.Env.DB, h.Env.DB, governance.Config{Now: h.Env.Clock.Now, InstanceName: "OpenBekci"})
	gov.SetTemplates(sp)
	title := "2026 olağan toplantı"
	m, err := gov.CreateMeeting(ctx, org, governance.MeetingCreate{SiteID: site, Kind: "general_assembly", Scope: "site", Title: &title,
		ScheduledAt: time.Date(2026, 2, 1, 16, 0, 0, 0, time.UTC), SecondCallAt: time.Date(2026, 2, 9, 16, 0, 0, 0, time.UTC), Location: "Site toplantı salonu"})
	if err != nil {
		t.Fatal(err)
	}
	topic := "budget"
	if _, err := gov.CreateAgendaItem(ctx, org, m.ID, governance.AgendaItemCreate{Title: "2026 bütçesi", WillBeVoted: true, Topic: &topic}); err != nil {
		t.Fatal(err)
	}
	if m, err = gov.CallMeeting(ctx, org, m.ID, governance.MeetingCall{}); err != nil {
		t.Fatalf("a meeting called 31 days ahead meets the pack's 15-day notice: %v", err)
	}
	sp.has(t, "call_letter", "KAT MALİKLERİ KURULU TOPLANTI ÇAĞRISI", "29. maddesi", "2026 bütçesi (oylama yapılacaktır)")
	if pdf, _, err := gov.CallLetterPDF(ctx, org, m.ID, nil); err != nil || !isPDF(pdf) {
		t.Fatalf("call letter: %v", err)
	}
	if pdf, _, err := gov.CallLetterPDF(ctx, org, m.ID, &p1); err != nil || !isPDF(pdf) {
		t.Fatalf("personal call letter: %v", err)
	}
	sp.has(t, "call_letter", "Sayın Ayşe Yılmaz", "[qr:")
	// the documents only the pack provides were filed with the call letter
	filed := func(kind string) int64 {
		return h.Int(`SELECT count(*) FROM meeting_document WHERE org_id = $1 AND meeting_id = $2 AND kind = $3 AND mime = 'application/pdf' AND length(content) > 1000 AND substring(content from 1 for 5) = '\x255044462d'::bytea`, org, m.ID, kind)
	}
	for _, kind := range []string{"call_letter", "delivery_checklist", "proxy_form"} {
		if n := filed(kind); n != 1 {
			t.Errorf("the called meeting has %d filed %s documents, want 1", n, kind)
		}
	}
	if n := filed("decision_book_draft"); n != 0 {
		t.Errorf("the decision book draft is filed with the minutes, not the call (%d)", n)
	}
	// a call 10 days ahead breaks the pack's 15-day minimum
	soon, _ := gov.CreateMeeting(ctx, org, governance.MeetingCreate{SiteID: site, Kind: "general_assembly", Scope: "site",
		ScheduledAt: time.Date(2026, 1, 11, 16, 0, 0, 0, time.UTC), SecondCallAt: time.Date(2026, 1, 19, 16, 0, 0, 0, time.UTC), Location: "Salon"})
	if _, err := gov.CreateAgendaItem(ctx, org, soon.ID, governance.AgendaItemCreate{Title: "X"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gov.CallMeeting(ctx, org, soon.ID, governance.MeetingCall{}); err == nil {
		t.Fatal("a call 10 days before the meeting is too late under the 15 day rule of the Turkish preset")
	}

	if pdf, _, err := gov.PackDocument(ctx, org, m.ID, "delivery_checklist", nil); err != nil || !isPDF(pdf) {
		t.Fatalf("delivery checklist: %v", err)
	}
	sp.has(t, "delivery_checklist", "ÇAĞRI TEBLİĞ LİSTESİ", "Ayşe Yılmaz", "Mehmet Çelik", "en az 15 gün")
	if pdf, _, err := gov.PackDocument(ctx, org, m.ID, "proxy_form", &p2); err != nil || !isPDF(pdf) {
		t.Fatalf("proxy form: %v", err)
	}
	sp.has(t, "proxy_form", "VEKÂLETNAME", "Mehmet Çelik", "A-2")

	if _, err := gov.OpenFirstCall(ctx, org, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gov.RecordAttendance(ctx, org, m.ID, governance.AttendanceCreate{PersonID: p1, UnitIDs: []uuid.UUID{u1}, Mode: "in_person"}); err != nil {
		t.Fatal(err)
	}
	if pdf, _, err := gov.AttendanceSheetPDF(ctx, org, m.ID); err != nil || !isPDF(pdf) {
		t.Fatalf("attendance sheet: %v", err)
	}
	sp.has(t, "attendance_list", "HAZİRUN CETVELİ", "Ayşe Yılmaz", "kendisi")
	if _, err := gov.GenerateMinutes(ctx, org, m.ID); err != nil {
		t.Fatal(err)
	}
	sp.has(t, "minutes", "KAT MALİKLERİ KURULU TOPLANTI TUTANAĞI", "yerine geçmez")
	if n := filed("minutes"); n != 1 || filed("decision_book_draft") != 1 {
		t.Errorf("the minutes and the decision book draft are filed together: %d minutes, %d drafts", n, filed("decision_book_draft"))
	}
	// generating the minutes again replaces the filed draft, it does not pile them up
	if _, err := gov.GenerateMinutes(ctx, org, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := filed("decision_book_draft"); n != 1 {
		t.Errorf("%d decision book drafts after regenerating the minutes", n)
	}
	if pdf, _, err := gov.PackDocument(ctx, org, m.ID, "decision_book_draft", nil); err != nil || !isPDF(pdf) {
		t.Fatalf("decision book draft: %v", err)
	}
	sp.has(t, "decision_book_draft", "KARAR DEFTERİ SAYFA TASLAĞI")
	if _, _, err := gov.PackDocument(ctx, org, m.ID, "no_such_document", nil); err == nil {
		t.Fatal("an unknown pack document is a 404")
	}

	// money: a debt, a payment, the receipt and the statement
	h.PostCharge(sdk.ID(u1.String()), 200000)
	pay := h.RecordPayment(sdk.ID(u1.String()), 50000)
	rep := reports.NewService(h.Env.DB, h.Env.Clock.Now)
	rep.SetTemplates(sp)
	mdocs := moneydocs.NewService(h.Env.DB, nil, h.Payments, rep, nil, nil)
	mdocs.PackDocs = sp
	payID, _ := uuid.Parse(string(pay))
	if pdf, _, err := mdocs.ReceiptPDF(ctx, org, payID); err != nil || !isPDF(pdf) {
		t.Fatalf("receipt: %v", err)
	}
	sp.has(t, "receipt", "AİDAT MAKBUZU", "Havale / EFT", "Kalan borç")
	st, err := rep.DebtorStatement(ctx, org, site, billing.Debtor{UnitID: &u1, Kind: "owner"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if pdf := rep.StatementPDF(ctx, org, st, "statement"); !isPDF(pdf) {
		t.Fatal("statement")
	}
	sp.has(t, "statement", "HESAP EKSTRESİ", "Tahakkuk", "Tahsilat")
	if pdf := rep.StatementPDF(ctx, org, st, "closing_statement"); !isPDF(pdf) {
		t.Fatal("closing statement")
	}
	sp.has(t, "closing_statement", "KAPANIŞ HESAP ÖZETİ", "kişisel hesabında")

	// the ihtarname for the debtor
	col := collections.NewService(h.Env.DB, h.Billing, h.Env.Clock.Now, nil)
	col.PackDocs = sp
	if pdf, err := col.Letter(ctx, org, collections.LetterInput{SiteID: site, Debtor: &billing.Debtor{UnitID: &u1, Kind: "owner"}}); err != nil || !isPDF(pdf) {
		t.Fatalf("ihtarname: %v", err)
	}
	sp.has(t, "demand_letter", "İHTARNAME", "20. maddesi", "22. maddesi")

	// the debt certificate has no flow in this fixture (move-out); render it through the same path
	if pdf, ok := packdocs.Render(ctx, sp, nil, org, "debt_certificate", "tr", map[string]any{"site_name": "Gül Sitesi", "person": "Ayşe Yılmaz", "unit": "A-1", "as_of": "2026-03-01", "has_debt": true, "balance": "1.500,00 ₺"}); !ok || !isPDF(pdf) {
		t.Fatal("debt certificate")
	}
	sp.has(t, "debt_certificate", "BORÇ DURUM BELGESİ", "borcu bulunmaktadır")

	// every template of the pack was exercised
	for _, tp := range tr.Templates() {
		sp.has(t, tp.Kind())
	}
}

// Without the pack the core renders its generic documents and the pack-only
// ones are not available.
func TestWithoutThePackTheGenericDocumentsAreRendered(t *testing.T) {
	h := newTR(t, sdktest.WithoutEnable())
	sp := &spy{r: h.Host}
	ctx, org := h.Ctx(), h.Env.OrgID
	if _, ok, _ := h.Host.Render(ctx, org, "call_letter", "tr", nil); ok {
		t.Fatal("a pack that is not enabled supplies no template")
	}
	p := h.Env.Person("A")
	u := h.Env.Unit("A-1", 0)
	h.Exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, share_pct, valid_from) VALUES (gen_random_uuid(), $1, $2, $3, $4, 'owner', 'owner', 100, '2020-01-01')`, org, h.Env.SiteID, u, p)
	gov := governance.NewService(h.Env.DB, h.Env.DB, governance.Config{Now: h.Env.Clock.Now})
	gov.SetTemplates(sp)
	m, err := gov.CreateMeeting(ctx, org, governance.MeetingCreate{SiteID: h.Env.SiteID, Kind: "general_assembly", Scope: "site", ScheduledAt: time.Date(2026, 1, 20, 16, 0, 0, 0, time.UTC), SecondCallAt: time.Date(2026, 1, 28, 16, 0, 0, 0, time.UTC), Location: "Salon"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = gov.CreateAgendaItem(ctx, org, m.ID, governance.AgendaItemCreate{Title: "Gündem"})
	if _, err := gov.CallMeeting(ctx, org, m.ID, governance.MeetingCall{}); err != nil {
		t.Fatalf("19 days ahead is fine under the generic 15 day default: %v", err)
	}
	if len(sp.docs) != 0 {
		t.Fatalf("no pack template may render: %v", sp.docs)
	}
	// without the pack only the generic call letter is filed
	if n := h.Int(`SELECT count(*) FROM meeting_document WHERE org_id = $1 AND meeting_id = $2 AND kind <> 'call_letter'`, h.Env.OrgID, m.ID); n != 0 {
		t.Fatalf("%d pack-only documents were filed without a pack", n)
	}
	if pdf, _, err := gov.CallLetterPDF(ctx, org, m.ID, nil); err != nil || !isPDF(pdf) {
		t.Fatalf("the generic call letter: %v", err)
	}
	if _, _, err := gov.PackDocument(ctx, org, m.ID, "delivery_checklist", nil); err == nil {
		t.Fatal("a pack-only document is not available without the pack")
	}
}

func TestTRValidatorsRejectBadInputAtTheCoreValidationPoints(t *testing.T) {
	h := newTR(t)
	ctx, org := h.Ctx(), h.Env.OrgID
	// the host's validators by kind
	for _, c := range []struct {
		kind, ok, bad string
	}{{"tax_id", "1234567890", "1234567891"}, {"iban", "TR33 0006 1005 1978 6457 8413 26", "TR33 0006 1005 1978 6457 8413 27"}, {"plate", "34 ABC 123", "34 QWX 123"}} {
		if ok, err := h.Host.ValidateValue(ctx, org, c.kind, "TR", c.ok); !ok || err != nil {
			t.Errorf("%s %q: %v %v", c.kind, c.ok, ok, err)
		}
		if _, err := h.Host.ValidateValue(ctx, org, c.kind, "TR", c.bad); err == nil {
			t.Errorf("%s %q must be rejected", c.kind, c.bad)
		}
	}
	// vendors: the tax id
	ps, err := people.NewService(h.Env.DB, nil, people.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ps.SetValidators(h.Host)
	if _, err := ps.CreateVendor(ctx, org, doc(map[string]any{"name": "Asansör AŞ", "tax_id": "1234567891"})); err == nil || !strings.Contains(err.Error(), "invalid_format") && !strings.Contains(err.Error(), "validation") {
		t.Fatalf("a vendor with a bad vergi numarası: %v", err)
	}
	if _, err := ps.CreateVendor(ctx, org, doc(map[string]any{"name": "Asansör AŞ", "tax_id": "1234567890"})); err != nil {
		t.Fatalf("a valid vergi numarası: %v", err)
	}
	// vehicles: the plate; site bank details: the IBAN
	tn := tenancy.NewService(h.Env.DB).WithValidators(h.Host)
	unit := h.Env.Unit("B-1", 0)
	if _, err := tn.CreateVehicle(ctx, org, unit, doc(map[string]any{"plate": "34 QWX 123"})); err == nil {
		t.Fatal("a plate with letters Turkish plates do not use")
	}
	if _, err := tn.CreateVehicle(ctx, org, unit, doc(map[string]any{"plate": "34 ABC 123"})); err != nil {
		t.Fatal(err)
	}
	site, _ := tn.GetSite(ctx, org, h.Env.SiteID)
	if _, err := tn.UpdateSite(ctx, org, h.Env.SiteID, site.Version, doc(map[string]any{"bank_details": map[string]any{"iban": "TR330006100519786457841327", "holder": "Gül Sitesi"}})); err == nil {
		t.Fatal("an IBAN that fails its check digits")
	}
	if _, err := tn.UpdateSite(ctx, org, h.Env.SiteID, site.Version, doc(map[string]any{"bank_details": map[string]any{"iban": "TR330006100519786457841326", "holder": "Gül Sitesi"}})); err != nil {
		t.Fatal(err)
	}
	// the same inputs are accepted where the pack is not enabled
	h.Disable()
	if _, err := ps.CreateVendor(ctx, org, doc(map[string]any{"name": "Başka AŞ", "tax_id": "1234567891"})); err != nil {
		t.Fatalf("without the pack any tax id is accepted: %v", err)
	}
}

func TestTRReferenceDataConsentsAndLabelsForEnabledOrgs(t *testing.T) {
	h := newTR(t)
	ctx, org := h.Ctx(), h.Env.OrgID
	prov, found, err := h.Host.Reference(ctx, org, "province", "")
	if err != nil || !found || len(prov) != 81 {
		t.Fatalf("provinces: %d %v %v", len(prov), found, err)
	}
	dist, _, _ := h.Host.Reference(ctx, org, "district", "06")
	if len(dist) != 25 {
		t.Fatalf("Ankara has 25 districts, got %d", len(dist))
	}
	hol, _, _ := h.Host.Reference(ctx, org, "holiday", "2026")
	if len(hol) != 17 {
		t.Fatalf("holidays 2026: %d", len(hol))
	}
	if _, found, _ := h.Host.Reference(ctx, org, "bank", ""); found {
		t.Fatal("no pack serves banks yet")
	}
	cs, err := h.Host.Consents(ctx, org, "tr")
	if err != nil || len(cs) != 5 || !strings.Contains(cs[0].Markdown, "VERBİS") {
		t.Fatalf("consents: %d %v", len(cs), err)
	}
	if en, _ := h.Host.Consents(ctx, org, "en"); len(en) != 5 || !strings.Contains(en[0].Markdown, "Privacy notice") {
		t.Fatal("English texts")
	}
	if de, _ := h.Host.Consents(ctx, org, "de"); len(de) != 0 {
		t.Fatal("no German texts: the endpoint falls back to English")
	}
	labels, err := h.Host.Labels(ctx, org, "tr")
	if err != nil || labels["tr.call_letter"] != "Toplantı çağrısı" || labels[tr.CodeTaxIDChecksum] == "" {
		t.Fatalf("labels: %v %v", labels, err)
	}
	if fmts, _ := h.Host.AddressFormats(ctx, org); len(fmts) != 1 || fmts[0].Country() != "TR" {
		t.Fatalf("address formats %v", fmts)
	}
	// the settings schema declares the KVKK controller and the TR-2 integrations
	list, err := h.Host.List(ctx, org)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	if !strings.Contains(string(list[0].Schemas["site"]), "tr_kvkk_data_controller") || !strings.Contains(string(list[0].Schemas["org"]), "tr_payments_iyzico_api_key") {
		t.Fatal("schemas")
	}
	// a disabled pack serves nothing
	h.Disable()
	if _, found, _ := h.Host.Reference(ctx, org, "province", ""); found {
		t.Fatal("a disabled pack serves no reference data")
	}
	if cs, _ := h.Host.Consents(ctx, org, "tr"); len(cs) != 0 {
		t.Fatal("a disabled pack serves no consent texts")
	}
}

var _ = pgx.ErrNoRows
