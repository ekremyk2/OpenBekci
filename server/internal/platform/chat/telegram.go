package chat

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/openbekci/openbekci/server/internal/platform/obs"
)

const maxReply = 1 << 20

var (
	botTokenRe  = regexp.MustCompile(`^[0-9]{3,20}:[A-Za-z0-9_-]{20,80}$`)
	botNameRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,63}$`)
	tgSecretRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)
	numericIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)
)

var telegramBuiltin = Builtin{
	ID: "telegram",
	Schema: map[string]any{
		"type":     "object",
		"required": []any{"bot_token", "bot_username", "webhook_secret"},
		"properties": map[string]any{
			"bot_username": map[string]any{"type": "string", "title": "Bot username", "description": "The bot's username without @ (for the t.me link).", "pattern": botNameRe.String(), "x-ui": map[string]any{"order": 1}},
			"bot_token":    map[string]any{"type": "string", "title": "Bot token", "description": "From @BotFather.", "pattern": botTokenRe.String(), "x-secret": true, "x-ui": map[string]any{"order": 2}},
			"webhook_secret": map[string]any{"type": "string", "title": "Webhook secret", "description": "16 to 256 letters, digits, - or _. Telegram sends it back in a header on every update.",
				"pattern": tgSecretRe.String(), "x-secret": true, "x-ui": map[string]any{"order": 3}},
		},
	},
	Factory: func(cfg map[string]string, o Options) (Provider, error) {
		t := &telegram{token: cfg["bot_token"], username: cfg["bot_username"], secret: cfg["webhook_secret"], base: o.TelegramBase, hc: client(o)}
		if t.base == "" {
			t.base = "https://api.telegram.org"
		}
		if !botTokenRe.MatchString(t.token) || !botNameRe.MatchString(t.username) || !tgSecretRe.MatchString(t.secret) {
			return nil, fmt.Errorf("telegram: bot_token, bot_username and webhook_secret are required and must be well formed")
		}
		return t, nil
	},
}

type telegram struct {
	token, username, secret, base string
	hc                            *http.Client
}

func client(o Options) *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: obs.Transport(http.DefaultTransport),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (t *telegram) ID() string            { return "telegram" }
func (t *telegram) Window() time.Duration { return 0 }
func (t *telegram) DeepLink(c string) string {
	return "https://t.me/" + t.username + "?start=" + url.QueryEscape(c)
}

func (t *telegram) call(ctx context.Context, method string, in any) (int, []byte, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("telegram: %s: request failed", method) // the URL holds the token: never wrap err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	return resp.StatusCode, raw, nil
}

// Send posts the text, HTML-escaped, with an inline button to the link.
func (t *telegram) Send(ctx context.Context, to string, m Message) error {
	if !numericIDRe.MatchString(strings.TrimPrefix(to, "-")) {
		return ErrDead
	}
	body := map[string]any{"chat_id": to, "text": html.EscapeString(m.Text), "parse_mode": "HTML", "disable_web_page_preview": true}
	if m.Link != "" {
		label := m.Label
		if label == "" {
			label = "Open"
		}
		body["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{"text": label, "url": m.Link}}}}
	}
	status, raw, err := t.call(ctx, "sendMessage", body)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusForbidden || (status == http.StatusBadRequest && strings.Contains(string(raw), "chat not found")):
		return ErrDead
	case status == http.StatusTooManyRequests || status >= 500:
		return fmt.Errorf("telegram: sendMessage: HTTP %d (retryable)", status)
	}
	return fmt.Errorf("telegram: sendMessage rejected: HTTP %d", status)
}

func (t *telegram) VerifyWebhook(r *http.Request, _ []byte) error {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(t.secret)) != 1 {
		return ErrBadSignature
	}
	return nil
}

func (t *telegram) ParseInbound(body []byte) ([]Inbound, error) {
	var u struct {
		Message struct {
			Text string `json:"text"`
			Chat struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("telegram: update: %w", err)
	}
	if u.Message.Chat.Type != "private" || u.Message.Text == "" {
		return nil, nil
	}
	text := strings.TrimSpace(u.Message.Text)
	if rest, ok := strings.CutPrefix(text, "/start"); ok {
		text = strings.TrimSpace(rest)
	}
	return []Inbound{{ExternalID: fmt.Sprint(u.Message.Chat.ID), Text: text}}, nil
}

// SetWebhook registers webhookURL with the secret token header.
func (t *telegram) SetWebhook(ctx context.Context, webhookURL string) error {
	status, raw, err := t.call(ctx, "setWebhook", map[string]any{"url": webhookURL, "secret_token": t.secret, "allowed_updates": []string{"message", "channel_post", "poll_answer"}})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("telegram: setWebhook: HTTP %d: %s", status, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
	}
	return nil
}
