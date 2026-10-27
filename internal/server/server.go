// Package server routes one port between the S3 API, the admin JSON API
// (/_api) and the web UI (/_ui). S3 bucket names cannot contain "_", so the
// prefixes never collide with a bucket.
package server

import (
	"io/fs"
	"net/http"
	"strings"
)

func New(s3, admin http.Handler, ui fs.FS) http.Handler {
	uiHandler := UI(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/_api/"):
			admin.ServeHTTP(w, r)
		case p == "/_ui" || strings.HasPrefix(p, "/_ui/"):
			uiHandler.ServeHTTP(w, r)
		case p == "/" && isBrowser(r):
			http.Redirect(w, r, "/_ui/", http.StatusFound)
		case p == "/favicon.ico":
			http.Redirect(w, r, "/_ui/favicon.svg", http.StatusMovedPermanently)
		default:
			// Not routed through ServeMux, which would clean and redirect
			// keys such as "a//b" or "../x".
			s3.ServeHTTP(w, r)
		}
	})
}

// isBrowser distinguishes a person opening the root URL from an S3
// ListBuckets call.
func isBrowser(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		r.URL.RawQuery == "" &&
		r.Header.Get("Authorization") == "" &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}

const placeholder = `<!doctype html><meta charset="utf-8"><title>go-objectstore</title>
<body style="font:14px system-ui;background:#0b0b0c;color:#d4d4d8;display:grid;place-items:center;height:100vh;margin:0">
<div><p>The UI has not been built.</p><pre style="color:#a1a1aa">cd frontend &amp;&amp; npm install &amp;&amp; npm run build</pre></div>`

// UI serves the built single-page app, falling back to index.html for
// client-side routes.
func UI(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ui" {
			http.Redirect(w, r, "/_ui/", http.StatusMovedPermanently)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/_ui/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, fsys, name)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			index = []byte(placeholder)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
