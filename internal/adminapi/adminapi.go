// Package adminapi is the JSON API behind the web UI, mounted at /_api.
package adminapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/SandroK0/pail/internal/events"
	"github.com/SandroK0/pail/internal/s3api"
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
	MCP        bool     // whether /_mcp is served
}

type API struct {
	store  *store.Store
	events *events.Broker
	cfg    Config
	mux    *http.ServeMux
	client *http.Client
	now    func() time.Time
}

func New(s *store.Store, broker *events.Broker, cfg Config) *API {
	a := &API{store: s, events: broker, cfg: cfg, mux: http.NewServeMux(), client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	a.mux.HandleFunc("GET /_api/info", a.info)
	a.mux.HandleFunc("GET /_api/events", a.streamEvents)
	a.mux.HandleFunc("POST /_api/selftest", a.selftest)
	a.mux.HandleFunc("GET /_api/buckets", a.listBuckets)
	a.mux.HandleFunc("POST /_api/buckets", a.createBucket)
	a.mux.HandleFunc("DELETE /_api/buckets/{bucket}", a.deleteBucket)
	a.mux.HandleFunc("GET /_api/buckets/{bucket}/objects", a.listObjects)
	a.mux.HandleFunc("GET /_api/buckets/{bucket}/search", a.search)
	a.mux.HandleFunc("GET /_api/buckets/{bucket}/object", a.getObject)
	a.mux.HandleFunc("PUT /_api/buckets/{bucket}/object", a.putObject)
	a.mux.HandleFunc("PATCH /_api/buckets/{bucket}/object", a.patchObject)
	a.mux.HandleFunc("GET /_api/buckets/{bucket}/raw", a.raw)
	a.mux.HandleFunc("POST /_api/buckets/{bucket}/delete", a.deleteObjects)
	a.mux.HandleFunc("POST /_api/buckets/{bucket}/folders", a.createFolder)
	a.mux.HandleFunc("POST /_api/buckets/{bucket}/move", a.move)
	a.mux.HandleFunc("POST /_api/buckets/{bucket}/presign", a.presign)
	a.mux.HandleFunc("/_api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return a
}

// ServeHTTP requires JSON on body-carrying POST/PATCH requests. That forces
// a CORS preflight (which is never granted) for cross-site requests, so
// other pages open in the browser cannot drive this API.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "requests must be application/json")
			return
		}
	}
	a.mux.ServeHTTP(w, r)
}

// --- JSON helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type errorJSON struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorJSON{msg})
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoSuchBucket):
		writeError(w, http.StatusNotFound, "Bucket not found")
	case errors.Is(err, store.ErrNoSuchKey):
		writeError(w, http.StatusNotFound, "Object not found")
	case errors.Is(err, store.ErrBucketExists):
		writeError(w, http.StatusConflict, "A bucket with that name already exists")
	case errors.Is(err, store.ErrBucketNotEmpty):
		writeError(w, http.StatusConflict, "Bucket is not empty")
	case errors.Is(err, store.ErrInvalidBucketName):
		writeError(w, http.StatusBadRequest, "Bucket names are 3–63 characters: lowercase letters, numbers, dots and hyphens")
	case errors.Is(err, store.ErrInvalidKey):
		writeError(w, http.StatusBadRequest, "Keys must be 1–1024 bytes of valid UTF-8")
	default:
		slog.Error("admin api", "err", err)
		writeError(w, http.StatusInternalServerError, "Internal error")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return false
	}
	return true
}

type bucketJSON struct {
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	ObjectCount int64     `json:"objectCount"`
	Size        int64     `json:"size"`
}

type objectJSON struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"`
	ContentType  string            `json:"contentType"`
	LastModified time.Time         `json:"lastModified"`
	ACL          string            `json:"acl"`
	URL          string            `json:"url"`
	Metadata     map[string]string `json:"metadata,omitzero"` // detail only
	Headers      map[string]string `json:"headers,omitzero"`  // detail only
}

type folderJSON struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

// baseName returns the last path segment, ignoring a trailing slash.
func baseName(key string) string {
	return path.Base("/" + strings.TrimSuffix(key, "/"))
}

func (a *API) objectURL(bucket, key string) string {
	return a.cfg.PublicURL + "/" + bucket + "/" + sigv4.URIEncode(key, false)
}

func (a *API) toJSON(o *store.Object, detail bool) objectJSON {
	j := objectJSON{
		Key: o.Key, Name: baseName(o.Key), Size: o.Size, ETag: o.ETag, ContentType: o.ContentType,
		LastModified: o.LastModified, ACL: o.ACL, URL: a.objectURL(o.Bucket, o.Key),
	}
	if detail {
		// Non-nil so empty maps still serialise as {}.
		j.Metadata, j.Headers = map[string]string{}, map[string]string{}
		maps.Copy(j.Metadata, o.Metadata)
		maps.Copy(j.Headers, o.Headers)
	}
	return j
}

func keyParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return "", false
	}
	return key, true
}

// --- info & buckets ----------------------------------------------------------

type infoJSON struct {
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	AccessKey     string `json:"accessKey"`
	SecretKey     string `json:"secretKey"`
	VerifyAuth    bool   `json:"verifyAuth"`
	DefaultBucket string `json:"defaultBucket"`
	MCP           bool   `json:"mcp"`
}

func (a *API) info(w http.ResponseWriter, r *http.Request) {
	bucket := ""
	if len(a.cfg.Buckets) > 0 {
		bucket = a.cfg.Buckets[0]
	}
	writeJSON(w, http.StatusOK, infoJSON{a.cfg.PublicURL, a.cfg.Region, a.cfg.AccessKey, a.cfg.SecretKey, a.cfg.VerifyAuth, bucket, a.cfg.MCP})
}

func (a *API) listBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := a.store.ListBuckets(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := []bucketJSON{}
	for _, b := range buckets {
		out = append(out, bucketJSON{b.Name, b.CreatedAt, b.ObjectCount, b.Size})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createBucket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.store.CreateBucket(r.Context(), body.Name); err != nil {
		writeStoreError(w, err)
		return
	}
	b, err := a.store.GetBucket(r.Context(), body.Name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, bucketJSON{b.Name, b.CreatedAt, b.ObjectCount, b.Size})
}

// deleteBucket removes a bucket; ?force=true empties it first.
func (a *API) deleteBucket(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if r.URL.Query().Get("force") == "true" {
		if _, err := a.store.DeletePrefix(r.Context(), bucket, ""); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := a.store.DeleteBucket(r.Context(), bucket); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- objects -----------------------------------------------------------------

type listingJSON struct {
	Bucket     string       `json:"bucket"`
	Prefix     string       `json:"prefix"`
	Folders    []folderJSON `json:"folders"`
	Objects    []objectJSON `json:"objects"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// listObjects lists one "folder" level: objects directly under prefix and
// the sub-prefixes below it.
func (a *API) listObjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bucket, prefix := r.PathValue("bucket"), q.Get("prefix")
	limit, _ := strconv.Atoi(q.Get("limit"))
	res, err := a.store.ListObjects(r.Context(), bucket, store.ListOptions{
		Prefix: prefix, Delimiter: "/", StartAfter: q.Get("cursor"), MaxKeys: limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := listingJSON{Bucket: bucket, Prefix: prefix, Folders: []folderJSON{}, Objects: []objectJSON{}}
	for _, p := range res.CommonPrefixes {
		out.Folders = append(out.Folders, folderJSON{p, baseName(p)})
	}
	for _, o := range res.Objects {
		if o.Key == prefix {
			continue // the folder's own marker object
		}
		out.Objects = append(out.Objects, a.toJSON(o, false))
	}
	if res.IsTruncated {
		out.NextCursor = res.NextMarker
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []objectJSON{}
	if q == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	objs, err := a.store.SearchObjects(r.Context(), r.PathValue("bucket"), q, 50)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, o := range objs {
		out = append(out, a.toJSON(o, false))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getObject(w http.ResponseWriter, r *http.Request) {
	key, ok := keyParam(w, r)
	if !ok {
		return
	}
	obj, err := a.store.HeadObject(r.Context(), r.PathValue("bucket"), key)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toJSON(obj, true))
}

// putObject uploads the raw request body to ?key=.
func (a *API) putObject(w http.ResponseWriter, r *http.Request) {
	key, ok := keyParam(w, r)
	if !ok {
		return
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(key))
	}
	obj, err := a.store.PutObject(r.Context(), r.PathValue("bucket"), key, r.Body, store.PutOptions{ContentType: ct})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a.toJSON(obj, true))
}

func (a *API) patchObject(w http.ResponseWriter, r *http.Request) {
	key, ok := keyParam(w, r)
	if !ok {
		return
	}
	var body struct {
		ACL string `json:"acl"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.ACL != "private" && body.ACL != "public-read" {
		writeError(w, http.StatusBadRequest, "acl must be private or public-read")
		return
	}
	bucket := r.PathValue("bucket")
	if err := a.store.SetACL(r.Context(), bucket, key, body.ACL); err != nil {
		writeStoreError(w, err)
		return
	}
	obj, err := a.store.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toJSON(obj, true))
}

// raw serves an object's body for previews and downloads.
func (a *API) raw(w http.ResponseWriter, r *http.Request) {
	key, ok := keyParam(w, r)
	if !ok {
		return
	}
	obj, f, err := a.store.GetObject(r.Context(), r.PathValue("bucket"), key)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", obj.ContentType)
	h.Set("ETag", `"`+obj.ETag+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	if s3api.IsActiveContent(obj.ContentType) {
		h.Set("Content-Security-Policy", "sandbox")
	}
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": baseName(key)}))
	}
	http.ServeContent(w, r, "", obj.LastModified, f)
}

func (a *API) deleteObjects(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys     []string `json:"keys"`
		Prefixes []string `json:"prefixes"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	bucket := r.PathValue("bucket")
	deleted := 0
	for _, k := range body.Keys {
		if err := a.store.DeleteObject(r.Context(), bucket, k); err != nil {
			writeStoreError(w, err)
			return
		}
		deleted++
	}
	for _, p := range body.Prefixes {
		if p == "" || !strings.HasSuffix(p, "/") {
			writeError(w, http.StatusBadRequest, "prefixes must end with /")
			return
		}
		n, err := a.store.DeletePrefix(r.Context(), bucket, p)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		deleted += n
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}

func normalizeFolder(p string) string {
	p = strings.TrimLeft(strings.TrimSpace(p), "/")
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// createFolder writes an empty "prefix/" marker, as Laravel's makeDirectory does.
func (a *API) createFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prefix string `json:"prefix"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	prefix := normalizeFolder(body.Prefix)
	if prefix == "" {
		writeError(w, http.StatusBadRequest, "Folder name is required")
		return
	}
	if _, err := a.store.PutObject(r.Context(), r.PathValue("bucket"), prefix, strings.NewReader(""), store.PutOptions{}); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, folderJSON{prefix, baseName(prefix)})
}

// move renames an object, or every object under a folder when from ends in /.
func (a *API) move(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	bucket := r.PathValue("bucket")
	if body.From == "" || body.To == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if !strings.HasSuffix(body.From, "/") {
		if err := a.store.RenameObject(r.Context(), bucket, body.From, body.To); err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"moved": 1})
		return
	}

	to := normalizeFolder(body.To)
	if to == body.From {
		writeJSON(w, http.StatusOK, map[string]int{"moved": 0})
		return
	}
	if strings.HasPrefix(to, body.From) {
		writeError(w, http.StatusBadRequest, "Cannot move a folder into itself")
		return
	}
	var keys []string
	marker := ""
	for {
		res, err := a.store.ListObjects(r.Context(), bucket, store.ListOptions{Prefix: body.From, StartAfter: marker})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		for _, o := range res.Objects {
			keys = append(keys, o.Key)
		}
		if !res.IsTruncated {
			break
		}
		marker = res.NextMarker
	}
	if len(keys) == 0 {
		writeStoreError(w, store.ErrNoSuchKey)
		return
	}
	for _, k := range keys {
		if err := a.store.RenameObject(r.Context(), bucket, k, to+strings.TrimPrefix(k, body.From)); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"moved": len(keys)})
}

func (a *API) presign(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key       string `json:"key"`
		Method    string `json:"method"`
		ExpiresIn int    `json:"expiresIn"` // seconds
	}
	if !readJSON(w, r, &body) {
		return
	}
	bucket := r.PathValue("bucket")
	if body.Method == "" {
		body.Method = http.MethodGet
	}
	if body.Method != http.MethodGet && body.Method != http.MethodPut {
		writeError(w, http.StatusBadRequest, "method must be GET or PUT")
		return
	}
	if body.ExpiresIn == 0 {
		body.ExpiresIn = 900
	}
	if body.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}
	if body.Method == http.MethodGet {
		if _, err := a.store.HeadObject(r.Context(), bucket, body.Key); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	now := a.now()
	expires := time.Duration(body.ExpiresIn) * time.Second
	u, err := sigv4.Presign(body.Method, a.objectURL(bucket, body.Key), a.cfg.AccessKey, a.cfg.SecretKey, a.cfg.Region, now, expires)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "expiresAt": now.Add(expires).UTC()})
}

// --- live request log ----------------------------------------------------------

const keepAlive = 20 * time.Second

func (a *API) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")

	recent, ch, cancel := a.events.Subscribe()
	defer cancel()
	send := func(e events.Event) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.ID, b)
	}
	for _, e := range recent {
		send(e)
	}
	io.WriteString(w, ": ready\n\n")
	flusher.Flush()

	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			send(e)
			flusher.Flush()
		case <-ticker.C:
			io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
