package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

var opsReq atomic.Int64

type opsEnv struct {
	*idEnv
	w *authztest.World
}

func newOpsEnv(t *testing.T) *opsEnv {
	t.Helper()
	e := newIDEnv(t, false, false)
	return &opsEnv{idEnv: e, w: authztest.NewWorld(t, e.db, e.svc)}
}

// call sends a request as the bearer token and returns the recorder.
func (e *opsEnv) call(t *testing.T, token, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = fmt.Sprintf("203.0.113.%d:4000", 1+opsReq.Add(1)%250)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *opsEnv) role(t *testing.T, name string) string { return e.w.Token(t, e.w.Actor(name)) }

func mustStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

func jsonOf[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

type resident struct {
	token  string
	person uuid.UUID
	user   uuid.UUID
}

// addResident gives a new user a person with an email and phone and an occupancy of kind on unit.
func (e *opsEnv) addResident(t *testing.T, f authztest.SiteFix, unit uuid.UUID, kind, name string) resident {
	t.Helper()
	u, err := e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: fmt.Sprintf("%s-%d@res.example.test", name, opsReq.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	p := id.New()
	e.w.Exec(t, `INSERT INTO person (id, org_id, user_id, display_name, contact) VALUES ($1, $2, $3, $4, $5)`, p, f.Org.ID, u.ID, name, fmt.Sprintf(`{"phone":"+90555%07d"}`, opsReq.Add(1)))
	e.w.NewOccupancy(t, f, unit, p, kind)
	return resident{token: e.w.Token(t, authztest.Actor{UserID: u.ID}), person: p, user: u.ID}
}

func orgURL(org authztest.Org, p string) string { return "/v1/orgs/" + org.ID.String() + p }

func jpegBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for x := 0; x < 64; x++ {
		for y := 0; y < 48; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 5), 120, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

// upload sends a photo as token and returns the file id (a draft until attached).
func (e *opsEnv) upload(t *testing.T, token string, org authztest.Org) uuid.UUID {
	t.Helper()
	body, ctype := multipartBody(map[string]string{}, "leak.jpg", jpegBytes())
	rec := e.call(t, token, "POST", orgURL(org, "/files"), body, "Content-Type", ctype)
	mustStatus(t, rec, 201)
	return jsonOf[struct {
		ID uuid.UUID `json:"id"`
	}](t, rec).ID
}

func (e *opsEnv) privateCategory(t *testing.T, s d2Site) uuid.UUID {
	t.Helper()
	cid := id.New()
	e.w.Exec(t, `INSERT INTO ticket_category (id, org_id, site_id, name, default_visibility, position) VALUES ($1, $2, $3, 'Flat repairs', 'private', 2)`, cid, s.Org.ID, s.Site)
	return cid
}

type ticketJSON struct {
	ID             uuid.UUID `json:"id"`
	Visibility     string    `json:"visibility"`
	StatusName     string    `json:"status_name"`
	StatusSemantic string    `json:"status_semantic"`
	VoteCount      int       `json:"vote_count"`
	VotedByMe      bool      `json:"voted_by_me"`
	MergedIntoID   *string   `json:"merged_into_id"`
	Version        int       `json:"version"`
	Attachments    []struct {
		FileID uuid.UUID `json:"file_id"`
		Mime   string    `json:"mime"`
	} `json:"attachments"`
}

func TestIntegrationTicketFlowAndVisibility(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	private := e.privateCategory(t, s)
	otherUnit := e.w.NewUnit(t, s.SiteFix)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	bob := e.addResident(t, s.SiteFix, otherUnit, "tenant", "Bob")

	// the resident opens a private ticket with a photo; the unit is her only one
	photo := e.upload(t, ann.token, org)
	rec := e.call(t, ann.token, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Kitchen tap leaks","description":"since monday","attachment_file_ids":[%q]}`, s.Site, private, photo))
	mustStatus(t, rec, 201)
	tk := jsonOf[ticketJSON](t, rec)
	if tk.Visibility != "private" || tk.StatusSemantic != "open_like" || len(tk.Attachments) != 1 || tk.Attachments[0].Mime != "image/jpeg" {
		t.Fatalf("created ticket: %s", rec.Body.String())
	}
	// the photo is visible to the reporter and refused to the neighbour
	mustStatus(t, e.call(t, ann.token, "GET", orgURL(org, "/files/"+photo.String()+"/content"), ""), 200)
	if rec := e.call(t, bob.token, "GET", orgURL(org, "/files/"+photo.String()+"/content"), ""); rec.Code == 200 {
		t.Fatalf("a neighbour downloaded the photo of a private ticket")
	}

	// who sees the private ticket: reporter and management, not the neighbour, not unassigned staff
	tpath := orgURL(org, "/tickets/"+tk.ID.String())
	for who, tok := range map[string]string{"reporter": ann.token, "site_manager": e.role(t, "site_manager"), "auditor": e.role(t, "auditor")} {
		if rec := e.call(t, tok, "GET", tpath, ""); rec.Code != 200 {
			t.Errorf("%s: GET ticket = %d", who, rec.Code)
		}
	}
	for who, tok := range map[string]string{"neighbour": bob.token, "staff": e.role(t, "staff")} {
		if rec := e.call(t, tok, "GET", tpath, ""); rec.Code != 404 {
			t.Errorf("%s sees a private ticket of another flat: %d", who, rec.Code)
		}
		list := e.call(t, tok, "GET", orgURL(org, "/tickets")+"?site_id="+s.Site.String(), "")
		mustStatus(t, list, 200)
		if strings.Contains(list.Body.String(), tk.ID.String()) {
			t.Errorf("%s lists a private ticket of another flat", who)
		}
	}

	// the manager moves it forward; the reporter is told
	e.ops.notes.take()
	rec = e.call(t, e.role(t, "site_manager"), "POST", tpath+":transition", fmt.Sprintf(`{"status_id":%q,"comment":"Plumber booked for Friday"}`, s.Done))
	mustStatus(t, rec, 200)
	sent := e.ops.notes.take()
	if len(sent) != 1 || sent[0].Type != "ticket.updated" || sent[0].Recipients[0].PersonID == nil || *sent[0].Recipients[0].PersonID != ann.person {
		t.Fatalf("notifications enqueued: %+v", sent)
	}
	if err := e.ops.notifications.Deliver(context.Background(), sent[0], 1); err != nil {
		t.Fatal(err)
	}
	rec = e.call(t, ann.token, "GET", orgURL(org, "/me/notifications/unread-count"), "")
	mustStatus(t, rec, 200)
	if n := jsonOf[struct {
		Unread int `json:"unread"`
	}](t, rec).Unread; n != 1 {
		t.Fatalf("unread = %d", n)
	}
	rec = e.call(t, ann.token, "GET", orgURL(org, "/me/notifications"), "")
	mustStatus(t, rec, 200)
	list := jsonOf[struct {
		Items []struct {
			ID      uuid.UUID `json:"id"`
			Type    string    `json:"type"`
			Link    string    `json:"link"`
			Payload struct {
				Status string `json:"status"`
			} `json:"payload"`
		} `json:"items"`
	}](t, rec)
	if len(list.Items) != 1 || list.Items[0].Type != "ticket.updated" || list.Items[0].Payload.Status != "Done" || list.Items[0].Link != "/tickets/"+tk.ID.String() {
		t.Fatalf("notifications: %s", rec.Body.String())
	}
	// ticket updates also go by email (the O5 default); no SMS
	if msgs := e.ops.mail.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "Done") || len(e.ops.sms.Messages()) != 0 {
		t.Fatalf("email %+v, sms %+v", msgs, e.ops.sms.Messages())
	}
	mustStatus(t, e.call(t, ann.token, "POST", orgURL(org, "/me/notifications/"+list.Items[0].ID.String()+":read"), ""), 200)
	rec = e.call(t, ann.token, "GET", orgURL(org, "/me/notifications/unread-count"), "")
	if n := jsonOf[struct {
		Unread int `json:"unread"`
	}](t, rec).Unread; n != 0 {
		t.Fatalf("unread after read = %d", n)
	}

	// history names who changed the status
	rec = e.call(t, ann.token, "GET", tpath+"/history", "")
	mustStatus(t, rec, 200)
	hist := jsonOf[struct {
		Items []struct {
			ToStatusName string `json:"to_status_name"`
			ChangedBy    *struct {
				DisplayName string `json:"display_name"`
			} `json:"changed_by"`
		} `json:"items"`
	}](t, rec)
	if len(hist.Items) != 2 || hist.Items[1].ToStatusName != "Done" || hist.Items[1].ChangedBy == nil || hist.Items[1].ChangedBy.DisplayName != "site_manager" {
		t.Fatalf("history: %s", rec.Body.String())
	}
}

func TestIntegrationCommonTicketsVotesAndMerge(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	otherUnit := e.w.NewUnit(t, s.SiteFix)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	bob := e.addResident(t, s.SiteFix, otherUnit, "tenant", "Bob")
	open := func(tok string) ticketJSON {
		rec := e.call(t, tok, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Elevator stuck","description":"floor 3"}`, s.Site, s.Category))
		mustStatus(t, rec, 201)
		return jsonOf[ticketJSON](t, rec)
	}
	first, dup := open(ann.token), open(bob.token)
	if first.Visibility != "common" {
		t.Fatalf("category default not applied: %s", first.Visibility)
	}
	// everybody on the site sees a common ticket, staff included
	mustStatus(t, e.call(t, bob.token, "GET", orgURL(org, "/tickets/"+first.ID.String()), ""), 200)
	mustStatus(t, e.call(t, e.role(t, "staff"), "GET", orgURL(org, "/tickets/"+first.ID.String()), ""), 200)
	// +1: one per person, idempotent, visible count
	vote := func(tok string, ticket uuid.UUID, method string) int {
		rec := e.call(t, tok, method, orgURL(org, "/tickets/"+ticket.String()+"/vote"), "")
		mustStatus(t, rec, 200)
		return jsonOf[struct {
			VoteCount int `json:"vote_count"`
		}](t, rec).VoteCount
	}
	if n := vote(bob.token, first.ID, "PUT"); n != 1 {
		t.Fatalf("votes = %d", n)
	}
	if n := vote(bob.token, first.ID, "PUT"); n != 1 {
		t.Fatalf("repeated vote counted: %d", n)
	}
	vote(ann.token, dup.ID, "PUT") // a vote on the duplicate, to carry over
	if n := vote(e.role(t, "site_manager"), first.ID, "PUT"); n != 2 {
		t.Fatalf("votes = %d", n)
	}
	if n := vote(e.role(t, "site_manager"), first.ID, "DELETE"); n != 1 {
		t.Fatalf("withdrawn vote: %d", n)
	}
	// the manager merges the duplicate: its vote carries over (ann voted on dup and not on first)
	rec := e.call(t, e.role(t, "site_manager"), "POST", orgURL(org, "/tickets/"+dup.ID.String()+":merge"), fmt.Sprintf(`{"into_ticket_id":%q}`, first.ID))
	mustStatus(t, rec, 200)
	if m := jsonOf[ticketJSON](t, rec); m.MergedIntoID == nil || m.StatusSemantic != "closed_like" {
		t.Fatalf("merged ticket: %s", rec.Body.String())
	}
	rec = e.call(t, ann.token, "GET", orgURL(org, "/tickets/"+first.ID.String()), "")
	if got := jsonOf[ticketJSON](t, rec); got.VoteCount != 2 || !got.VotedByMe {
		t.Fatalf("after merge: %s", rec.Body.String())
	}
	// a private ticket cannot be voted
	priv := e.privateCategory(t, s)
	rec = e.call(t, ann.token, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Door","description":"x"}`, s.Site, priv))
	mustStatus(t, rec, 201)
	mustStatus(t, e.call(t, ann.token, "PUT", orgURL(org, "/tickets/"+jsonOf[ticketJSON](t, rec).ID.String()+"/vote"), ""), 409)
	// internal notes stay hidden from residents
	mustStatus(t, e.call(t, e.role(t, "site_manager"), "POST", orgURL(org, "/tickets/"+first.ID.String()+"/comments"), `{"body":"call the lift company","visibility":"internal"}`), 201)
	mustStatus(t, e.call(t, ann.token, "POST", orgURL(org, "/tickets/"+first.ID.String()+"/comments"), `{"body":"thanks"}`), 201)
	mustStatus(t, e.call(t, ann.token, "POST", orgURL(org, "/tickets/"+first.ID.String()+"/comments"), `{"body":"secret","visibility":"internal"}`), 403)
	rec = e.call(t, ann.token, "GET", orgURL(org, "/tickets/"+first.ID.String()+"/comments"), "")
	mustStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "lift company") || !strings.Contains(rec.Body.String(), "thanks") {
		t.Fatalf("resident sees internal notes: %s", rec.Body.String())
	}
	rec = e.call(t, e.role(t, "site_manager"), "GET", orgURL(org, "/tickets/"+first.ID.String()+"/comments"), "")
	if !strings.Contains(rec.Body.String(), "lift company") {
		t.Fatalf("manager misses internal notes")
	}
}

func TestIntegrationWorkflowEditing(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	mgr := e.role(t, "site_manager")
	tk := s.ticket(e.w, t) // in the Open status
	path := orgURL(org, "/ticket-workflow") + "?site_id=" + s.Site.String()
	put := func(body string, ver string) *httptest.ResponseRecorder {
		return e.call(t, mgr, "PUT", path, body, "If-Match", ver)
	}
	custom := fmt.Sprintf(`{"statuses":[{"name":"New","semantic":"open_like"},{"id":%q,"name":"Done","semantic":"closed_like"}]}`, s.Done)
	// Open is removed and the ticket is in it: a replacement is needed
	rec := put(custom, `"1"`)
	mustStatus(t, rec, 422)
	if !strings.Contains(rec.Body.String(), "replacements.") {
		t.Fatalf("422 without the field: %s", rec.Body.String())
	}
	// a replacement must point at a kept status; an entry for an unused status is refused
	mustStatus(t, put(strings.TrimSuffix(custom, "}")+fmt.Sprintf(`,"replacements":{%q:%q}}`, s.Open, s.Open), `"1"`), 422)
	mustStatus(t, put(strings.TrimSuffix(custom, "}")+fmt.Sprintf(`,"replacements":{%q:%q,%q:%q}}`, s.Open, s.Done, uuid.New(), s.Done), `"1"`), 422)
	// a stale version
	mustStatus(t, put(strings.TrimSuffix(custom, "}")+fmt.Sprintf(`,"replacements":{%q:%q}}`, s.Open, s.Done), `"7"`), 409)
	mustStatus(t, put(strings.TrimSuffix(custom, "}")+fmt.Sprintf(`,"replacements":{%q:%q}}`, s.Open, s.Done), ""), 422)
	rec = put(strings.TrimSuffix(custom, "}")+fmt.Sprintf(`,"replacements":{%q:%q}}`, s.Open, s.Done), `"1"`)
	mustStatus(t, rec, 200)
	wf := jsonOf[struct {
		Statuses  []struct{ Name string } `json:"statuses"`
		IsDefault bool                    `json:"is_default"`
		Version   int                     `json:"version"`
	}](t, rec)
	if len(wf.Statuses) != 2 || wf.IsDefault || wf.Version != 2 {
		t.Fatalf("workflow: %s", rec.Body.String())
	}
	// the ticket moved to Done and is closed
	rec = e.call(t, mgr, "GET", orgURL(org, "/tickets/"+tk.String()), "")
	if got := jsonOf[ticketJSON](t, rec); got.StatusName != "Done" || got.StatusSemantic != "closed_like" {
		t.Fatalf("ticket after the edit: %s", rec.Body.String())
	}
	// residents and staff cannot edit the workflow
	mustStatus(t, e.call(t, e.role(t, "staff"), "PUT", path, custom, "If-Match", `"2"`), 403)
	// restore the default: the "New" status is unused, the others come back
	rec = e.call(t, mgr, "POST", orgURL(org, "/ticket-workflow:restore-default")+"?site_id="+s.Site.String(), ``, "If-Match", `"2"`)
	mustStatus(t, rec, 200)
	wf = jsonOf[struct {
		Statuses  []struct{ Name string } `json:"statuses"`
		IsDefault bool                    `json:"is_default"`
		Version   int                     `json:"version"`
	}](t, rec)
	if len(wf.Statuses) != 3 || !wf.IsDefault || wf.Statuses[1].Name != "In progress" {
		t.Fatalf("restored workflow: %s", rec.Body.String())
	}
	// categories: archived is tri-state
	cat := orgURL(org, "/ticket-categories")
	mustStatus(t, e.call(t, mgr, "PATCH", cat+"/"+s.Category.String(), `{"archived":true}`, "If-Match", `"1"`), 200)
	count := func(mode string) int {
		rec := e.call(t, mgr, "GET", cat+"?site_id="+s.Site.String()+"&archived="+mode, "")
		mustStatus(t, rec, 200)
		return len(jsonOf[struct {
			Items []struct{} `json:"items"`
		}](t, rec).Items)
	}
	if a, b, c := count("active"), count("archived"), count("all"); b != 1 || c != a+b {
		t.Fatalf("categories active=%d archived=%d all=%d", a, b, c)
	}
}

func TestIntegrationAnnouncementPreviewPublishAndReads(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	u2, u3 := e.w.NewUnit(t, s.SiteFix), e.w.NewUnit(t, s.SiteFix)
	a1 := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	a2 := e.addResident(t, s.SiteFix, u2, "tenant", "Bob")
	e.addResident(t, s.SiteFix, u3, "owner", "Cem")
	// a person without a phone is not an SMS recipient
	nophone := id.New()
	e.w.Exec(t, `INSERT INTO person (id, org_id, display_name) VALUES ($1, $2, 'No Phone')`, nophone, org.ID)
	e.w.NewOccupancy(t, s.SiteFix, u3, nophone, "household")
	mgr := e.role(t, "site_manager")

	preview := func(aud, ch string) struct{ Recipients, SMS, Email, InApp int } {
		rec := e.call(t, mgr, "POST", orgURL(org, "/announcements:preview"), fmt.Sprintf(`{"site_id":%q,"audience":%s,"channels":%s}`, s.Site, aud, ch))
		mustStatus(t, rec, 200)
		v := jsonOf[struct {
			R int `json:"recipient_count"`
			S int `json:"sms_count"`
			E int `json:"email_count"`
			A int `json:"in_app_count"`
		}](t, rec)
		return struct{ Recipients, SMS, Email, InApp int }{v.R, v.S, v.E, v.A}
	}
	if p := preview(`{"kind":"site"}`, `["in_app","sms"]`); p.Recipients != 4 || p.SMS != 3 || p.InApp != 3 || p.Email != 0 {
		t.Fatalf("site preview: %+v", p)
	}
	if p := preview(`{"kind":"owners"}`, `["sms"]`); p.Recipients != 2 || p.SMS != 2 {
		t.Fatalf("owners preview: %+v", p)
	}
	if p := preview(`{"kind":"tenants"}`, `["sms","email"]`); p.Recipients != 1 || p.SMS != 1 || p.Email != 1 {
		t.Fatalf("tenants preview: %+v", p)
	}
	if p := preview(fmt.Sprintf(`{"kind":"units","unit_ids":[%q]}`, u3), `["sms"]`); p.Recipients != 2 || p.SMS != 1 {
		t.Fatalf("units preview: %+v", p)
	}
	mustStatus(t, e.call(t, a1.token, "POST", orgURL(org, "/announcements:preview"), fmt.Sprintf(`{"site_id":%q,"audience":{"kind":"site"},"channels":["sms"]}`, s.Site)), 403)

	rec := e.call(t, mgr, "POST", orgURL(org, "/announcements"), fmt.Sprintf(`{"site_id":%q,"title":"Water cut","body_md":"Saturday 10:00-14:00","audience":{"kind":"site"},"channels":["in_app","sms"]}`, s.Site))
	mustStatus(t, rec, 201)
	ann := jsonOf[struct {
		ID          uuid.UUID `json:"id"`
		Status      string    `json:"status"`
		Attachments []any     `json:"attachments"`
	}](t, rec)
	if ann.Status != "draft" {
		t.Fatalf("status %s", ann.Status)
	}
	// residents do not see drafts
	mustStatus(t, e.call(t, a1.token, "GET", orgURL(org, "/announcements/"+ann.ID.String()), ""), 404)
	// publishing needs an Idempotency-Key and replays with the same key
	pub := orgURL(org, "/announcements/"+ann.ID.String()+":publish")
	mustStatus(t, e.call(t, mgr, "POST", pub, ``), 422)
	e.ops.notes.take()
	rec = e.call(t, mgr, "POST", pub, ``, "Idempotency-Key", "publish-water-cut-1")
	mustStatus(t, rec, 200)
	published := jsonOf[struct {
		Status         string `json:"status"`
		RecipientCount int    `json:"recipient_count"`
		SMSCount       int    `json:"sms_count"`
	}](t, rec)
	if published.Status != "published" || published.RecipientCount != 4 || published.SMSCount != 3 {
		t.Fatalf("published: %s", rec.Body.String())
	}
	mustStatus(t, e.call(t, mgr, "POST", pub, ``, "Idempotency-Key", "publish-water-cut-1"), 200) // replay
	sent := e.ops.notes.take()
	if len(sent) != 1 || sent[0].Type != "announcement.published" || len(sent[0].Recipients) != 4 || strings.Join(sent[0].Channels, ",") != "in_app,sms" {
		t.Fatalf("enqueued: %+v", sent)
	}
	// the dispatcher sends one SMS per recipient with a phone and writes in-app rows for the others
	if err := e.ops.notifications.Deliver(context.Background(), sent[0], 1); err != nil {
		t.Fatal(err)
	}
	if got := len(e.ops.sms.Messages()); got != 3 {
		t.Fatalf("sms sent: %d", got)
	}
	if m, _ := e.ops.sms.Last(); !strings.Contains(m.Text, "Water cut") {
		t.Fatalf("sms text %q", m.Text)
	}
	// delivering the same job again changes nothing (retries are safe)
	if err := e.ops.notifications.Deliver(context.Background(), sent[0], 2); err != nil {
		t.Fatal(err)
	}
	if got := len(e.ops.sms.Messages()); got != 3 {
		t.Fatalf("sms after redelivery: %d", got)
	}
	// the monthly counter of the site
	rec = e.call(t, mgr, "GET", orgURL(org, "/sms-usage")+"?site_id="+s.Site.String(), "")
	mustStatus(t, rec, 200)
	if u := jsonOf[struct {
		Sent int `json:"sent"`
	}](t, rec); u.Sent != 3 {
		t.Fatalf("sms usage: %s", rec.Body.String())
	}
	// read receipts count only opens in the app
	rd := orgURL(org, "/announcements/"+ann.ID.String())
	mustStatus(t, e.call(t, a1.token, "GET", rd, ""), 200)
	mustStatus(t, e.call(t, a1.token, "PUT", rd+"/read", ``), 204)
	mustStatus(t, e.call(t, a1.token, "PUT", rd+"/read", ``), 204)
	rec = e.call(t, mgr, "GET", rd+"/reads", "")
	mustStatus(t, rec, 200)
	reads := jsonOf[struct {
		Items []struct {
			DisplayName string  `json:"display_name"`
			ReadAt      *string `json:"read_at"`
		} `json:"items"`
		AudienceCount int `json:"audience_count"`
		ReadCount     int `json:"read_count"`
	}](t, rec)
	if reads.AudienceCount != 4 || reads.ReadCount != 1 || len(reads.Items) != 4 || reads.Items[0].DisplayName != "Ayse" || reads.Items[0].ReadAt == nil {
		t.Fatalf("reads: %s", rec.Body.String())
	}
	rec = e.call(t, mgr, "GET", rd+"/reads?read=false", "")
	if n := len(jsonOf[struct {
		Items []struct{} `json:"items"`
	}](t, rec).Items); n != 3 {
		t.Fatalf("unread list: %s", rec.Body.String())
	}
	_ = a2
	// the published content is frozen; pin and expiry still change
	mustStatus(t, e.call(t, mgr, "PATCH", rd, `{"title":"New title"}`, "If-Match", `"2"`), 409)
	mustStatus(t, e.call(t, mgr, "PATCH", rd, `{"pinned":true}`, "If-Match", `"2"`), 200)
	// a resident of another site's unit does not see it
	otherSite := newD2Site(e.w, org, t)
	rec = e.call(t, a2.token, "GET", orgURL(org, "/announcements")+"?site_id="+otherSite.Site.String(), "")
	if rec.Code == 200 && strings.Contains(rec.Body.String(), ann.ID.String()) {
		t.Fatalf("announcement leaked to another site")
	}
}

func TestIntegrationMutesAndDefaults(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	// mutes: sign-in and invitations cannot be muted; the rest can, and the list says which
	mustStatus(t, e.call(t, ann.token, "PUT", orgURL(org, "/me/notification-mutes"), `{"muted_types":["auth.sign_in_code"]}`), 422)
	mustStatus(t, e.call(t, ann.token, "PUT", orgURL(org, "/me/notification-mutes"), `{"muted_types":["no.such_type"]}`), 422)
	rec := e.call(t, ann.token, "PUT", orgURL(org, "/me/notification-mutes"), `{"muted_types":["charge.due_soon"]}`)
	mustStatus(t, rec, 200)
	got := jsonOf[struct {
		MutedTypes     []string `json:"muted_types"`
		UnmutableTypes []string `json:"unmutable_types"`
		AvailableTypes []string `json:"available_types"`
	}](t, rec)
	if len(got.MutedTypes) != 1 || len(got.UnmutableTypes) != 2 || len(got.AvailableTypes) < 20 {
		t.Fatalf("mutes: %s", rec.Body.String())
	}
	for _, a := range got.AvailableTypes {
		if a == "auth.sign_in_code" || a == "invitation.sent" {
			t.Fatalf("available types list an unmutable one: %s", a)
		}
	}
	args := jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "charge.due_soon", Key: "k1", Recipients: []jobs.Recipient{{PersonID: &ann.person}},
		Payload: map[string]any{"amount": "TRY 850.00", "unit": "A-1", "due_on": "2026-12-05"}}
	if err := e.ops.notifications.Deliver(context.Background(), args, 1); err != nil {
		t.Fatal(err)
	}
	rec = e.call(t, ann.token, "GET", orgURL(org, "/me/notifications"), "")
	if strings.Contains(rec.Body.String(), "charge.due_soon") || len(e.ops.sms.Messages()) != 0 {
		t.Fatalf("a muted type was delivered: %s", rec.Body.String())
	}
	// site defaults: due reminders go in-app plus SMS until the manager turns SMS off
	mgr := e.role(t, "site_manager")
	dpath := orgURL(org, "/notification-defaults") + "?site_id=" + s.Site.String()
	rec = e.call(t, mgr, "GET", dpath, "")
	mustStatus(t, rec, 200)
	def := jsonOf[struct {
		Version  int `json:"version"`
		Defaults []struct {
			Type     string   `json:"type"`
			Channels []string `json:"channels"`
			Locked   bool     `json:"locked"`
		} `json:"defaults"`
	}](t, rec)
	for _, d := range def.Defaults {
		if d.Type == "charge.due_soon" && strings.Join(d.Channels, ",") != "in_app,sms" {
			t.Fatalf("due_soon default: %v", d.Channels)
		}
		if d.Type == "ticket.updated" && strings.Join(d.Channels, ",") != "in_app,email" {
			t.Fatalf("ticket.updated default: %v", d.Channels)
		}
	}
	mustStatus(t, e.call(t, mgr, "PUT", dpath, `{"defaults":[{"type":"charge.due_soon","channels":["in_app"]}]}`), 422) // If-Match required
	mustStatus(t, e.call(t, mgr, "PUT", dpath, `{"defaults":[{"type":"auth.sign_in_code","channels":["in_app"]}]}`, "If-Match", `"1"`), 422)
	mustStatus(t, e.call(t, mgr, "PUT", dpath, `{"defaults":[{"type":"charge.due_soon","channels":["in_app"]}]}`, "If-Match", `"1"`), 200)
	mustStatus(t, e.call(t, mgr, "PUT", dpath, `{"defaults":[{"type":"charge.due_soon","channels":["in_app"]}]}`, "If-Match", `"1"`), 409)
	// unmute and deliver again: now in-app only
	mustStatus(t, e.call(t, ann.token, "PUT", orgURL(org, "/me/notification-mutes"), `{"muted_types":[]}`), 200)
	args.Key = "k2"
	if err := e.ops.notifications.Deliver(context.Background(), args, 1); err != nil {
		t.Fatal(err)
	}
	if len(e.ops.sms.Messages()) != 0 {
		t.Fatalf("SMS sent although the site turned it off")
	}
	rec = e.call(t, ann.token, "GET", orgURL(org, "/me/notifications"), "")
	if !strings.Contains(rec.Body.String(), "charge.due_soon") {
		t.Fatalf("not delivered in-app: %s", rec.Body.String())
	}
	// only managers read and write defaults
	mustStatus(t, e.call(t, ann.token, "GET", dpath, ""), 403)
}

func TestIntegrationDocumentsVisibilityAndFormerOccupants(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	unitB := e.w.NewUnit(t, s.SiteFix) // its private folder is created by the trigger
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	bob := e.addResident(t, s.SiteFix, unitB, "tenant", "Bob")
	mgr := e.role(t, "site_manager")

	folders := func(tok string) map[string]string { // name -> kind
		rec := e.call(t, tok, "GET", orgURL(org, "/folders")+"?site_id="+s.Site.String(), "")
		mustStatus(t, rec, 200)
		out := map[string]string{}
		for _, f := range jsonOf[struct {
			Items []struct{ Name, Kind string } `json:"items"`
		}](t, rec).Items {
			out[f.Name] = f.Kind
		}
		return out
	}
	// each unit has its private folder; a resident opens their own only, the manager all
	if f := folders(mgr); f[s.DoorNum] != "unit" || len(f) < 2 {
		t.Fatalf("manager folders: %v", f)
	}
	if f := folders(ann.token); f[s.DoorNum] != "unit" || len(f) != 1 {
		t.Fatalf("resident A folders: %v", f)
	}
	if f := folders(bob.token); len(f) != 1 || f[s.DoorNum] != "" {
		t.Fatalf("resident B sees %v", f)
	}
	// the manager makes site folders with each visibility
	mk := func(name, vis string) uuid.UUID {
		rec := e.call(t, mgr, "POST", orgURL(org, "/folders"), fmt.Sprintf(`{"site_id":%q,"name":%q,"visibility":%s}`, s.Site, name, vis))
		mustStatus(t, rec, 201)
		return jsonOf[struct {
			ID uuid.UUID `json:"id"`
		}](t, rec).ID
	}
	board := mk("Board", `{"level":"board"}`)
	owners := mk("Owners", `{"level":"owners"}`)
	everyone := mk("Everyone", `{"level":"everyone"}`)
	unitsOnly := mk("Unit B only", fmt.Sprintf(`{"level":"units","unit_ids":[%q]}`, unitB))
	seen := func(tok string) []string {
		var names []string
		for n, k := range folders(tok) {
			if k == "site" {
				names = append(names, n)
			}
		}
		return names
	}
	has := func(names []string, n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	owner, tenant, auditor := seen(ann.token), seen(bob.token), seen(e.role(t, "auditor"))
	if has(owner, "Board") || !has(owner, "Owners") || !has(owner, "Everyone") || has(owner, "Unit B only") {
		t.Errorf("owner sees %v", owner)
	}
	if has(tenant, "Board") || has(tenant, "Owners") || !has(tenant, "Everyone") || !has(tenant, "Unit B only") {
		t.Errorf("tenant sees %v", tenant)
	}
	if len(auditor) != 4 {
		t.Errorf("auditor sees %v", auditor)
	}
	_, _ = board, owners
	// documents: a file filed in a folder is readable (file download follows the folder)
	file := e.upload(t, mgr, org)
	rec := e.call(t, mgr, "POST", orgURL(org, "/documents"), fmt.Sprintf(`{"site_id":%q,"folder_id":%q,"title":"Insurance policy","file_id":%q}`, s.Site, everyone, file))
	mustStatus(t, rec, 201)
	doc := jsonOf[struct {
		ID uuid.UUID `json:"id"`
	}](t, rec)
	mustStatus(t, e.call(t, ann.token, "GET", orgURL(org, "/documents/"+doc.ID.String()), ""), 200)
	mustStatus(t, e.call(t, ann.token, "GET", orgURL(org, "/files/"+file.String()+"/content"), ""), 200)
	// ... and refused where the folder is closed
	file2 := e.upload(t, mgr, org)
	rec = e.call(t, mgr, "POST", orgURL(org, "/documents"), fmt.Sprintf(`{"site_id":%q,"folder_id":%q,"title":"Board minutes","file_id":%q}`, s.Site, board, file2))
	mustStatus(t, rec, 201)
	closed := jsonOf[struct {
		ID uuid.UUID `json:"id"`
	}](t, rec)
	mustStatus(t, e.call(t, ann.token, "GET", orgURL(org, "/documents/"+closed.ID.String()), ""), 404)
	if rec := e.call(t, ann.token, "GET", orgURL(org, "/files/"+file2.String()+"/content"), ""); rec.Code == 200 {
		t.Fatalf("a resident downloaded a board-only document")
	}
	rec = e.call(t, ann.token, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String(), "")
	if strings.Contains(rec.Body.String(), "Board minutes") || !strings.Contains(rec.Body.String(), "Insurance policy") {
		t.Fatalf("resident documents: %s", rec.Body.String())
	}
	// a resident uploads into the own unit's folder (documents.upload_own), not into another's
	var ownFolder uuid.UUID
	rec = e.call(t, ann.token, "GET", orgURL(org, "/folders")+"?site_id="+s.Site.String()+"&kind=unit", "")
	for _, f := range jsonOf[struct {
		Items []struct {
			ID     uuid.UUID `json:"id"`
			UnitID uuid.UUID `json:"unit_id"`
		} `json:"items"`
	}](t, rec).Items {
		ownFolder = f.ID
	}
	lease := e.upload(t, ann.token, org)
	mustStatus(t, e.call(t, ann.token, "POST", orgURL(org, "/documents"), fmt.Sprintf(`{"site_id":%q,"folder_id":%q,"title":"Lease","file_id":%q}`, s.Site, ownFolder, lease)), 201)
	// the neighbour cannot read it, the manager can
	rec = e.call(t, mgr, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String()+"&folder_id="+ownFolder.String(), "")
	if !strings.Contains(rec.Body.String(), "Lease") {
		t.Fatalf("manager misses the lease: %s", rec.Body.String())
	}
	rec = e.call(t, bob.token, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String(), "")
	if strings.Contains(rec.Body.String(), "Lease") {
		t.Fatalf("the neighbour sees the lease")
	}
	if rec := e.call(t, bob.token, "GET", orgURL(org, "/files/"+lease.String()+"/content"), ""); rec.Code == 200 {
		t.Fatalf("the neighbour downloaded the lease")
	}

	// a former occupant: the closing statement is addressed to them; they see it and nothing else
	former := e.addResident(t, s.SiteFix, s.Unit, "tenant", "Dora")
	e.w.Exec(t, `UPDATE occupancy SET valid_to = current_date - 1, valid_from = current_date - 400 WHERE org_id = $1 AND person_id = $2`, org.ID, former.person)
	var gen documents.Document
	err := e.db.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) error {
		var err error
		gen, err = e.ops.documents.FileGenerated(context.Background(), tx, documents.GeneratedParams{
			OrgID: org.ID, SiteID: s.Site, Title: "Closing statement", FileName: "closing.txt", Content: strings.NewReader("balance: 0"),
			UnitID: &s.Unit, GeneratedType: "closing_statement", GeneratedID: id.New(), AddressedPersonID: &former.person})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen.Source != "generated" || gen.GeneratedFrom == nil || gen.GeneratedFrom.Type != "closing_statement" {
		t.Fatalf("generated document: %+v", gen)
	}
	rec = e.call(t, former.token, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String(), "")
	mustStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), "Closing statement") || strings.Contains(rec.Body.String(), "Insurance policy") || strings.Contains(rec.Body.String(), "Lease") {
		t.Fatalf("former occupant documents: %s", rec.Body.String())
	}
	if f := folders(former.token); len(f) != 0 {
		t.Fatalf("a former occupant sees folders: %v", f)
	}
	mustStatus(t, e.call(t, former.token, "GET", orgURL(org, "/files/"+gen.FileID.String()+"/content"), ""), 200)
	// current residents of the unit read it through the unit folder
	rec = e.call(t, ann.token, "GET", orgURL(org, "/documents")+"?site_id="+s.Site.String()+"&source=generated", "")
	if !strings.Contains(rec.Body.String(), "Closing statement") {
		t.Fatalf("owner misses the closing statement in the unit folder: %s", rec.Body.String())
	}
	_ = unitsOnly
}

func TestIntegrationDashboardsAndCodes(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	s.ticket(e.w, t)
	s.ticket(e.w, t)
	dash := orgURL(org, "/sites/"+s.Site.String()+"/dashboard")
	rec := e.call(t, e.role(t, "accountant"), "GET", dash, "")
	mustStatus(t, rec, 200)
	site := jsonOf[struct {
		OpenTickets       int            `json:"open_tickets"`
		OpenCommonTickets int            `json:"open_common_tickets"`
		Cash              map[string]any `json:"cash"`
		ExpiringTerms     []any          `json:"expiring_terms"`
	}](t, rec)
	if site.OpenTickets != 2 || site.OpenCommonTickets != 2 || site.Cash["currency"] != "TRY" || site.ExpiringTerms == nil {
		t.Fatalf("site dashboard: %s", rec.Body.String())
	}
	// staff never see money and cannot open the site dashboard
	mustStatus(t, e.call(t, e.role(t, "staff"), "GET", dash, ""), 403)
	mustStatus(t, e.call(t, e.role(t, "guard"), "GET", dash, ""), 403)
	rec = e.call(t, e.role(t, "staff"), "GET", orgURL(org, "/sites/"+s.Site.String()+"/staff-dashboard"), "")
	mustStatus(t, rec, 200)
	if body := rec.Body.String(); strings.Contains(body, "cash") || strings.Contains(body, "receivable") || strings.Contains(body, "overdue_total") || !strings.Contains(body, `"assigned_open_tickets":2`) {
		t.Fatalf("staff dashboard: %s", body)
	}
	rec = e.call(t, e.role(t, "org_admin"), "GET", orgURL(org, "/dashboard/portfolio"), "")
	mustStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), s.Site.String()) {
		t.Fatalf("portfolio misses the site: %s", rec.Body.String())
	}
	mustStatus(t, e.call(t, e.role(t, "staff"), "GET", orgURL(org, "/dashboard/portfolio"), ""), 403)
	// the home summary of a resident
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	rec = e.call(t, ann.token, "GET", orgURL(org, "/me/dashboard"), "")
	mustStatus(t, rec, 200)
	if me := jsonOf[struct {
		Home  string `json:"home"`
		Sides []any  `json:"sides"`
	}](t, rec); me.Home != "resident" || me.Sides == nil {
		t.Fatalf("me dashboard: %s", rec.Body.String())
	}
	if me := jsonOf[struct{ Home string }](t, e.call(t, e.role(t, "org_admin"), "GET", orgURL(org, "/me/dashboard"), "")); me.Home != "portfolio" {
		t.Fatalf("org admin home %q", me.Home)
	}
	// codes: merged labels in both languages, including the site's own ticket statuses
	rec = e.call(t, ann.token, "GET", orgURL(org, "/codes")+"?locale=tr&site_id="+s.Site.String(), "")
	mustStatus(t, rec, 200)
	cs := jsonOf[struct {
		Locale string `json:"locale"`
		Enums  map[string][]struct {
			Code  string `json:"code"`
			Label string `json:"label"`
		} `json:"enums"`
		Errors         []any `json:"errors"`
		TicketStatuses []struct {
			Label string `json:"label"`
		} `json:"ticket_statuses"`
	}](t, rec)
	if cs.Locale != "tr" || len(cs.Enums["ticket_visibility"]) != 2 || len(cs.Errors) < 10 || len(cs.TicketStatuses) != 2 {
		t.Fatalf("codes: %s", rec.Body.String())
	}
	found := false
	for _, l := range cs.Enums["announcement_statuses"] {
		found = found || l.Code == "draft" && l.Label == "Taslak"
	}
	if !found {
		t.Fatalf("turkish label missing: %v", cs.Enums["announcement_statuses"])
	}
}
