package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/governance/govtest"
	pchat "github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

type tgCall struct {
	method string
	body   map[string]any
}

// fakeTelegram answers every Bot API method the group features use and records the calls.
type fakeTelegram struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []tgCall
	msgID int
	dead  bool // sendMessage answers 403: the bot was removed
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		method := strings.TrimPrefix(r.URL.Path, "/bot"+tgToken+"/")
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, tgCall{method, m})
		if f.dead && method == "sendMessage" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.msgID++
		if method == "sendPoll" {
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"poll":{"id":"poll-%d"}}}`, f.msgID, f.msgID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, f.msgID)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) take(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out, rest []tgCall
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		} else {
			rest = append(rest, c)
		}
	}
	f.calls = rest
	bodies := make([]map[string]any, len(out))
	for i, c := range out {
		bodies[i] = c.body
	}
	return bodies
}

func (f *fakeTelegram) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

type fakeCmds struct{ opened []string }

func (c *fakeCmds) Balance(_ context.Context, _, _ uuid.UUID, _ string) ([]string, string, error) {
	return []string{"Daire A (malik): 1.250,00 TL"}, "https://example.test/orgs/x/money/statements", nil
}

func (c *fakeCmds) OpenTicket(_ context.Context, _, _ uuid.UUID, text string) (string, error) {
	c.opened = append(c.opened, text)
	return "AB12CD34", nil
}

func (c *fakeCmds) Announcements(_ context.Context, _, _ uuid.UUID, n int) ([]string, error) {
	return []string{"Water cut", "Elevator <service>"}[:min(n, 2)], nil
}

// groupEnv is the chat service with the group features wired to fakes: posts
// are queued (the test runs them) instead of going through the job queue.
type groupEnv struct {
	*opsEnv
	svc    *chat.Service
	tg     *fakeTelegram
	cmds   *fakeCmds
	queued []jobs.GroupPostArgs
	org    authztest.Org
	oid    string
	site   d2Site
	mgr    string
	seq    int
}

func newGroupEnv(t *testing.T) *groupEnv {
	t.Helper()
	e := newOpsEnv(t)
	f := newFakeChat(t)
	e.withChat(t, f)
	tg := newFakeTelegram(t)
	svc, err := chat.NewService(e.db, chat.Config{EncryptionKey: []byte("0123456789abcdef0123456789abcdef-chat"), BaseURL: "https://example.test", Options: pchat.Options{TelegramBase: tg.srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	e.deps.Chat = svc
	e.h = NewRouter(e.deps, Options{})
	g := &groupEnv{opsEnv: e, svc: svc, tg: tg, cmds: &fakeCmds{}, org: e.w.OrgA, oid: e.w.OrgA.ID.String()}
	svc.SetInsert(func(_ context.Context, _ pgx.Tx, a river.JobArgs) error {
		g.queued = append(g.queued, a.(jobs.GroupPostArgs))
		return nil
	})
	svc.SetCommands(g.cmds)
	e.ops.announcements.SetGroups(svc)
	e.gov.SetGroups(svc)
	svc.SetPollSink(e.gov)
	g.site = newD2Site(e.w, g.org, t)
	g.mgr = e.role(t, "site_manager")
	g.configureChat(t, g.oid)
	return g
}

func (g *groupEnv) update(t *testing.T, body string, want int) {
	t.Helper()
	mustStatus(t, g.hook("/v1/orgs/"+g.oid+"/chat/webhooks/telegram", body, "X-Telegram-Bot-Api-Secret-Token", tgSecret), want)
}

func (g *groupEnv) nextID() int { g.seq++; return 1000 + g.seq }

func (g *groupEnv) groupMsg(chatID int, kind, title, text string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"text":%q,"chat":{"id":%d,"type":%q,"title":%q},"from":{"id":42}}}`, g.nextID(), text, chatID, kind, title)
}

func (g *groupEnv) privateMsg(updateID, from int, text string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"text":%q,"chat":{"id":%d,"type":"private"},"from":{"id":%d}}}`, updateID, text, from, from)
}

func (g *groupEnv) vote(updateID int, pollID string, user, option int) string {
	return fmt.Sprintf(`{"update_id":%d,"poll_answer":{"poll_id":%q,"user":{"id":%d},"option_ids":[%d]}}`, updateID, pollID, user, option)
}

// linkedGroup marks the site's group as linked without the code flow.
func (g *groupEnv) linkedGroup(t *testing.T, title string) {
	t.Helper()
	g.w.Exec(t, `INSERT INTO chat_group (org_id, id, site_id, provider, chat_id, title, kind, verified, verified_at) VALUES ($1, $2, $3, 'telegram', '-1001', $4, 'group', true, now())`,
		g.org.ID, id.New(), g.site.Site, title)
}

// linkUser links a resident's Telegram account (chat id = Telegram user id) through the real flow.
func (g *groupEnv) linkUser(t *testing.T, r resident, tgUser int) {
	t.Helper()
	code, _ := g.startLink(t, r.token, g.oid, "telegram")
	g.update(t, g.privateMsg(g.nextID(), tgUser, "/start "+code), 200)
}

func (g *groupEnv) run(t *testing.T) {
	t.Helper()
	q := g.queued
	g.queued = nil
	for _, a := range q {
		if err := g.svc.RunGroupPost(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
}

func (g *groupEnv) groupURL() string {
	return orgURL(g.org, "/sites/"+g.site.Site.String()+"/chat-group")
}

func TestIntegrationChatGroupLinkFlowUnlinkAndReplay(t *testing.T) {
	g := newGroupEnv(t)
	ann := g.addResident(t, g.site.SiteFix, g.site.Unit, "owner", "Ann")
	type status struct {
		Status string  `json:"status"`
		Title  *string `json:"title"`
		Kind   *string `json:"kind"`
		Bot    *string `json:"bot_username"`
	}
	get := func(tok string, want int) status {
		rec := g.call(t, tok, "GET", g.groupURL(), "")
		mustStatus(t, rec, want)
		if want != 200 {
			return status{}
		}
		return jsonOf[status](t, rec)
	}
	if st := get(g.mgr, 200); st.Status != "none" || st.Bot == nil || *st.Bot != "bekci_test_bot" {
		t.Fatalf("fresh status = %+v", st)
	}
	// residents manage nothing
	get(ann.token, 403)
	mustStatus(t, g.call(t, ann.token, "POST", g.groupURL()+":start", ""), 403)
	mustStatus(t, g.call(t, ann.token, "DELETE", g.groupURL(), ""), 403)

	rec := g.call(t, g.mgr, "POST", g.groupURL()+":start", "")
	mustStatus(t, rec, 200)
	code := jsonOf[struct {
		Code string `json:"code"`
	}](t, rec).Code
	if st := get(g.mgr, 200); st.Status != "pending" {
		t.Fatalf("after start = %+v", st)
	}
	// the code is not stored in clear text
	if n := scalarOf[int](t, g.opsEnv, `SELECT count(*) FROM chat_group WHERE org_id = $1 AND code_hash = $2`, g.org.ID, []byte(code)); n != 0 {
		t.Fatal("the code is stored in clear text")
	}

	// the webhook authenticates; a wrong code and a private chat link nothing
	path := "/v1/orgs/" + g.oid + "/chat/webhooks/telegram"
	mustStatus(t, g.hook(path, g.groupMsg(-1001, "supergroup", "Site Group", "/link "+code)), 401)
	g.update(t, g.groupMsg(-1001, "supergroup", "Site Group", "/link BEKCI-AAAAAAAAAAAAAAAAAAAAAAAAAA"), 200)
	g.update(t, g.groupMsg(-1001, "supergroup", "Site Group", "hello "+code), 200)
	if st := get(g.mgr, 200); st.Status != "pending" {
		t.Fatalf("a wrong code or plain text linked: %+v", st)
	}
	g.tg.reset()
	linkMsg := g.groupMsg(-1001, "supergroup", "Site <Group>", "/link@bekci_test_bot "+code)
	g.update(t, linkMsg, 200)
	st := get(g.mgr, 200)
	if st.Status != "linked" || st.Title == nil || *st.Title != "Site <Group>" || st.Kind == nil || *st.Kind != "group" {
		t.Fatalf("after /link = %+v", st)
	}
	acks := g.tg.take("sendMessage")
	if len(acks) != 1 || acks[0]["chat_id"] != "-1001" {
		t.Fatalf("acknowledgement = %v", acks)
	}
	// replay: the same update again does nothing, and the spent code links nothing else
	g.update(t, linkMsg, 200)
	g.update(t, g.groupMsg(-1002, "group", "Other", "/link "+code), 200)
	if a := g.tg.take("sendMessage"); len(a) != 0 {
		t.Fatalf("a replay answered again: %v", a)
	}
	if n := scalarOf[int](t, g.opsEnv, `SELECT count(*) FROM chat_group WHERE org_id = $1 AND verified`, g.org.ID); n != 1 {
		t.Fatalf("%d linked groups", n)
	}

	// unlink: the bot leaves and the site has no group
	mustStatus(t, g.call(t, g.mgr, "DELETE", g.groupURL(), ""), 204)
	left := g.tg.take("leaveChat")
	if len(left) != 1 || left[0]["chat_id"] != "-1001" {
		t.Fatalf("leaveChat = %v", left)
	}
	if st := get(g.mgr, 200); st.Status != "none" {
		t.Fatalf("after unlink = %+v", st)
	}
	mustStatus(t, g.call(t, g.mgr, "DELETE", g.groupURL(), ""), 404)
	for _, a := range []string{"chat.group_link_start", "chat.group_link", "chat.group_unlink"} {
		if n := scalarOf[int](t, g.opsEnv, `SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = $2`, g.org.ID, a); n != 1 {
			t.Fatalf("audit %s: %d", a, n)
		}
	}
}

func TestIntegrationChatGroupWhatsAppInviteForResidents(t *testing.T) {
	g := newGroupEnv(t)
	ann := g.addResident(t, g.site.SiteFix, g.site.Unit, "owner", "Ann")
	info := orgURL(g.org, "/sites/"+g.site.Site.String()+"/chat-info")
	get := func() *string {
		rec := g.call(t, ann.token, "GET", info, "")
		mustStatus(t, rec, 200)
		return jsonOf[struct {
			URL *string `json:"whatsapp_invite_url"`
		}](t, rec).URL
	}
	if get() != nil {
		t.Fatal("an invite link before one was set")
	}
	mustStatus(t, g.call(t, ann.token, "PATCH", g.groupURL(), `{"whatsapp_invite_url":"https://chat.whatsapp.com/AbCdEfGhIjKl"}`), 403)
	mustStatus(t, g.call(t, g.mgr, "PATCH", g.groupURL(), `{"whatsapp_invite_url":"https://evil.example/AbCdEfGhIjKl"}`), 422)
	mustStatus(t, g.call(t, g.mgr, "PATCH", g.groupURL(), `{"whatsapp_invite_url":"https://chat.whatsapp.com/AbCdEfGhIjKl"}`), 200)
	if u := get(); u == nil || *u != "https://chat.whatsapp.com/AbCdEfGhIjKl" {
		t.Fatalf("invite = %v", u)
	}
	mustStatus(t, g.call(t, g.mgr, "PATCH", g.groupURL(), `{"whatsapp_invite_url":null}`), 200)
	if get() != nil {
		t.Fatal("the invite link was not cleared")
	}
}

func TestIntegrationChatGroupAnnouncementPostAudienceGuardAndEscaping(t *testing.T) {
	g := newGroupEnv(t)
	other := g.w.NewUnit(t, g.site.SiteFix)
	type preview struct {
		TG *struct {
			Title    string `json:"title"`
			WillPost bool   `json:"will_post"`
			Reason   string `json:"reason"`
		} `json:"telegram_group"`
	}
	prev := func(aud string) preview {
		rec := g.call(t, g.mgr, "POST", orgURL(g.org, "/announcements:preview"), fmt.Sprintf(`{"site_id":%q,"audience":%s,"channels":["telegram_group"]}`, g.site.Site, aud))
		mustStatus(t, rec, 200)
		return jsonOf[preview](t, rec)
	}
	if p := prev(`{"kind":"site"}`); p.TG == nil || p.TG.WillPost || p.TG.Reason != "not_linked" {
		t.Fatalf("unlinked preview = %+v", p.TG)
	}
	g.linkedGroup(t, "Site Group")
	if p := prev(`{"kind":"site"}`); p.TG == nil || !p.TG.WillPost || p.TG.Title != "Site Group" {
		t.Fatalf("linked preview = %+v", p.TG)
	}
	narrow := fmt.Sprintf(`{"kind":"units","unit_ids":[%q]}`, other)
	if p := prev(narrow); p.TG == nil || p.TG.WillPost || p.TG.Reason != "audience_narrower" || p.TG.Title != "Site Group" {
		t.Fatalf("narrow preview = %+v", p.TG)
	}

	create := func(aud, pinned string) string {
		rec := g.call(t, g.mgr, "POST", orgURL(g.org, "/announcements"), fmt.Sprintf(
			`{"site_id":%q,"title":"Water <cut> & more","body_md":"**Saturday** <script>alert(1)</script> see [plan](https://example.test/p?a=1&b=2)","audience":%s,"channels":["telegram_group"],"pinned":%s}`, g.site.Site, aud, pinned))
		mustStatus(t, rec, 201)
		return jsonOf[struct {
			ID string `json:"id"`
		}](t, rec).ID
	}
	publish := func(annID, key string) {
		mustStatus(t, g.call(t, g.mgr, "POST", orgURL(g.org, "/announcements/"+annID+":publish"), ``, "Idempotency-Key", key), 200)
	}

	// a whole-site notice is posted once, as HTML, escaped, and pinned
	publish(create(`{"kind":"site"}`, "true"), "ann-group-1")
	if len(g.queued) != 1 || g.queued[0].Type != jobs.GroupAnnouncement || !g.queued[0].Pin {
		t.Fatalf("queued = %+v", g.queued)
	}
	g.run(t)
	msgs := g.tg.take("sendMessage")
	if len(msgs) != 1 || msgs[0]["parse_mode"] != "HTML" || msgs[0]["chat_id"] != "-1001" {
		t.Fatalf("sendMessage = %v", msgs)
	}
	html := msgs[0]["text"].(string)
	for _, want := range []string{"<b>Water &lt;cut&gt; &amp; more</b>", "<b>Saturday</b>", "&lt;script&gt;alert(1)&lt;/script&gt;", `<a href="https://example.test/p?a=1&amp;b=2">plan</a>`} {
		if !strings.Contains(html, want) {
			t.Errorf("post lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "<cut>") {
		t.Fatalf("unescaped HTML in the post:\n%s", html)
	}
	if pins := g.tg.take("pinChatMessage"); len(pins) != 1 {
		t.Fatalf("pins = %v", pins)
	}

	// a narrower audience is skipped: nothing is queued, the notice is still published
	publish(create(narrow, "false"), "ann-group-2")
	if len(g.queued) != 0 {
		t.Fatalf("a narrow audience was queued for the group: %+v", g.queued)
	}
	// an explicit publish with no group linked is refused
	g.w.Exec(t, `DELETE FROM chat_group WHERE org_id = $1`, g.org.ID)
	id3 := create(`{"kind":"site"}`, "false")
	mustStatus(t, g.call(t, g.mgr, "POST", orgURL(g.org, "/announcements/"+id3+":publish"), ``, "Idempotency-Key", "ann-group-3"), 409)
	// a bot that was removed from the group loses the link
	g.linkedGroup(t, "Site Group")
	g.tg.dead = true
	if err := g.svc.RunGroupPost(context.Background(), jobs.GroupPostArgs{OrgID: g.org.ID, SiteID: g.site.Site, Type: jobs.GroupAnnouncement, HTML: "x"}); err != nil {
		t.Fatal(err)
	}
	if n := scalarOf[int](t, g.opsEnv, `SELECT count(*) FROM chat_group WHERE org_id = $1`, g.org.ID); n != 0 {
		t.Fatal("the link survived the bot's removal")
	}
}

func TestIntegrationChatGroupMeetingCallAndDuesTextsCarryNoPerson(t *testing.T) {
	// the dues reminder in a group never names a person or an amount (KVKK)
	dues := pchat.DuesHTML("tr", "Gül Sitesi", "")
	if regexp.MustCompile(`[0-9₺]`).MatchString(dues) || !strings.Contains(dues, "hesap ekstrenizi") {
		t.Fatalf("dues text = %q", dues)
	}
	call := pchat.MeetingCallHTML("en", "Rose <Court>", "AGM", "1 Dec 2026 19:00", "Hall & Garden", []string{"Budget", "Elections <1>"}, "https://example.test/meetings/1")
	for _, want := range []string{"Rose &lt;Court&gt;", "Hall &amp; Garden", "1. Budget", "2. Elections &lt;1&gt;", `<a href="https://example.test/meetings/1">`} {
		if !strings.Contains(call, want) {
			t.Errorf("call lacks %q:\n%s", want, call)
		}
	}
}

// pollEnv seeds a called meeting with an open poll whose snapshot has Ann's unit.
func (g *groupEnv) pollEnv(t *testing.T, ann resident) (meeting, item uuid.UUID) {
	t.Helper()
	meeting, item = id.New(), id.New()
	now := time.Now().UTC()
	snap, _ := json.Marshal(governance.Snapshot{AsOf: now, VoteWeight: "per_unit", ShareBasis: "equal", UnitCount: 2, TotalVotes: 2, TotalShare: 2, Unrepresented: []string{},
		Owners: []governance.SnapOwner{{PersonID: ann.person, DisplayName: "Ann", Units: []governance.SnapUnit{{UnitID: g.site.Unit, DoorNumber: g.site.DoorNum, Share: 1}}, Votes: 1, Share: 1}}})
	rules, _ := json.Marshal(govtest.Law634())
	g.w.Exec(t, `INSERT INTO meeting (id, org_id, site_id, kind, scope, scheduled_at, second_call_at, location, status, eligibility_snapshot, settings_snapshot, called_at)
		VALUES ($1, $2, $3, 'general_assembly', 'site', $4, $5, 'Hall', 'called', $6::jsonb, $7::jsonb, $8)`, meeting, g.org.ID, g.site.Site, now.Add(40*24*time.Hour), now.Add(50*24*time.Hour), string(snap), string(rules), now)
	g.w.Exec(t, `INSERT INTO agenda_item (id, org_id, site_id, meeting_id, position, title, will_be_voted) VALUES ($1, $2, $3, $4, 1, 'Budget', true)`, item, g.org.ID, g.site.Site, meeting)
	g.w.Exec(t, `INSERT INTO poll (org_id, site_id, agenda_item_id, question, opens_at, closes_at) VALUES ($1, $2, $3, 'Agree with the budget?', $4, $5)`,
		g.org.ID, g.site.Site, item, now.Add(-time.Hour), now.Add(24*time.Hour))
	return meeting, item
}

func TestIntegrationChatGroupPollMirrorVoteMappingAndResult(t *testing.T) {
	g := newGroupEnv(t)
	ctx := context.Background()
	ann := g.addResident(t, g.site.SiteFix, g.site.Unit, "owner", "Ann")
	_, item := g.pollEnv(t, ann)

	// without a linked group nothing is mirrored, and nothing is marked, so a later link still mirrors it
	if err := g.gov.RunPollMirror(ctx); err != nil || len(g.queued) != 0 {
		t.Fatalf("mirror without a group: %v %+v", err, g.queued)
	}
	g.linkedGroup(t, "Site Group")
	if err := g.gov.RunPollMirror(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.queued) != 1 || g.queued[0].Poll == nil || g.queued[0].Poll.Ref != item {
		t.Fatalf("queued = %+v", g.queued)
	}
	g.run(t)
	polls := g.tg.take("sendPoll")
	if len(polls) != 1 || polls[0]["is_anonymous"] != false || polls[0]["allows_multiple_answers"] != false || polls[0]["question"] != "Agree with the budget?" {
		t.Fatalf("sendPoll = %v", polls)
	}
	if opts, _ := polls[0]["options"].([]any); len(opts) != 3 {
		t.Fatalf("options = %v", polls[0]["options"])
	}
	// once only
	if err := g.gov.RunPollMirror(ctx); err != nil || len(g.queued) != 0 {
		t.Fatalf("a poll was mirrored twice: %v %+v", err, g.queued)
	}
	pollID := scalarOf[string](t, g.opsEnv, `SELECT tg_poll_id FROM chat_group_poll WHERE org_id = $1 AND agenda_item_id = $2`, g.org.ID, item)

	answers := func() int {
		return scalarOf[int](t, g.opsEnv, `SELECT count(*) FROM poll_answer WHERE org_id = $1 AND agenda_item_id = $2`, g.org.ID, item)
	}
	// a voter who is not linked is ignored, and told by DM, not in the group
	g.tg.reset()
	g.update(t, g.vote(1, pollID, 999, 0), 200)
	dm := g.tg.take("sendMessage")
	if answers() != 0 || len(dm) != 1 || dm[0]["chat_id"] != "999" || !strings.Contains(dm[0]["text"].(string), "Link your OpenBekci account first") {
		t.Fatalf("unlinked vote: answers=%d dm=%v", answers(), dm)
	}
	// a linked owner's vote is recorded for the unit
	g.linkUser(t, ann, 555)
	g.tg.reset()
	voteYes := g.vote(2, pollID, 555, 0)
	g.update(t, voteYes, 200)
	if got := scalarOf[string](t, g.opsEnv, `SELECT answer FROM poll_answer WHERE org_id = $1 AND agenda_item_id = $2 AND unit_id = $3`, g.org.ID, item, g.site.Unit); got != "yes" {
		t.Fatalf("answer = %q", got)
	}
	// a replayed webhook and a second vote for an answered unit change nothing; the second is told by DM
	g.update(t, voteYes, 200)
	g.update(t, g.vote(3, pollID, 555, 1), 200)
	if got := scalarOf[string](t, g.opsEnv, `SELECT answer FROM poll_answer WHERE org_id = $1 AND agenda_item_id = $2 AND unit_id = $3`, g.org.ID, item, g.site.Unit); got != "yes" || answers() != 1 {
		t.Fatalf("answer changed to %q (%d answers)", got, answers())
	}
	if dm := g.tg.take("sendMessage"); len(dm) != 1 || dm[0]["chat_id"] != "555" {
		t.Fatalf("DMs after the second vote = %v", dm)
	}
	// a withdrawn vote and a vote in an unknown poll are ignored
	g.update(t, fmt.Sprintf(`{"update_id":4,"poll_answer":{"poll_id":%q,"user":{"id":555},"option_ids":[]}}`, pollID), 200)
	g.update(t, g.vote(5, "poll-unknown", 555, 0), 200)

	// closing stops the poll and posts the result once
	g.w.Exec(t, `UPDATE poll SET opens_at = now() - interval '3 days', closes_at = now() - interval '1 minute' WHERE org_id = $1 AND agenda_item_id = $2`, g.org.ID, item)
	g.tg.reset()
	if err := g.gov.RunPollMirror(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.queued) != 1 || g.queued[0].ClosePoll == nil || *g.queued[0].ClosePoll != item {
		t.Fatalf("close queued = %+v", g.queued)
	}
	g.run(t)
	if stops := g.tg.take("stopPoll"); len(stops) != 1 {
		t.Fatalf("stopPoll = %v", stops)
	}
	res := g.tg.take("sendMessage")
	if len(res) != 1 || res[0]["chat_id"] != "-1001" || !regexp.MustCompile(`1\D+2`).MatchString(res[0]["text"].(string)) {
		t.Fatalf("result post = %v", res)
	}
	if err := g.gov.RunPollMirror(ctx); err != nil || len(g.queued) != 0 {
		t.Fatalf("the result was posted twice: %v %+v", err, g.queued)
	}
	// votes after the close are ignored
	g.update(t, g.vote(6, pollID, 555, 2), 200)
	if answers() != 1 {
		t.Fatal("a vote after the close was recorded")
	}
}

func TestIntegrationChatBotCommandsLinkedUsersOnly(t *testing.T) {
	g := newGroupEnv(t)
	ann := g.addResident(t, g.site.SiteFix, g.site.Unit, "owner", "Ann")
	say := func(updateID, from int, text string) string {
		g.tg.reset()
		g.update(t, g.privateMsg(updateID, from, text), 200)
		out := g.tg.take("sendMessage")
		if len(out) != 1 || out[0]["chat_id"] != fmt.Sprint(from) {
			t.Fatalf("%q: replies = %v", text, out)
		}
		return out[0]["text"].(string)
	}
	// a stranger gets only the way to link
	if r := say(1, 888, "/borcum"); !strings.Contains(r, "Link your OpenBekci account first") {
		t.Fatalf("stranger: %q", r)
	}
	g.linkUser(t, ann, 555)
	// plain talk is not answered
	g.tg.reset()
	g.update(t, g.privateMsg(10, 555, "thanks"), 200)
	if out := g.tg.take("sendMessage"); len(out) != 0 {
		t.Fatalf("plain text was answered: %v", out)
	}
	// Turkish by the user's locale
	if r := say(11, 555, "/borcum"); !strings.Contains(r, "Daire A (malik)") || !strings.Contains(r, "/money/statements") {
		t.Fatalf("/borcum: %q", r)
	}
	if r := say(12, 555, "/ariza  the tap leaks <b>"); !strings.Contains(r, "#AB12CD34") || len(g.cmds.opened) != 1 || g.cmds.opened[0] != "the tap leaks <b>" {
		t.Fatalf("/ariza: %q %v", r, g.cmds.opened)
	}
	if r := say(13, 555, "/ariza"); !strings.Contains(r, "Kullanım") || len(g.cmds.opened) != 1 {
		t.Fatalf("/ariza without text: %q", r)
	}
	if r := say(14, 555, "/duyurular"); !strings.Contains(r, "1. Water cut") || !strings.Contains(r, "Elevator &lt;service&gt;") {
		t.Fatalf("/duyurular: %q", r)
	}
	if r := say(15, 555, "/yardim"); !strings.Contains(r, "Komutlar") {
		t.Fatalf("/yardim: %q", r)
	}
	if r := say(16, 555, "/nonsense"); !strings.Contains(r, "Komutlar") {
		t.Fatalf("unknown command: %q", r)
	}
	// English by the user's locale; a replayed update answers once
	g.w.Exec(t, `UPDATE "user" SET locale = 'en' WHERE id = $1`, ann.user)
	if r := say(17, 555, "/help@bekci_test_bot"); !strings.Contains(r, "Commands:") {
		t.Fatalf("/help: %q", r)
	}
	g.tg.reset()
	g.update(t, g.privateMsg(17, 555, "/help@bekci_test_bot"), 200)
	if out := g.tg.take("sendMessage"); len(out) != 0 {
		t.Fatalf("a replayed command answered again: %v", out)
	}
	// a command in a group is not a command
	g.update(t, g.groupMsg(-1001, "supergroup", "G", "/borcum"), 200)
	if out := g.tg.take("sendMessage"); len(out) != 0 {
		t.Fatalf("a command in a group was answered: %v", out)
	}
}
