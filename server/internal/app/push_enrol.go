package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/platform/push"
)

// PushEnrolOptions are the flags of `admin push enrol`.
type PushEnrolOptions struct {
	RelayURL   string
	AppID      string
	Invite     string
	Manual     bool
	RelayID    string // the local name for the OPENBEKCI_PUSH_RELAYS entry (default official)
	SecretFile string // default <DATA_DIR>/push-relay-<relay id>.secret
}

var appIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// challengeTTLSeconds is the life of the enrollment challenge (infra plan 4.2).
const challengeTTLSeconds = 900

// PushEnrol enrols this server with a relay: it publishes a fresh challenge in
// the discovery document (the running server serves it), asks the relay, which
// fetches the document and compares it, writes the returned secret to a
// mode-600 file and returns the OPENBEKCI_PUSH_RELAYS entry to add. With
// Manual the relay does not fetch and no challenge is published.
func (a *Admin) PushEnrol(ctx context.Context, o PushEnrolOptions) (entry, secretFile string, verified bool, err error) {
	if o.RelayID == "" {
		o.RelayID = "official"
	}
	if !pushRelayIDRe.MatchString(o.RelayID) {
		return "", "", false, fmt.Errorf("--relay-id %q is not a valid relay id", o.RelayID)
	}
	if !appIDRe.MatchString(o.AppID) {
		return "", "", false, errors.New("--app-id is required (the store app the relay serves, for example org.openbekci.app)")
	}
	if !strings.HasPrefix(o.RelayURL, "https://") && (!a.cfg.InsecureDev || !strings.HasPrefix(o.RelayURL, "http://")) {
		return "", "", false, errors.New("--relay must be an https URL")
	}
	if o.SecretFile == "" {
		o.SecretFile = filepath.Join(a.cfg.DataDir, "push-relay-"+o.RelayID+".secret")
	}
	if _, statErr := os.Stat(o.SecretFile); statErr == nil {
		return "", "", false, fmt.Errorf("%s already exists; remove it or choose --secret-file", o.SecretFile)
	}
	// 1. begin: the relay issues the challenge and an enrollment token (memory only)
	begun, err := push.Begin(ctx, o.RelayURL, push.BeginRequest{AppID: o.AppID, InviteCode: o.Invite})
	if err != nil {
		return "", "", false, err
	}
	// 2. publish the challenge in the discovery document of the running server
	ch := instance.PGChallenges{DB: a.db}
	if !o.Manual {
		if err := ch.Set(ctx, begun.Challenge, challengeTTLSeconds); err != nil {
			return "", "", false, fmt.Errorf("publish the challenge: %w", err)
		}
		defer func() { _ = ch.Clear(context.WithoutCancel(ctx)) }()
	}
	// 3. complete: the relay fetches the document and compares the challenge
	en, err := push.Complete(ctx, o.RelayURL, push.CompleteRequest{EnrollmentID: begun.EnrollmentID, EnrollmentToken: begun.EnrollmentToken, BaseURL: a.cfg.BaseURL, Manual: o.Manual})
	if err != nil {
		if s := err.Error(); strings.Contains(s, "discovery_unreachable") || strings.Contains(s, "challenge_mismatch") {
			err = fmt.Errorf("%w\nthe relay must be able to fetch %s/.well-known/openbekci.json over public HTTPS and see the challenge (is `openbekci serve` running against this database, and is BASE_URL reachable from the internet?); fix that and run the command again (a pending enrollment survives a few attempts)", err, a.cfg.BaseURL)
		}
		return "", "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(o.SecretFile), 0o750); err != nil {
		return "", "", false, fmt.Errorf("the relay enrolled this server, but the secret directory could not be created (server_id %s): %w", en.ServerID, err)
	}
	f, err := os.OpenFile(filepath.Clean(o.SecretFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", false, fmt.Errorf("the relay enrolled this server (server_id %s) but the secret file could not be written: %w", en.ServerID, err)
	}
	if _, err := f.WriteString(en.ServerSecret + "\n"); err != nil {
		_ = f.Close()
		return "", "", false, fmt.Errorf("write the secret file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", "", false, fmt.Errorf("write the secret file: %w", err)
	}
	entry = fmt.Sprintf("%s=%s;%s;%s;%s", o.RelayID, strings.TrimRight(o.RelayURL, "/"), en.ServerID, o.SecretFile, o.AppID)
	return entry, o.SecretFile, en.Verified, nil
}
