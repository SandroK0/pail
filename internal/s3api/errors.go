package s3api

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/SandroK0/pail/internal/awschunked"
	"github.com/SandroK0/pail/internal/store"
)

// apiError is an S3 error response.
type apiError struct {
	Code    string
	Message string
	Status  int
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

var (
	errAccessDenied          = &apiError{"AccessDenied", "Access Denied", http.StatusForbidden}
	errExpiredRequest        = &apiError{"AccessDenied", "Request has expired", http.StatusForbidden}
	errAuthMalformed         = &apiError{"AuthorizationHeaderMalformed", "The authorization header is malformed", http.StatusBadRequest}
	errBadDigest             = &apiError{"BadDigest", "The Content-MD5 you specified did not match what we received.", http.StatusBadRequest}
	errBucketExists          = &apiError{"BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.", http.StatusConflict}
	errBucketNotEmpty        = &apiError{"BucketNotEmpty", "The bucket you tried to delete is not empty", http.StatusConflict}
	errIncompleteBody        = &apiError{"IncompleteBody", "You did not provide the number of bytes specified by the Content-Length HTTP header", http.StatusBadRequest}
	errInternal              = &apiError{"InternalError", "We encountered an internal error. Please try again.", http.StatusInternalServerError}
	errInvalidAccessKey      = &apiError{"InvalidAccessKeyId", "The AWS Access Key Id you provided does not exist in our records.", http.StatusForbidden}
	errInvalidArgument       = &apiError{"InvalidArgument", "Invalid Argument", http.StatusBadRequest}
	errInvalidBucketName     = &apiError{"InvalidBucketName", "The specified bucket is not valid.", http.StatusBadRequest}
	errInvalidDigest         = &apiError{"InvalidDigest", "The Content-MD5 you specified is not valid.", http.StatusBadRequest}
	errInvalidPart           = &apiError{"InvalidPart", "One or more of the specified parts could not be found.", http.StatusBadRequest}
	errInvalidPartOrder      = &apiError{"InvalidPartOrder", "The list of parts was not in ascending order.", http.StatusBadRequest}
	errKeyTooLong            = &apiError{"KeyTooLongError", "Your key is too long", http.StatusBadRequest}
	errMalformedXML          = &apiError{"MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema.", http.StatusBadRequest}
	errMethodNotAllowed      = &apiError{"MethodNotAllowed", "The specified method is not allowed against this resource.", http.StatusMethodNotAllowed}
	errNoSuchBucket          = &apiError{"NoSuchBucket", "The specified bucket does not exist", http.StatusNotFound}
	errNoSuchKey             = &apiError{"NoSuchKey", "The specified key does not exist.", http.StatusNotFound}
	errNoSuchUpload          = &apiError{"NoSuchUpload", "The specified upload does not exist.", http.StatusNotFound}
	errNotImplemented        = &apiError{"NotImplemented", "A header or query you provided implies functionality that is not implemented.", http.StatusNotImplemented}
	errRequestTimeTooSkewed  = &apiError{"RequestTimeTooSkewed", "The difference between the request time and the server's time is too large.", http.StatusForbidden}
	errSignatureDoesNotMatch = &apiError{"SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided. Check your key and signing method.", http.StatusForbidden}
)

func withMessage(e *apiError, msg string) *apiError {
	return &apiError{e.Code, msg, e.Status}
}

func fromStore(err error) *apiError {
	switch {
	case errors.Is(err, store.ErrNoSuchBucket):
		return errNoSuchBucket
	case errors.Is(err, store.ErrNoSuchKey):
		return errNoSuchKey
	case errors.Is(err, store.ErrBucketExists):
		return errBucketExists
	case errors.Is(err, store.ErrBucketNotEmpty):
		return errBucketNotEmpty
	case errors.Is(err, store.ErrInvalidBucketName):
		return errInvalidBucketName
	case errors.Is(err, store.ErrInvalidKey):
		return withMessage(errKeyTooLong, "Keys must be 1–1024 bytes of valid UTF-8")
	case errors.Is(err, store.ErrBadDigest):
		return errBadDigest
	case errors.Is(err, store.ErrNoSuchUpload):
		return errNoSuchUpload
	case errors.Is(err, store.ErrInvalidPart):
		return errInvalidPart
	case errors.Is(err, store.ErrInvalidPartOrder):
		return errInvalidPartOrder
	case errors.Is(err, errBodyLength), errors.Is(err, io.ErrUnexpectedEOF):
		return errIncompleteBody
	case errors.Is(err, awschunked.ErrMalformed):
		return withMessage(errIncompleteBody, err.Error())
	}
	slog.Error("s3 internal error", "err", err)
	return errInternal
}

type errorXML struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId"`
}

func (h *Handler) writeError(w *recorder, r *http.Request, e *apiError) {
	w.errCode, w.errMessage = e.Code, e.Message
	if r.Method == http.MethodHead {
		w.WriteHeader(e.Status)
		return
	}
	id := w.Header().Get("X-Amz-Request-Id")
	writeXML(w, e.Status, errorXML{
		Code: e.Code,
		// SDKs put the error message into their exception text, which is what
		// ends up in laravel.log. Including the request ID there lets you (or
		// an agent via MCP) find the exact request from a logged error.
		Message:   fmt.Sprintf("%s (pail request %s)", e.Message, id),
		Resource:  r.URL.Path,
		RequestID: id,
	})
}

func writeXML(w http.ResponseWriter, status int, v any) {
	out, err := xml.Marshal(v)
	if err != nil {
		slog.Error("marshal xml", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	w.Write([]byte(xml.Header))
	w.Write(out)
}
