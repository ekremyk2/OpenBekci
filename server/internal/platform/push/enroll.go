package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/openbekci/openbekci/server/internal/platform/obs"
	"io"
	"net/http"
	"strings"
	"time"
)

// Enrollment is two steps (relay docs/PROTOCOL.md):
//
//  1. Begin: POST {relay}/v1/enrollments/begin {app_id, invite_code} returns an
//     enrollment id, a secret enrollment token (kept in memory only) and the
//     challenge.
//  2. The server publishes the challenge as relay_enrollment.challenge in its
//     discovery document.
//  3. Complete: POST {relay}/v1/enrollments/complete {enrollment_id,
//     enrollment_token, base_url, manual}: the relay fetches the discovery
//     document, compares the challenge and returns the server id and secret.

// BeginRequest is the body of POST /v1/enrollments/begin.
type BeginRequest struct {
	AppID      string `json:"app_id"`
	InviteCode string `json:"invite_code,omitempty"`
}

// Begun is the relay's answer to Begin.
type Begun struct {
	EnrollmentID    string `json:"enrollment_id"`
	EnrollmentToken string `json:"enrollment_token"`
	Challenge       string `json:"challenge"`
	ExpiresAt       string `json:"expires_at"`
}

// CompleteRequest is the body of POST /v1/enrollments/complete.
type CompleteRequest struct {
	EnrollmentID    string `json:"enrollment_id"`
	EnrollmentToken string `json:"enrollment_token"`
	BaseURL         string `json:"base_url"`
	Manual          bool   `json:"manual,omitempty"`
}

// Enrollment is the relay's final answer, shown once.
type Enrollment struct {
	ServerID     string `json:"server_id"`
	ServerSecret string `json:"server_secret"`
	Verified     bool   `json:"verified"`
}

func postJSON(ctx context.Context, relayURL, path string, in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(relayURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("relay url: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 30 * time.Second, Transport: obs.Transport(http.DefaultTransport), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("reach the relay: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("the relay refused the request (HTTP %d %s): %s", resp.StatusCode, clip(e.Error.Code, 64), clip(e.Error.Message, 200))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the relay's answer is malformed")
	}
	return nil
}

// Begin starts an enrollment.
func Begin(ctx context.Context, relayURL string, req BeginRequest) (Begun, error) {
	var out Begun
	if err := postJSON(ctx, relayURL, "/v1/enrollments/begin", req, &out); err != nil {
		return Begun{}, err
	}
	if out.EnrollmentID == "" || out.EnrollmentToken == "" || out.Challenge == "" {
		return Begun{}, fmt.Errorf("the relay's answer is malformed")
	}
	return out, nil
}

// Complete finishes an enrollment once the challenge is published.
func Complete(ctx context.Context, relayURL string, req CompleteRequest) (Enrollment, error) {
	var out Enrollment
	if err := postJSON(ctx, relayURL, "/v1/enrollments/complete", req, &out); err != nil {
		return Enrollment{}, err
	}
	if out.ServerID == "" || out.ServerSecret == "" {
		return Enrollment{}, fmt.Errorf("the relay's answer is malformed")
	}
	return out, nil
}
