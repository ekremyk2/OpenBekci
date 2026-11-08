package httpapi

import "testing"

// A request that is missing a required parameter and carries no credentials is a
// 401, not a 400/422 about the parameter: authentication comes before binding.
func TestIntegrationBindErrorsDoNotLeakBeforeAuthentication(t *testing.T) {
	c := newCommEnv(t)
	for _, path := range []string{"/visitor-entries", "/recurring-visitors", "/facilities", "/gate/console", "/gate/visitors", "/deliveries"} {
		rec := c.req("", "GET", c.path(path), "") // site_id is required and missing
		c.problemCode(rec, 401, "unauthenticated")
	}
	// the same request signed in gets the parameter problem
	if rec := c.req(c.manager, "GET", c.path("/visitor-entries"), ""); rec.Code != 400 && rec.Code != 422 {
		t.Fatalf("signed in, missing site_id: status %d: %s", rec.Code, rec.Body)
	}
}
