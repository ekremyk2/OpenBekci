//go:build !nowebassets

// Package webassets serves the embedded web app.
//
// The release build copies web/dist into ./dist before compiling (go:embed
// cannot reach outside the package directory). Until the web app exists, dist
// holds only a .gitkeep and the placeholder page is served.
package webassets

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler returns the handler for everything the API does not claim: static
// files from the embedded build, with unknown non-file paths falling back to
// index.html for client-side routing.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return placeholderHandler()
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return placeholderHandler()
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}
