package s3api

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SandroK0/pail/internal/awschunked"
	"github.com/SandroK0/pail/internal/store"
)

const xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"

// Every object belongs to one fake owner.
const (
	ownerID   = "75aa57f09aa0c8caeab4f8c24e99d10f8e7faeebf76c078efc7c6caea54ba06a"
	ownerName = "local"
)

type ownerXML struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

var owner = &ownerXML{ownerID, ownerName}

func xmlTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func quote(etag string) string { return `"` + etag + `"` }

var errBodyLength = errors.New("body length does not match x-amz-decoded-content-length")

// lengthReader fails if the decoded body is not exactly want bytes long.
type lengthReader struct {
	r         io.Reader
	want, got int64
}

func (l *lengthReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.got += int64(n)
	if l.got > l.want || (err == io.EOF && l.got != l.want) {
		return n, errBodyLength
	}
	return n, err
}

// requestBody returns the request payload, decoding aws-chunked framing.
// Plain bodies are already bounded by Content-Length in net/http.
func requestBody(r *http.Request) io.Reader {
	if !awschunked.Is(r) {
		return r.Body
	}
	var body io.Reader = awschunked.NewReader(r.Body)
	if n, err := strconv.ParseInt(r.Header.Get("X-Amz-Decoded-Content-Length"), 10, 64); err == nil {
		body = &lengthReader{r: body, want: n}
	}
	return body
}

// Headers stored with an object and returned on GET/HEAD.
var storedHeaders = []string{"Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "Expires"}

var cannedACLs = []string{
	"private", "public-read", "public-read-write", "authenticated-read",
	"aws-exec-read", "bucket-owner-read", "bucket-owner-full-control",
}

const allUsersURI = "http://acs.amazonaws.com/groups/global/AllUsers"

// aclFromHeaders returns the canned ACL requested by x-amz-acl or an
// x-amz-grant-read for all users, or "" when neither is present.
func aclFromHeaders(h http.Header) (string, *apiError) {
	if v := h.Get("X-Amz-Acl"); v != "" {
		if !slices.Contains(cannedACLs, v) {
			return "", withMessage(errInvalidArgument, "Unknown canned ACL "+v)
		}
		return v, nil
	}
	if strings.Contains(h.Get("X-Amz-Grant-Read"), allUsersURI) {
		return "public-read", nil
	}
	return "", nil
}

func stripAWSChunked(enc string) string {
	var keep []string
	for e := range strings.SplitSeq(enc, ",") {
		if e = strings.TrimSpace(e); e != "" && e != "aws-chunked" {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, ", ")
}

func putOptions(r *http.Request) (store.PutOptions, *apiError) {
	opts := store.PutOptions{
		ContentType: r.Header.Get("Content-Type"),
		Metadata:    map[string]string{},
		Headers:     map[string]string{},
	}
	for k, vs := range r.Header {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-amz-meta-") {
			opts.Metadata[strings.TrimPrefix(lk, "x-amz-meta-")] = strings.Join(vs, ",")
		}
	}
	for _, name := range storedHeaders {
		v := r.Header.Get(name)
		if name == "Content-Encoding" {
			v = stripAWSChunked(v)
		}
		if v != "" {
			opts.Headers[name] = v
		}
	}
	acl, aerr := aclFromHeaders(r.Header)
	if aerr != nil {
		return opts, aerr
	}
	opts.ACL = acl
	if v := r.Header.Get("Content-Md5"); v != "" {
		sum, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(sum) != 16 {
			return opts, errInvalidDigest
		}
		opts.ContentMD5 = sum
	}
	return opts, nil
}

// Query parameters that override response headers on GET, as used by
// temporaryUrl() with ResponseContentDisposition and friends.
var responseOverrides = map[string]string{
	"response-content-type":        "Content-Type",
	"response-content-language":    "Content-Language",
	"response-expires":             "Expires",
	"response-cache-control":       "Cache-Control",
	"response-content-disposition": "Content-Disposition",
	"response-content-encoding":    "Content-Encoding",
}

func writeObjectHeaders(w http.ResponseWriter, obj *store.Object, q url.Values) {
	h := w.Header()
	h.Set("Content-Type", obj.ContentType)
	h.Set("ETag", quote(obj.ETag))
	h.Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Amz-Storage-Class", "STANDARD")
	// Set directly rather than via h.Set, which would canonicalise the name to
	// X-Amz-Meta-Author. S3 sends these lowercase, and aws-sdk-php derives
	// metadata keys from the header name verbatim.
	for k, v := range obj.Metadata {
		h["x-amz-meta-"+k] = []string{v}
	}
	for k, v := range obj.Headers {
		h.Set(k, v)
	}
	for param, header := range responseOverrides {
		if v := q.Get(param); v != "" {
			h.Set(header, v)
		}
	}
}

// Content types a browser could run script from.
var activeTypes = []string{"text/html", "application/xhtml+xml", "image/svg+xml", "text/xml", "application/xml"}

// IsActiveContent reports whether contentType could execute script when
// opened directly in a browser.
func IsActiveContent(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return slices.Contains(activeTypes, mt)
}

// getObject serves GET and HEAD, including Range and conditional requests.
func (h *Handler) getObject(w *recorder, r *http.Request, req *request) {
	obj, f, err := h.store.GetObject(r.Context(), req.bucket, req.key)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	defer f.Close()
	if req.anonymous && h.cfg.VerifyAuth && !obj.Public() {
		h.writeError(w, r, errAccessDenied)
		return
	}
	writeObjectHeaders(w, obj, r.URL.Query())
	// Objects share an origin with the admin UI. Uploaded HTML/SVG still
	// renders and runs script, but in an opaque origin that cannot reach it.
	if IsActiveContent(w.Header().Get("Content-Type")) {
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads")
	}
	http.ServeContent(w, r, "", obj.LastModified, f)
}

func (h *Handler) putObject(w *recorder, r *http.Request, bucket, key string) {
	opts, aerr := putOptions(r)
	if aerr != nil {
		h.writeError(w, r, aerr)
		return
	}
	h.ensureBucket(r.Context(), bucket)
	obj, err := h.store.PutObject(r.Context(), bucket, key, requestBody(r), opts)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.Header().Set("ETag", quote(obj.ETag))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteObject(w *recorder, r *http.Request, bucket, key string) {
	if err := h.store.DeleteObject(r.Context(), bucket, key); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type copyObjectResult struct {
	XMLName      xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CopyObjectResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

func (h *Handler) copyObject(w *recorder, r *http.Request, bucket, key string) {
	src, _, _ := strings.Cut(r.Header.Get("X-Amz-Copy-Source"), "?")
	src, err := url.PathUnescape(src)
	if err != nil {
		h.writeError(w, r, withMessage(errInvalidArgument, "Invalid copy source encoding"))
		return
	}
	srcBucket, srcKey := splitPath("/" + strings.TrimPrefix(src, "/"))
	if srcBucket == "" || srcKey == "" {
		h.writeError(w, r, withMessage(errInvalidArgument, "Copy Source must mention the source bucket and key: sourcebucket/sourcekey"))
		return
	}

	var opts store.CopyOptions
	switch directive := strings.ToUpper(r.Header.Get("X-Amz-Metadata-Directive")); directive {
	case "", "COPY":
	case "REPLACE":
		put, aerr := putOptions(r)
		if aerr != nil {
			h.writeError(w, r, aerr)
			return
		}
		opts = store.CopyOptions{ReplaceMetadata: true, ContentType: put.ContentType, Metadata: put.Metadata, Headers: put.Headers}
	default:
		h.writeError(w, r, withMessage(errInvalidArgument, "Unknown metadata directive."))
		return
	}
	acl, aerr := aclFromHeaders(r.Header)
	if aerr != nil {
		h.writeError(w, r, aerr)
		return
	}
	opts.ACL = acl

	h.ensureBucket(r.Context(), bucket)
	obj, err := h.store.CopyObject(r.Context(), srcBucket, srcKey, bucket, key, opts)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	writeXML(w, http.StatusOK, copyObjectResult{LastModified: xmlTime(obj.LastModified), ETag: quote(obj.ETag)})
}

// --- ACLs --------------------------------------------------------------------

func writeACL(w http.ResponseWriter, public bool) {
	var b strings.Builder
	fmt.Fprintf(&b, `%s<AccessControlPolicy xmlns="%s"><Owner><ID>%s</ID><DisplayName>%s</DisplayName></Owner><AccessControlList>`,
		xml.Header, xmlns, ownerID, ownerName)
	fmt.Fprintf(&b, `<Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser"><ID>%s</ID><DisplayName>%s</DisplayName></Grantee><Permission>FULL_CONTROL</Permission></Grant>`,
		ownerID, ownerName)
	if public {
		fmt.Fprintf(&b, `<Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group"><URI>%s</URI></Grantee><Permission>READ</Permission></Grant>`,
			html.EscapeString(allUsersURI))
	}
	b.WriteString(`</AccessControlList></AccessControlPolicy>`)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, b.String())
}

type accessControlPolicy struct {
	Grants []struct {
		URI        string `xml:"Grantee>URI"`
		Permission string `xml:"Permission"`
	} `xml:"AccessControlList>Grant"`
}

func (h *Handler) getObjectACL(w *recorder, r *http.Request, bucket, key string) {
	obj, err := h.store.HeadObject(r.Context(), bucket, key)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	writeACL(w, obj.Public())
}

func (h *Handler) putObjectACL(w *recorder, r *http.Request, bucket, key string) {
	acl, aerr := aclFromHeaders(r.Header)
	if aerr != nil {
		h.writeError(w, r, aerr)
		return
	}
	if acl == "" {
		var policy accessControlPolicy
		if err := xml.NewDecoder(io.LimitReader(requestBody(r), 1<<20)).Decode(&policy); err != nil {
			h.writeError(w, r, errMalformedXML)
			return
		}
		acl = "private"
		for _, g := range policy.Grants {
			if g.URI == allUsersURI && (g.Permission == "READ" || g.Permission == "FULL_CONTROL") {
				acl = "public-read"
			}
		}
	}
	if err := h.store.SetACL(r.Context(), bucket, key, acl); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.WriteHeader(http.StatusOK)
}
