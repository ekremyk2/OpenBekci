//go:build nowebassets

// Package webassets serves the embedded web app. This build variant
// (-tags nowebassets) serves only a "web app not built" page, so Go-only
// contributors need neither Node nor a built web/dist.
package webassets

import "net/http"

// Handler returns the handler for everything the API does not claim.
func Handler() http.Handler { return placeholderHandler() }
