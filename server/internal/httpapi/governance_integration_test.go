package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// recordingNotifier is a governance.Notifier that remembers what was enqueued.
type recordingNotifier struct {
	mu       sync.Mutex
	notifies []jobs.NotifyArgs
	absent   []uuid.UUID
}

func (r *recordingNotifier) Notify(_ context.Context, _ pgx.Tx, n jobs.NotifyArgs) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notifies = append(r.notifies, n)
	return nil
}

func (r *recordingNotifier) EnqueueNotifyAbsentees(_ context.Context, _ pgx.Tx, _, meetingID uuid.UUID, _ []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.absent = append(r.absent, meetingID)
	return nil
}

type gcall struct {
	t *testing.T
	e *authzEnv
}

func (c gcall) do(a authztest.Actor, method, path, body string, headers ...string) (int, map[string]any, string) {
	c.t.Helper()
	rec := c.e.as(a, method, path, body, headers...)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

func (c gcall) must(code int, a authztest.Actor, method, path, body string, headers ...string) map[string]any {
	c.t.Helper()
	got, m, raw := c.do(a, method, path, body, headers...)
	if got != code {
		c.t.Fatalf("%s %s as %s: status %d, want %d: %s", method, path, a.Name, got, code, raw)
	}
	return m
}

func litems(m map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range m["items"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func gstr(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func gnum(m map[string]any, k string) int    { f, _ := m[k].(float64); return int(f) }
func gmap(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

// The whole governance flow over HTTP: settings, a meeting with a snapshot at
// the call, notices, a non-binding poll, QR and desk check-in with the
// per-person cap, the live quorum, in-room counts, numbered decisions, an
// elected role that creates a site-owned membership (and applying twice), the
// minutes, closing and the decisions to the absent owner.
func TestIntegrationGovernanceFlow(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	rn := &recordingNotifier{}
	e.gov.SetNotifier(rn)
	mgr := w.Actor("site_manager")
	org := w.OrgA
	f := w.NewSite(t, org)

	// five units with arsa payi 10/20/30/30/10 and a sixth without an owner
	u := []uuid.UUID{f.Unit, w.NewUnit(t, f), w.NewUnit(t, f), w.NewUnit(t, f), w.NewUnit(t, f), w.NewUnit(t, f)}
	shares := []int{10, 20, 30, 30, 10, 0}
	for i, un := range u {
		w.Exec(t, `INSERT INTO allocation_key_value (id, org_id, site_id, key_id, unit_id, value, raw) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id.New(), org.ID, f.Site, f.Key, un, shares[i], fmt.Sprint(shares[i]))
	}
	owner2 := w.Person(t, org, w.Actor("no_membership")) // an owner with an account (answers the poll)
	p1, p3, p4, p5 := w.NewPerson(t, org), w.NewPerson(t, org), w.NewPerson(t, org), w.NewPerson(t, org)
	w.NewOccupancy(t, f, u[0], p1, "owner")
	w.NewOccupancy(t, f, u[1], p1, "owner")
	w.NewOccupancy(t, f, u[2], owner2.PersonID, "owner")
	co := w.NewOccupancy(t, f, u[3], p3, "owner") // co-owners of unit 4: p3 is the correspondent, p4 the other
	w.Exec(t, `UPDATE occupancy SET is_correspondent = true, share_pct = 50 WHERE org_id = $1 AND id = $2`, org.ID, co)
	w.Exec(t, `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, share_pct, valid_from) VALUES ($1, $2, $3, $4, $5, 'owner', 'owner', 50, '2020-01-01')`,
		id.New(), org.ID, f.Site, u[3], p4)
	w.NewOccupancy(t, f, u[4], p5, "owner")
	site := "/sites/" + f.Site.String()
	base := func(s string) string { return orgPath(org, s) }

	// settings: Law 634 preset, editable, versioned
	st := c.must(200, mgr, "GET", base(site+"/governance-settings"), "")
	if gstr(st, "preset") != "tr-634" || gnum(st, "version") != 1 {
		t.Fatalf("default settings = %v", st)
	}
	set := gmap(st, "settings")
	set["call_min_days"] = 10
	body, _ := json.Marshal(set)
	// below the legal default: a reason is required, and goes to the audit trail
	if r := c.must(422, mgr, "PUT", base(site+"/governance-settings"), string(body), "If-Match", `"1"`); fmt.Sprint(r["errors"]) == "" {
		t.Fatalf("no field errors: %v", r)
	}
	set["deviation_reason"] = "yonetim plani madde 12 allows ten days"
	body, _ = json.Marshal(set)
	st = c.must(200, mgr, "PUT", base(site+"/governance-settings"), string(body), "If-Match", `"1"`)
	if gnum(st, "version") != 2 || st["preset"] != nil || gnum(gmap(st, "settings"), "call_min_days") != 10 {
		t.Fatalf("edited settings = %v", st)
	}
	var withReason int
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'governance.settings_update' AND diff::text LIKE '%madde 12%'`, org.ID).Scan(&withReason)
	})
	if withReason != 1 {
		t.Fatalf("the deviation reason is not in the audit trail (%d events)", withReason)
	}
	c.must(409, mgr, "PUT", base(site+"/governance-settings"), string(body), "If-Match", `"1"`)
	set["thresholds"] = []any{map[string]any{"topic": "x", "rule": "coin_toss"}}
	bad, _ := json.Marshal(set)
	c.must(422, mgr, "PUT", base(site+"/governance-settings"), string(bad), "If-Match", `"2"`)
	set["call_min_days"] = 15
	set["notice_electronic_allowed"] = true
	set["thresholds"] = gmap(c.must(200, mgr, "GET", base(site+"/governance-settings"), ""), "settings")["thresholds"]
	body, _ = json.Marshal(set)
	c.must(200, mgr, "PUT", base(site+"/governance-settings"), string(body), "If-Match", `"2"`)

	// a meeting: the second call must fall in the 7 to 15 day window
	at := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Hour)
	mk := func(sched, second time.Time) string {
		return fmt.Sprintf(`{"site_id":%q,"kind":"general_assembly","title":"Yearly assembly","scheduled_at":%q,"second_call_at":%q,"location":"Community room"}`,
			f.Site, sched.Format(time.RFC3339), second.Format(time.RFC3339))
	}
	c.must(422, mgr, "POST", base("/meetings"), mk(at, at.Add(48*time.Hour)))
	mt := c.must(201, mgr, "POST", base("/meetings"), mk(at, at.Add(10*24*time.Hour)))
	mid := gstr(mt, "id")
	mpath := "/meetings/" + mid
	if gstr(mt, "status") != "draft" || mt["eligibility_snapshot"] != nil {
		t.Fatalf("new meeting = %v", mt)
	}
	// calling needs an agenda
	c.must(422, mgr, "POST", base(mpath+":call"), `{}`)
	item1 := gstr(c.must(201, mgr, "POST", base(mpath+"/agenda-items"), `{"title":"2026 budget","will_be_voted":true,"topic":"budget"}`), "id")
	item2 := gstr(c.must(201, mgr, "POST", base(mpath+"/agenda-items"), `{"title":"Elect the board","will_be_voted":true,"topic":"election"}`), "id")
	c.must(201, mgr, "POST", base(mpath+"/agenda-items"), `{"title":"Any other business"}`)
	c.must(422, mgr, "POST", base(mpath+"/agenda-items"), `{"title":"Bad topic","topic":"no_such_topic"}`)
	list := litems(c.must(200, mgr, "GET", base(mpath+"/agenda-items"), ""))
	if len(list) != 3 || gnum(list[0], "position") != 1 || gnum(list[2], "position") != 3 {
		t.Fatalf("agenda = %v", list)
	}

	// the call: snapshot of the owners of record, notices, letter
	called := c.must(200, mgr, "POST", base(mpath+":call"), `{"notify_channels":["in_app"]}`)
	if gstr(called, "status") != "called" || called["call_letter_document_id"] == nil {
		t.Fatalf("called meeting = %v", called)
	}
	snap := gmap(called, "eligibility_snapshot")
	if gnum(snap, "total_votes") != 5 || gnum(snap, "total_share") != 100 || gnum(snap, "unit_count") != 5 || gstr(snap, "share_basis") != "arsa_payi" {
		t.Fatalf("snapshot = %v", snap)
	}
	owners := snap["owners"].([]any)
	if len(owners) != 4 { // p1 (2 units), owner2, p3 (representing the co-owned unit), p5; p4 does not vote
		t.Fatalf("snapshot owners = %v", owners)
	}
	if un := snap["unrepresented_units"].([]any); len(un) != 1 {
		t.Fatalf("unrepresented units = %v", un)
	}
	// later changes do not rewrite the snapshot
	late := w.NewOccupancy(t, f, u[5], w.NewPerson(t, org), "owner")
	if again := c.must(200, mgr, "GET", base(mpath), ""); gnum(gmap(again, "eligibility_snapshot"), "total_votes") != 5 {
		t.Fatalf("snapshot changed after the call: %v", again["eligibility_snapshot"])
	}
	// (the snapshot is retaken when the session opens: this late owner is taken out again first)
	w.Exec(t, `DELETE FROM occupancy WHERE org_id = $1 AND id = $2`, org.ID, late)
	c.must(409, mgr, "POST", base(mpath+":call"), `{}`)
	c.must(409, mgr, "PATCH", base(mpath), `{"location":"Garden"}`, "If-Match", `"2"`)
	// too late: less than call_min_days before the meeting
	soon := c.must(201, mgr, "POST", base("/meetings"), mk(time.Now().UTC().Add(9*24*time.Hour), time.Now().UTC().Add(19*24*time.Hour)))
	c.must(201, mgr, "POST", base("/meetings/"+gstr(soon, "id")+"/agenda-items"), `{"title":"Soon"}`)
	c.must(422, mgr, "POST", base("/meetings/"+gstr(soon, "id")+":call"), `{}`)

	// call letters: generic, and personal with the QR
	rec := e.as(mgr, "GET", base(mpath+"/call-letter"), "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("call letter: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec = e.as(mgr, "GET", base(mpath+"/call-letter?person_id="+p1.String()), ""); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("personal call letter: %d", rec.Code)
	}
	// an owner fetches their own letter, not another's
	if rec = e.as(owner2, "GET", base(mpath+"/call-letter?person_id="+owner2.PersonID.String()), ""); rec.Code != 200 {
		t.Fatalf("own call letter: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.as(owner2, "GET", base(mpath+"/call-letter?person_id="+p1.String()), ""); rec.Code != 403 {
		t.Fatalf("someone else's call letter: %d", rec.Code)
	}

	// notices: the delivery record per owner
	nl := litems(c.must(200, mgr, "GET", base(mpath+"/notices"), ""))
	if len(nl) != 4 || gstr(nl[0], "status") != "pending" {
		t.Fatalf("notices = %v", nl)
	}
	nt := c.must(200, mgr, "PUT", base(mpath+"/notices/"+p1.String()),
		fmt.Sprintf(`{"phase":"call","method":"registered_mail","sent_at":%q,"tracking_no":"RR123456789TR"}`, time.Now().UTC().Format(time.RFC3339)))
	if gstr(nt, "status") != "recorded" || gstr(nt, "tracking_no") != "RR123456789TR" {
		t.Fatalf("recorded notice = %v", nt)
	}
	if n := gnum(c.must(200, mgr, "POST", base(mpath+"/notices:bulk"),
		fmt.Sprintf(`{"person_ids":[%q,%q],"phase":"call","method":"signed","sent_at":%q}`, owner2.PersonID, p3, time.Now().UTC().Format(time.RFC3339))), "recorded"); n != 2 {
		t.Fatalf("bulk recorded %d", n)
	}
	if rl := litems(c.must(200, mgr, "GET", base(mpath+"/notices?status=recorded"), "")); len(rl) != 3 {
		t.Fatalf("recorded notices = %d", len(rl))
	}
	c.must(422, mgr, "PUT", base(mpath+"/notices/"+p4.String()), fmt.Sprintf(`{"phase":"call","method":"signed","sent_at":%q}`, time.Now().UTC().Format(time.RFC3339))) // co-owner who does not vote

	// a non-binding poll: the owner answers for their unit only
	now := time.Now().UTC()
	pl := c.must(200, mgr, "PUT", base("/agenda-items/"+item1+"/poll"), fmt.Sprintf(`{"question":"Do you agree?","opens_at":%q,"closes_at":%q}`, now.Add(-time.Hour).Format(time.RFC3339), now.Add(24*time.Hour).Format(time.RFC3339)), "If-Match", `"0"`)
	if gstr(pl, "status") != "open" {
		t.Fatalf("poll = %v", pl)
	}
	my := c.must(200, owner2, "GET", base(mpath+"/me"), "")
	if len(my["eligible_unit_ids"].([]any)) != 1 || my["attendance_token"] == nil || len(my["polls"].([]any)) != 1 || my["notice"] == nil || my["eligibility_snapshot"] != nil {
		t.Fatalf("my meeting = %v", my)
	}
	c.must(200, owner2, "PUT", base("/agenda-items/"+item1+"/poll/answer"), fmt.Sprintf(`{"unit_id":%q,"answer":"yes"}`, u[2]))
	c.must(200, owner2, "PUT", base("/agenda-items/"+item1+"/poll/answer"), fmt.Sprintf(`{"unit_id":%q,"answer":"yes"}`, u[2])) // one answer per unit: replaced
	c.must(403, owner2, "PUT", base("/agenda-items/"+item1+"/poll/answer"), fmt.Sprintf(`{"unit_id":%q,"answer":"no"}`, u[0]))
	res := c.must(200, owner2, "GET", base("/agenda-items/"+item1+"/poll/results"), "")
	if gnum(res, "yes") != 1 || gnum(res, "units_answered") != 1 || gnum(res, "units_total") != 5 || res["binding"] != false {
		t.Fatalf("poll results = %v", res)
	}
	if al := litems(c.must(200, mgr, "GET", base("/agenda-items/"+item1+"/poll/answers"), "")); len(al) != 1 {
		t.Fatalf("poll answers = %v", al)
	}
	c.must(403, owner2, "GET", base("/agenda-items/"+item1+"/poll/answers"), "")

	// the session: nobody present, then QR and desk check-in; the per-person cap holds p1 to one vote
	c.must(200, mgr, "POST", base(mpath+":open-first-call"), ``)
	q := c.must(200, mgr, "GET", base(mpath+"/quorum"), "")
	if gnum(q, "present_count") != 0 || q["reached"] != false || gstr(q, "call") != "first" || gnum(q, "eligible_count") != 5 {
		t.Fatalf("quorum before anybody arrives = %v", q)
	}
	tok := "scanned-qr-token-of-p1" // only the hash is stored: set a token whose QR the desk scans
	w.Exec(t, `UPDATE attendance_token SET token_hash = $4 WHERE org_id = $1 AND meeting_id = $2 AND person_id = $3`, org.ID, uuid.MustParse(mid), p1, governance.HashToken(tok))
	var plain int
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM attendance_token WHERE org_id = $1 AND token_hash = $2`, org.ID, tok).Scan(&plain)
	})
	if plain != 0 {
		t.Fatal("a token is stored in the clear")
	}
	att := c.must(201, mgr, "POST", base(mpath+"/attendance:checkin"), fmt.Sprintf(`{"token":%q}`, tok))
	if gstr(att, "source") != "qr" || len(att["unit_ids"].([]any)) != 2 || gstr(att, "mode") != "in_person" {
		t.Fatalf("qr check-in = %v", att)
	}
	if again := c.must(201, mgr, "POST", base(mpath+"/attendance:checkin"), fmt.Sprintf(`{"token":%q}`, tok)); gstr(again, "id") != gstr(att, "id") {
		t.Fatalf("scanning twice made a second record: %v", again)
	}
	c.must(404, mgr, "POST", base(mpath+"/attendance:checkin"), `{"token":"not-a-token"}`)
	q = c.must(200, mgr, "GET", base(mpath+"/quorum"), "")
	if gnum(q, "present_count") != 1 { // two votes, capped to one third of five votes (one)
		t.Fatalf("quorum after p1 = %v", q)
	}
	deskBody := func(person uuid.UUID, units ...uuid.UUID) string {
		if units == nil {
			units = []uuid.UUID{}
		}
		b, _ := json.Marshal(map[string]any{"person_id": person, "unit_ids": units, "mode": "in_person"})
		return string(b)
	}
	c.must(201, mgr, "POST", base(mpath+"/attendance"), deskBody(owner2.PersonID, u[2]))
	c.must(409, mgr, "POST", base(mpath+"/attendance"), deskBody(owner2.PersonID, u[2])) // already represented
	c.must(422, mgr, "POST", base(mpath+"/attendance"), deskBody(owner2.PersonID, u[0])) // not their unit
	c.must(422, mgr, "POST", base(mpath+"/attendance"), deskBody(p4, u[3]))              // a co-owner who does not vote
	if q = c.must(200, mgr, "GET", base(mpath+"/quorum"), ""); q["reached"] != false || gnum(q, "present_count") != 2 {
		t.Fatalf("quorum after p1 and owner2 = %v", q)
	}
	c.must(201, mgr, "POST", base(mpath+"/attendance"), deskBody(p3)) // all of p3's units
	q = c.must(200, mgr, "GET", base(mpath+"/quorum"), "")
	if q["reached"] != true || gnum(q, "present_count") != 3 || gnum(q, "present_share") != 75 || gnum(q, "required_by_count_pct") != 50 {
		t.Fatalf("quorum with three of five = %v", q)
	}
	// a proxy: owner2 also holds p5's vote; the cap still counts them as one vote
	px := c.must(201, mgr, "POST", base(mpath+"/attendance"), fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"proxy","proxy_for_person_id":%q}`, owner2.PersonID, u[4], p5))
	if gstr(px, "mode") != "proxy" {
		t.Fatalf("proxy = %v", px)
	}
	if q = c.must(200, mgr, "GET", base(mpath+"/quorum"), ""); gnum(q, "present_count") != 3 || gnum(q, "present_share") != 65 {
		t.Fatalf("quorum with a capped proxy holder = %v", q)
	}
	if al := litems(c.must(200, mgr, "GET", base(mpath+"/attendance"), "")); len(al) != 4 {
		t.Fatalf("attendance = %d rows", len(al))
	}
	rec = e.as(mgr, "GET", base(mpath+"/attendance-sheet"), "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("attendance sheet: %d", rec.Code)
	}
	c.must(204, mgr, "DELETE", base(mpath+"/attendance/"+gstr(px, "id")), "") // p5 stays absent

	// in-room counts: the poll said yes, the room decides
	c.must(422, mgr, "PUT", base("/agenda-items/"+item1+"/vote-counts"), `{"yes_count":4,"no_count":0,"abstain_count":0}`)
	vc := c.must(200, mgr, "PUT", base("/agenda-items/"+item1+"/vote-counts"), `{"yes_count":0,"no_count":3,"abstain_count":0,"yes_share":0,"no_share":75}`)
	if gstr(vc, "outcome_hint") != "rejected" {
		t.Fatalf("a room that says no, whatever the poll said: %v", vc)
	}
	vc = c.must(200, mgr, "PUT", base("/agenda-items/"+item1+"/vote-counts"), `{"yes_count":3,"no_count":0,"abstain_count":0,"yes_share":75}`)
	if gstr(vc, "outcome_hint") != "accepted" || vc["recorded_at"] == nil {
		t.Fatalf("counts = %v", vc)
	}
	if vc = c.must(200, mgr, "PUT", base("/agenda-items/"+item2+"/vote-counts"), `{"yes_count":1,"no_count":2,"abstain_count":0,"yes_share":25,"no_share":50}`); gstr(vc, "outcome_hint") != "rejected" {
		t.Fatalf("election counts = %v", vc)
	}
	c.must(409, mgr, "PUT", base("/agenda-items/"+list[2]["id"].(string)+"/vote-counts"), `{"yes_count":1,"no_count":0,"abstain_count":0}`) // not a voted item
	c.must(403, owner2, "PUT", base("/agenda-items/"+item1+"/vote-counts"), `{"yes_count":1,"no_count":0,"abstain_count":0}`)
	c.must(403, owner2, "GET", base("/agenda-items/"+item1+"/vote-counts"), "") // owners see counts only where the site allows it

	// decisions: numbered, tied to the counts
	dec := func(item, text, outcome, extra string) string {
		return fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"agenda_item_id":%q,"text":%q,"outcome":%q%s}`, f.Site, mid, item, text, outcome, extra)
	}
	c.must(422, mgr, "POST", base("/decisions"), dec(item2, "Board elected", "accepted", "")) // the room rejected it
	// the election is re-counted while it has no decision: the room did accept it
	c.must(200, mgr, "PUT", base("/agenda-items/"+item2+"/vote-counts"), `{"yes_count":3,"no_count":0,"abstain_count":0,"yes_share":75}`)
	d1 := c.must(201, mgr, "POST", base("/decisions"), dec(item1, "2026 budget approved; dues become 2,000 TRY", "accepted", `,"concerns_residents":true`))
	d2 := c.must(201, mgr, "POST", base("/decisions"), dec(item2, "Board elected", "accepted", fmt.Sprintf(`,"dissents":[{"person_id":%q,"text":"I object to the process"}]`, owner2.PersonID)))
	// counts are final once the item has a decision
	c.must(409, mgr, "PUT", base("/agenda-items/"+item2+"/vote-counts"), `{"yes_count":0,"no_count":3,"abstain_count":0,"yes_share":0,"no_share":75}`)
	if gnum(d1, "sequence_no") != 1 || gnum(d2, "sequence_no") != 2 || gnum(d1, "year") != at.Year() {
		t.Fatalf("decision numbers = %v %v (year %d)", d1["sequence_no"], d2["sequence_no"], at.Year())
	}
	if ds := d2["dissents"].([]any); len(ds) != 1 || gstr(ds[0].(map[string]any), "display_name") != "no_membership" {
		t.Fatalf("dissents = %v", d2["dissents"])
	}
	c.must(422, mgr, "POST", base("/decisions"), fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"text":"x","outcome":"noted","dissents":[{"person_id":%q,"text":"y"}]}`, f.Site, mid, p4))
	c.must(409, mgr, "POST", base("/decisions"), dec(item1, "again", "accepted", "")) // one decision per item
	// the decision text is corrected while the session is open
	d2 = c.must(200, mgr, "PATCH", base("/decisions/"+gstr(d2, "id")), `{"text":"Ayse Yilmaz elected board member for one year"}`, "If-Match", `"1"`)
	if gnum(d2, "version") != 2 || gnum(d2, "sequence_no") != 2 {
		t.Fatalf("corrected decision = %v", d2)
	}
	// numbers stay gap-free under concurrency
	var wg sync.WaitGroup
	seqs := make([]int, 6)
	for i := range seqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, m, raw := c.do(mgr, "POST", base("/decisions"), fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"text":"Noted %d","outcome":"noted"}`, f.Site, mid, i))
			if code != 201 {
				t.Errorf("concurrent decision: %d %s", code, raw)
				return
			}
			seqs[i] = gnum(m, "sequence_no")
		}()
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, s := range seqs {
		seen[s] = true
	}
	for n := 3; n < 9; n++ {
		if !seen[n] {
			t.Fatalf("decision numbers %v are not 3..8 without gaps", seqs)
		}
	}
	all := litems(c.must(200, mgr, "GET", base("/decisions?site_id="+f.Site.String()+"&year="+fmt.Sprint(at.Year())), ""))
	if len(all) != 8 {
		t.Fatalf("decision history has %d entries, want 8", len(all))
	}
	if hits := litems(c.must(200, mgr, "GET", base("/decisions?site_id="+f.Site.String()+"&q=budget"), "")); len(hits) != 1 {
		t.Fatalf("search = %d hits", len(hits))
	}

	// applying: an election creates a site-owned membership with its term, linked to the decision
	apply := func(id, body, key string, code int) map[string]any {
		return c.must(code, mgr, "POST", base("/decisions/"+id+":apply"), body, "Idempotency-Key", key)
	}
	mship := fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"board_member","scope_type":"site","scope_id":%q,"valid_from":"2026-12-01","valid_to":"2027-12-01"}`, p1, f.Site)
	// the money module is not installed here: its effects are refused clearly
	apply(gstr(d1, "id"), `{"kind":"dues_rate","plan_id":"`+uuid.NewString()+`","from":"2027-03-01","amount":{"amount":200000,"currency":"TRY"}}`, "apply-dues-0001", 409)
	r := apply(gstr(d2, "id"), mship, "apply-key-0001", 200)
	eff := gmap(gmap(r, "decision"), "effect")
	if gstr(eff, "kind") != "membership" || gstr(eff, "record_type") != "membership" {
		t.Fatalf("effect = %v", r)
	}
	var siteOwned bool
	var decisionID uuid.UUID
	var validTo time.Time
	if err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT site_owned, decision_id, valid_to FROM membership WHERE org_id = $1 AND id = $2`, org.ID, uuid.MustParse(gstr(eff, "record_id"))).Scan(&siteOwned, &decisionID, &validTo)
	}); err != nil {
		t.Fatal(err)
	}
	if !siteOwned || decisionID.String() != gstr(d2, "id") || validTo.UTC().Format("2006-01-02") != "2027-12-01" {
		t.Fatalf("membership: site_owned=%v decision=%v valid_to=%v", siteOwned, decisionID, validTo)
	}
	// applying twice creates nothing new
	r2 := apply(gstr(d2, "id"), mship, "apply-key-0002", 200)
	if gstr(gmap(gmap(r2, "decision"), "effect"), "record_id") != gstr(eff, "record_id") {
		t.Fatalf("second apply created another record: %v", r2)
	}
	var n int
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM membership WHERE org_id = $1 AND decision_id = $2`, org.ID, uuid.MustParse(gstr(d2, "id"))).Scan(&n)
	})
	if n != 1 {
		t.Fatalf("%d memberships for the decision, want 1", n)
	}
	rec = e.as(mgr, "POST", base("/decisions/"+gstr(d2, "id")+":apply"), mship, "Idempotency-Key", "apply-key-0001")
	if rec.Code != 200 || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay with the same key: %d %s", rec.Code, rec.Header().Get("Idempotent-Replayed"))
	}
	// a different effect on the same decision is a conflict
	apply(gstr(d2, "id"), strings.Replace(mship, p1.String(), p3.String(), 1), "apply-key-0003", 409)
	apply(gstr(d2, "id"), `{"kind":"budget_final","budget_id":"`+uuid.NewString()+`"}`, "apply-key-0004", 409)
	// dry run previews and writes nothing
	pv := c.must(200, mgr, "POST", base("/decisions/"+gstr(d1, "id")+":apply?dry_run=true"),
		fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"auditor","scope_type":"site","scope_id":%q,"valid_from":"2026-12-01","valid_to":"2027-12-01"}`, p3, f.Site), "Idempotency-Key", "apply-key-0006")
	if pv["dry_run"] != true || pv["preview"] == nil || gmap(pv, "decision")["effect"] != nil {
		t.Fatalf("dry run = %v", pv)
	}
	// end_previous: the new manager ends the old one's term
	oldMgr := id.New()
	w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from) VALUES ($1, $2, $3, 'site_manager', 'site', $4, '2025-01-01')`, oldMgr, org.ID, p4, f.Site)
	// no term end given: the site's default_term_months (12) applies
	pv2 := c.must(200, mgr, "POST", base("/decisions/"+gstr(d1, "id")+":apply?dry_run=true"),
		fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"auditor","scope_type":"site","scope_id":%q,"valid_from":"2026-12-01"}`, p3, f.Site), "Idempotency-Key", "apply-key-0009")
	if gstr(gmap(pv2, "preview"), "valid_to") != "2027-12-01" {
		t.Fatalf("default term = %v", pv2["preview"])
	}
	// a second site manager is not touched, and neither is a board member: only the named holder ends
	otherMgr := id.New()
	w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from) VALUES ($1, $2, $3, 'site_manager', 'site', $4, '2025-01-01')`, otherMgr, org.ID, p5, f.Site)
	apply(gstr(d1, "id"), fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"site_manager","scope_type":"site","scope_id":%q,"valid_from":"2026-12-01","valid_to":"2027-12-01","end_previous":true}`, p3, f.Site), "apply-key-0005", 422)
	apply(gstr(d1, "id"), fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"site_manager","scope_type":"site","scope_id":%q,"valid_from":"2026-12-01","valid_to":"2027-12-01","replaces_membership_ids":[%q]}`, p3, f.Site, gstr(eff, "record_id")), "apply-key-0008", 422) // a board member's membership is not a manager's
	applied := apply(gstr(d1, "id"), fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"site_manager","scope_type":"site","scope_id":%q,"valid_from":"2026-12-01","valid_to":"2027-12-01","replaces_membership_ids":[%q]}`, p3, f.Site, oldMgr), "apply-key-0007", 200)
	if gmap(gmap(applied, "decision"), "effect") == nil {
		t.Fatalf("applied = %v", applied)
	}
	var endedAt time.Time
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT valid_to FROM membership WHERE org_id = $1 AND id = $2`, org.ID, oldMgr).Scan(&endedAt)
	})
	if endedAt.UTC().Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("the previous manager's term ends %v, want 2026-12-01", endedAt)
	}
	var otherEnd *time.Time
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT valid_to FROM membership WHERE org_id = $1 AND id = $2`, org.ID, otherMgr).Scan(&otherEnd)
	})
	if otherEnd != nil {
		t.Fatalf("a holder that was not named ended too: %v", otherEnd)
	}

	// minutes
	c.must(200, mgr, "POST", base(mpath+"/minutes:generate"), ``)
	mn := c.must(200, mgr, "POST", base(mpath+"/minutes:generate"), ``) // regenerating replaces the stored bytes: one minutes document per meeting
	var minutesRows int
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM meeting_document WHERE org_id = $1 AND meeting_id = $2 AND kind = 'minutes'`, org.ID, uuid.MustParse(mid)).Scan(&minutesRows)
	})
	if minutesRows != 1 {
		t.Fatalf("%d stored minutes documents, want 1", minutesRows)
	}
	got := c.must(200, mgr, "GET", base(mpath+"/minutes"), "")
	if gstr(mn, "document_id") == "" || gstr(got, "document_id") != gstr(mn, "document_id") || gstr(got, "file_id") != gstr(mn, "file_id") {
		t.Fatalf("minutes = %v / %v", mn, got)
	}
	var stored []byte
	_ = e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT content FROM meeting_document WHERE org_id = $1 AND id = $2`, org.ID, uuid.MustParse(gstr(mn, "document_id"))).Scan(&stored)
	})
	if !strings.HasPrefix(string(stored), "%PDF-") {
		t.Fatalf("the stored minutes are not a PDF: %d bytes", len(stored))
	}

	// closing: the decisions go to the absent owner, recorded like the call
	nd := func(query, key string, code int) map[string]any {
		return c.must(code, mgr, "POST", base(mpath+":notify-decisions"+query), `{"channels":["in_app","sms"]}`, "Idempotency-Key", key)
	}
	c.must(409, mgr, "POST", base(mpath+":notify-decisions?dry_run=true"), `{"channels":["in_app"]}`, "Idempotency-Key", "notify-key-0000") // still in session
	c.must(200, mgr, "POST", base(mpath+":close"), ``)
	if len(rn.absent) != 0 {
		t.Fatalf("closing must not schedule the delivery of decisions: %v", rn.absent)
	}
	if r := nd("?dry_run=true", "notify-key-0001", 200); gnum(r, "recipient_count") != 1 || gnum(r, "sms_count") != 1 || r["dry_run"] != true {
		t.Fatalf("dry run = %v", r)
	}
	if r := nd("", "notify-key-0002", 200); gnum(r, "recipient_count") != 1 {
		t.Fatalf("notify = %v", r)
	}
	if len(rn.absent) != 1 {
		t.Fatalf(":notify-decisions did not schedule the delivery job: %v", rn.absent)
	}
	// the legal record is written at delivery (when the job runs), not when it is scheduled
	if dn := litems(c.must(200, mgr, "GET", base(mpath+"/notices?phase=decisions"), "")); len(dn) != 0 {
		t.Fatalf("decision notices before the job ran = %v", dn)
	}
	runJob := func() {
		if err := e.gov.RunNotifyAbsentees(context.Background(), org.ID, uuid.MustParse(mid), []string{"in_app", "sms"}); err != nil {
			t.Fatal(err)
		}
	}
	runJob() // what the job does
	dn := litems(c.must(200, mgr, "GET", base(mpath+"/notices?phase=decisions"), ""))
	if len(dn) != 1 || gstr(dn[0], "status") != "recorded" || gstr(dn[0], "method") != "sms" || gstr(dn[0], "person_id") != p5.String() {
		t.Fatalf("decision notices = %v", dn)
	}
	nBefore := len(rn.notifies)
	runJob() // a repeat does nothing
	if len(rn.notifies) != nBefore {
		t.Fatalf("a repeated delivery notified again")
	}
	if r := nd("", "notify-key-0003", 200); gnum(r, "recipient_count") != 0 {
		t.Fatalf("an owner whose delivery is recorded must be skipped: %v", r)
	}
	if g1 := c.must(200, mgr, "GET", base("/decisions/"+gstr(d1, "id")), ""); g1["notified_at"] == nil {
		t.Fatalf("decision not stamped notified: %v", g1)
	}
	c.must(409, mgr, "POST", base(mpath+"/attendance"), deskBody(p5)) // the meeting is closed

	// a tenant sees only what concerns residents
	tenant := w.Person(t, org, w.BAdmin)
	w.NewOccupancy(t, f, u[5], tenant.PersonID, "tenant")
	tl := litems(c.must(200, tenant, "GET", base("/decisions?site_id="+f.Site.String()), ""))
	if len(tl) != 1 || gstr(tl[0], "id") != gstr(d1, "id") {
		t.Fatalf("tenant decisions = %v", tl)
	}
	c.must(404, tenant, "GET", base("/decisions/"+gstr(d2, "id")), "")
	c.must(403, tenant, "GET", base(mpath+"/minutes"), "")
	if tm := c.must(200, tenant, "GET", base(mpath), ""); tm["eligibility_snapshot"] != nil {
		t.Fatalf("a tenant must not see who owns what: %v", tm)
	}
	c.must(403, tenant, "GET", base(mpath+"/notices"), "")
	// a tenant never sees the counts, and an owner sees dissents and counts only where the site allows it
	if tl[0]["vote_count"] != nil {
		t.Fatalf("a tenant must not see vote counts: %v", tl[0])
	}
	if od := c.must(200, owner2, "GET", base("/decisions/"+gstr(d2, "id")), ""); len(od["dissents"].([]any)) != 0 || od["vote_count"] != nil {
		t.Fatalf("an owner sees dissents and counts only where the site allows it: %v", od)
	}
	if md := c.must(200, mgr, "GET", base("/decisions/"+gstr(d2, "id")), ""); len(md["dissents"].([]any)) != 1 || md["vote_count"] == nil {
		t.Fatalf("staff see dissents and counts: %v", md)
	}
	w.Exec(t, `UPDATE governance_settings SET settings = jsonb_set(settings, '{decision_details_visible_to_owners}', 'true') WHERE org_id = $1 AND site_id = $2`, org.ID, f.Site)
	if od := c.must(200, owner2, "GET", base("/decisions/"+gstr(d2, "id")), ""); len(od["dissents"].([]any)) != 1 || od["vote_count"] == nil {
		t.Fatalf("with the setting on, owners see them: %v", od)
	}
	c.must(200, owner2, "GET", base("/agenda-items/"+item1+"/vote-counts"), "")
	c.must(404, tenant, "GET", base("/decisions/"+gstr(d2, "id")), "")
}

// Term reminders: 90/30/7 days before an elected term ends and once after,
// one per membership and lead time, notified through the shared Notify job.
func TestIntegrationGovernanceTermReminders(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	rec := &recordingNotifier{}
	e.gov.SetNotifier(rec)
	mgr := w.Actor("site_manager")
	f := w.NewSite(t, w.OrgA)
	board, auditor, long, mgrP := w.NewPerson(t, w.OrgA), w.NewPerson(t, w.OrgA), w.NewPerson(t, w.OrgA), w.NewPerson(t, w.OrgA)
	add := func(person uuid.UUID, role string, from, to time.Duration) uuid.UUID {
		mid := id.New()
		w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from, valid_to, site_owned) VALUES ($1, $2, $3, $4, 'site', $5, $6, $7, true)`,
			mid, w.OrgA.ID, person, role, f.Site, time.Now().Add(from), time.Now().Add(to))
		return mid
	}
	day := 24 * time.Hour
	dueID := add(board, "board_member", -300*day, 20*day)
	_ = add(auditor, "auditor", -400*day, -3*day)
	_ = add(long, "board_member", -10*day, 300*day)
	_ = add(mgrP, "site_manager", -300*day, 85*day)
	for i := 0; i < 2; i++ {
		if err := e.gov.RunTermReminders(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	items := litems(c.must(200, mgr, "GET", orgPath(w.OrgA, "/term-reminders?site_id="+f.Site.String()), ""))
	status := map[string]string{}
	for _, it := range items {
		status[gstr(it, "membership_id")] = gstr(it, "status")
	}
	var overdue, upcoming int
	for _, s := range status {
		switch s {
		case "overdue":
			overdue++
		case "upcoming":
			upcoming++
		}
	}
	if len(items) != 3 || status[dueID.String()] != "due" || overdue != 1 || upcoming != 1 {
		t.Fatalf("reminders = %v (want one due, one overdue, one upcoming, none for the long term)", status)
	}
	ofType := func(typ string) (n int) {
		for _, x := range rec.notifies {
			if x.Type == typ {
				n++
				if len(x.Recipients) == 0 || x.Key == "" || len(x.Channels) == 0 {
					t.Fatalf("notification = %+v", x)
				}
			}
		}
		return n
	}
	if n := ofType("term.expiring"); n != 3 {
		t.Fatalf("%d notifications for 3 terms (the second run must add none), want 3", n)
	}
	// the yearly reminder that the ordinary assembly is due: in the month before ordinary_assembly_month, once
	preset := governance.TurkishPreset()
	preset.OrdinaryAssemblyMonth = int64(time.Now().UTC().Month())%12 + 1
	sb, _ := json.Marshal(preset)
	w.Exec(t, `INSERT INTO governance_settings (org_id, site_id, preset, settings, version) VALUES ($1, $2, NULL, $3::jsonb, 2)`, w.OrgA.ID, f.Site, string(sb))
	for i := 0; i < 2; i++ {
		if err := e.gov.RunTermReminders(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	ofSite := 0
	for _, x := range rec.notifies {
		if x.Type == "meeting.assembly_due" && x.SiteID != nil && *x.SiteID == f.Site {
			ofSite++
		}
	}
	if ofSite != 1 {
		t.Fatalf("%d ordinary assembly reminders for the site, want exactly one for the year", ofSite)
	}
	if over := litems(c.must(200, mgr, "GET", orgPath(w.OrgA, "/term-reminders?site_id="+f.Site.String()+"&status=overdue"), "")); len(over) != 1 {
		t.Fatalf("overdue filter = %d", len(over))
	}
}
