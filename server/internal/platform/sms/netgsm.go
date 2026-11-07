package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Netgsm implements Gateway against the Netgsm REST v2 API.
//
// API implemented (from Netgsm's public documentation, "SMS Gonder / REST v2";
// not yet exercised against a live account, so verify with a real sender id
// before relying on it):
//
//	POST https://api.netgsm.com.tr/sms/rest/v2/send
//	Authorization: Basic base64(usercode:password)
//	Content-Type: application/json
//	{"msgheader":"<sender id>","encoding":"TR",
//	 "messages":[{"msg":"<text>","no":"<905xxxxxxxxx>"}]}
//
// Response (HTTP 200 also for most errors): {"code":"00","jobid":"<id>","description":"queued"}.
// code "00" means accepted; documented failures: 20 text problem or too long,
// 30 invalid credentials or API access not permitted (or source IP not
// whitelisted), 40 sender id not defined, 50 and 51 IYS filter problems,
// 70 invalid parameters, 80 send limit exceeded, 85 duplicate send limit.
// Numbers are sent as digits with the country code and no plus sign.
type Netgsm struct {
	Usercode string
	Password string
	// MsgHeader is the approved sender id.
	MsgHeader string
	// BaseURL overrides the API endpoint (tests); empty means production.
	BaseURL string
	// Client overrides the HTTP client; the default has a 10 second timeout.
	Client *http.Client
}

const netgsmEndpoint = "https://api.netgsm.com.tr/sms/rest/v2/send"

type netgsmRequest struct {
	MsgHeader string           `json:"msgheader"`
	Encoding  string           `json:"encoding"`
	Messages  []netgsmMessages `json:"messages"`
}

type netgsmMessages struct {
	Msg string `json:"msg"`
	No  string `json:"no"`
}

type netgsmResponse struct {
	Code        string `json:"code"`
	JobID       string `json:"jobid"`
	Description string `json:"description"`
}

// Send implements Gateway.
func (n *Netgsm) Send(ctx context.Context, to, text string) (string, error) {
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := n.BaseURL
	if endpoint == "" {
		endpoint = netgsmEndpoint
	}
	body, err := json.Marshal(netgsmRequest{
		MsgHeader: n.MsgHeader,
		Encoding:  "TR",
		Messages:  []netgsmMessages{{Msg: text, No: strings.TrimPrefix(to, "+")}},
	})
	if err != nil {
		return "", fmt.Errorf("netgsm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("netgsm: build request: %w", err)
	}
	req.SetBasicAuth(n.Usercode, n.Password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("%w: read response: %w", ErrUnavailable, err)
	}
	var out netgsmResponse
	if jerr := json.Unmarshal(raw, &out); jerr != nil || out.Code == "" {
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return "", ErrCredentials
		case resp.StatusCode >= 500:
			return "", fmt.Errorf("%w: http %d", ErrUnavailable, resp.StatusCode)
		}
		return "", fmt.Errorf("%w: unexpected response (http %d)", ErrRejected, resp.StatusCode)
	}
	if err := mapNetgsmCode(out.Code); err != nil {
		return "", fmt.Errorf("%w (netgsm code %s)", err, out.Code)
	}
	return out.JobID, nil
}

func mapNetgsmCode(code string) error {
	switch code {
	case "00", "01", "02":
		return nil
	case "30":
		return ErrCredentials
	case "40":
		return ErrSender
	case "80", "85":
		return ErrQuota
	case "20", "50", "51", "70":
		return ErrRejected
	}
	return errors.Join(ErrRejected, fmt.Errorf("unknown netgsm code %q", code))
}
