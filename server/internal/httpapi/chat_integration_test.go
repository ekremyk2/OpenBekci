package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	pchat "github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

const (
	tgToken  = "123456789:AAH_fake-token_for_tests_0123456789abcdef"
	tgSecret = "tg-secret-0123456789abcd"
	waApp    = "wa-app-secret-123"
	waVerify = "wa-verify-123"
)

// fakeChat stands in for the Telegram Bot API and the WhatsApp Cloud API.
type fakeChat struct {
	tg, wa *httptest.Server

	mu       sync.Mutex
	tgBodies []map[string]any
	waBodies []map[string]any
	tgStatus int // 0 means 200
	waCode   int // Meta error code to answer with (400), 0 means ok
}

func newFakeChat(t *testing.T) *fakeChat {
	t.Helper()
	f := &fakeChat{}
	f.tg = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/bot"+tgToken+"/sendMessage" {
			w.WriteHeader(404)
			return
		}
		f.tgBodies = append(f.tgBodies, m)
		if f.tgStatus != 0 {
			w.WriteHeader(f.tgStatus)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	f.wa = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/1234567890/messages" || r.Header.Get("Authorization") != "Bearer wa-access-token-xyz" {
			w.WriteHeader(401)
			return
		}
		f.waBodies = append(f.waBodies, m)
		if f.waCode != 0 {
			w.WriteHeader(400)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%d}}`, f.waCode)
			return
		}
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.1"}]}`))
	}))
	t.Cleanup(f.tg.Close)
	t.Cleanup(f.wa.Close)
	return f
}

func (f *fakeChat) take() (tg, wa []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tg, wa = f.tgBodies, f.waBodies
	f.tgBodies, f.waBodies = nil, nil
	return
}

// withChat wires the chat service into the environment: the router, and the
// dispatcher the test delivers through.
func (e *opsEnv) withChat(t *testing.T, f *fakeChat) *chat.Service {
	t.Helper()
	svc, err := chat.NewService(e.db, chat.Config{EncryptionKey: []byte("0123456789abcdef0123456789abcdef-chat"), BaseURL: "https://example.test",
		Options: pchat.Options{TelegramBase: f.tg.URL, WhatsAppBase: f.wa.URL}})
	if err != nil {
		t.Fatal(err)
	}
	e.deps.Chat = svc
	e.h = NewRouter(e.deps, Options{})
	e.ops.notifications = notifications.NewService(e.db, notifications.Config{Mail: e.ops.mail, SMS: e.ops.sms, BaseURL: "https://example.test", Chat: svc})
	return svc
}

func (e *opsEnv) configureChat(t *testing.T, org string) {
	t.Helper()
	admin := e.role(t, "org_admin")
	mustStatus(t, e.call(t, admin, "PUT", "/v1/orgs/"+org+"/chat-settings/telegram",
		fmt.Sprintf(`{"values":{"bot_username":"bekci_test_bot","bot_token":%q,"webhook_secret":%q}}`, tgToken, tgSecret)), 200)
	mustStatus(t, e.call(t, admin, "PUT", "/v1/orgs/"+org+"/chat-settings/whatsapp",
		fmt.Sprintf(`{"values":{"display_number":"+905551112233","phone_number_id":"1234567890","access_token":"wa-access-token-xyz","app_secret":%q,"verify_token":%q}}`, waApp, waVerify)), 200)
}

func (e *opsEnv) hook(path, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.RemoteAddr = "203.0.113.9:4000"
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func tgUpdate(chatID int, text string) string {
	return fmt.Sprintf(`{"update_id":1,"message":{"text":%q,"chat":{"id":%d,"type":"private"}}}`, text, chatID)
}

func waPayload(from, text string) string {
	return fmt.Sprintf(`{"entry":[{"changes":[{"value":{"messages":[{"from":%q,"type":"text","text":{"body":%q}}]}}]}]}`, from, text)
}

func waSig(body string) string {
	m := hmac.New(sha256.New, []byte(waApp))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

var codeFromLink = regexp.MustCompile(`BEKCI-[A-Z2-7]{26}`)

type startedJSON struct {
	Provider  string `json:"provider"`
	DeepLink  string `json:"deep_link"`
	ExpiresAt string `json:"expires_at"`
}

type linkJSON struct {
	ID       string  `json:"id"`
	Provider string  `json:"provider"`
	Status   string  `json:"status"`
	LinkedAt *string `json:"linked_at"`
}

func (e *opsEnv) links(t *testing.T, token, org string) []linkJSON {
	t.Helper()
	rec := e.call(t, token, "GET", "/v1/orgs/"+org+"/me/chat-links", "")
	mustStatus(t, rec, 200)
	return jsonOf[struct {
		Items []linkJSON `json:"items"`
	}](t, rec).Items
}

// start asks for a code and returns it with the deep link.
func (e *opsEnv) startLink(t *testing.T, token, org, provider string) (string, startedJSON) {
	t.Helper()
	rec := e.call(t, token, "POST", "/v1/orgs/"+org+"/me/chat-links/"+provider+":start", "")
	mustStatus(t, rec, 200)
	st := jsonOf[startedJSON](t, rec)
	code := codeFromLink.FindString(st.DeepLink)
	if code == "" {
		t.Fatalf("no code in %q", st.DeepLink)
	}
	return code, st
}

func TestIntegrationChatLinkingTelegramHappyPathReplayAndWrongCode(t *testing.T) {
	e := newOpsEnv(t)
	f := newFakeChat(t)
	e.withChat(t, f)
	org := e.w.OrgA
	oid := org.ID.String()
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	bob := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Bob")

	// not configured yet: no link can start
	mustStatus(t, e.call(t, ann.token, "POST", "/v1/orgs/"+oid+"/me/chat-links/telegram:start", ""), 409)
	e.configureChat(t, oid)

	code, st := e.startLink(t, ann.token, oid, "telegram")
	if want := "https://t.me/bekci_test_bot?start=" + code; st.DeepLink != want {
		t.Fatalf("deep link = %q, want %q", st.DeepLink, want)
	}
	if ls := e.links(t, ann.token, oid); len(ls) != 1 || ls[0].Status != "pending" {
		t.Fatalf("links after start: %+v", ls)
	}
	path := "/v1/orgs/" + oid + "/chat/webhooks/telegram"

	// the webhook must carry the secret token
	mustStatus(t, e.hook(path, tgUpdate(555, "/start "+code)), 401)
	mustStatus(t, e.hook(path, tgUpdate(555, "/start "+code), "X-Telegram-Bot-Api-Secret-Token", "wrong-secret-0123456789"), 401)
	if ls := e.links(t, ann.token, oid); ls[0].Status != "pending" {
		t.Fatal("an unauthenticated webhook verified a link")
	}
	// a wrong code verifies nothing and tells nothing
	mustStatus(t, e.hook(path, tgUpdate(555, "/start BEKCI-AAAAAAAAAA"), "X-Telegram-Bot-Api-Secret-Token", tgSecret), 200)
	if ls := e.links(t, ann.token, oid); ls[0].Status != "pending" {
		t.Fatal("a wrong code verified the link")
	}
	// the right code
	mustStatus(t, e.hook(path, tgUpdate(555, "/start "+code), "X-Telegram-Bot-Api-Secret-Token", tgSecret), 200)
	ls := e.links(t, ann.token, oid)
	if len(ls) != 1 || ls[0].Status != "linked" || ls[0].LinkedAt == nil {
		t.Fatalf("links after verify: %+v", ls)
	}
	// the chat id is sealed
	if raw := scalarOf[[]byte](t, e, `SELECT external_id_sealed FROM chat_link WHERE org_id = $1 AND user_id = $2`, org.ID, ann.user); strings.Contains(string(raw), "555") {
		t.Fatal("the chat id is stored in clear text")
	}
	// replay: the code is spent; another account sending it links nobody
	mustStatus(t, e.hook(path, tgUpdate(777, "/start "+code), "X-Telegram-Bot-Api-Secret-Token", tgSecret), 200)
	if n := scalarOf[int](t, e, `SELECT count(*) FROM chat_link WHERE org_id = $1 AND verified_at IS NOT NULL`, org.ID); n != 1 {
		t.Fatalf("%d verified links after a replay", n)
	}
	// an account already linked to Ann cannot be taken over by Bob's code
	bobCode, _ := e.startLink(t, bob.token, oid, "telegram")
	mustStatus(t, e.hook(path, tgUpdate(555, bobCode), "X-Telegram-Bot-Api-Secret-Token", tgSecret), 200)
	if ls := e.links(t, bob.token, oid); len(ls) != 1 || ls[0].Status != "pending" {
		t.Fatalf("bob's links: %+v", ls)
	}
	// the links are listed in /v1/me, and only the owner sees them
	rec := e.call(t, ann.token, "GET", "/v1/me", "")
	mustStatus(t, rec, 200)
	me := jsonOf[struct {
		ChatLinks []linkJSON `json:"chat_links"`
	}](t, rec)
	if len(me.ChatLinks) != 1 || me.ChatLinks[0].Status != "linked" || me.ChatLinks[0].Provider != "telegram" {
		t.Fatalf("/v1/me chat_links = %+v", me.ChatLinks)
	}
	// unlinking: someone else's link is a 404, mine goes
	mustStatus(t, e.call(t, bob.token, "DELETE", "/v1/orgs/"+oid+"/me/chat-links/"+ls[0].ID, ""), 404)
	mustStatus(t, e.call(t, ann.token, "DELETE", "/v1/orgs/"+oid+"/me/chat-links/"+ls[0].ID, ""), 204)
	if got := e.links(t, ann.token, oid); len(got) != 0 {
		t.Fatalf("links after unlink: %+v", got)
	}
}

func TestIntegrationChatSettingsSealSecretsAndNeedAdmin(t *testing.T) {
	e := newOpsEnv(t)
	e.withChat(t, newFakeChat(t))
	oid := e.w.OrgA.ID.String()
	e.configureChat(t, oid)
	rec := e.call(t, e.role(t, "org_admin"), "GET", "/v1/orgs/"+oid+"/chat-settings", "")
	mustStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), tgToken) || strings.Contains(rec.Body.String(), waApp) {
		t.Fatalf("a secret is in the settings response: %s", rec.Body.String())
	}
	body := jsonOf[struct {
		Items []struct {
			Provider   string   `json:"provider"`
			Configured bool     `json:"configured"`
			SecretsSet []string `json:"secrets_set"`
			WebhookURL string   `json:"webhook_url"`
		} `json:"items"`
	}](t, rec)
	if len(body.Items) != 2 || !body.Items[0].Configured || len(body.Items[0].SecretsSet) != 2 || !strings.HasSuffix(body.Items[0].WebhookURL, "/chat/webhooks/telegram") {
		t.Fatalf("settings = %+v", body)
	}
	if raw := scalarOf[string](t, e, `SELECT secrets::text FROM chat_config WHERE org_id = $1 AND provider = 'telegram'`, e.w.OrgA.ID); strings.Contains(raw, tgToken) {
		t.Fatal("the bot token is stored in clear text")
	}
	mustStatus(t, e.call(t, e.role(t, "org_admin"), "PUT", "/v1/orgs/"+oid+"/chat-settings/telegram", `{"values":{"bot_token":"not-a-token"}}`), 422)
	mustStatus(t, e.call(t, e.role(t, "org_admin"), "PUT", "/v1/orgs/"+oid+"/chat-settings/nope", `{"values":{}}`), 404)
	mustStatus(t, e.call(t, e.role(t, "site_manager"), "GET", "/v1/orgs/"+oid+"/chat-settings", ""), 403)
	// null clears a secret: the provider is no longer configured
	mustStatus(t, e.call(t, e.role(t, "org_admin"), "PUT", "/v1/orgs/"+oid+"/chat-settings/telegram", `{"values":{"bot_token":null}}`), 200)
	mustStatus(t, e.call(t, e.role(t, "org_admin"), "POST", "/v1/orgs/"+oid+"/me/chat-links/telegram:start", ""), 409)
}

// linkTelegram links the resident to Telegram chat 555 through the webhook.
func (e *opsEnv) linkTelegram(t *testing.T, r resident, oid string) {
	t.Helper()
	code, _ := e.startLink(t, r.token, oid, "telegram")
	mustStatus(t, e.hook("/v1/orgs/"+oid+"/chat/webhooks/telegram", tgUpdate(555, "/start "+code), "X-Telegram-Bot-Api-Secret-Token", tgSecret), 200)
}

func TestIntegrationChatDispatcherSendsMinimalTextThroughTelegramAndRespectsMutes(t *testing.T) {
	e := newOpsEnv(t)
	f := newFakeChat(t)
	e.withChat(t, f)
	org := e.w.OrgA
	oid := org.ID.String()
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	cy := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Cy") // never links a chat
	e.configureChat(t, oid)
	e.linkTelegram(t, ann, oid)

	job := func(key string) jobs.NotifyArgs {
		return jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "announcement.published", Key: key, Link: "/announcements/1",
			Channels:   []string{jobs.ChannelInApp, jobs.ChannelChat},
			Recipients: []jobs.Recipient{{PersonID: &ann.person}, {PersonID: &cy.person}},
			Payload:    map[string]any{"title": "Water <b>cut</b> & more", "snippet": "secret-snippet 4B", "sms_title": "x"}}
	}
	e.deliver(t, job("chat:1"))
	tg, wa := f.take()
	if len(tg) != 1 || len(wa) != 0 {
		t.Fatalf("telegram %d, whatsapp %d messages; want 1 and 0 (Cy has no link)", len(tg), len(wa))
	}
	m := tg[0]
	text, _ := m["text"].(string)
	if m["chat_id"] != "555" || m["parse_mode"] != "HTML" || !strings.Contains(text, "Water &lt;b&gt;cut&lt;/b&gt; &amp; more") || strings.Contains(text, "<b>") || strings.Contains(text, "secret-snippet") {
		t.Fatalf("message = %v", m)
	}
	kb := m["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	if kb["url"] != "https://example.test/announcements/1" {
		t.Fatalf("button = %v", kb)
	}
	if st := scalarOf[string](t, e, `SELECT d.status FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id WHERE n.person_id = $1 AND d.channel = 'chat'`, ann.person); st != "sent" {
		t.Fatalf("chat delivery = %s", st)
	}
	if why := scalarOf[string](t, e, `SELECT d.last_error FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id WHERE n.person_id = $1 AND d.channel = 'chat'`, cy.person); why != "no_link" {
		t.Fatalf("Cy's chat delivery reason = %q", why)
	}
	// a repeated job sends nothing twice
	e.deliver(t, job("chat:1"))
	if tg, _ = f.take(); len(tg) != 0 {
		t.Fatalf("a repeated job sent %d messages", len(tg))
	}
	// Ann mutes the type: nothing is sent
	mustStatus(t, e.call(t, ann.token, "PUT", "/v1/orgs/"+oid+"/me/notification-mutes", `{"muted_types":["announcement.published"]}`), 200)
	e.deliver(t, job("chat:2"))
	if tg, _ = f.take(); len(tg) != 0 {
		t.Fatalf("a muted type was sent to chat: %v", tg)
	}
}

func TestIntegrationChatDeadLinkIsMarkedUnverifiedAndNotRetried(t *testing.T) {
	e := newOpsEnv(t)
	f := newFakeChat(t)
	e.withChat(t, f)
	org := e.w.OrgA
	oid := org.ID.String()
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	e.configureChat(t, oid)
	e.linkTelegram(t, ann, oid)
	job := func(key string) jobs.NotifyArgs {
		return jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "announcement.published", Key: key, Channels: []string{jobs.ChannelChat},
			Recipients: []jobs.Recipient{{PersonID: &ann.person}}, Payload: map[string]any{"title": "t"}}
	}
	f.mu.Lock()
	f.tgStatus = 403 // the person blocked the bot
	f.mu.Unlock()
	if err := e.ops.notifications.Deliver(context.Background(), job("dead:1"), 1); err != nil {
		t.Fatalf("a dead chat must not fail the job: %v", err)
	}
	if ls := e.links(t, ann.token, oid); len(ls) != 1 || ls[0].Status != "unverified" {
		t.Fatalf("links = %+v", ls)
	}
	if why := scalarOf[string](t, e, `SELECT last_error FROM notification_delivery WHERE channel = 'chat' AND org_id = $1`, org.ID); why != "link_dead" {
		t.Fatalf("reason = %q", why)
	}
	// later messages do not reach the provider
	f.mu.Lock()
	f.tgStatus = 0
	f.mu.Unlock()
	f.take()
	e.deliver(t, job("dead:2"))
	if tg, _ := f.take(); len(tg) != 0 {
		t.Fatalf("sent to an unverified link: %v", tg)
	}
	// a provider outage fails the job so the job policy retries it
	e.linkTelegram(t, ann, oid)
	f.mu.Lock()
	f.tgStatus = 503
	f.mu.Unlock()
	if err := e.ops.notifications.Deliver(context.Background(), job("flaky:1"), 1); err == nil {
		t.Fatal("a provider outage must fail the job so it is retried")
	}
	f.mu.Lock()
	f.tgStatus = 0
	f.mu.Unlock()
	if err := e.ops.notifications.Deliver(context.Background(), job("flaky:1"), 2); err != nil {
		t.Fatal(err)
	}
	if st := scalarOf[string](t, e, `SELECT d.status FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id WHERE n.dedupe_key = 'flaky:1' AND d.channel = 'chat'`); st != "sent" {
		t.Fatalf("after the retry: %s", st)
	}
}

func TestIntegrationChatWhatsAppSignatureWindowAndFallback(t *testing.T) {
	e := newOpsEnv(t)
	f := newFakeChat(t)
	e.withChat(t, f)
	org := e.w.OrgA
	oid := org.ID.String()
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ann")
	e.configureChat(t, oid)
	path := "/v1/orgs/" + oid + "/chat/webhooks/whatsapp"

	code, st := e.startLink(t, ann.token, oid, "whatsapp")
	if want := "https://wa.me/905551112233?text=" + url.QueryEscape(code); st.DeepLink != want {
		t.Fatalf("deep link = %q, want %q", st.DeepLink, want)
	}
	body := waPayload("905550001122", code)
	// signature: missing, wrong, and the right one
	mustStatus(t, e.hook(path, body), 401)
	mustStatus(t, e.hook(path, body, "X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64)), 401)
	mustStatus(t, e.hook(path, body+" ", "X-Hub-Signature-256", waSig(body)), 401) // a tampered body
	if ls := e.links(t, ann.token, oid); ls[0].Status != "pending" {
		t.Fatal("a bad signature verified the link")
	}
	mustStatus(t, e.hook(path, body, "X-Hub-Signature-256", waSig(body)), 200)
	if ls := e.links(t, ann.token, oid); ls[0].Status != "linked" {
		t.Fatalf("links = %+v", ls)
	}
	// Meta's URL verification
	get := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path+"?hub.mode=subscribe&hub.verify_token="+token+"&hub.challenge=abc123", nil)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, r)
		return rec
	}
	if rec := get(waVerify); rec.Code != 200 || rec.Body.String() != "abc123" {
		t.Fatalf("challenge: %d %q", rec.Code, rec.Body.String())
	}
	mustStatus(t, get("wrong-token-1"), 401)

	job := func(key string) jobs.NotifyArgs {
		return jobs.NotifyArgs{OrgID: org.ID, SiteID: &s.Site, Type: "announcement.published", Key: key, Link: "/announcements/2",
			Channels: []string{jobs.ChannelChat}, Recipients: []jobs.Recipient{{PersonID: &ann.person}}, Payload: map[string]any{"title": "Meeting tonight"}}
	}
	// inside the 24 hour window: a session text message
	e.deliver(t, job("wa:1"))
	_, wa := f.take()
	if len(wa) != 1 || wa[0]["to"] != "905550001122" || wa[0]["type"] != "text" {
		t.Fatalf("whatsapp messages = %v", wa)
	}
	if b := wa[0]["text"].(map[string]any)["body"].(string); !strings.Contains(b, "Meeting tonight") || !strings.Contains(b, "https://example.test/announcements/2") {
		t.Fatalf("body = %q", b)
	}
	if _, has := wa[0]["template"]; has {
		t.Fatal("a template message was sent")
	}
	// the window is over: nothing is sent to WhatsApp, the message falls back to SMS
	e.w.Exec(t, `UPDATE chat_link SET last_inbound_at = now() - interval '25 hours' WHERE org_id = $1`, org.ID)
	before := len(e.ops.sms.Messages())
	e.deliver(t, job("wa:2"))
	if _, wa = f.take(); len(wa) != 0 {
		t.Fatalf("sent outside the window: %v", wa)
	}
	if got := e.ops.sms.Messages(); len(got) != before+1 {
		t.Fatalf("sms fallback: %d messages, want %d", len(got), before+1)
	}
	if why := scalarOf[string](t, e, `SELECT d.last_error FROM notification_delivery d WHERE d.channel = 'chat' AND d.org_id = $1 AND d.status = 'skipped'`, org.ID); why != "window_closed" {
		t.Fatalf("reason = %q", why)
	}
	// the person writes to the bot again: the window reopens
	mustStatus(t, e.hook(path, waPayload("905550001122", "hello"), "X-Hub-Signature-256", waSig(waPayload("905550001122", "hello"))), 200)
	e.deliver(t, job("wa:3"))
	if _, wa = f.take(); len(wa) != 1 {
		t.Fatalf("after the person wrote again: %d messages", len(wa))
	}
	// Meta says the window is closed although we thought it open: skipped, no retry storm
	f.mu.Lock()
	f.waCode = 131047
	f.mu.Unlock()
	if err := e.ops.notifications.Deliver(context.Background(), job("wa:4"), 1); err != nil {
		t.Fatalf("a closed window must not fail the job: %v", err)
	}
}
