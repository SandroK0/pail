// Package s3api serves the S3 REST API (path-style addressing) on top of
// the store.
package s3api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SandroK0/pail/internal/events"
	"github.com/SandroK0/pail/internal/sigv4"
	"github.com/SandroK0/pail/internal/store"
)

type Config struct {
	Region    string
	AccessKey string
	SecretKey string
	// VerifyAuth checks SigV4 signatures and refuses anonymous access to
	// private objects. When false any credentials are accepted.
	VerifyAuth bool
	// AutoCreateBuckets creates a bucket on the first write to it.
	AutoCreateBuckets bool
	// CORSOrigins lists allowed browser origins; "*" allows any.
	CORSOrigins []string
}

type Handler struct {
	store  *store.Store
	cfg    Config
	events *events.Broker // may be nil
	now    func() time.Time
}

func New(s *store.Store, cfg Config, broker *events.Broker) *Handler {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return &Handler{store: s, cfg: cfg, events: broker, now: time.Now}
}

// maxClockSkew is how far a header-signed request's date may be from now.
const maxClockSkew = 15 * time.Minute

// recorder captures what a handler did for the request log.
type recorder struct {
	http.ResponseWriter
	status     int
	written    int64
	op         string
	errCode    string
	errMessage string
}

func (w *recorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *recorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

type countingReader struct {
	io.ReadCloser
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}

// request carries per-request auth state to the operation handlers.
type request struct {
	bucket, key string
	anonymous   bool
}

func requestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func splitPath(p string) (bucket, key string) {
	bucket, key, _ = strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return bucket, key
}

func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w := &recorder{ResponseWriter: rw}
	body := &countingReader{ReadCloser: r.Body}
	r.Body = body

	w.Header().Set("X-Amz-Request-Id", requestID())
	w.Header().Set("Server", "pail")
	h.setCORS(w, r)

	req := &request{}
	req.bucket, req.key = splitPath(r.URL.Path)
	auth, authErr := h.authenticate(r)

	switch {
	case r.Method == http.MethodOptions:
		w.op = "CORSPreflight"
		h.preflight(w, r)
	case authErr != nil:
		w.op = "Auth"
		h.writeError(w, r, authErr)
	default:
		req.anonymous = auth == nil
		h.route(w, r, req)
	}

	if h.events != nil {
		e := events.Event{
			Time:         start,
			Op:           w.op,
			Method:       r.Method,
			Path:         r.URL.RequestURI(),
			Bucket:       req.bucket,
			Key:          req.key,
			Status:       w.status,
			ErrorCode:    w.errCode,
			ErrorMessage: w.errMessage,
			BytesIn:      body.n,
			BytesOut:     w.written,
			DurationMs:   float64(time.Since(start).Microseconds()) / 1000,
			UserAgent:    r.UserAgent(),
		}
		if e.Status == 0 {
			e.Status = http.StatusOK
		}
		if auth != nil {
			e.AccessKey, e.Presigned = auth.AccessKey, auth.Presigned
		}
		h.events.Publish(e)
	}
}

// authenticate returns the request's signature info (nil when anonymous).
// Expiry of presigned URLs is enforced even when verification is off, so
// temporaryUrl() behaves like it does against real S3.
func (h *Handler) authenticate(r *http.Request) (*sigv4.Auth, *apiError) {
	auth, err := sigv4.Parse(r)
	if err != nil {
		if h.cfg.VerifyAuth {
			return nil, withMessage(errAuthMalformed, err.Error())
		}
		return nil, nil
	}
	if auth == nil {
		return nil, nil
	}
	now := h.now()
	if auth.Presigned && auth.Expired(now) {
		return auth, errExpiredRequest
	}
	if !h.cfg.VerifyAuth {
		return auth, nil
	}
	if auth.V2 {
		return auth, withMessage(errAuthMalformed, "Signature Version 2 is not supported; use SigV4")
	}
	if auth.AccessKey != h.cfg.AccessKey {
		return auth, errInvalidAccessKey
	}
	if !auth.Presigned && (now.Sub(auth.Time) > maxClockSkew || auth.Time.Sub(now) > maxClockSkew) {
		return auth, errRequestTimeTooSkewed
	}
	if err := sigv4.Verify(r, auth, h.cfg.SecretKey); err != nil {
		return auth, errSignatureDoesNotMatch
	}
	return auth, nil
}

// --- CORS --------------------------------------------------------------------

func (h *Handler) originAllowed(origin string) bool {
	return slices.Contains(h.cfg.CORSOrigins, "*") || slices.Contains(h.cfg.CORSOrigins, origin)
}

func (h *Handler) setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !h.originAllowed(origin) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Expose-Headers",
		"ETag, Content-Length, Content-Type, Content-Range, Last-Modified, Accept-Ranges, X-Amz-Request-Id")
}

func (h *Handler) preflight(w *recorder, r *http.Request) {
	if !h.originAllowed(r.Header.Get("Origin")) {
		h.writeError(w, r, withMessage(errAccessDenied, "CORSResponse: This CORS request is not allowed."))
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, HEAD")
	if hdrs := r.Header.Get("Access-Control-Request-Headers"); hdrs != "" {
		w.Header().Set("Access-Control-Allow-Headers", hdrs)
	}
	w.Header().Set("Access-Control-Max-Age", "3000")
	w.WriteHeader(http.StatusOK)
}

// --- routing -----------------------------------------------------------------

// Bucket subresources that exist in S3 but are not supported here.
var unsupportedSubresources = []string{
	"accelerate", "analytics", "cors", "encryption", "intelligent-tiering", "inventory",
	"lifecycle", "logging", "metrics", "notification", "object-lock", "ownershipControls",
	"policy", "publicAccessBlock", "replication", "requestPayment", "tagging", "website",
	"legal-hold", "retention", "torrent", "restore", "select", "attributes",
}

func (h *Handler) route(w *recorder, r *http.Request, req *request) {
	q := r.URL.Query()
	has := func(k string) bool { _, ok := q[k]; return ok }
	bucket, key := req.bucket, req.key

	// Anonymous clients may only read objects (and then only public ones
	// when auth is verified).
	if req.anonymous && h.cfg.VerifyAuth && !(key != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead)) {
		w.op = "Auth"
		h.writeError(w, r, errAccessDenied)
		return
	}

	for _, sub := range unsupportedSubresources {
		if has(sub) {
			w.op = "Unsupported:" + sub
			h.writeError(w, r, errNotImplemented)
			return
		}
	}

	if bucket == "" {
		w.op = "ListBuckets"
		// HEAD is answered too (net/http drops the body), so health checks
		// probing "/" don't show up as errors in the request log.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.writeError(w, r, errMethodNotAllowed)
			return
		}
		h.listBuckets(w, r)
		return
	}

	if key == "" {
		switch r.Method {
		case http.MethodGet:
			switch {
			case has("location"):
				w.op = "GetBucketLocation"
				h.getBucketLocation(w, r, bucket)
			case has("versioning"):
				w.op = "GetBucketVersioning"
				h.getBucketVersioning(w, r, bucket)
			case has("acl"):
				w.op = "GetBucketAcl"
				h.getBucketACL(w, r, bucket)
			case has("uploads"):
				w.op = "ListMultipartUploads"
				h.listMultipartUploads(w, r, bucket)
			case q.Get("list-type") == "2":
				w.op = "ListObjectsV2"
				h.listObjects(w, r, bucket, true)
			default:
				w.op = "ListObjects"
				h.listObjects(w, r, bucket, false)
			}
		case http.MethodPut:
			if has("acl") {
				w.op = "PutBucketAcl"
				h.putBucketACL(w, r, bucket)
				return
			}
			w.op = "CreateBucket"
			h.createBucket(w, r, bucket)
		case http.MethodHead:
			w.op = "HeadBucket"
			h.headBucket(w, r, bucket)
		case http.MethodDelete:
			w.op = "DeleteBucket"
			h.deleteBucket(w, r, bucket)
		case http.MethodPost:
			if has("delete") {
				w.op = "DeleteObjects"
				h.deleteObjects(w, r, bucket)
				return
			}
			w.op = "PostObject"
			h.writeError(w, r, errNotImplemented)
		default:
			h.writeError(w, r, errMethodNotAllowed)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		switch {
		case has("uploadId"):
			w.op = "ListParts"
			h.listParts(w, r, bucket, key)
		case has("acl"):
			w.op = "GetObjectAcl"
			h.getObjectACL(w, r, bucket, key)
		default:
			w.op = "GetObject"
			h.getObject(w, r, req)
		}
	case http.MethodHead:
		w.op = "HeadObject"
		h.getObject(w, r, req)
	case http.MethodPut:
		switch {
		case has("uploadId") && r.Header.Get("X-Amz-Copy-Source") != "":
			w.op = "UploadPartCopy"
			h.writeError(w, r, errNotImplemented)
		case has("uploadId"):
			w.op = "UploadPart"
			h.uploadPart(w, r, bucket, key)
		case has("acl"):
			w.op = "PutObjectAcl"
			h.putObjectACL(w, r, bucket, key)
		case r.Header.Get("X-Amz-Copy-Source") != "":
			w.op = "CopyObject"
			h.copyObject(w, r, bucket, key)
		default:
			w.op = "PutObject"
			h.putObject(w, r, bucket, key)
		}
	case http.MethodDelete:
		if has("uploadId") {
			w.op = "AbortMultipartUpload"
			h.abortMultipartUpload(w, r, bucket, key)
			return
		}
		w.op = "DeleteObject"
		h.deleteObject(w, r, bucket, key)
	case http.MethodPost:
		switch {
		case has("uploads"):
			w.op = "CreateMultipartUpload"
			h.createMultipartUpload(w, r, bucket, key)
		case has("uploadId"):
			w.op = "CompleteMultipartUpload"
			h.completeMultipartUpload(w, r, bucket, key)
		default:
			h.writeError(w, r, errNotImplemented)
		}
	default:
		h.writeError(w, r, errMethodNotAllowed)
	}
}

// ensureBucket creates bucket when auto-creation is on. Failures (it exists,
// or the name is invalid) surface from the write that follows.
func (h *Handler) ensureBucket(ctx context.Context, bucket string) {
	if h.cfg.AutoCreateBuckets {
		h.store.CreateBucket(ctx, bucket)
	}
}
