package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func tag(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, name+" "+r.URL.Path)
	})
}

var builtUI = fstest.MapFS{
	"index.html":         {Data: []byte("<html>app</html>")},
	"favicon.svg":        {Data: []byte("<svg/>")},
	"assets/app-abc.js":  {Data: []byte("console.log(1)")},
	"assets/app-abc.css": {Data: []byte("body{}")},
}

func serve(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRouting(t *testing.T) {
	h := New(tag("s3"), tag("admin"), tag("mcp"), builtUI)
	browser := map[string]string{"Accept": "text/html,application/xhtml+xml"}

	cases := []struct {
		method, target string
		hdr            map[string]string
		wantCode       int
		wantBody       string
		wantLocation   string
	}{
		{"GET", "/_api/buckets", nil, 200, "admin /_api/buckets", ""},
		{"GET", "/", nil, 200, "s3 /", ""},                      // ListBuckets
		{"GET", "/", browser, 302, "", "/_ui/"},                 // a person
		{"GET", "/?x-id=ListBuckets", browser, 200, "s3 /", ""}, // SDK with query
		{"GET", "/laravel/a.txt", browser, 200, "s3 /laravel/a.txt", ""},
		{"PUT", "/laravel//double", nil, 200, "s3 /laravel//double", ""},
		{"GET", "/_ui", nil, 301, "", "/_ui/"},
		{"GET", "/_ui/", nil, 200, "<html>app</html>", ""},
		{"GET", "/_ui/b/laravel/avatars", nil, 200, "<html>app</html>", ""}, // client route
		{"GET", "/_ui/assets/app-abc.js", nil, 200, "console.log(1)", ""},
		{"GET", "/_ui/assets/missing.js", nil, 404, "", ""},
		{"GET", "/favicon.ico", nil, 301, "", "/_ui/favicon.svg"},
		{"GET", "/_apiish/x", nil, 200, "s3 /_apiish/x", ""},
		{"POST", "/_mcp", nil, 200, "mcp /_mcp", ""},
		{"POST", "/_mcpx", nil, 200, "s3 /_mcpx", ""},
	}
	for _, c := range cases {
		rec := serve(h, c.method, c.target, c.hdr)
		if rec.Code != c.wantCode {
			t.Errorf("%s %s: code %d, want %d", c.method, c.target, rec.Code, c.wantCode)
			continue
		}
		if c.wantBody != "" && !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s %s: body %q, want %q", c.method, c.target, rec.Body.String(), c.wantBody)
		}
		if c.wantLocation != "" && rec.Header().Get("Location") != c.wantLocation {
			t.Errorf("%s %s: location %q", c.method, c.target, rec.Header().Get("Location"))
		}
	}
}

func TestMCPDisabled(t *testing.T) {
	rec := serve(New(tag("s3"), tag("admin"), nil, builtUI), "POST", "/_mcp", nil)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "MCP=off") {
		t.Fatalf("disabled MCP: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBrowserDetectionNeedsNoAuth(t *testing.T) {
	h := New(tag("s3"), tag("admin"), tag("mcp"), builtUI)
	rec := serve(h, "GET", "/", map[string]string{"Accept": "text/html", "Authorization": "AWS4-HMAC-SHA256 ..."})
	if rec.Code != 200 || rec.Body.String() != "s3 /" {
		t.Fatalf("signed request was treated as a browser: %d", rec.Code)
	}
}

func TestUICaching(t *testing.T) {
	h := UI(builtUI)
	if cc := serve(h, "GET", "/_ui/assets/app-abc.css", nil).Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("hashed assets should be immutable: %q", cc)
	}
	if cc := serve(h, "GET", "/_ui/", nil).Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index should not be cached: %q", cc)
	}
	rec := serve(h, "GET", "/_ui/index.html", nil)
	if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Errorf("index.html: %d", rec.Code)
	}
}

func TestUIPlaceholderWhenNotBuilt(t *testing.T) {
	rec := serve(UI(fstest.MapFS{}), "GET", "/_ui/", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Fatalf("placeholder: %d %s", rec.Code, rec.Body.String())
	}
}
