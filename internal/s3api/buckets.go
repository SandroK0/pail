package s3api

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"

	"github.com/SandroK0/pail/internal/sigv4"
	"github.com/SandroK0/pail/internal/store"
)

type bucketXML struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type listAllMyBucketsResult struct {
	XMLName xml.Name    `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListAllMyBucketsResult"`
	Owner   *ownerXML   `xml:"Owner"`
	Buckets []bucketXML `xml:"Buckets>Bucket"`
}

func (h *Handler) listBuckets(w *recorder, r *http.Request) {
	buckets, err := h.store.ListBuckets(r.Context())
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	res := listAllMyBucketsResult{Owner: owner, Buckets: []bucketXML{}}
	for _, b := range buckets {
		res.Buckets = append(res.Buckets, bucketXML{b.Name, xmlTime(b.CreatedAt)})
	}
	writeXML(w, http.StatusOK, res)
}

func (h *Handler) createBucket(w *recorder, r *http.Request, bucket string) {
	if err := h.store.CreateBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) headBucket(w *recorder, r *http.Request, bucket string) {
	if _, err := h.store.GetBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.Header().Set("X-Amz-Bucket-Region", h.cfg.Region)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteBucket(w *recorder, r *http.Request, bucket string) {
	if err := h.store.DeleteBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type locationConstraint struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ LocationConstraint"`
	Region  string   `xml:",chardata"`
}

func (h *Handler) getBucketLocation(w *recorder, r *http.Request, bucket string) {
	if _, err := h.store.GetBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	region := h.cfg.Region
	if region == "us-east-1" {
		region = "" // S3 reports the default region as empty
	}
	writeXML(w, http.StatusOK, locationConstraint{Region: region})
}

type versioningConfiguration struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ VersioningConfiguration"`
}

func (h *Handler) getBucketVersioning(w *recorder, r *http.Request, bucket string) {
	if _, err := h.store.GetBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	writeXML(w, http.StatusOK, versioningConfiguration{})
}

func (h *Handler) getBucketACL(w *recorder, r *http.Request, bucket string) {
	if _, err := h.store.GetBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	writeACL(w, false)
}

// putBucketACL accepts and ignores bucket ACLs; only object ACLs matter here.
func (h *Handler) putBucketACL(w *recorder, r *http.Request, bucket string) {
	if _, err := h.store.GetBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- listing -----------------------------------------------------------------

type objectXML struct {
	Key          string    `xml:"Key"`
	LastModified string    `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        *ownerXML `xml:"Owner,omitempty"`
}

type commonPrefixXML struct {
	Prefix string `xml:"Prefix"`
}

type listBucketResult struct {
	XMLName        xml.Name          `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
	Name           string            `xml:"Name"`
	Prefix         string            `xml:"Prefix"`
	Delimiter      string            `xml:"Delimiter,omitempty"`
	MaxKeys        int               `xml:"MaxKeys"`
	EncodingType   string            `xml:"EncodingType,omitempty"`
	IsTruncated    bool              `xml:"IsTruncated"`
	Contents       []objectXML       `xml:"Contents"`
	CommonPrefixes []commonPrefixXML `xml:"CommonPrefixes"`

	// V1 only.
	Marker     *string `xml:"Marker"`
	NextMarker string  `xml:"NextMarker,omitempty"`

	// V2 only.
	KeyCount              *int   `xml:"KeyCount"`
	ContinuationToken     string `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string `xml:"NextContinuationToken,omitempty"`
	StartAfter            string `xml:"StartAfter,omitempty"`
}

func (h *Handler) listObjects(w *recorder, r *http.Request, bucket string, v2 bool) {
	q := r.URL.Query()
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			h.writeError(w, r, withMessage(errInvalidArgument, "Provided max-keys not an integer or within integer range"))
			return
		}
		maxKeys = min(n, 1000)
	}
	encodingType := q.Get("encoding-type")
	if encodingType != "" && encodingType != "url" {
		h.writeError(w, r, withMessage(errInvalidArgument, "Invalid Encoding Method specified in Request"))
		return
	}
	enc := func(s string) string {
		if encodingType == "url" {
			return sigv4.URIEncode(s, false)
		}
		return s
	}

	opts := store.ListOptions{Prefix: q.Get("prefix"), Delimiter: q.Get("delimiter"), MaxKeys: maxKeys}
	token := q.Get("continuation-token")
	switch {
	case !v2:
		opts.StartAfter = q.Get("marker")
	case token != "":
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			h.writeError(w, r, withMessage(errInvalidArgument, "The continuation token provided is incorrect"))
			return
		}
		opts.StartAfter = string(raw)
	default:
		opts.StartAfter = q.Get("start-after")
	}

	var res *store.ListResult
	var err error
	if maxKeys == 0 {
		res = &store.ListResult{}
		_, err = h.store.GetBucket(r.Context(), bucket)
	} else {
		res, err = h.store.ListObjects(r.Context(), bucket, opts)
	}
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}

	out := listBucketResult{
		Name:         bucket,
		Prefix:       enc(opts.Prefix),
		Delimiter:    enc(opts.Delimiter),
		MaxKeys:      maxKeys,
		EncodingType: encodingType,
		IsTruncated:  res.IsTruncated,
	}
	withOwner := !v2 || q.Get("fetch-owner") == "true"
	for _, o := range res.Objects {
		x := objectXML{Key: enc(o.Key), LastModified: xmlTime(o.LastModified), ETag: quote(o.ETag), Size: o.Size, StorageClass: "STANDARD"}
		if withOwner {
			x.Owner = owner
		}
		out.Contents = append(out.Contents, x)
	}
	for _, p := range res.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, commonPrefixXML{enc(p)})
	}
	if v2 {
		count := len(res.Objects) + len(res.CommonPrefixes)
		out.KeyCount = &count
		out.ContinuationToken = token
		out.StartAfter = enc(q.Get("start-after"))
		if res.IsTruncated {
			out.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(res.NextMarker))
		}
	} else {
		marker := enc(opts.StartAfter)
		out.Marker = &marker
		if res.IsTruncated {
			out.NextMarker = enc(res.NextMarker)
		}
	}
	writeXML(w, http.StatusOK, out)
}

// --- batch delete ------------------------------------------------------------

type deleteRequest struct {
	Quiet   bool `xml:"Quiet"`
	Objects []struct {
		Key string `xml:"Key"`
	} `xml:"Object"`
}

type deletedXML struct {
	Key string `xml:"Key"`
}

type deleteErrorXML struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

type deleteResult struct {
	XMLName xml.Name         `xml:"http://s3.amazonaws.com/doc/2006-03-01/ DeleteResult"`
	Deleted []deletedXML     `xml:"Deleted"`
	Errors  []deleteErrorXML `xml:"Error"`
}

func (h *Handler) deleteObjects(w *recorder, r *http.Request, bucket string) {
	var req deleteRequest
	if err := xml.NewDecoder(io.LimitReader(requestBody(r), 2<<20)).Decode(&req); err != nil || len(req.Objects) > 1000 {
		h.writeError(w, r, errMalformedXML)
		return
	}
	if _, err := h.store.GetBucket(r.Context(), bucket); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	var res deleteResult
	for _, o := range req.Objects {
		if err := h.store.DeleteObject(r.Context(), bucket, o.Key); err != nil {
			e := fromStore(err)
			res.Errors = append(res.Errors, deleteErrorXML{o.Key, e.Code, e.Message})
			continue
		}
		if !req.Quiet {
			res.Deleted = append(res.Deleted, deletedXML{o.Key})
		}
	}
	writeXML(w, http.StatusOK, res)
}
