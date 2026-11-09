package app

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// lazySMSPicker is the sms.Picker the modules are built with before the pack
// host exists (the host needs the notification queue those modules create).
type lazySMSPicker struct{ h atomic.Pointer[packs.Host] }

func (l *lazySMSPicker) Pick(ctx context.Context, org uuid.UUID) (sms.Gateway, bool) {
	h := l.h.Load()
	if h == nil {
		return nil, false
	}
	return h.SMSPicker().Pick(ctx, org)
}
