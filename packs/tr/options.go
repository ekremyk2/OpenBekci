// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"net/http"
	"time"
)

var defaultHTTPClient = &http.Client{Timeout: 20 * time.Second}

// Option configures New. The options exist for tests and recordings; a
// production build calls New() without any.
type Option func(*Pack)

// WithHTTPClient sets the HTTP client every provider call of the pack goes
// through (iyzico, İleti Merkezi). The tests give it a client whose transport
// routes to the in-process fakes of package trtest; a go-vcr recorder is
// an http.RoundTripper too, so a recording replaces the fake without a code
// change (see PAYMENTS.md).
func WithHTTPClient(c *http.Client) Option { return func(p *Pack) { p.http = c } }

// WithSMSTestEndpoint makes ValidateSettings test the İleti Merkezi
// credentials with a balance request against the given base URL. Without it
// ValidateSettings never calls the provider (a settings form must not trigger
// traffic to a live SMS account), see README.md.
func WithSMSTestEndpoint(baseURL string) Option { return func(p *Pack) { p.smsTestURL = baseURL } }

func (p *Pack) client() *http.Client {
	if p.http != nil {
		return p.http
	}
	return defaultHTTPClient
}
