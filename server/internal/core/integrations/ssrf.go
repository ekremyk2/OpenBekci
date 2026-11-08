package integrations

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Limits of one webhook delivery (backend plan section 16).
const (
	// DeliveryTimeout bounds the whole request: dial, TLS, send and read.
	DeliveryTimeout = 10 * time.Second
	// MaxResponseBytes caps how much of a response body is read.
	MaxResponseBytes = 1 << 20
)

// ErrForbiddenTarget is returned when a webhook target resolves to an address
// that must not be reached from the server (loopback, private, link-local and
// other special-purpose ranges) and the operator has not allowed it.
var ErrForbiddenTarget = errors.New("webhook target address is not allowed")

// blockedRanges are special-purpose ranges a webhook must never reach unless
// the operator lists them in WEBHOOK_ALLOWED_CIDRS (self-hosted receivers on
// the LAN). IPv4-mapped IPv6 addresses are unmapped before the check, the
// IPv4 embedded in a well-known NAT64 address is checked too, and the other
// IPv6 forms that carry an IPv4 address (6to4, Teredo, IPv4-compatible,
// IPv4-translated, local-use NAT64) are refused outright.
var blockedRanges = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"10.0.0.0/8",      // private
	"100.64.0.0/10",   // carrier-grade NAT
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, cloud metadata
	"172.16.0.0/12",   // private
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"192.168.0.0/16",  // private
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // documentation
	"203.0.113.0/24",  // documentation
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, broadcast
	"192.88.99.0/24",  // 6to4 relay anycast (deprecated)
	"::/96",           // unspecified, loopback and deprecated IPv4-compatible addresses (::127.0.0.1)
	"::ffff:0:0:0/96", // IPv4-translated (SIIT) addresses
	"64:ff9b::/96",    // NAT64 (the embedded IPv4 is checked as well)
	"64:ff9b:1::/48",  // local-use NAT64
	"100::/64",        // discard-only
	"2001::/23",       // IETF protocol assignments, including Teredo (2001::/32)
	"2001:db8::/32",   // documentation
	"2002::/16",       // 6to4: embeds an IPv4 address the host may tunnel to; deprecated, refused outright
	"3fff::/20",       // documentation
	"5f00::/16",       // SRv6 segment identifiers
	"fc00::/7",        // unique local
	"fe80::/10",       // link-local
	"ff00::/8",        // multicast
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// AddrAllowed reports whether a delivery may connect to ip. Addresses in the
// operator's allowed ranges pass even when they are in a blocked range.
func AddrAllowed(ip netip.Addr, allowed []netip.Prefix) bool {
	ip = ip.Unmap()
	for _, p := range allowed {
		if p.Contains(ip) {
			return true
		}
	}
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedRanges {
		if p.Contains(ip) {
			if p.Addr() == netip.MustParseAddr("64:ff9b::") {
				b := ip.As16()
				if v4 := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}); AddrAllowed(v4, allowed) {
					continue // NAT64 to a public IPv4 address
				}
			}
			return false
		}
	}
	return true
}

// Resolver looks up host names; *net.Resolver satisfies it, tests replace it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// resolveAllowed resolves host and fails unless every address it resolves to
// is allowed (a host that points anywhere internal is refused outright, so a
// mixed answer cannot slip a private address through). It returns the
// addresses for the dialer to connect to directly.
func resolveAllowed(ctx context.Context, r Resolver, host string, allowed []netip.Prefix) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !AddrAllowed(ip, allowed) {
			return nil, fmt.Errorf("%w: %s", ErrForbiddenTarget, ip)
		}
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if !AddrAllowed(ip, allowed) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrForbiddenTarget, host, ip.Unmap())
		}
		out = append(out, ip.Unmap())
	}
	return out, nil
}

// NewGuardedClient returns the HTTP client deliveries use: addresses are
// validated at connect time (after resolution, so DNS rebinding cannot swap a
// public answer for a private one between check and use), proxies from the
// environment are ignored, redirects are not followed (a 3xx is a failed
// delivery) and the whole request is bounded by DeliveryTimeout. tlsCfg may be
// nil (system roots); tests pass the receiver's roots.
func NewGuardedClient(allowed []netip.Prefix, tlsCfg *tls.Config) *http.Client {
	return newGuardedClient(net.DefaultResolver, allowed, tlsCfg)
}

func newGuardedClient(r Resolver, allowed []netip.Prefix, tlsCfg *tls.Config) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	cfg := tlsCfg.Clone()
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := resolveAllowed(ctx, r, host, allowed)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
		TLSClientConfig:        cfg,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  DeliveryTimeout,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   DeliveryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ValidateTarget checks a webhook URL when an endpoint is created or changed:
// https only, a host, no credentials in the URL, no fragment, and a host that
// does not resolve to a forbidden address. It returns the field code to
// report ("invalid_url", "https_required", "forbidden_target", "unresolvable").
// Delivery checks the address again at connect time.
func ValidateTarget(ctx context.Context, r Resolver, raw string, allowed []netip.Prefix) (*url.URL, string) {
	if r == nil {
		r = net.DefaultResolver
	}
	if len(raw) > 2000 {
		return nil, "too_long"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, "invalid_url"
	}
	if u.Scheme != "https" {
		return nil, "https_required"
	}
	if u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, "invalid_url"
	}
	if p := u.Port(); p != "" {
		if n := atoiPort(p); n < 1 || n > 65535 {
			return nil, "invalid_url"
		}
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := resolveAllowed(rctx, r, u.Hostname(), allowed); err != nil {
		if errors.Is(err, ErrForbiddenTarget) {
			return nil, "forbidden_target"
		}
		return nil, "unresolvable"
	}
	return u, ""
}

func atoiPort(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' || n > 65535 {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}
