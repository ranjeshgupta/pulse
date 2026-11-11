// Package web embeds the built web app (npm run build → dist/) so the Go binary serves it.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist always has a committed .gitkeep, so the Go build works before the app is built (a fresh
// clone, make test); Handler then says how to build it. npm run build recreates .gitkeep.
//
//go:embed all:dist
var dist embed.FS

// Built reports whether the web app was built into dist before this binary was compiled.
func Built() bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

const notBuilt = `<!doctype html><title>Pulse</title><p>The web app isn't built into this binary.
Run <code>cd web && npm ci && npm run build</code>, then build Pulse again (the Docker image does both).</p>`

// Handler serves the app. Unknown paths get index.html so client-side routes (/segments/4)
// load directly; hashed assets are cached for a year, index.html never.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(root)
	built := Built()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuilt))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		// Directories (assets/, sdk/) are never listed: they are app routes too (/assets).
		if st, err := fs.Stat(root, p); err != nil || st.IsDir() {
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
