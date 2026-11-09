package chat

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// whatsappWindow is the session window of the Cloud API: free-form messages
// only within 24 hours of the person's last message to the business.
const whatsappWindow = 24 * time.Hour

var (
	waPhoneIDRe = regexp.MustCompile(`^[0-9]{5,30}$`)
	waNumberRe  = regexp.MustCompile(`^\+?[0-9]{7,15}$`)
	waSecretRe  = regexp.MustCompile(`^[A-Za-z0-9_.~-]{8,256}$`)
	waTokenRe   = regexp.MustCompile(`^[A-Za-z0-9_.~-]{8,512}$`)
)

var whatsappBuiltin = Builtin{
	ID:     "whatsapp",
	Window: whatsappWindow,
	Schema: map[string]any{
		"type":     "object",
		"required": []any{"phone_number_id", "display_number", "access_token", "app_secret", "verify_token"},
		"properties": map[string]any{
			"display_number":  map[string]any{"type": "string", "title": "Business number", "description": "The WhatsApp number people write to, international format (for the wa.me link).", "pattern": waNumberRe.String(), "x-ui": map[string]any{"order": 1}},
			"phone_number_id": map[string]any{"type": "string", "title": "Phone number id", "description": "From the Meta app's WhatsApp setup.", "pattern": waPhoneIDRe.String(), "x-ui": map[string]any{"order": 2}},
			"access_token":    map[string]any{"type": "string", "title": "Access token", "pattern": waTokenRe.String(), "x-secret": true, "x-ui": map[string]any{"order": 3}},
			"app_secret":      map[string]any{"type": "string", "title": "App secret", "description": "Signs the webhook (X-Hub-Signature-256).", "pattern": waSecretRe.String(), "x-secret": true, "x-ui": map[string]any{"order": 4}},
			"verify_token":    map[string]any{"type": "string", "title": "Webhook verify token", "description": "The token you enter in Meta's webhook setup.", "pattern": waSecretRe.String(), "x-secret": true, "x-ui": map[string]any{"order": 5}},
		},
	},
	Factory: func(cfg map[string]string, o Options) (Provider, error) {
		w := &whatsapp{senderID: cfg["phone_number_id"], number: strings.TrimPrefix(cfg["display_number"], "+"), token: cfg["access_token"], appSecret: cfg["app_secret"], verify: cfg["verify_token"], base: o.WhatsAppBase, hc: client(o)}
		if w.base == "" {
			w.base = "https://graph.facebook.com/v21.0"
		}
		if !waPhoneIDRe.MatchString(w.senderID) || !waNumberRe.MatchString(cfg["display_number"]) || !waTokenRe.MatchString(w.token) || !waSecretRe.MatchString(w.appSecret) || !waSecretRe.MatchString(w.verify) {
			return nil, fmt.Errorf("whatsapp: display_number, phone_number_id, access_token, app_secret and verify_token are required and must be well formed")
		}
		return w, nil
	},
}

type whatsapp struct {
	senderID, number, token, appSecret, verify, base string
	hc                                              *http.Client
}

func (w *whatsapp) ID() string            { return "whatsapp" }
func (w *whatsapp) Window() time.Duration { return whatsappWindow }
func (w *whatsapp) DeepLink(c string) string {
	return "https://wa.me/" + w.number + "?text=" + url.QueryEscape(c)
}

// Send posts a session (template-free) text message; the caller has checked
// the window, and Meta's re-engagement error (131047) is mapped too.
func (w *whatsapp) Send(ctx context.Context, to string, m Message) error {
	if !regexp.MustCompile(`^[0-9]{5,20}$`).MatchString(to) {
		return ErrDead
	}
	text := m.Text
	if m.Link != "" {
		text += "\n" + m.Link
	}
	b, _ := json.Marshal(map[string]any{"messaging_product": "whatsapp", "to": to, "type": "text", "text": map[string]any{"preview_url": false, "body": text}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.base+"/"+w.senderID+"/messages", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.token)
	resp, err := w.hc.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: send: request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	switch {
	case e.Error.Code == 131047:
		return ErrWindowClosed
	case e.Error.Code == 131026 || e.Error.Code == 131021:
		return ErrDead
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("whatsapp: send: HTTP %d (retryable)", resp.StatusCode)
	}
	return fmt.Errorf("whatsapp: send rejected: HTTP %d code %d", resp.StatusCode, e.Error.Code)
}

// VerifyWebhook checks X-Hub-Signature-256 = "sha256=" + hex HMAC-SHA256(app secret, body).
func (w *whatsapp) VerifyWebhook(r *http.Request, body []byte) error {
	got, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	m := hmac.New(sha256.New, []byte(w.appSecret))
	m.Write(body)
	if !ok || !hmac.Equal([]byte(got), []byte(hex.EncodeToString(m.Sum(nil)))) {
		return ErrBadSignature
	}
	return nil
}

func (w *whatsapp) ParseInbound(body []byte) ([]Inbound, error) {
	var p struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						From string `json:"from"`
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("whatsapp: payload: %w", err)
	}
	var out []Inbound
	for _, e := range p.Entry {
		for _, c := range e.Changes {
			for _, m := range c.Value.Messages {
				// any message opens the window; only text can carry a code
				txt := ""
				if m.Type == "text" {
					txt = strings.TrimSpace(m.Text.Body)
				}
				out = append(out, Inbound{ExternalID: m.From, Text: txt})
			}
		}
	}
	return out, nil
}

// Challenge answers Meta's GET verification (hub.mode, hub.verify_token, hub.challenge).
func (w *whatsapp) Challenge(q url.Values) (string, bool) {
	if q.Get("hub.mode") == "subscribe" && subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(w.verify)) == 1 {
		return q.Get("hub.challenge"), true
	}
	return "", false
}
