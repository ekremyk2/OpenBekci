package example

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// ---- chat (SDK 1.1, optional extension point)

// Chat is a message the example chat provider "sent".
type Chat struct{ To, Text, Link string }

// ChatSent returns the messages the example chat provider was asked to send.
func (p *Pack) ChatSent() []Chat {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Chat(nil), p.chat...)
}

// chatProvider shows the shape of a messenger provider (for example BiP): a deep
// link that carries the linking code, a webhook that authenticates itself and
// yields "<chat id> <text>" messages, and a send. The example accepts any webhook
// carrying the shared secret header; a real provider verifies a signature.
type chatProvider struct{ p *Pack }

func (*chatProvider) ID() string { return ID }

func (*chatProvider) Window() time.Duration { return 0 }

func (*chatProvider) DeepLink(_ context.Context, code string) (string, error) {
	return "https://chat.example.test/start?code=" + code, nil
}

func (c *chatProvider) Send(_ context.Context, to string, m sdk.ChatMessage) error {
	if to == "gone" {
		return sdk.ErrChatUnreachable
	}
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	c.p.chat = append(c.p.chat, Chat{To: to, Text: m.Text, Link: m.Link})
	return nil
}

func (*chatProvider) VerifyWebhook(_ context.Context, r *http.Request, _ []byte) error {
	if r.Header.Get("X-Example-Secret") != "example-chat-secret" {
		return sdk.ErrBadWebhook
	}
	return nil
}

func (*chatProvider) ParseInbound(_ context.Context, body []byte) ([]sdk.ChatInbound, error) {
	var in struct{ From, Text string }
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	return []sdk.ChatInbound{{ExternalID: strings.TrimSpace(in.From), Text: in.Text}}, nil
}
