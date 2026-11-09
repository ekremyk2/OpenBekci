package identity

import "testing"

func TestSafeNext(t *testing.T) {
	const org = "0192a3f0-1000-7000-8000-000000000001"
	good := []string{
		"", "/orgs/" + org, "/orgs/" + org + "/", "/orgs/" + org + "/tickets",
		"/orgs/" + org + "/tickets/0192a3f0-1000-7000-8000-000000000002?tab=comments&x=1",
	}
	for _, s := range good {
		if !SafeNext(s) {
			t.Errorf("SafeNext(%q) = false", s)
		}
	}
	bad := []string{
		"/", "/orgs", "/orgs/", "/orgs/nope", "/login", "/app/orgs/" + org, "orgs/" + org,
		"https://evil.example/orgs/" + org, "//evil.example/orgs/" + org, "/\\evil.example",
		"/orgs/" + org + "/../x", "/orgs/" + org + "/./x", "/orgs/" + org + "/%2e%2e/x", "/orgs/" + org + "%2fx", "/orgs/" + org + "/%5cx",
		"/orgs/" + org + "/x y", "/orgs/" + org + "/x\ty", "/orgs/" + org + "/x\x00", "/orgs/" + org + "/#frag",
		"/orgs/" + org + "/ü", "javascript:alert(1)", "/orgs/" + org + "/" + string(make([]byte, 600)),
	}
	for _, s := range bad {
		if SafeNext(s) {
			t.Errorf("SafeNext(%q) = true", s)
		}
	}
}
