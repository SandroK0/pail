package adminapi

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SandroK0/pail/internal/events"
	"github.com/SandroK0/pail/internal/s3api"
	"github.com/SandroK0/pail/internal/store"
)

type env struct {
	srv    *httptest.Server
	store  *store.Store
	events *events.Broker
	api    *API
}

// newEnv serves the admin API and the S3 API on one test server, as main does.
func newEnv(t *testing.T, verify bool) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	broker := events.NewBroker(100)
	s3 := s3api.New(st, s3api.Config{AccessKey: "local", SecretKey: "localsecret", VerifyAuth: verify}, broker)
	e := &env{store: st, events: broker}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_api/") {
			e.api.ServeHTTP(w, r)
			return
		}
		s3.ServeHTTP(w, r)
	}))
	t.Cleanup(e.srv.Close)
	e.api = New(st, broker, Config{
		PublicURL: e.srv.URL, Region: "us-east-1", AccessKey: "local", SecretKey: "localsecret",
		VerifyAuth: verify, Buckets: []string{"laravel"},
	})
	if err := st.CreateBucket(t.Context(), "laravel"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) call(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var r io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case string:
		r, ct = strings.NewReader(b), "text/plain"
	default:
		j, _ := json.Marshal(b)
		r, ct = strings.NewReader(string(j)), "application/json"
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (e *env) put(t *testing.T, bucket, key, body string) {
	t.Helper()
	if _, err := e.store.PutObject(t.Context(), bucket, key, strings.NewReader(body), store.PutOptions{}); err != nil {
		t.Fatal(err)
	}
}

func q(key string) string { return url.QueryEscape(key) }

func TestInfo(t *testing.T) {
	e := newEnv(t, false)
	var info infoJSON
	if e.call(t, "GET", "/_api/info", nil, &info) != 200 {
		t.Fatal("status")
	}
	if info.Endpoint != e.srv.URL || info.AccessKey != "local" || info.DefaultBucket != "laravel" || info.Region != "us-east-1" {
		t.Fatalf("info = %+v", info)
	}
}

func TestBuckets(t *testing.T) {
	e := newEnv(t, false)
	var created bucketJSON
	if code := e.call(t, "POST", "/_api/buckets", map[string]string{"name": "media"}, &created); code != 201 || created.Name != "media" {
		t.Fatalf("create = %d %+v", code, created)
	}
	var errBody errorJSON
	if code := e.call(t, "POST", "/_api/buckets", map[string]string{"name": "media"}, &errBody); code != 409 || errBody.Error == "" {
		t.Fatalf("duplicate = %d %+v", code, errBody)
	}
	if code := e.call(t, "POST", "/_api/buckets", map[string]string{"name": "No"}, nil); code != 400 {
		t.Fatalf("invalid = %d", code)
	}

	e.put(t, "media", "a.jpg", "12345")
	var list []bucketJSON
	e.call(t, "GET", "/_api/buckets", nil, &list)
	if len(list) != 2 || list[1].Name != "media" || list[1].ObjectCount != 1 || list[1].Size != 5 {
		t.Fatalf("list = %+v", list)
	}

	if code := e.call(t, "DELETE", "/_api/buckets/media", nil, nil); code != 409 {
		t.Fatalf("delete non-empty = %d", code)
	}
	if code := e.call(t, "DELETE", "/_api/buckets/media?force=true", nil, nil); code != 204 {
		t.Fatalf("force delete = %d", code)
	}
	if code := e.call(t, "DELETE", "/_api/buckets/media", nil, nil); code != 404 {
		t.Fatalf("delete missing = %d", code)
	}
}

func TestListObjects(t *testing.T) {
	e := newEnv(t, false)
	for _, k := range []string{"readme.md", "avatars/", "avatars/me.png", "avatars/2026/you.png", "docs/a.pdf"} {
		e.put(t, "laravel", k, "x")
	}
	var root listingJSON
	e.call(t, "GET", "/_api/buckets/laravel/objects", nil, &root)
	if len(root.Folders) != 2 || root.Folders[0].Name != "avatars" || root.Folders[0].Prefix != "avatars/" ||
		len(root.Objects) != 1 || root.Objects[0].Name != "readme.md" {
		t.Fatalf("root = %+v", root)
	}
	if root.Objects[0].URL != e.srv.URL+"/laravel/readme.md" {
		t.Fatalf("url = %s", root.Objects[0].URL)
	}

	var sub listingJSON
	e.call(t, "GET", "/_api/buckets/laravel/objects?prefix=avatars/", nil, &sub)
	if len(sub.Objects) != 1 || sub.Objects[0].Key != "avatars/me.png" || len(sub.Folders) != 1 || sub.Folders[0].Name != "2026" {
		t.Fatalf("avatars/ = %+v (folder marker must be hidden)", sub)
	}

	var page listingJSON
	e.call(t, "GET", "/_api/buckets/laravel/objects?limit=1", nil, &page)
	if page.NextCursor == "" {
		t.Fatal("no cursor on a truncated page")
	}
	var next listingJSON
	e.call(t, "GET", "/_api/buckets/laravel/objects?limit=1&cursor="+q(page.NextCursor), nil, &next)
	if len(next.Folders)+len(next.Objects) != 1 || (len(next.Folders) == 1 && next.Folders[0].Prefix == page.Folders[0].Prefix) {
		t.Fatalf("second page = %+v", next)
	}

	if code := e.call(t, "GET", "/_api/buckets/nope/objects", nil, nil); code != 404 {
		t.Fatalf("missing bucket = %d", code)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t, false)
	e.put(t, "laravel", "invoices/2026/INV-001.pdf", "x")
	e.put(t, "laravel", "avatars/me.png", "x")
	var hits []objectJSON
	e.call(t, "GET", "/_api/buckets/laravel/search?q=inv-0", nil, &hits)
	if len(hits) != 1 || hits[0].Key != "invoices/2026/INV-001.pdf" {
		t.Fatalf("hits = %+v", hits)
	}
	e.call(t, "GET", "/_api/buckets/laravel/search?q=", nil, &hits)
	if len(hits) != 0 {
		t.Fatal("empty query should return nothing")
	}
}

func TestUploadDetailAndACL(t *testing.T) {
	e := newEnv(t, false)
	var obj objectJSON
	code := e.call(t, "PUT", "/_api/buckets/laravel/object?key="+q("docs/notes.txt"), "hello", &obj)
	if code != 201 || obj.Size != 5 || obj.ContentType != "text/plain" || obj.ACL != "private" {
		t.Fatalf("upload = %d %+v", code, obj)
	}

	// No Content-Type: guessed from the extension.
	req, _ := http.NewRequest("PUT", e.srv.URL+"/_api/buckets/laravel/object?key=pic.png", strings.NewReader("png"))
	resp, _ := http.DefaultClient.Do(req)
	json.NewDecoder(resp.Body).Decode(&obj)
	resp.Body.Close()
	if obj.ContentType != "image/png" {
		t.Fatalf("guessed type = %s", obj.ContentType)
	}

	var detail objectJSON
	e.call(t, "GET", "/_api/buckets/laravel/object?key="+q("docs/notes.txt"), nil, &detail)
	if detail.Metadata == nil || detail.ETag == "" {
		t.Fatalf("detail = %+v", detail)
	}
	if code := e.call(t, "GET", "/_api/buckets/laravel/object", nil, nil); code != 400 {
		t.Fatalf("missing key = %d", code)
	}
	if code := e.call(t, "GET", "/_api/buckets/laravel/object?key=nope", nil, nil); code != 404 {
		t.Fatalf("missing object = %d", code)
	}

	e.call(t, "PATCH", "/_api/buckets/laravel/object?key="+q("docs/notes.txt"), map[string]string{"acl": "public-read"}, &detail)
	if detail.ACL != "public-read" {
		t.Fatalf("acl = %s", detail.ACL)
	}
	if code := e.call(t, "PATCH", "/_api/buckets/laravel/object?key="+q("docs/notes.txt"), map[string]string{"acl": "everyone"}, nil); code != 400 {
		t.Fatalf("bad acl = %d", code)
	}
}

func TestRaw(t *testing.T) {
	e := newEnv(t, false)
	e.store.PutObject(t.Context(), "laravel", "page.html", strings.NewReader("<script>alert(1)</script>"), store.PutOptions{ContentType: "text/html"})
	e.store.PutObject(t.Context(), "laravel", "dir/photo.jpg", strings.NewReader("jpeg"), store.PutOptions{ContentType: "image/jpeg"})

	resp, _ := http.Get(e.srv.URL + "/_api/buckets/laravel/raw?key=page.html")
	resp.Body.Close()
	if resp.Header.Get("Content-Security-Policy") != "sandbox" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("html served without sandbox: %v", resp.Header)
	}

	resp, _ = http.Get(e.srv.URL + "/_api/buckets/laravel/raw?key=" + q("dir/photo.jpg") + "&download=1")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "jpeg" || resp.Header.Get("Content-Type") != "image/jpeg" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename=photo.jpg` || resp.Header.Get("Content-Security-Policy") != "" {
		t.Fatalf("download: %q %v", body, resp.Header)
	}

	req, _ := http.NewRequest("GET", e.srv.URL+"/_api/buckets/laravel/raw?key="+q("dir/photo.jpg"), nil)
	req.Header.Set("Range", "bytes=1-2")
	resp, _ = http.DefaultClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(body) != "pe" {
		t.Fatalf("range = %d %q", resp.StatusCode, body)
	}
}

func TestDeleteObjectsAndFolders(t *testing.T) {
	e := newEnv(t, false)
	for _, k := range []string{"a.txt", "b.txt", "logs/1", "logs/2", "logs/sub/3"} {
		e.put(t, "laravel", k, "x")
	}
	var res map[string]int
	code := e.call(t, "POST", "/_api/buckets/laravel/delete", map[string]any{"keys": []string{"a.txt", "missing"}, "prefixes": []string{"logs/"}}, &res)
	if code != 200 || res["deleted"] != 5 {
		t.Fatalf("delete = %d %v", code, res)
	}
	b, _ := e.store.GetBucket(t.Context(), "laravel")
	if b.ObjectCount != 1 {
		t.Fatalf("left %d objects", b.ObjectCount)
	}
	if code := e.call(t, "POST", "/_api/buckets/laravel/delete", map[string]any{"prefixes": []string{""}}, nil); code != 400 {
		t.Fatalf("empty prefix (would wipe the bucket) = %d", code)
	}
}

func TestCreateFolder(t *testing.T) {
	e := newEnv(t, false)
	var f folderJSON
	if code := e.call(t, "POST", "/_api/buckets/laravel/folders", map[string]string{"prefix": "/uploads/2026"}, &f); code != 201 || f.Prefix != "uploads/2026/" || f.Name != "2026" {
		t.Fatalf("folder = %d %+v", code, f)
	}
	if _, err := e.store.HeadObject(t.Context(), "laravel", "uploads/2026/"); err != nil {
		t.Fatal("marker not written")
	}
	if code := e.call(t, "POST", "/_api/buckets/laravel/folders", map[string]string{"prefix": " / "}, nil); code != 400 {
		t.Fatalf("empty folder = %d", code)
	}
}

func TestMove(t *testing.T) {
	e := newEnv(t, false)
	for _, k := range []string{"old.txt", "photos/", "photos/a.jpg", "photos/2026/b.jpg"} {
		e.put(t, "laravel", k, k)
	}
	var res map[string]int
	if code := e.call(t, "POST", "/_api/buckets/laravel/move", map[string]string{"from": "old.txt", "to": "new.txt"}, &res); code != 200 || res["moved"] != 1 {
		t.Fatalf("rename = %d %v", code, res)
	}
	if code := e.call(t, "POST", "/_api/buckets/laravel/move", map[string]string{"from": "photos/", "to": "images"}, &res); code != 200 || res["moved"] != 3 {
		t.Fatalf("folder move = %d %v", code, res)
	}
	for _, k := range []string{"new.txt", "images/", "images/a.jpg", "images/2026/b.jpg"} {
		if _, err := e.store.HeadObject(t.Context(), "laravel", k); err != nil {
			t.Errorf("%s missing after move", k)
		}
	}
	if code := e.call(t, "POST", "/_api/buckets/laravel/move", map[string]string{"from": "images/", "to": "images/inner/"}, nil); code != 400 {
		t.Fatalf("move into itself = %d", code)
	}
	if code := e.call(t, "POST", "/_api/buckets/laravel/move", map[string]string{"from": "ghost/", "to": "x/"}, nil); code != 404 {
		t.Fatalf("missing folder = %d", code)
	}
	if code := e.call(t, "POST", "/_api/buckets/laravel/move", map[string]string{"from": "", "to": "x"}, nil); code != 400 {
		t.Fatalf("empty from = %d", code)
	}
}

func TestPresignWorksAgainstS3(t *testing.T) {
	e := newEnv(t, true) // signatures are verified
	e.put(t, "laravel", "docs/report 2026.pdf", "pdf")

	var res struct {
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	code := e.call(t, "POST", "/_api/buckets/laravel/presign", map[string]any{"key": "docs/report 2026.pdf", "expiresIn": 60}, &res)
	if code != 200 || time.Until(res.ExpiresAt) > time.Minute || time.Until(res.ExpiresAt) < 50*time.Second {
		t.Fatalf("presign = %d %+v", code, res)
	}
	resp, err := http.Get(res.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "pdf" {
		t.Fatalf("GET presigned = %d %q", resp.StatusCode, body)
	}

	e.call(t, "POST", "/_api/buckets/laravel/presign", map[string]any{"key": "upload.bin", "method": "PUT"}, &res)
	req, _ := http.NewRequest("PUT", res.URL, strings.NewReader("data"))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT presigned = %d", resp.StatusCode)
	}

	for _, body := range []map[string]any{
		{"key": "missing", "method": "GET"},
		{"key": "x", "method": "DELETE"},
		{"key": "x", "method": "PUT", "expiresIn": 999999999},
		{"method": "PUT"},
	} {
		if code := e.call(t, "POST", "/_api/buckets/laravel/presign", body, nil); code < 400 {
			t.Errorf("%v accepted", body)
		}
	}
}

func TestSelftest(t *testing.T) {
	e := newEnv(t, true)
	var steps []stepJSON
	if code := e.call(t, "POST", "/_api/selftest", map[string]string{"bucket": "laravel"}, &steps); code != 200 {
		t.Fatal(code)
	}
	if len(steps) != 4 {
		t.Fatalf("steps = %+v", steps)
	}
	for _, s := range steps {
		if !s.OK {
			t.Errorf("step %s failed: %+v", s.Name, s)
		}
	}
	b, _ := e.store.GetBucket(t.Context(), "laravel")
	if b.ObjectCount != 0 {
		t.Error("selftest left its object behind")
	}
	if len(e.events.Recent()) != 4 {
		t.Errorf("selftest should appear in the request log: %d events", len(e.events.Recent()))
	}

	// Wrong credentials show up as a failed first step.
	e.api.cfg.SecretKey = "wrong"
	e.call(t, "POST", "/_api/selftest", map[string]string{"bucket": "laravel"}, &steps)
	if len(steps) != 1 || steps[0].OK || !strings.Contains(steps[0].Error, "403") {
		t.Fatalf("bad creds = %+v", steps)
	}
	e.api.cfg.SecretKey = "localsecret"
	e.call(t, "POST", "/_api/selftest", map[string]string{"bucket": "nope"}, &steps)
	if steps[0].OK || steps[0].Status != 404 {
		t.Fatalf("missing bucket = %+v", steps)
	}
}

func TestRequiresJSONForMutations(t *testing.T) {
	e := newEnv(t, false)
	// A cross-site form post would arrive as form-encoded or text/plain.
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/_api/buckets", strings.NewReader(`{"name":"evil"}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: %d", ct, resp.StatusCode)
		}
	}
	if code := e.call(t, "POST", "/_api/buckets", `{"name":"x"`, nil); code != 415 {
		t.Fatalf("text body = %d", code)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/_api/buckets", strings.NewReader(`{bad json`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("malformed JSON = %d", resp.StatusCode)
	}
}

func TestUnknownRoute(t *testing.T) {
	e := newEnv(t, false)
	var body errorJSON
	if code := e.call(t, "GET", "/_api/nope", nil, &body); code != 404 || body.Error == "" {
		t.Fatalf("unknown = %d %+v", code, body)
	}
}

func TestEventStream(t *testing.T) {
	e := newEnv(t, false)
	e.events.Publish(events.Event{Op: "Earlier"})

	req, _ := http.NewRequestWithContext(t.Context(), "GET", e.srv.URL+"/_api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header.Get("Content-Type"))
	}

	lines := bufio.NewScanner(resp.Body)
	next := func() events.Event {
		t.Helper()
		for lines.Scan() {
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				var ev events.Event
				json.Unmarshal([]byte(data), &ev)
				return ev
			}
		}
		t.Fatal("stream ended")
		return events.Event{}
	}
	if ev := next(); ev.Op != "Earlier" {
		t.Fatalf("backlog = %+v", ev)
	}
	// A real S3 request shows up live.
	go http.Get(e.srv.URL + "/laravel/missing.txt")
	if ev := next(); ev.Op != "GetObject" || ev.Status != 404 || ev.Key != "missing.txt" {
		t.Fatalf("live = %+v", ev)
	}
}

func TestBaseName(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "a/b.txt": "b.txt", "a/b/": "b", "x/": "x"} {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q", in, got)
		}
	}
}
