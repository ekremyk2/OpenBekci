package identity

import (
	"net/url"
	"strings"
	"testing"
)

// Every mobile link names the instance as its host; a plain-http base
// (development) says so; a port is kept; the web form stays under the base URL.
func TestLinkURLCarriesTheInstance(t *testing.T) {
	for _, c := range []struct {
		base string
		kind LinkKind
		want string
	}{
		{"https://bekci.example.org", LinkInvite, "openbekci://bekci.example.org/invite/T"},
		{"http://localhost:8484", LinkInvite, "openbekci://localhost:8484/invite/T?scheme=http"},
		{"http://localhost:8484/", LinkMagic, "openbekci://localhost:8484/login/magic?token=T&scheme=http"},
		{"https://bekci.example.org:8443", LinkJoin, "openbekci://bekci.example.org:8443/join/T"},
		{"bekci.example.org", LinkReset, "openbekci://bekci.example.org/reset/T"},
	} {
		if got := LinkURL(c.base, ClientMobile, c.kind, "T"); got != c.want {
			t.Errorf("LinkURL(%q, mobile, %s) = %q, want %q", c.base, c.kind, got, c.want)
		}
	}
	if got := LinkURL("http://localhost:8484", ClientWeb, LinkInvite, "T"); got != "http://localhost:8484/invite/T" {
		t.Errorf("web link = %q", got)
	}
}

// Invitation tokens and join codes are "<org>.<secret>": the dot must survive
// URL parsing, in the path and in the magic-link query, and must not be taken
// for part of the host.
func TestLinkURLDottedTokensSurviveParsing(t *testing.T) {
	const tok = "AbC_d-E0AbC_d-E0AbC_d-.xyz_9-Q.more"
	for _, k := range []LinkKind{LinkInvite, LinkReset, LinkJoin, LinkMagic} {
		raw := LinkURL("https://bekci.example.org", ClientMobile, k, tok)
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if u.Scheme != "openbekci" || u.Host != "bekci.example.org" {
			t.Errorf("%s: scheme %q host %q", raw, u.Scheme, u.Host)
		}
		got := u.Query().Get("token")
		if k != LinkMagic {
			got = u.Path[strings.LastIndexByte(u.Path, '/')+1:]
		}
		if got != tok {
			t.Errorf("%s: token %q, want %q", raw, got, tok)
		}
	}
}
