// Package chat is the chat channel's provider abstraction (backend plan,
// section 12): Telegram and WhatsApp in core, more through packs
// (sdk.ChatProvider). A provider sends one short message to a linked chat,
// authenticates the webhook the chat service calls it on, and reads the
// messages people send the bot (the linking code).
package chat

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Errors providers map failures to.
var (
	// ErrDead: the chat cannot be reached any more (the person blocked the bot,
	// the chat is gone). The link is marked unverified.
	ErrDead = errors.New("chat: the chat is unreachable")
	// ErrWindowClosed: the provider only accepts session messages for a while
	// after the person wrote (WhatsApp, 24 hours) and that time is over.
	ErrWindowClosed = errors.New("chat: the session window is closed")
	// ErrBadSignature: the webhook is not from the provider.
	ErrBadSignature = errors.New("chat: webhook authentication failed")
)

// Message is what a provider sends: content-minimal by construction (a title
// and a link back to the web app).
type Message struct {
	Text string // plain text; the provider escapes it as its format needs
	Link string // absolute URL, may be empty
	// Label names the link button where the provider has buttons.
	Label string
}

// Inbound is one message a person sent to the bot.
type Inbound struct {
	// ExternalID is the chat id (Telegram) or the number (WhatsApp).
	ExternalID string
	// Text is the message; for Telegram "/start <code>" arrives as "<code>".
	Text string
}

// Provider is one chat service configured for an organization.
type Provider interface {
	ID() string
	// DeepLink is the link a person opens to start the chat with the code.
	DeepLink(code string) string
	Send(ctx context.Context, to string, m Message) error
	// VerifyWebhook authenticates a webhook request (body already read).
	VerifyWebhook(r *http.Request, body []byte) error
	ParseInbound(body []byte) ([]Inbound, error)
	// Window is how long after a person's last message the provider accepts
	// messages; zero means no limit.
	Window() time.Duration
}

// WebhookRegistrar is implemented by providers that register their webhook
// with the service (Telegram setWebhook).
type WebhookRegistrar interface {
	SetWebhook(ctx context.Context, webhookURL string) error
}

// Challenger is implemented by providers whose service verifies the webhook
// URL with a GET (WhatsApp hub.challenge). It returns the body to answer.
type Challenger interface {
	Challenge(q url.Values) (string, bool)
}

// Options point the built-in providers at their services; tests point them at
// in-process fakes.
type Options struct {
	TelegramBase string // default https://api.telegram.org
	WhatsAppBase string // default https://graph.facebook.com/v21.0
	HTTP         *http.Client
}

// Builtin is a provider the core ships.
type Builtin struct {
	ID string
	// Schema is the JSON schema of the settings (x-secret marks the secrets).
	Schema map[string]any
	// Window is the provider's session window (see Provider.Window), known
	// without credentials so the dispatcher can plan a fallback.
	Window  time.Duration
	Factory func(cfg map[string]string, o Options) (Provider, error)
}

// Builtins are the providers of the core, in display order.
func Builtins() []Builtin { return []Builtin{telegramBuiltin, whatsappBuiltin} }

// BuiltinByID returns the core provider with that id.
func BuiltinByID(id string) (Builtin, bool) {
	for _, b := range Builtins() {
		if b.ID == id {
			return b, true
		}
	}
	return Builtin{}, false
}
