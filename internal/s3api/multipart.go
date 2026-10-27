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

type initiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

func (h *Handler) createMultipartUpload(w *recorder, r *http.Request, bucket, key string) {
	opts, aerr := putOptions(r)
	if aerr != nil {
		h.writeError(w, r, aerr)
		return
	}
	opts.ContentMD5 = nil
	h.ensureBucket(r.Context(), bucket)
	id, err := h.store.CreateMultipartUpload(r.Context(), bucket, key, opts)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	writeXML(w, http.StatusOK, initiateMultipartUploadResult{Bucket: bucket, Key: key, UploadID: id})
}

func (h *Handler) uploadPart(w *recorder, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	n, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || n < 1 || n > store.MaxPartNumber {
		h.writeError(w, r, withMessage(errInvalidArgument, "Part number must be an integer between 1 and 10000, inclusive"))
		return
	}
	var sum []byte
	if v := r.Header.Get("Content-Md5"); v != "" {
		if sum, err = base64.StdEncoding.DecodeString(v); err != nil || len(sum) != 16 {
			h.writeError(w, r, errInvalidDigest)
			return
		}
	}
	etag, err := h.store.UploadPart(r.Context(), bucket, key, q.Get("uploadId"), n, requestBody(r), sum)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.Header().Set("ETag", quote(etag))
	w.WriteHeader(http.StatusOK)
}

type completeMultipartUpload struct {
	Parts []struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	} `xml:"Part"`
}

type completeMultipartUploadResult struct {
	XMLName  xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

func (h *Handler) completeMultipartUpload(w *recorder, r *http.Request, bucket, key string) {
	var req completeMultipartUpload
	if err := xml.NewDecoder(io.LimitReader(requestBody(r), 4<<20)).Decode(&req); err != nil {
		h.writeError(w, r, errMalformedXML)
		return
	}
	parts := make([]store.CompletedPart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = store.CompletedPart{Number: p.PartNumber, ETag: p.ETag}
	}
	obj, err := h.store.CompleteMultipartUpload(r.Context(), bucket, key, r.URL.Query().Get("uploadId"), parts)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	writeXML(w, http.StatusOK, completeMultipartUploadResult{
		Location: scheme + "://" + r.Host + "/" + bucket + "/" + sigv4.URIEncode(key, false),
		Bucket:   bucket,
		Key:      key,
		ETag:     quote(obj.ETag),
	})
}

func (h *Handler) abortMultipartUpload(w *recorder, r *http.Request, bucket, key string) {
	if err := h.store.AbortMultipartUpload(r.Context(), bucket, key, r.URL.Query().Get("uploadId")); err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type partXML struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

type listPartsResult struct {
	XMLName      xml.Name  `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListPartsResult"`
	Bucket       string    `xml:"Bucket"`
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	Initiator    *ownerXML `xml:"Initiator"`
	Owner        *ownerXML `xml:"Owner"`
	StorageClass string    `xml:"StorageClass"`
	MaxParts     int       `xml:"MaxParts"`
	IsTruncated  bool      `xml:"IsTruncated"`
	Parts        []partXML `xml:"Part"`
}

func (h *Handler) listParts(w *recorder, r *http.Request, bucket, key string) {
	id := r.URL.Query().Get("uploadId")
	parts, err := h.store.ListParts(r.Context(), bucket, key, id)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	res := listPartsResult{Bucket: bucket, Key: key, UploadID: id, Initiator: owner, Owner: owner, StorageClass: "STANDARD", MaxParts: store.MaxPartNumber}
	for _, p := range parts {
		res.Parts = append(res.Parts, partXML{p.Number, xmlTime(p.LastModified), quote(p.ETag), p.Size})
	}
	writeXML(w, http.StatusOK, res)
}

type uploadXML struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	Initiator    *ownerXML `xml:"Initiator"`
	Owner        *ownerXML `xml:"Owner"`
	StorageClass string    `xml:"StorageClass"`
	Initiated    string    `xml:"Initiated"`
}

type listMultipartUploadsResult struct {
	XMLName     xml.Name    `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListMultipartUploadsResult"`
	Bucket      string      `xml:"Bucket"`
	Prefix      string      `xml:"Prefix"`
	MaxUploads  int         `xml:"MaxUploads"`
	IsTruncated bool        `xml:"IsTruncated"`
	Uploads     []uploadXML `xml:"Upload"`
}

func (h *Handler) listMultipartUploads(w *recorder, r *http.Request, bucket string) {
	prefix := r.URL.Query().Get("prefix")
	uploads, err := h.store.ListMultipartUploads(r.Context(), bucket, prefix)
	if err != nil {
		h.writeError(w, r, fromStore(err))
		return
	}
	res := listMultipartUploadsResult{Bucket: bucket, Prefix: prefix, MaxUploads: 1000}
	for _, u := range uploads {
		res.Uploads = append(res.Uploads, uploadXML{u.Key, u.ID, owner, owner, "STANDARD", xmlTime(u.Initiated)})
	}
	writeXML(w, http.StatusOK, res)
}
