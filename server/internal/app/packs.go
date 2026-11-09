package app

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// lazySMSPicker is the sms.Picker the modules are built with before the pack
// host exists (the host needs the notification queue those modules create).
type lazySMSPicker struct{ h atomic.Pointer[packs.Host] }

// lazyChatPacks is the chat service's pack provider source, set once the host exists.
type lazyChatPacks struct{ h atomic.Pointer[packs.Host] }

func (l *lazyChatPacks) ChatProviders(ctx context.Context, org uuid.UUID) ([]chat.Provider, error) {
	h := l.h.Load()
	if h == nil {
		return nil, nil
	}
	return h.ChatProviders(ctx, org)
}

func (l *lazySMSPicker) Pick(ctx context.Context, org uuid.UUID) (sms.Gateway, bool) {
	h := l.h.Load()
	if h == nil {
		return nil, false
	}
	return h.SMSPicker().Pick(ctx, org)
}
