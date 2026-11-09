package chat

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/platform/chat"
)

// RegisterWebhook tells the provider (Telegram setWebhook) to call this
// server's webhook URL of the organization, and returns the URL. override
// replaces the URL built from BASE_URL (a tunnel in development); it must be https.
func (s *Service) RegisterWebhook(ctx context.Context, org uuid.UUID, providerID, override string) (string, error) {
	p, err := s.provider(ctx, org, providerID)
	if err != nil {
		return "", err
	}
	reg, ok := p.(chat.WebhookRegistrar)
	if !ok {
		return "", fmt.Errorf("provider %q registers its webhook in its own console; use the URL shown in the settings: %s", providerID, s.webhookURL(org, providerID))
	}
	u := s.webhookURL(org, providerID)
	if override != "" {
		u = override
	}
	if pu, err := url.Parse(u); err != nil || pu.Scheme != "https" || pu.Host == "" {
		return "", fmt.Errorf("the webhook URL must be https (set OPENBEKCI_BASE_URL or pass --url): %q", u)
	}
	return u, reg.SetWebhook(ctx, u)
}
