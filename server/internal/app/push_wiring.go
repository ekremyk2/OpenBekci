package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/devices"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/push"
)

// newDevices builds the devices service (push token registration); the tokens
// are sealed with ENCRYPTION_KEY.
func newDevices(cfg Config, d *db.DB) (*devices.Service, error) {
	return devices.NewService(d, devices.Config{EncryptionKey: cfg.EncryptionKey, AllowPrivateEndpoints: cfg.PushAllowPrivate})
}

// newPushSender builds the sender behind the dispatcher's push channel: the
// configured relays for fcm and apns, direct posts for UnifiedPush.
func newPushSender(cfg Config, log *slog.Logger) (push.Sender, error) {
	relays := make([]push.RelayConfig, 0, len(cfg.PushRelays))
	for _, e := range cfg.PushRelays {
		rc := push.RelayConfig{ID: e.ID, URL: e.URL, ServerID: e.ServerID, AppID: e.AppID}
		if e.URL == "log" {
			log.Warn("push relay uses the log driver: payloads are written to the log, not sent", "relay_id", e.ID)
		} else {
			b, err := os.ReadFile(filepath.Clean(e.SecretFile))
			if err != nil {
				return nil, fmt.Errorf("read the secret file of push relay %q: %w", e.ID, err)
			}
			rc.Secret = []byte(strings.TrimSpace(string(b)))
			if len(rc.Secret) == 0 {
				return nil, fmt.Errorf("the secret file of push relay %q is empty", e.ID)
			}
		}
		relays = append(relays, rc)
	}
	return push.NewRouter(push.Options{Relays: relays, AllowPrivate: cfg.PushAllowPrivate, Log: log}), nil
}

// pushRelayIDs are the relay ids the discovery document announces.
func pushRelayIDs(cfg Config) []string {
	ids := make([]string, 0, len(cfg.PushRelays))
	for _, e := range cfg.PushRelays {
		ids = append(ids, e.ID)
	}
	return ids
}

// instanceIDFunc reads the instance id for push payloads.
func instanceIDFunc(d *db.DB) func(context.Context) (uuid.UUID, error) {
	return instance.PGIDSource{DB: d}.InstanceID
}
