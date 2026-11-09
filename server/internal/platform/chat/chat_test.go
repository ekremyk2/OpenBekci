package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramInboundSendAndSetWebhookAgainstFake(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, m)
	}))
	defer srv.Close()
	b, _ := BuiltinByID("telegram")
	cfg := map[string]string{"bot_token": "123456789:AAH_fake-token_for_tests_0123456789abcdef", "bot_username": "bekci_bot", "webhook_secret": "secret-0123456789abcdef"}
	p, err := b.Factory(cfg, Options{TelegramBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Factory(map[string]string{"bot_token": "x"}, Options{}); err == nil {
		t.Fatal("an incomplete configuration built a provider")
	}
	// a private message "/start CODE" yields the code; a group message and a non-text update yield nothing
	in, err := p.ParseInbound([]byte(`{"message":{"text":"/start BEKCI-ABC","chat":{"id":42,"type":"private"}}}`))
	if err != nil || len(in) != 1 || in[0].ExternalID != "42" || in[0].Text != "BEKCI-ABC" {
		t.Fatalf("inbound = %v %v", in, err)
	}
	if in, _ := p.ParseInbound([]byte(`{"message":{"text":"hi","chat":{"id":-9,"type":"group"}}}`)); len(in) != 0 {
		t.Fatalf("a group message was parsed: %v", in)
	}
	// the text is HTML-escaped and the link is an inline button
	if err := p.Send(context.Background(), "42", Message{Text: "<script>&", Link: "https://x.test/a", Label: "Open"}); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["text"] != "&lt;script&gt;&amp;" || !strings.Contains(paths[0], "/sendMessage") {
		t.Fatalf("send = %v %v", paths[0], bodies[0])
	}
	if err := p.(WebhookRegistrar).SetWebhook(context.Background(), "https://x.test/hook"); err != nil {
		t.Fatal(err)
	}
	if bodies[1]["secret_token"] != cfg["webhook_secret"] || bodies[1]["url"] != "https://x.test/hook" {
		t.Fatalf("setWebhook = %v", bodies[1])
	}
	r := httptest.NewRequest("POST", "/", nil)
	if p.VerifyWebhook(r, nil) == nil {
		t.Fatal("a request without the secret header was accepted")
	}
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", cfg["webhook_secret"])
	if err := p.VerifyWebhook(r, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWhatsAppInboundOpensWindowForAnyMessage(t *testing.T) {
	b, _ := BuiltinByID("whatsapp")
	p, err := b.Factory(map[string]string{"display_number": "+905551112233", "phone_number_id": "1234567890", "access_token": "token-abcdefgh", "app_secret": "secret-abcdefgh", "verify_token": "verify-abcdefgh"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// an image message carries no code but still opens the session window
	in, err := p.ParseInbound([]byte(`{"entry":[{"changes":[{"value":{"messages":[{"from":"905550001122","type":"image"},{"from":"905550001122","type":"text","text":{"body":" BEKCI-X "}}]}}]}]}`))
	if err != nil || len(in) != 2 || in[0].Text != "" || in[1].Text != "BEKCI-X" || in[0].ExternalID != "905550001122" {
		t.Fatalf("inbound = %v %v", in, err)
	}
	if p.Window().Hours() != 24 {
		t.Fatalf("window = %v", p.Window())
	}
}
