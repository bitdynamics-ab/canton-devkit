// Package ui hosts the Web UI server: an HTTP server bound to loopback
// that embeds the Vite/React build, exposes REST endpoints under /api/,
// and streams live updates over /events (SSE).
//
// This file owns the embedded-asset layer only. Server lifecycle lives in
// server.go; route wiring is in router.go.
//
// # Why go:embed
//
// The Web UI ships inside the same single `dpm` binary as the CLI — no
// `npm install` on the user side, no static server, no second process.
// `make frontend` populates `internal/ui/dist/` with the Vite build
// output and `go build` rolls it into the binary via `//go:embed`.
//
// # The dist/ placeholder
//
// `internal/ui/dist/index.html` is tracked with placeholder content so
// `go:embed` always has at least one match (an empty match is a
// build-time error). `make frontend` overwrites it; CI runs it before
// `go build` so the released binary never carries the placeholder.
package ui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// placeholderSentinel is the marker string embedded in the placeholder
// dist/index.html. `dpm localnet ui` checks IsPlaceholderBundle() at
// startup and prints a stderr warning so a release binary built without
// `make frontend` doesn't silently ship the dev placeholder.
const placeholderSentinel = "DEVKIT_FRONTEND_PLACEHOLDER"

// IsPlaceholderBundle reports whether the embedded dist/index.html
// is the build-time placeholder rather than a real Vite build.
// Cheap: a single byte-scan of the embedded index.
func IsPlaceholderBundle() bool {
	body, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		return true // can't read = effectively broken; warn either way
	}
	return bytes.Contains(body, []byte(placeholderSentinel))
}

// distFS is the embedded Vite build output. Everything under dist/ is
// rolled into the binary at compile time.
//
//go:embed all:dist
var distFS embed.FS

// AssetsHandler returns the HTTP handler that serves the embedded Vite
// bundle, with SPA fallback: requests that don't match an embedded file
// path are served `index.html` so React Router can take over.
//
// The fs.Sub strip is what lets the embedded "dist/foo.js" be served at
// "/foo.js" instead of leaking the build directory layout as
// "/dist/foo.js".
//
// Cache-Control headers are intentionally NOT set here — hashed-bundle
// caching belongs in middleware where the policy is visible from
// router.go, not buried in the asset handler.
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
	// file, serve index.html so React Router takes the URL. "Doesn't
	// resolve" is detected by stat-ing the FS — cheaper than calling
	// the file server and inspecting its 404.
	//
	// Traversal defence: http.FileServer already refuses traversal for
	// real-file requests, but we run our own fs.Stat first and the
	// embed.FS reject-on-traversal is implementation-defined, so any
	// path containing ".." (or a non-canonical path) gets an explicit
	// 400 — a security signal, not a stealth SPA-index response.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := r.URL.Path
		if clean == "" || clean == "/" {
			serveIndex(w, index)
			return
		}
		if strings.Contains(r.URL.Path, "..") {
			http.Error(w, "bad request: path traversal",
				http.StatusBadRequest)
			return
		}
		cleaned := path.Clean(clean)
		if cleaned != clean && cleaned+"/" != clean {
			http.Error(w, "bad request: non-canonical path",
				http.StatusBadRequest)
			return
		}
		// The embedded FS uses paths without a leading slash.
		statPath := cleaned
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
func serveIndex(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No-store: index.html carries the bootstrap script tag referencing
	// the hashed bundle, so a stale index after a deploy would load the
	// old bundle.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
