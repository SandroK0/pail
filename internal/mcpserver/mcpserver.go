// Package mcpserver exposes read-only tools over the Model Context Protocol
// (streamable HTTP at /_mcp) so AI assistants can inspect buckets, objects
// and the live request log while helping debug an app's storage.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SandroK0/pail/internal/events"
	"github.com/SandroK0/pail/internal/sigv4"
	"github.com/SandroK0/pail/internal/store"
)

type Config struct {
	PublicURL  string
	Region     string
	AccessKey  string
	SecretKey  string
	VerifyAuth bool
	Buckets    []string // created at startup; the first is the suggested AWS_BUCKET
	Version    string
}

const instructions = `pail is the local S3-compatible object store this app uses for file storage in development: usually Laravel's "s3" filesystem disk (the Storage facade, via Flysystem and aws-sdk-php), but any S3 SDK works.

Use these tools whenever the user is debugging file storage: failed or missing uploads, Storage::put/get/exists/delete/copy/move/url/temporaryUrl errors, "file not found", broken image or download links, 403/404 from storage URLs, wrong public/private visibility, or a wrong bucket or credentials in .env. Prefer them over asking the user to check pail's web UI.

Start with recent_requests (errorsOnly: true): every failed call includes its S3 error code and a hint with the likely cause and fix. App error messages and logs contain "(pail request <ID>)"; pass that ID as requestId to get the exact request. Then inspect stored data with list_objects, search_objects, get_object and read_object, or check configuration with server_info. All tools are read-only.`

// New returns the MCP endpoint, guarded against cross-origin browser requests.
func New(s *store.Store, broker *events.Broker, cfg Config) http.Handler {
	t := &tools{store: s, events: broker, cfg: cfg, now: time.Now}
	server := mcp.NewServer(&mcp.Implementation{Name: "pail", Title: "pail object store", Version: cfg.Version},
		&mcp.ServerOptions{Instructions: instructions})
	t.register(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	// Rejects cross-site browser requests, so web pages cannot query it.
	return http.NewCrossOriginProtection().Handler(handler)
}

type tools struct {
	store  *store.Store
	events *events.Broker
	cfg    Config
	now    func() time.Time
}

func readOnly(title string) *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
}

func (t *tools) register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "server_info",
		Description: "How the app should connect to its local S3 storage (pail): endpoint, region, credentials, auth mode, and the exact Laravel .env block (FILESYSTEM_DISK, AWS_ACCESS_KEY_ID, AWS_BUCKET, AWS_ENDPOINT, AWS_URL, …). Use it to check an app's storage configuration.",
		Annotations: readOnly("Server info"),
	}, t.serverInfo)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_buckets",
		Description: "List the storage buckets (the values AWS_BUCKET can point to) with their file counts and total sizes.",
		Annotations: readOnly("List buckets"),
	}, t.listBuckets)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_objects",
		Description: "Browse stored files: one folder level of a bucket (sub-folders and files) or every file under a prefix when recursive is true, like Storage::files()/allFiles()/directories(). Shows size, content type, last modified and public/private visibility.",
		Annotations: readOnly("List objects"),
	}, t.listObjects)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_objects",
		Description: "Find stored files whose path contains some text (case-insensitive), e.g. to check whether an upload landed or to locate a file when the exact path is unknown.",
		Annotations: readOnly("Search objects"),
	}, t.searchObjects)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_object",
		Description: "Details of one stored file: size, content type, ETag, public/private visibility, custom metadata, stored headers (Cache-Control, Content-Disposition) and its URL, like Storage::size()/mimeType()/getVisibility()/url().",
		Annotations: readOnly("Get object"),
	}, t.getObject)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_object",
		Description: "Read the contents of a stored text file (JSON, CSV, logs, HTML, …), like Storage::get(); up to 256 KB. Binary files such as images and PDFs are refused with their type and size.",
		Annotations: readOnly("Read object"),
	}, t.readObject)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "recent_requests",
		Description: "Debug failing file storage: the live log of every S3 call the app made (Laravel Storage::put/get/exists/delete/copy/move, uploads, downloads, temporaryUrl links), newest first. Failed requests include the S3 error code (NoSuchKey, NoSuchBucket, SignatureDoesNotMatch, AccessDenied, …) and a hint with the likely cause and fix. Filter by errorsOnly, operation, bucket, key fragment, time window, or requestId (shown as \"(pail request <ID>)\" in app error messages).",
		Annotations: readOnly("Recent requests"),
	}, t.recentRequests)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "request_stats",
		Description: "Overview of the app's storage traffic: request and error counts, errors and average latency per S3 operation, the most common error codes with their likely cause, and which clients are calling. Useful for spotting request loops (e.g. many HeadObject/GetObjectAcl calls from exists()/getVisibility() in a list) or slow operations.",
		Annotations: readOnly("Request stats"),
	}, t.requestStats)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "presign_url",
		Description: "Create a temporary download (GET) or upload (PUT) URL for a stored file, like Storage::temporaryUrl()/temporaryUploadUrl(), e.g. to open or test a private file.",
		Annotations: readOnly("Presign URL"),
	}, t.presignURL)
}

// toolError turns store errors into messages a model can act on.
func toolError(err error, bucket, key string) error {
	switch {
	case errors.Is(err, store.ErrNoSuchBucket):
		return fmt.Errorf("bucket %q does not exist; call list_buckets to see the available ones", bucket)
	case errors.Is(err, store.ErrNoSuchKey):
		return fmt.Errorf("object %q not found in bucket %q; try search_objects", key, bucket)
	}
	return err
}

func (t *tools) objectURL(bucket, key string) string {
	return t.cfg.PublicURL + "/" + bucket + "/" + sigv4.URIEncode(key, false)
}

// --- server & buckets -----------------------------------------------------------

type ServerInfo struct {
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	AccessKey     string `json:"accessKey"`
	SecretKey     string `json:"secretKey"`
	AuthMode      string `json:"authMode" jsonschema:"off accepts any credentials; verify checks SigV4 signatures"`
	DefaultBucket string `json:"defaultBucket"`
	LaravelEnv    string `json:"laravelEnv"`
	UI            string `json:"ui"`
}

func (t *tools) serverInfo(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ServerInfo, error) {
	bucket := "laravel"
	if len(t.cfg.Buckets) > 0 {
		bucket = t.cfg.Buckets[0]
	}
	mode := "off"
	if t.cfg.VerifyAuth {
		mode = "verify"
	}
	env := strings.Join([]string{
		"FILESYSTEM_DISK=s3",
		"AWS_ACCESS_KEY_ID=" + t.cfg.AccessKey,
		"AWS_SECRET_ACCESS_KEY=" + t.cfg.SecretKey,
		"AWS_DEFAULT_REGION=" + t.cfg.Region,
		"AWS_BUCKET=" + bucket,
		"AWS_ENDPOINT=" + t.cfg.PublicURL,
		"AWS_USE_PATH_STYLE_ENDPOINT=true",
		"AWS_URL=" + t.cfg.PublicURL + "/" + bucket,
	}, "\n")
	return nil, ServerInfo{t.cfg.PublicURL, t.cfg.Region, t.cfg.AccessKey, t.cfg.SecretKey, mode, bucket, env, t.cfg.PublicURL + "/_ui/"}, nil
}

type Bucket struct {
	Name        string    `json:"name"`
	ObjectCount int64     `json:"objectCount"`
	SizeBytes   int64     `json:"sizeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
}

type BucketList struct {
	Buckets []Bucket `json:"buckets"`
}

func (t *tools) listBuckets(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, BucketList, error) {
	buckets, err := t.store.ListBuckets(ctx)
	if err != nil {
		return nil, BucketList{}, err
	}
	out := BucketList{Buckets: []Bucket{}}
	for _, b := range buckets {
		out.Buckets = append(out.Buckets, Bucket{b.Name, b.ObjectCount, b.Size, b.CreatedAt})
	}
	return nil, out, nil
}

// --- objects --------------------------------------------------------------------

type Object struct {
	Key          string    `json:"key"`
	SizeBytes    int64     `json:"sizeBytes"`
	ContentType  string    `json:"contentType"`
	LastModified time.Time `json:"lastModified"`
	Visibility   string    `json:"visibility" jsonschema:"public or private"`
}

func visibility(o *store.Object) string {
	if o.Public() {
		return "public"
	}
	return "private"
}

func summary(o *store.Object) Object {
	return Object{o.Key, o.Size, o.ContentType, o.LastModified, visibility(o)}
}

type ListObjectsInput struct {
	Bucket    string `json:"bucket" jsonschema:"bucket name"`
	Prefix    string `json:"prefix,omitempty" jsonschema:"folder or key prefix, e.g. avatars/ (default: bucket root)"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"list every object under the prefix instead of one folder level"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum entries to return, 1-1000 (default 200)"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous truncated call"`
}

type ObjectListing struct {
	Bucket     string   `json:"bucket"`
	Prefix     string   `json:"prefix"`
	Folders    []string `json:"folders"`
	Objects    []Object `json:"objects"`
	Truncated  bool     `json:"truncated"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

func clamp(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}

func (t *tools) listObjects(ctx context.Context, _ *mcp.CallToolRequest, in ListObjectsInput) (*mcp.CallToolResult, ObjectListing, error) {
	opts := store.ListOptions{Prefix: in.Prefix, StartAfter: in.Cursor, MaxKeys: clamp(in.Limit, 200, 1000)}
	if !in.Recursive {
		opts.Delimiter = "/"
	}
	res, err := t.store.ListObjects(ctx, in.Bucket, opts)
	if err != nil {
		return nil, ObjectListing{}, toolError(err, in.Bucket, "")
	}
	out := ObjectListing{Bucket: in.Bucket, Prefix: in.Prefix, Folders: res.CommonPrefixes, Objects: []Object{}, Truncated: res.IsTruncated}
	for _, o := range res.Objects {
		out.Objects = append(out.Objects, summary(o))
	}
	if res.IsTruncated {
		out.NextCursor = res.NextMarker
	}
	return nil, out, nil
}

type SearchInput struct {
	Bucket string `json:"bucket"`
	Query  string `json:"query" jsonschema:"text the key must contain"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum results, 1-200 (default 50)"`
}

type SearchResult struct {
	Objects []Object `json:"objects"`
}

func (t *tools) searchObjects(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchResult, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, SearchResult{}, errors.New("query is required")
	}
	objs, err := t.store.SearchObjects(ctx, in.Bucket, in.Query, clamp(in.Limit, 50, 200))
	if err != nil {
		return nil, SearchResult{}, toolError(err, in.Bucket, "")
	}
	out := SearchResult{Objects: []Object{}}
	for _, o := range objs {
		out.Objects = append(out.Objects, summary(o))
	}
	return nil, out, nil
}

type ObjectInput struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key" jsonschema:"full object key, e.g. avatars/me.png"`
}

type ObjectDetail struct {
	Object
	ETag     string            `json:"etag"`
	Metadata map[string]string `json:"metadata" jsonschema:"user metadata (x-amz-meta-*)"`
	Headers  map[string]string `json:"headers" jsonschema:"stored response headers such as Cache-Control"`
	URL      string            `json:"url" jsonschema:"public URL; only works anonymously for public objects when AUTH=verify"`
}

func (t *tools) getObject(ctx context.Context, _ *mcp.CallToolRequest, in ObjectInput) (*mcp.CallToolResult, ObjectDetail, error) {
	o, err := t.store.HeadObject(ctx, in.Bucket, in.Key)
	if err != nil {
		return nil, ObjectDetail{}, toolError(err, in.Bucket, in.Key)
	}
	return nil, ObjectDetail{summary(o), o.ETag, o.Metadata, o.Headers, t.objectURL(in.Bucket, in.Key)}, nil
}

type ReadInput struct {
	Bucket   string `json:"bucket"`
	Key      string `json:"key"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"bytes to read, up to 262144 (default 16384)"`
}

type ReadResult struct {
	Key         string `json:"key"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated"`
}

func (t *tools) readObject(ctx context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, ReadResult, error) {
	o, f, err := t.store.GetObject(ctx, in.Bucket, in.Key)
	if err != nil {
		return nil, ReadResult{}, toolError(err, in.Bucket, in.Key)
	}
	defer f.Close()
	limit := clamp(in.MaxBytes, 16<<10, 256<<10)
	buf, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, ReadResult{}, err
	}
	// A cut can split a multi-byte character; drop the partial rune before
	// deciding whether the content is text.
	text := buf
	if int64(len(buf)) < o.Size {
		for i := 0; i < utf8.UTFMax && len(text) > 0 && !utf8.Valid(text); i++ {
			text = text[:len(text)-1]
		}
	}
	if !utf8.Valid(text) || slices.Contains(text, 0) {
		return nil, ReadResult{}, fmt.Errorf("%s is binary (%s, %d bytes); use get_object for its metadata or presign_url to download it", in.Key, o.ContentType, o.Size)
	}
	return nil, ReadResult{o.Key, o.ContentType, o.Size, string(text), int64(len(buf)) < o.Size}, nil
}

// --- request log ------------------------------------------------------------------

type RequestsInput struct {
	Limit        int    `json:"limit,omitempty" jsonschema:"maximum requests, 1-500 (default 50)"`
	ErrorsOnly   bool   `json:"errorsOnly,omitempty" jsonschema:"only requests with status >= 400"`
	Operation    string `json:"operation,omitempty" jsonschema:"S3 operation, e.g. PutObject, GetObject, ListObjectsV2"`
	Bucket       string `json:"bucket,omitempty"`
	KeyContains  string `json:"keyContains,omitempty" jsonschema:"case-insensitive fragment of the object key"`
	SinceSeconds int    `json:"sinceSeconds,omitempty" jsonschema:"only requests from the last N seconds"`
	RequestID    string `json:"requestId,omitempty" jsonschema:"a request ID, e.g. from '(pail request 0BE03810ACD7C0D1)' in an app's error message or log"`
}

type Request struct {
	RequestID  string    `json:"requestId"`
	Time       time.Time `json:"time"`
	Operation  string    `json:"operation"`
	Method     string    `json:"method"`
	Bucket     string    `json:"bucket,omitempty"`
	Key        string    `json:"key,omitempty"`
	Status     int       `json:"status"`
	ErrorCode  string    `json:"errorCode,omitempty"`
	Error      string    `json:"error,omitempty"`
	Hint       string    `json:"hint,omitempty" jsonschema:"likely cause of the failure and how to fix it"`
	BytesIn    int64     `json:"bytesIn"`
	BytesOut   int64     `json:"bytesOut"`
	DurationMs float64   `json:"durationMs"`
	Presigned  bool      `json:"presigned,omitempty"`
	Client     string    `json:"client,omitempty"`
}

type RequestList struct {
	Requests []Request `json:"requests"`
	Matched  int       `json:"matched" jsonschema:"requests matching the filters before limit was applied"`
	Logged   int       `json:"logged" jsonschema:"requests kept in the log (the most recent 1000)"`
}

func (t *tools) filtered(in RequestsInput) (matched []events.Event, logged int) {
	all := t.events.Recent()
	var since time.Time
	if in.SinceSeconds > 0 {
		since = t.now().Add(-time.Duration(in.SinceSeconds) * time.Second)
	}
	needle := strings.ToLower(in.KeyContains)
	for i := len(all) - 1; i >= 0; i-- { // newest first
		e := all[i]
		switch {
		case in.ErrorsOnly && e.Status < 400,
			in.Operation != "" && !strings.EqualFold(e.Op, in.Operation),
			in.Bucket != "" && e.Bucket != in.Bucket,
			needle != "" && !strings.Contains(strings.ToLower(e.Key), needle),
			!since.IsZero() && e.Time.Before(since),
			in.RequestID != "" && !strings.EqualFold(e.RequestID, strings.TrimSpace(in.RequestID)):
			continue
		}
		matched = append(matched, e)
	}
	return matched, len(all)
}

// bucketNames lists existing buckets for hints; errors just mean no list.
func (t *tools) bucketNames(ctx context.Context) []string {
	buckets, _ := t.store.ListBuckets(ctx)
	names := make([]string, len(buckets))
	for i, b := range buckets {
		names[i] = b.Name
	}
	return names
}

func (t *tools) recentRequests(ctx context.Context, _ *mcp.CallToolRequest, in RequestsInput) (*mcp.CallToolResult, RequestList, error) {
	matched, logged := t.filtered(in)
	out := RequestList{Requests: []Request{}, Matched: len(matched), Logged: logged}
	buckets := t.bucketNames(ctx)
	for _, e := range matched[:min(len(matched), clamp(in.Limit, 50, 500))] {
		out.Requests = append(out.Requests, Request{
			e.RequestID, e.Time, e.Op, e.Method, e.Bucket, e.Key, e.Status, e.ErrorCode, e.ErrorMessage,
			hint(e, t.cfg, buckets), e.BytesIn, e.BytesOut, e.DurationMs, e.Presigned, e.UserAgent,
		})
	}
	return nil, out, nil
}

type StatsInput struct {
	SinceSeconds int `json:"sinceSeconds,omitempty" jsonschema:"only count requests from the last N seconds"`
}

type OperationStats struct {
	Operation     string  `json:"operation"`
	Count         int     `json:"count"`
	Errors        int     `json:"errors"`
	AvgDurationMs float64 `json:"avgDurationMs"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ErrorCount struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
	Hint  string `json:"hint,omitempty" jsonschema:"likely cause, from the most recent request with this code"`
}

type Stats struct {
	Total        int              `json:"total"`
	Errors       int              `json:"errors"`
	BytesIn      int64            `json:"bytesIn"`
	BytesOut     int64            `json:"bytesOut"`
	Oldest       *time.Time       `json:"oldest,omitempty"`
	Operations   []OperationStats `json:"operations"`
	ErrorCodes   []ErrorCount     `json:"errorCodes"`
	Clients      []Count          `json:"clients"`
	StatusCounts []Count          `json:"statusCounts"`
}

// byCount sorts a tally most frequent first, then by name.
func byCount(m map[string]int) []Count {
	out := []Count{}
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	slices.SortFunc(out, func(a, b Count) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func (t *tools) requestStats(ctx context.Context, _ *mcp.CallToolRequest, in StatsInput) (*mcp.CallToolResult, Stats, error) {
	matched, _ := t.filtered(RequestsInput{SinceSeconds: in.SinceSeconds})
	out := Stats{Operations: []OperationStats{}}
	ops := map[string]*OperationStats{}
	codes, clients, statuses := map[string]int{}, map[string]int{}, map[string]int{}
	latest := map[string]events.Event{} // newest request per error code
	for _, e := range matched {
		out.Total++
		out.BytesIn += e.BytesIn
		out.BytesOut += e.BytesOut
		s := ops[e.Op]
		if s == nil {
			s = &OperationStats{Operation: e.Op}
			ops[e.Op] = s
		}
		s.Count++
		s.AvgDurationMs += e.DurationMs
		if e.Status >= 400 {
			out.Errors++
			s.Errors++
			if e.ErrorCode != "" {
				codes[e.ErrorCode]++
				if _, seen := latest[e.ErrorCode]; !seen {
					latest[e.ErrorCode] = e
				}
			}
		}
		if e.UserAgent != "" {
			clients[strings.Fields(e.UserAgent)[0]]++
		}
		statuses[fmt.Sprint(e.Status)]++
	}
	if n := len(matched); n > 0 {
		oldest := matched[n-1].Time
		out.Oldest = &oldest
	}
	for _, s := range ops {
		s.AvgDurationMs = float64(int(s.AvgDurationMs/float64(s.Count)*1000)) / 1000
		out.Operations = append(out.Operations, *s)
	}
	slices.SortFunc(out.Operations, func(a, b OperationStats) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Operation, b.Operation)
	})
	buckets := t.bucketNames(ctx)
	out.ErrorCodes = []ErrorCount{}
	for _, c := range byCount(codes) {
		out.ErrorCodes = append(out.ErrorCodes, ErrorCount{c.Name, c.Count, hint(latest[c.Name], t.cfg, buckets)})
	}
	out.Clients, out.StatusCounts = byCount(clients), byCount(statuses)
	return nil, out, nil
}

// --- presigning ---------------------------------------------------------------------

type PresignInput struct {
	Bucket           string `json:"bucket"`
	Key              string `json:"key"`
	Method           string `json:"method,omitempty" jsonschema:"GET to download (default) or PUT to upload"`
	ExpiresInSeconds int    `json:"expiresInSeconds,omitempty" jsonschema:"1-604800 (default 900)"`
}

type PresignResult struct {
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (t *tools) presignURL(ctx context.Context, _ *mcp.CallToolRequest, in PresignInput) (*mcp.CallToolResult, PresignResult, error) {
	method := strings.ToUpper(in.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPut {
		return nil, PresignResult{}, errors.New("method must be GET or PUT")
	}
	if method == http.MethodGet {
		if _, err := t.store.HeadObject(ctx, in.Bucket, in.Key); err != nil {
			return nil, PresignResult{}, toolError(err, in.Bucket, in.Key)
		}
	} else if _, err := t.store.GetBucket(ctx, in.Bucket); err != nil {
		return nil, PresignResult{}, toolError(err, in.Bucket, "")
	}
	expires := time.Duration(in.ExpiresInSeconds) * time.Second
	if expires == 0 {
		expires = 15 * time.Minute
	}
	now := t.now()
	u, err := sigv4.Presign(method, t.objectURL(in.Bucket, in.Key), t.cfg.AccessKey, t.cfg.SecretKey, t.cfg.Region, now, expires)
	if err != nil {
		return nil, PresignResult{}, err
	}
	return nil, PresignResult{u, method, now.Add(expires).UTC()}, nil
}
