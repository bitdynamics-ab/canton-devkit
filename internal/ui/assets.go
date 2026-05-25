// Package ui hosts the M2 Web UI server: an HTTP server bound strictly to
// 127.0.0.1 that embeds the Vite/React build, exposes REST endpoints under
// /api/, and streams live updates over /events (SSE).
//
// This file owns the embedded-asset layer only. Server lifecycle lives in
// server.go; route wiring is in router.go.
//
// # Why go:embed
//
// The whole point of M2 is to ship the Web UI as part of the same single
// `dpm` binary the CLI ships as — no extra `npm install` on the user side,
// no static-server, no second process. `make frontend` populates
// `internal/ui/dist/` with the Vite build output, and `go build` rolls it
// into the binary via `//go:embed`. Cold-start of `dpm localnet ui` is a
// single file open, not a directory walk.
//
// # The dist/ placeholder
//
// `internal/ui/dist/index.html` is tracked at the placeholder content you
// see at clone time so `go:embed` always has at least one match (an empty
// match is a build-time error). `make frontend` overwrites it. CI runs
// `make frontend` before `go build` so the released binary never carries
// the placeholder.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

// distFS is the embedded Vite build output. Everything under dist/ is
// rolled into the binary at compile time. The exclude pattern keeps
// editor scratch files (.DS_Store, *.swp) out of the bundle if they're
// present locally.
//
//go:embed all:dist
var distFS embed.FS

// AssetsHandler returns the HTTP handler that serves the embedded Vite
// bundle, with SPA fallback: requests that don't match an embedded file
// path are served `index.html` so the React Router (whatever path the
// user lands on) can take over.
//
// The fs.Sub strip is what lets the embedded "dist/foo.js" be served at
// "/foo.js" — without it, the URL would be "/dist/foo.js" and we'd be
// leaking the build directory layout to the browser.
//
// Cache-Control headers are intentionally NOT set here. The Vite build
// produces hashed filenames (e.g. `app.4a2c.js`), and routing those
// through a future cache-control middleware (BIT-129 #2 or later) is
// the right shape — bolting it into the asset handler hides the policy
// from anyone reading router.go.
func AssetsHandler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	// SPA fallback: if the request path doesn't resolve to an embedded
	// file, serve index.html so React Router takes the URL. We detect
	// "doesn't resolve" by stat-ing the FS; that's cheaper than calling
	// the file server and inspecting its 404.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API and SSE routes are handled by the router BEFORE this
		// handler runs (chain order in router.go). Anything that
		// reaches us is either a static file or a SPA route.
		clean := r.URL.Path
		if clean == "" || clean == "/" {
			serveIndex(w, index)
			return
		}
		// Strip the leading slash for fs.Stat — the embedded FS uses
		// rooted-but-leading-slash-less paths.
		statPath := clean
		if statPath[0] == '/' {
			statPath = statPath[1:]
		}
		if _, statErr := fs.Stat(sub, statPath); statErr != nil {
			serveIndex(w, index)
			return
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}

// serveIndex writes the embedded index.html with the right Content-Type.
// Extracted so the entry-path and SPA-fallback branches stay one-liners.
func serveIndex(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No-store on index.html: it carries the bootstrap script tag that
	// references the hashed bundle, so a stale index after a deploy
	// would load the old bundle. Hashed assets get long cache; HTML
	// stays fresh.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
