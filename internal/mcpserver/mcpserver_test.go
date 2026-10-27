package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SandroK0/pail/internal/events"
	"github.com/SandroK0/pail/internal/sigv4"
	"github.com/SandroK0/pail/internal/store"
)

type env struct {
	store   *store.Store
	events  *events.Broker
	srv     *httptest.Server
	session *mcp.ClientSession
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	broker := events.NewBroker(100)
	srv := httptest.NewServer(New(st, broker, Config{
		PublicURL: "http://localhost:8080", Region: "us-east-1", AccessKey: "local", SecretKey: "localsecret",
		Buckets: []string{"laravel"}, Version: "test",
	}))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &env{st, broker, srv, session}
}

func (e *env) put(t *testing.T, bucket, key, body string, opts store.PutOptions) {
	t.Helper()
	if err := e.store.CreateBucket(context.Background(), bucket); err != nil && err != store.ErrBucketExists {
		t.Fatal(err)
	}
	if _, err := e.store.PutObject(context.Background(), bucket, key, strings.NewReader(body), opts); err != nil {
		t.Fatal(err)
	}
}

// call invokes a tool and decodes its structured result into out. It returns
// the error text when the tool reports an error.
func (e *env) call(t *testing.T, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := e.session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		var msgs []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msgs = append(msgs, tc.Text)
			}
		}
		return strings.Join(msgs, " ")
	}
	if out != nil {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: decode %s: %v", name, b, err)
		}
	}
	return ""
}

func TestToolsAreListedAndReadOnly(t *testing.T) {
	e := newEnv(t)
	res, err := e.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		if tool.Description == "" || tool.InputSchema == nil {
			t.Errorf("%s lacks a description or schema", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"get_object", "list_buckets", "list_objects", "presign_url", "read_object", "recent_requests", "request_stats", "search_objects", "server_info"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v", names)
	}
	instructions := e.session.InitializeResult().Instructions
	for _, want := range []string{"recent_requests", "Laravel", "Storage::put", "(pail request <ID>)"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
	// Agents find tools by matching the user's words against descriptions.
	for _, tool := range res.Tools {
		if tool.Name == "recent_requests" {
			for _, want := range []string{"Storage::put", "upload", "NoSuchKey", "requestId", "hint"} {
				if !strings.Contains(tool.Description, want) {
					t.Errorf("recent_requests description lacks %q", want)
				}
			}
		}
	}
}

func TestServerInfo(t *testing.T) {
	e := newEnv(t)
	var info ServerInfo
	e.call(t, "server_info", nil, &info)
	if info.Endpoint != "http://localhost:8080" || info.AuthMode != "off" || info.DefaultBucket != "laravel" ||
		!strings.Contains(info.LaravelEnv, "AWS_USE_PATH_STYLE_ENDPOINT=true") || info.UI != "http://localhost:8080/_ui/" {
		t.Fatalf("info = %+v", info)
	}
}

func TestBucketsAndListing(t *testing.T) {
	e := newEnv(t)
	for _, k := range []string{"readme.md", "avatars/me.png", "avatars/you.png", "avatars/2026/old.png"} {
		e.put(t, "laravel", k, "12345", store.PutOptions{})
	}
	e.put(t, "media", "a.txt", "x", store.PutOptions{ACL: "public-read"})

	var buckets BucketList
	e.call(t, "list_buckets", nil, &buckets)
	if len(buckets.Buckets) != 2 || buckets.Buckets[0].Name != "laravel" || buckets.Buckets[0].ObjectCount != 4 || buckets.Buckets[0].SizeBytes != 20 {
		t.Fatalf("buckets = %+v", buckets)
	}

	var root ObjectListing
	e.call(t, "list_objects", map[string]any{"bucket": "laravel"}, &root)
	if !slices.Equal(root.Folders, []string{"avatars/"}) || len(root.Objects) != 1 || root.Objects[0].Key != "readme.md" {
		t.Fatalf("root = %+v", root)
	}

	var deep ObjectListing
	e.call(t, "list_objects", map[string]any{"bucket": "laravel", "prefix": "avatars/", "recursive": true}, &deep)
	if len(deep.Folders) != 0 || len(deep.Objects) != 3 {
		t.Fatalf("recursive = %+v", deep)
	}

	var page1, page2 ObjectListing
	e.call(t, "list_objects", map[string]any{"bucket": "laravel", "recursive": true, "limit": 3}, &page1)
	e.call(t, "list_objects", map[string]any{"bucket": "laravel", "recursive": true, "limit": 3, "cursor": page1.NextCursor}, &page2)
	if !page1.Truncated || page1.NextCursor == "" || len(page2.Objects) != 1 || page2.Truncated {
		t.Fatalf("pages = %+v / %+v", page1, page2)
	}

	var media ObjectListing
	e.call(t, "list_objects", map[string]any{"bucket": "media"}, &media)
	if media.Objects[0].Visibility != "public" {
		t.Fatalf("visibility = %s", media.Objects[0].Visibility)
	}

	if msg := e.call(t, "list_objects", map[string]any{"bucket": "nope"}, nil); !strings.Contains(msg, "list_buckets") {
		t.Fatalf("missing bucket error = %q", msg)
	}
}

func TestSearchAndGetObject(t *testing.T) {
	e := newEnv(t)
	e.put(t, "laravel", "invoices/2026/INV-042.pdf", "pdf", store.PutOptions{
		ContentType: "application/pdf", Metadata: map[string]string{"customer": "acme"}, Headers: map[string]string{"Cache-Control": "no-store"},
	})
	e.put(t, "laravel", "readme.md", "x", store.PutOptions{})

	var found SearchResult
	e.call(t, "search_objects", map[string]any{"bucket": "laravel", "query": "inv-04"}, &found)
	if len(found.Objects) != 1 || found.Objects[0].ContentType != "application/pdf" {
		t.Fatalf("search = %+v", found)
	}
	if msg := e.call(t, "search_objects", map[string]any{"bucket": "laravel", "query": " "}, nil); msg == "" {
		t.Fatal("empty query accepted")
	}

	var obj ObjectDetail
	e.call(t, "get_object", map[string]any{"bucket": "laravel", "key": "invoices/2026/INV-042.pdf"}, &obj)
	if obj.SizeBytes != 3 || obj.Metadata["customer"] != "acme" || obj.Headers["Cache-Control"] != "no-store" ||
		obj.URL != "http://localhost:8080/laravel/invoices/2026/INV-042.pdf" || obj.ETag == "" {
		t.Fatalf("object = %+v", obj)
	}
	if msg := e.call(t, "get_object", map[string]any{"bucket": "laravel", "key": "nope.txt"}, nil); !strings.Contains(msg, "search_objects") {
		t.Fatalf("missing key error = %q", msg)
	}
}

func TestReadObject(t *testing.T) {
	e := newEnv(t)
	e.put(t, "laravel", "data.json", `{"name":"Ada"}`, store.PutOptions{ContentType: "application/json"})
	e.put(t, "laravel", "big.log", strings.Repeat("line\n", 10000), store.PutOptions{})
	e.put(t, "laravel", "photo.png", "\x89PNG\r\n\x1a\n\x00\x00binary", store.PutOptions{ContentType: "image/png"})
	e.put(t, "laravel", "utf8.txt", "ab€€€", store.PutOptions{})

	var r ReadResult
	e.call(t, "read_object", map[string]any{"bucket": "laravel", "key": "data.json"}, &r)
	if r.Content != `{"name":"Ada"}` || r.Truncated || r.ContentType != "application/json" {
		t.Fatalf("json = %+v", r)
	}

	e.call(t, "read_object", map[string]any{"bucket": "laravel", "key": "big.log", "maxBytes": 12}, &r)
	if r.Content != "line\nline\nli" || !r.Truncated || r.SizeBytes != 50000 {
		t.Fatalf("truncated = %+v", r)
	}

	// Cutting through a 3-byte "€" must not make the text look binary.
	e.call(t, "read_object", map[string]any{"bucket": "laravel", "key": "utf8.txt", "maxBytes": 4}, &r)
	if r.Content != "ab" || !r.Truncated {
		t.Fatalf("utf8 cut = %+v", r)
	}

	if msg := e.call(t, "read_object", map[string]any{"bucket": "laravel", "key": "photo.png"}, nil); !strings.Contains(msg, "binary (image/png") {
		t.Fatalf("binary = %q", msg)
	}
}

func TestRecentRequestsAndStats(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	for _, ev := range []events.Event{
		{Time: now.Add(-time.Hour), Op: "PutObject", Method: "PUT", Bucket: "laravel", Key: "old.txt", Status: 200, BytesIn: 100, DurationMs: 2, UserAgent: "aws-sdk-php/3.402.1 GuzzleHttp/7"},
		{Time: now, Op: "PutObject", Method: "PUT", Bucket: "laravel", Key: "Avatars/me.png", Status: 200, BytesIn: 50, DurationMs: 4, UserAgent: "aws-sdk-php/3.402.1"},
		{RequestID: "0BE03810ACD7C0D1", Time: now, Op: "GetObject", Method: "GET", Bucket: "laravel", Key: "missing.png", Status: 404, ErrorCode: "NoSuchKey", ErrorMessage: "The specified key does not exist.", BytesOut: 200, DurationMs: 1, UserAgent: "aws-sdk-php/3.402.1"},
		{RequestID: "77AA", Time: now, Op: "PutObject", Method: "PUT", Bucket: "typo", Key: "x.txt", Status: 404, ErrorCode: "NoSuchBucket", DurationMs: 1, UserAgent: "curl/8.7.1"},
	} {
		e.events.Publish(ev)
	}

	var all RequestList
	e.call(t, "recent_requests", nil, &all)
	if all.Matched != 4 || all.Logged != 4 || all.Requests[0].Key != "x.txt" || all.Requests[3].Key != "old.txt" {
		t.Fatalf("all = %+v", all)
	}

	cases := []struct {
		args map[string]any
		keys []string
	}{
		{map[string]any{"errorsOnly": true}, []string{"x.txt", "missing.png"}},
		{map[string]any{"operation": "putobject"}, []string{"x.txt", "Avatars/me.png", "old.txt"}},
		{map[string]any{"bucket": "laravel", "errorsOnly": true}, []string{"missing.png"}},
		{map[string]any{"keyContains": "AVATARS"}, []string{"Avatars/me.png"}},
		{map[string]any{"sinceSeconds": 60}, []string{"x.txt", "missing.png", "Avatars/me.png"}},
		{map[string]any{"limit": 1}, []string{"x.txt"}},
		// IDs as copied from "(pail request 0BE03810ACD7C0D1)" in a Laravel error.
		{map[string]any{"requestId": "0be03810acd7c0d1 "}, []string{"missing.png"}},
	}
	for _, c := range cases {
		var got RequestList
		e.call(t, "recent_requests", c.args, &got)
		var keys []string
		for _, r := range got.Requests {
			keys = append(keys, r.Key)
		}
		if !slices.Equal(keys, c.keys) {
			t.Errorf("%v: keys = %v, want %v", c.args, keys, c.keys)
		}
	}

	var missing RequestList
	e.call(t, "recent_requests", map[string]any{"errorsOnly": true, "bucket": "laravel"}, &missing)
	if r := missing.Requests[0]; r.ErrorCode != "NoSuchKey" || r.Error == "" || r.Client != "aws-sdk-php/3.402.1" ||
		r.RequestID != "0BE03810ACD7C0D1" || !strings.Contains(r.Hint, "search_objects") {
		t.Fatalf("error detail = %+v", r)
	}
	var typo RequestList
	e.call(t, "recent_requests", map[string]any{"bucket": "typo"}, &typo)
	// The hint names the buckets that do exist (none were created here).
	if h := typo.Requests[0].Hint; !strings.Contains(h, `"typo"`) || !strings.Contains(h, "existing: none") {
		t.Fatalf("bucket hint = %q", h)
	}
	if all.Requests[3].Hint != "" {
		t.Fatalf("successful request has a hint: %q", all.Requests[3].Hint)
	}

	var stats Stats
	e.call(t, "request_stats", nil, &stats)
	if stats.Total != 4 || stats.Errors != 2 || stats.BytesIn != 150 || stats.BytesOut != 200 || stats.Oldest == nil {
		t.Fatalf("stats = %+v", stats)
	}
	if put := stats.Operations[0]; put.Operation != "PutObject" || put.Count != 3 || put.Errors != 1 || put.AvgDurationMs < 2.333 || put.AvgDurationMs > 2.334 {
		t.Fatalf("operations = %+v", stats.Operations)
	}
	if len(stats.ErrorCodes) != 2 || stats.ErrorCodes[0].Hint == "" || stats.Clients[0] != (Count{"aws-sdk-php/3.402.1", 3}) || stats.StatusCounts[0] != (Count{"200", 2}) {
		t.Fatalf("tallies = %+v %+v %+v", stats.ErrorCodes, stats.Clients, stats.StatusCounts)
	}

	var recent Stats
	e.call(t, "request_stats", map[string]any{"sinceSeconds": 60}, &recent)
	if recent.Total != 3 {
		t.Fatalf("windowed stats total = %d", recent.Total)
	}
}

func TestPresignURL(t *testing.T) {
	e := newEnv(t)
	e.put(t, "laravel", "docs/a b.pdf", "pdf", store.PutOptions{})

	var res PresignResult
	e.call(t, "presign_url", map[string]any{"bucket": "laravel", "key": "docs/a b.pdf", "expiresInSeconds": 60}, &res)
	if res.Method != "GET" || time.Until(res.ExpiresAt) > time.Minute {
		t.Fatalf("presign = %+v", res)
	}
	// The URL must verify exactly as the S3 API checks it.
	r := httptest.NewRequest("GET", res.URL, nil)
	auth, err := sigv4.Parse(r)
	if err != nil || r.URL.Path != "/laravel/docs/a b.pdf" {
		t.Fatalf("parse %s: %v", res.URL, err)
	}
	if err := sigv4.Verify(r, auth, "localsecret"); err != nil {
		t.Fatal(err)
	}

	e.call(t, "presign_url", map[string]any{"bucket": "laravel", "key": "new-upload.bin", "method": "put"}, &res)
	if res.Method != "PUT" || !strings.Contains(res.URL, "X-Amz-Expires=900") {
		t.Fatalf("put presign = %+v", res)
	}

	for _, args := range []map[string]any{
		{"bucket": "laravel", "key": "missing.pdf"},
		{"bucket": "nope", "key": "x", "method": "PUT"},
		{"bucket": "laravel", "key": "docs/a b.pdf", "method": "DELETE"},
		{"bucket": "laravel", "key": "docs/a b.pdf", "expiresInSeconds": 999999999},
	} {
		if msg := e.call(t, "presign_url", args, nil); msg == "" {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	e := newEnv(t)
	res, err := e.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_object", Arguments: map[string]any{"bucket": 42}})
	if err == nil && !res.IsError {
		t.Fatal("a wrongly typed argument was accepted")
	}
}

func TestRejectsCrossOriginBrowserRequests(t *testing.T) {
	e := newEnv(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req, _ := http.NewRequest("POST", e.srv.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "http://evil.test")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site request: %d", resp.StatusCode)
	}
}
