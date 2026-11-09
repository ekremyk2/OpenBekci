// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openbekci/openbekci/sdk"
)

// IletiMerkeziID is the SMS provider id; the organization setting sms_provider names it.
const IletiMerkeziID = ID + ".iletimerkezi"

const iletiBase = "https://api.iletimerkezi.com"

// iletiMerkezi is the İleti Merkezi SMSProvider (https://www.iletimerkezi.com,
// send-sms JSON API: POST /v1/send-sms/json, credentials in the request body).
//
// İYS: every message is sent with iys "0" (not a commercial message). The pack
// sends operational notices only (sign-in codes, dues reminders, meeting
// calls); marketing is out of scope and the İYS consent flow is not
// implemented. See LEGAL.md.
type iletiMerkezi struct{ p *Pack }

var _ sdk.SMSProvider = (*iletiMerkezi)(nil)

func (*iletiMerkezi) ID() string { return IletiMerkeziID }

type iletiCreds struct{ key, hash, sender string }

func (m *iletiMerkezi) creds(ctx context.Context) (iletiCreds, error) {
	doc, err := m.p.svc.Settings().All(ctx, sdk.OrgScope())
	if err != nil {
		return iletiCreds{}, err
	}
	return credsFrom(doc)
}

func credsFrom(doc map[string]any) (iletiCreds, error) {
	s := func(k string) string { v, _ := doc[k].(string); return v }
	if en, _ := doc["tr_sms_iletimerkezi_enabled"].(bool); !en {
		return iletiCreds{}, fmt.Errorf("İleti Merkezi is not enabled in the settings: %w", sdk.ErrUnsupported)
	}
	c := iletiCreds{key: s("tr_sms_iletimerkezi_username"), hash: s("tr_sms_iletimerkezi_password"), sender: s("tr_sms_iletimerkezi_sender_id")}
	if c.key == "" || c.hash == "" {
		return c, fmt.Errorf("the İleti Merkezi username (API key) and password (API hash) are not set: %w", sdk.ErrCredentials)
	}
	return c, nil
}

type iletiAuth struct {
	Key  string `json:"key"`
	Hash string `json:"hash"`
}

type iletiSendRequest struct {
	Request struct {
		Authentication iletiAuth `json:"authentication"`
		Order          struct {
			Sender  string `json:"sender"`
			IYS     string `json:"iys"`
			Message struct {
				Text       string `json:"text"`
				Receipents struct {
					Number []string `json:"number"`
				} `json:"receipents"`
			} `json:"message"`
		} `json:"order"`
	} `json:"request"`
}

type iletiResponse struct {
	Response struct {
		Status struct {
			Code    json.Number `json:"code"`
			Message string      `json:"message"`
		} `json:"status"`
		Order struct {
			ID json.Number `json:"id"`
		} `json:"order"`
		Balance struct {
			Amount json.Number `json:"amount"`
		} `json:"balance"`
	} `json:"response"`
}

// number turns +90 5xx xxx xx xx into the 905xxxxxxxxx form the API takes.
func number(to string) string {
	var b strings.Builder
	for _, r := range to {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *iletiMerkezi) Send(ctx context.Context, to, text string) (string, error) {
	c, err := m.creds(ctx)
	if err != nil {
		return "", err
	}
	if c.sender == "" {
		return "", fmt.Errorf("the İleti Merkezi sender id is not set: %w", sdk.ErrRejected)
	}
	n := number(to)
	if n == "" {
		return "", fmt.Errorf("phone number %q: %w", to, sdk.ErrRejected)
	}
	var req iletiSendRequest
	req.Request.Authentication = iletiAuth{Key: c.key, Hash: c.hash}
	req.Request.Order.Sender = c.sender
	req.Request.Order.IYS = "0" // operational, not a commercial message
	req.Request.Order.Message.Text = text
	req.Request.Order.Message.Receipents.Number = []string{n}
	resp, err := m.post(ctx, iletiBase, "/v1/send-sms/json", req)
	if err != nil {
		return "", err
	}
	return resp.Response.Order.ID.String(), nil
}

func (m *iletiMerkezi) post(ctx context.Context, base, path string, in any) (iletiResponse, error) {
	var out iletiResponse
	body, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.p.client().Do(req)
	if err != nil {
		return out, fmt.Errorf("%w: İleti Merkezi request failed", sdk.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return out, fmt.Errorf("%w: İleti Merkezi: read response", sdk.ErrUnavailable)
	}
	if resp.StatusCode >= 500 {
		return out, fmt.Errorf("%w: İleti Merkezi answered http %d", sdk.ErrUnavailable, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Response.Status.Code == "" {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return out, sdk.ErrCredentials
		}
		return out, fmt.Errorf("%w: İleti Merkezi: unexpected answer (http %d)", sdk.ErrRejected, resp.StatusCode)
	}
	return out, mapIletiCode(out.Response.Status.Code.String(), out.Response.Status.Message)
}

// mapIletiCode maps the status code of the answer: 200 is success, 401 the
// credentials (or API access switched off in the panel), 451 a duplicate, 452
// and 466 recipient problems (documented); 402 (no credit) and 450 (sender
// not allowed) are assumptions.
func mapIletiCode(code, msg string) error {
	switch code {
	case "200":
		return nil
	case "401":
		return fmt.Errorf("%w: İleti Merkezi code 401", sdk.ErrCredentials)
	}
	return fmt.Errorf("%w: İleti Merkezi code %s: %s", sdk.ErrRejected, code, msg)
}

// ValidateSettings implements sdk.ValidateSettings for the iyzico and İleti
// Merkezi settings. The shape checks always run. The İleti Merkezi credentials
// are tested with a balance request only when the pack was built with
// WithSMSTestEndpoint (tests and staging); a live settings form never sends
// anything to the provider.
func (p *Pack) ValidateSettings(ctx context.Context, scope sdk.Scope, values map[string]any) error {
	s := func(k string) string { v, _ := values[k].(string); return v }
	b := func(k string) bool { v, _ := values[k].(bool); return v }
	if (s("tr_payments_iyzico_api_key") == "") != (s("tr_payments_iyzico_secret_key") == "") {
		return fmt.Errorf("iyzico needs both the api key and the secret key")
	}
	if b("tr_payments_iyzico_enabled") && scope.Kind == sdk.ScopeOrg && s("tr_payments_iyzico_api_key") == "" {
		return fmt.Errorf("iyzico is enabled: set the api key and the secret key")
	}
	if u := s("tr_payments_iyzico_callback_base_url"); u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://localhost") {
		return fmt.Errorf("the public address of the server must be an https:// address (iyzico requires a valid certificate)")
	}
	if (s("tr_sms_iletimerkezi_username") == "") != (s("tr_sms_iletimerkezi_password") == "") {
		return fmt.Errorf("İleti Merkezi needs both the username (API key) and the password (API hash)")
	}
	if b("tr_sms_iletimerkezi_enabled") && scope.Kind == sdk.ScopeOrg && (s("tr_sms_iletimerkezi_username") == "" || s("tr_sms_iletimerkezi_sender_id") == "") {
		return fmt.Errorf("İleti Merkezi is enabled: set the username, the password and the sender id")
	}
	if p.smsTestURL != "" && b("tr_sms_iletimerkezi_enabled") && s("tr_sms_iletimerkezi_username") != "" {
		m := &iletiMerkezi{p: p}
		var req struct {
			Request struct {
				Authentication iletiAuth `json:"authentication"`
			} `json:"request"`
		}
		req.Request.Authentication = iletiAuth{Key: s("tr_sms_iletimerkezi_username"), Hash: s("tr_sms_iletimerkezi_password")}
		if _, err := m.post(ctx, p.smsTestURL, "/v1/get-balance/json", req); err != nil {
			return err
		}
	}
	return nil
}

var _ sdk.ValidateSettings = (*Pack)(nil)
