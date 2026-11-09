package sdk

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Added in 1.1.0 as an optional extension point (the 1.x freeze forbids adding
// a method to [Registry]): the host's registry also implements [ChatRegistrar];
// a pack finds it by type assertion in Register:
//
//	if cr, ok := r.(sdk.ChatRegistrar); ok { cr.RegisterChatProvider(p) }

// ChatRegistrar is the optional registration call of [ChatProvider].
type ChatRegistrar interface {
	// RegisterChatProvider adds a chat provider (called by the core chat channel).
	RegisterChatProvider(ChatProvider)
}

// Errors a [ChatProvider] returns.
var (
	// ErrChatUnreachable: the chat is gone for good (the person blocked the bot).
	// The core marks the person's link unverified; nothing is retried.
	ErrChatUnreachable = errors.New("sdk: chat unreachable")
	// ErrChatWindowClosed: the service only accepts session messages for a while
	// after the person wrote, and that time is over. The core falls back to the
	// person's next channel.
	ErrChatWindowClosed = errors.New("sdk: chat session window closed")
	// ErrBadWebhook: VerifyWebhook rejects the request (the core answers 401).
	ErrBadWebhook = errors.New("sdk: webhook authentication failed")
)

// ChatMessage is what a provider sends: a title and a link, nothing else.
type ChatMessage struct {
	Text string // plain text; escape it as the service needs
	Link string // absolute URL back to the web app; may be empty
	// Label names the link where the service has buttons ("Open", "Aç").
	Label string
}

// ChatInbound is one message a person sent to the organization's bot.
type ChatInbound struct {
	// ExternalID is the person's address at the service (chat id, number).
	ExternalID string
	// Text is the message; a linking message carries the code.
	Text string
}

// ChatProvider delivers notifications to a messenger (for example BiP). The
// organization's enabled packs provide them; the person links a chat account
// through the provider's deep link and the webhook the core mounts at
// /v1/orgs/{org}/chat/webhooks/<provider id>. The provider reads its
// credentials from its pack's settings through [Services].
//
// Core call sites: the notification dispatcher (channel "chat"), the linking
// flow and the chat webhook. Map failures to [ErrChatUnreachable],
// [ErrChatWindowClosed] and [ErrUnavailable] (retried by the job policy).
type ChatProvider interface {
	// ID is the provider id ("<packid>" or "<packid>.<name>").
	ID() string
	// DeepLink is the link a person opens to start a chat that sends code.
	DeepLink(ctx context.Context, code string) (string, error)
	Send(ctx context.Context, to string, m ChatMessage) error
	// VerifyWebhook authenticates a webhook request; body is already read.
	VerifyWebhook(ctx context.Context, r *http.Request, body []byte) error
	// ParseInbound lists the messages a webhook body carries.
	ParseInbound(ctx context.Context, body []byte) ([]ChatInbound, error)
	// Window is how long after a person's last message the service accepts
	// messages; zero means no limit.
	Window() time.Duration
}
