package app

import (
	"net/netip"
	"testing"
)

func TestConfigIntegrations(t *testing.T) {
	c, err := LoadConfig("", env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKeyRatePerMinute != 0 || c.WebhookMaxAttempts != 0 || len(c.WebhookAllowedCIDRs) != 0 {
		t.Errorf("defaults = %d, %d, %v (zero means the service default)", c.APIKeyRatePerMinute, c.WebhookMaxAttempts, c.WebhookAllowedCIDRs)
	}
	e := baseEnv()
	e["OPENBEKCI_API_KEY_RATE_PER_MINUTE"] = "-1"
	e["OPENBEKCI_WEBHOOK_MAX_ATTEMPTS"] = "5"
	e["OPENBEKCI_WEBHOOK_ALLOWED_CIDRS"] = "192.168.1.0/24, 10.1.2.3"
	c, err = LoadConfig("", env(e))
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKeyRatePerMinute != -1 || c.WebhookMaxAttempts != 5 || len(c.WebhookAllowedCIDRs) != 2 ||
		c.WebhookAllowedCIDRs[0] != netip.MustParsePrefix("192.168.1.0/24") || c.WebhookAllowedCIDRs[1] != netip.MustParsePrefix("10.1.2.3/32") {
		t.Errorf("config = %d, %d, %v", c.APIKeyRatePerMinute, c.WebhookMaxAttempts, c.WebhookAllowedCIDRs)
	}
	for name, bad := range map[string]string{
		"OPENBEKCI_API_KEY_RATE_PER_MINUTE": "fast", "OPENBEKCI_WEBHOOK_MAX_ATTEMPTS": "0", "OPENBEKCI_WEBHOOK_ALLOWED_CIDRS": "not-a-cidr",
	} {
		e := baseEnv()
		e[name] = bad
		if _, err := LoadConfig("", env(e)); err == nil {
			t.Errorf("%s=%q accepted", name, bad)
		}
	}
	e = baseEnv()
	e["OPENBEKCI_WEBHOOK_MAX_ATTEMPTS"] = "21"
	if _, err := LoadConfig("", env(e)); err == nil {
		t.Error("21 attempts accepted")
	}
}
