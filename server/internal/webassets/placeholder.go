package webassets

import "net/http"

const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OpenBekci</title>
</head>
<body>
<h1>OpenBekci</h1>
<p>The web app is not built into this binary. The API is available under <code>/v1</code>.</p>
<p>Build the web app and rebuild without <code>-tags nowebassets</code> to embed it.</p>
</body>
</html>
`

func placeholderHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		// 200 only at the root; any other unknown path is a plain 404.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(placeholderHTML))
	})
}
