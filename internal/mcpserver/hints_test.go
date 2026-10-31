package mcpserver

import (
	"strings"
	"testing"

	"github.com/SandroK0/local-s3/internal/events"
)

func TestHints(t *testing.T) {
	cfg := Config{AccessKey: "local"}
	buckets := []string{"laravel", "media"}
	tests := []struct {
		name string
		e    events.Event
		want []string // substrings the hint must contain
	}{
		{"success", events.Event{Status: 200}, nil},
		{"not modified", events.Event{Status: 304}, nil},
		{"missing bucket lists real ones", events.Event{Status: 404, ErrorCode: "NoSuchBucket", Bucket: "larvel"},
			[]string{`"larvel"`, "laravel, media", "AWS_BUCKET", "AUTO_CREATE_BUCKETS"}},
		{"missing bucket with none", events.Event{Status: 404, ErrorCode: "NoSuchBucket", Bucket: "x"}, []string{"existing: laravel"}},
		{"missing key on read", events.Event{Status: 404, ErrorCode: "NoSuchKey", Op: "GetObject", Key: "avatars/1.png"},
			[]string{`"avatars/1.png"`, "'root' prefix", "search_objects"}},
		{"missing key on exists()", events.Event{Status: 404, ErrorCode: "NoSuchKey", Op: "HeadObject"}, []string{"Storage::exists()"}},
		{"missing copy source", events.Event{Status: 404, ErrorCode: "NoSuchKey", Op: "CopyObject"}, []string{"Storage::copy()/move()"}},
		{"wrong secret", events.Event{Status: 403, ErrorCode: "SignatureDoesNotMatch", AccessKey: "local"}, []string{"AWS_SECRET_ACCESS_KEY", "SECRET_KEY"}},
		{"tampered link", events.Event{Status: 403, ErrorCode: "SignatureDoesNotMatch", Presigned: true}, []string{"temporaryUrl()", "altered"}},
		{"wrong access key", events.Event{Status: 403, ErrorCode: "InvalidAccessKeyId", AccessKey: "nope"}, []string{`"nope"`, `"local"`, "AWS_ACCESS_KEY_ID"}},
		{"expired link", events.Event{Status: 403, ErrorCode: "AccessDenied", ErrorMessage: "Request has expired", Presigned: true}, []string{"expired", "fresh"}},
		{"private file via url()", events.Event{Status: 403, ErrorCode: "AccessDenied", Op: "GetObject"}, []string{"Storage::url()", "temporaryUrl()", "setVisibility"}},
		{"unsigned write", events.Event{Status: 403, ErrorCode: "AccessDenied", Op: "PutObject"}, []string{"credentials"}},
		{"CORS", events.Event{Status: 403, ErrorCode: "AccessDenied", Op: "CORSPreflight"}, []string{"CORS_ORIGINS"}},
		{"clock", events.Event{Status: 403, ErrorCode: "RequestTimeTooSkewed"}, []string{"clock"}},
		{"sigv2", events.Event{Status: 400, ErrorCode: "AuthorizationHeaderMalformed", ErrorMessage: "Signature Version 2 is not supported"}, []string{"Version 2", "Version 4"}},
		{"unsupported feature", events.Event{Status: 501, ErrorCode: "NotImplemented", Op: "Unsupported:tagging"}, []string{"(tagging)"}},
		{"bad digest", events.Event{Status: 400, ErrorCode: "BadDigest"}, []string{"Content-MD5"}},
		{"truncated upload", events.Event{Status: 400, ErrorCode: "IncompleteBody"}, []string{"shorter or longer"}},
		{"multipart retry", events.Event{Status: 404, ErrorCode: "NoSuchUpload"}, []string{"retry"}},
		{"range", events.Event{Status: 416}, []string{"Range"}},
		{"internal", events.Event{Status: 500, ErrorCode: "InternalError"}, []string{"pail's own log"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buckets
			if tt.name == "missing bucket with none" {
				b = []string{"laravel"}
			}
			got := hint(tt.e, cfg, b)
			if tt.want == nil {
				if got != "" {
					t.Fatalf("hint = %q, want none", got)
				}
				return
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("hint %q lacks %q", got, w)
				}
			}
		})
	}
}

func TestEveryErrorCodeHasAHint(t *testing.T) {
	// Every error code the S3 API can return.
	for _, code := range []string{
		"AccessDenied", "AuthorizationHeaderMalformed", "BadDigest", "BucketAlreadyOwnedByYou", "BucketNotEmpty",
		"IncompleteBody", "InternalError", "InvalidAccessKeyId", "InvalidArgument", "InvalidBucketName", "InvalidDigest",
		"InvalidPart", "InvalidPartOrder", "KeyTooLongError", "MalformedXML", "MethodNotAllowed", "NoSuchBucket",
		"NoSuchKey", "NoSuchUpload", "NotImplemented", "RequestTimeTooSkewed", "SignatureDoesNotMatch",
	} {
		status := 400
		if code == "InternalError" {
			status = 500
		}
		if hint(events.Event{Status: status, ErrorCode: code}, Config{}, nil) == "" {
			t.Errorf("no hint for %s", code)
		}
	}
}
