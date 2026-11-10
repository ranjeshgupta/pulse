// Package web embeds the built web app (npm run build → dist/) so the Go binary serves it.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the app. Unknown paths get index.html so client-side routes (/segments/4)
// load directly; hashed assets are cached for a year, index.html never.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(root, p); err != nil {
			p = "index.html"
		}
		if p == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Security-Policy", csp)
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if strings.HasPrefix(p, "sdk/") { // the web SDK keeps a stable URL, so cache it briefly
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		files.ServeHTTP(w, r)
	})
}

// csp keeps the app to its own scripts. Images may come from anywhere (template previews use
// remote image URLs); authored HTML is only ever previewed in sandboxed frames.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src * data: blob:; " +
	"font-src 'self' data:; frame-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
