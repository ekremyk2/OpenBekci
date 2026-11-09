package packs

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
)

// ChatProviders are the chat providers of the packs enabled for org, as the
// core chat service's providers (a pack provider reads its own pack settings).
func (h *Host) ChatProviders(ctx context.Context, org uuid.UUID) ([]chat.Provider, error) {
	packs, err := h.Active(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []chat.Provider
	for _, pk := range packs {
		for _, pr := range pk.C.ChatProviders {
			out = append(out, packChat{h: h, org: org, pack: pk.Manifest.ID, p: pr})
		}
	}
	return out, nil
}

// packChat adapts an sdk.ChatProvider to chat.Provider, running pack code
// through Host.call (panics recovered, tenant context set).
type packChat struct {
	h    *Host
	org  uuid.UUID
	pack string
	p    sdk.ChatProvider
}

func (c packChat) ID() string            { return c.p.ID() }
func (c packChat) Window() time.Duration { return c.p.Window() }

// DeepLink has no context in chat.Provider; the link is built from settings, so a short one is enough.
func (c packChat) DeepLink(code string) string {
	var out string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.h.call(ctx, c.org, c.pack, "ChatProvider DeepLink", func(ctx context.Context) (err error) { out, err = c.p.DeepLink(ctx, code); return err })
	return out
}

func (c packChat) Send(ctx context.Context, to string, m chat.Message) error {
	err := c.h.call(ctx, c.org, c.pack, "ChatProvider Send", func(ctx context.Context) error {
		return c.p.Send(ctx, to, sdk.ChatMessage{Text: m.Text, Link: m.Link, Label: m.Label})
	})
	switch {
	case errors.Is(err, sdk.ErrChatUnreachable):
		return chat.ErrDead
	case errors.Is(err, sdk.ErrChatWindowClosed):
		return chat.ErrWindowClosed
	}
	return err
}

func (c packChat) VerifyWebhook(r *http.Request, body []byte) error {
	err := c.h.call(r.Context(), c.org, c.pack, "ChatProvider VerifyWebhook", func(ctx context.Context) error { return c.p.VerifyWebhook(ctx, r, body) })
	if err != nil {
		return chat.ErrBadSignature
	}
	return nil
}

func (c packChat) ParseInbound(body []byte) ([]chat.Inbound, error) {
	var in []sdk.ChatInbound
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.h.call(ctx, c.org, c.pack, "ChatProvider ParseInbound", func(ctx context.Context) (err error) { in, err = c.p.ParseInbound(ctx, body); return err }); err != nil {
		return nil, err
	}
	out := make([]chat.Inbound, len(in))
	for i, m := range in {
		out[i] = chat.Inbound{ExternalID: m.ExternalID, Text: m.Text}
	}
	return out, nil
}
