// Package web serves the web app built from web/ (npm run build writes it
// to dist). Every app path gets index.html and React Router takes over;
// /ui/ serves the built assets.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

//go:embed all:dist
var dist embed.FS

// csp allows only the app's own scripts and calls. Tailwind and Radix set
// inline styles, so styles may be inline.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// placeholder is served when the binary was built without the app.
const placeholder = `<!doctype html><meta charset="utf-8"><title>Nexus</title>
<p>This build of Nexus has no web app. Run <code>npm ci && npm run build</code> in <code>web/</code>, then build again.`

// Register mounts the app's pages and assets on r.
func Register(r *router.Router[*core.RequestEvent]) {
	files, _ := fs.Sub(dist, "dist")
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		index = []byte(placeholder)
	}
	page := func(e *core.RequestEvent) error {
		h := e.Response.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		return e.HTML(http.StatusOK, string(index))
	}
	r.GET("/account", page)
	r.GET("/account/{path...}", page)

	assets := http.StripPrefix("/ui/", http.FileServerFS(files))
	r.GET("/ui/{path...}", func(e *core.RequestEvent) error {
		p := e.Request.PathValue("path")
		if p == "" || p == "index.html" || strings.HasSuffix(p, "/") || strings.HasPrefix(p, ".") {
			return e.NotFoundError("", nil)
		}
		h := e.Response.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(p, "assets/") {
			// Vite puts a content hash in every asset name.
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		assets.ServeHTTP(e.Response, e.Request)
		return nil
	})
}
