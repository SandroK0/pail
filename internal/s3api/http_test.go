package s3api

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SandroK0/pail/internal/sigv4"
	"github.com/SandroK0/pail/internal/store"
)

// do sends a raw request to the handler, signing it unless anonymous.
func (e *env) do(t *testing.T, method, target string, body io.Reader, hdr map[string]string, anonymous bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+target, body)
	if err != nil {
		t.Fatal(err)
	}
	// Stop the transport from transparently decoding stored Content-Encodings.
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if !anonymous {
		sigv4.Sign(req, testAK, testSecret, "us-east-1", time.Now())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readAll(t *testing.T, r *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func errorCode(t *testing.T, r *http.Response) string {
	t.Helper()
	var e errorXML
	if err := xml.NewDecoder(r.Body).Decode(&e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return e.Code
}

func seed(t *testing.T, e *env, bucket string, objects map[string]store.PutOptions) {
	t.Helper()
	if err := e.store.CreateBucket(t.Context(), bucket); err != nil {
		t.Fatal(err)
	}
	for k, opts := range objects {
		if _, err := e.store.PutObject(t.Context(), bucket, k, strings.NewReader("body of "+k), opts); err != nil {
			t.Fatal(err)
		}
	}
}

func TestErrorFormat(t *testing.T) {
	e := verifiedEnv(t)
	resp := e.do(t, "GET", "/missing/key", nil, nil, false)
	if resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/xml" {
		t.Fatalf("status %d, type %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body := readAll(t, resp)
	for _, want := range []string{xml.Header[:5], "<Code>NoSuchBucket</Code>", "<Resource>/missing/key</Resource>", "<RequestId>" + resp.Header.Get("X-Amz-Request-Id")} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}

	resp = e.do(t, "HEAD", "/missing/key", nil, nil, false)
	if resp.StatusCode != 404 || readAll(t, resp) != "" {
		t.Fatal("HEAD errors must have no body")
	}
}

func TestAnonymousAccess(t *testing.T) {
	strict := verifiedEnv(t)
	seed(t, strict, "site", map[string]store.PutOptions{
		"public.css":  {ACL: "public-read", ContentType: "text/css"},
		"private.txt": {},
	})

	resp := strict.do(t, "GET", "/site/public.css", nil, nil, true)
	if resp.StatusCode != 200 || readAll(t, resp) != "body of public.css" || resp.Header.Get("Content-Type") != "text/css" {
		t.Fatalf("public object: %d", resp.StatusCode)
	}
	resp = strict.do(t, "GET", "/site/private.txt", nil, nil, true)
	if resp.StatusCode != 403 || errorCode(t, resp) != "AccessDenied" {
		t.Fatalf("private object: %d", resp.StatusCode)
	}
	resp = strict.do(t, "PUT", "/site/new.txt", strings.NewReader("x"), nil, true)
	if resp.StatusCode != 403 {
		t.Fatalf("anonymous write: %d", resp.StatusCode)
	}
	resp = strict.do(t, "GET", "/site", nil, nil, true)
	if resp.StatusCode != 403 {
		t.Fatalf("anonymous list: %d", resp.StatusCode)
	}

	// With auth off (the default), Storage::url() links just work.
	loose := newEnv(t, Config{})
	seed(t, loose, "site", map[string]store.PutOptions{"private.txt": {}})
	resp = loose.do(t, "GET", "/site/private.txt", nil, nil, true)
	if resp.StatusCode != 200 {
		t.Fatalf("auth off, private object: %d", resp.StatusCode)
	}
}

func TestPresignExpiryEnforcedWithAuthOff(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "files", map[string]store.PutOptions{"a.txt": {}})
	u, _ := sigv4.Presign("GET", e.srv.URL+"/files/a.txt", "whatever", "whatever", "us-east-1", time.Now().Add(-time.Hour), time.Minute)
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("expired presigned URL: %d", resp.StatusCode)
	}
}

func TestAuthErrors(t *testing.T) {
	e := verifiedEnv(t)
	cases := []struct {
		name string
		hdr  map[string]string
		code string
	}{
		{"malformed", map[string]string{"Authorization": "AWS4-HMAC-SHA256 garbage"}, "AuthorizationHeaderMalformed"},
		{"v2", map[string]string{"Authorization": "AWS local:sig"}, "AuthorizationHeaderMalformed"},
		{"skewed", map[string]string{
			"Authorization": "AWS4-HMAC-SHA256 Credential=local/20200101/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=00",
			"X-Amz-Date":    "20200101T000000Z",
		}, "RequestTimeTooSkewed"},
	}
	for _, c := range cases {
		resp := e.do(t, "GET", "/", nil, c.hdr, true)
		if got := errorCode(t, resp); got != c.code {
			t.Errorf("%s: code %s, want %s", c.name, got, c.code)
		}
	}
}

func TestCORS(t *testing.T) {
	e := newEnv(t, Config{CORSOrigins: []string{"http://app.test"}})
	seed(t, e, "uploads", nil)

	resp := e.do(t, "OPTIONS", "/uploads/file.png", nil, map[string]string{
		"Origin":                         "http://app.test",
		"Access-Control-Request-Method":  "PUT",
		"Access-Control-Request-Headers": "content-type,x-amz-acl",
	}, true)
	if resp.StatusCode != 200 ||
		resp.Header.Get("Access-Control-Allow-Origin") != "http://app.test" ||
		!strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "PUT") ||
		resp.Header.Get("Access-Control-Allow-Headers") != "content-type,x-amz-acl" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}

	resp = e.do(t, "OPTIONS", "/uploads/file.png", nil, map[string]string{"Origin": "http://evil.test"}, true)
	if resp.StatusCode != 403 || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin: %d", resp.StatusCode)
	}

	resp = e.do(t, "PUT", "/uploads/file.png", strings.NewReader("png"), map[string]string{"Origin": "http://app.test"}, false)
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "http://app.test" ||
		!strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), "ETag") {
		t.Fatalf("simple request CORS headers: %v", resp.Header)
	}
}

func TestRawAWSChunkedPut(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "raw", nil)
	body := "5;chunk-signature=aa\r\nhello\r\n6;chunk-signature=bb\r\n world\r\n0;chunk-signature=cc\r\n\r\n"
	resp := e.do(t, "PUT", "/raw/k", strings.NewReader(body), map[string]string{
		"X-Amz-Content-Sha256":         "STREAMING-AWS4-HMAC-SHA256-PAYLOAD",
		"X-Amz-Decoded-Content-Length": "11",
		"Content-Encoding":             "aws-chunked,gzip",
	}, true)
	if resp.StatusCode != 200 {
		t.Fatalf("put: %d %s", resp.StatusCode, readAll(t, resp))
	}
	resp = e.do(t, "GET", "/raw/k", nil, nil, true)
	if got := readAll(t, resp); got != "hello world" {
		t.Fatalf("stored %q", got)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q", enc)
	}

	resp = e.do(t, "PUT", "/raw/short", strings.NewReader(body), map[string]string{
		"Content-Encoding":             "aws-chunked",
		"X-Amz-Decoded-Content-Length": "99",
	}, true)
	if resp.StatusCode != 400 || errorCode(t, resp) != "IncompleteBody" {
		t.Fatalf("length mismatch: %d", resp.StatusCode)
	}
	resp = e.do(t, "PUT", "/raw/garbage", strings.NewReader("zz\r\n"), map[string]string{"Content-Encoding": "aws-chunked"}, true)
	if resp.StatusCode != 400 {
		t.Fatalf("malformed chunks: %d", resp.StatusCode)
	}
	if _, err := e.store.HeadObject(t.Context(), "raw", "short"); err == nil {
		t.Fatal("a failed upload was stored")
	}
}

func TestHeadRootForHealthChecks(t *testing.T) {
	e := newEnv(t, Config{})
	resp := e.do(t, "HEAD", "/", nil, nil, true)
	if resp.StatusCode != 200 || readAll(t, resp) != "" {
		t.Fatalf("HEAD / = %d", resp.StatusCode)
	}
}

func TestMetadataHeadersAreLowercase(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "meta", nil)
	e.do(t, "PUT", "/meta/k", strings.NewReader("x"), map[string]string{"X-Amz-Meta-Author": "ada"}, true)

	// Read the raw response: http.Response canonicalises header names.
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "HEAD /meta/k HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	raw, _ := io.ReadAll(conn)
	if !strings.Contains(string(raw), "\r\nx-amz-meta-author: ada\r\n") {
		t.Fatalf("metadata header not sent lowercase:\n%s", raw)
	}
}

func TestContentMD5(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "md5s", nil)
	good := md5.Sum([]byte("data"))
	resp := e.do(t, "PUT", "/md5s/ok", strings.NewReader("data"), map[string]string{"Content-MD5": base64.StdEncoding.EncodeToString(good[:])}, true)
	if resp.StatusCode != 200 {
		t.Fatalf("good md5: %d", resp.StatusCode)
	}
	bad := md5.Sum([]byte("other"))
	resp = e.do(t, "PUT", "/md5s/bad", strings.NewReader("data"), map[string]string{"Content-MD5": base64.StdEncoding.EncodeToString(bad[:])}, true)
	if errorCode(t, resp) != "BadDigest" {
		t.Fatal("bad md5 accepted")
	}
	resp = e.do(t, "PUT", "/md5s/invalid", strings.NewReader("data"), map[string]string{"Content-MD5": "not base64!"}, true)
	if errorCode(t, resp) != "InvalidDigest" {
		t.Fatal("invalid md5 accepted")
	}
}

func TestListEncodingTypeURL(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "enc", map[string]store.PutOptions{"a b/c&d.txt": {}})
	resp := e.do(t, "GET", "/enc?list-type=2&encoding-type=url&delimiter=/", nil, nil, true)
	body := readAll(t, resp)
	if !strings.Contains(body, "<Prefix>a%20b/</Prefix>") || !strings.Contains(body, "<EncodingType>url</EncodingType>") {
		t.Fatalf("encoded listing:\n%s", body)
	}
	resp = e.do(t, "GET", "/enc?list-type=2&encoding-type=base64", nil, nil, true)
	if errorCode(t, resp) != "InvalidArgument" {
		t.Fatal("unknown encoding accepted")
	}
}

func TestListArgumentErrors(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "args", map[string]store.PutOptions{"a": {}})
	for _, q := range []string{"max-keys=-1", "max-keys=abc", "continuation-token=***"} {
		resp := e.do(t, "GET", "/args?list-type=2&"+q, nil, nil, true)
		if errorCode(t, resp) != "InvalidArgument" {
			t.Errorf("%s accepted", q)
		}
	}
	resp := e.do(t, "GET", "/args?list-type=2&max-keys=0", nil, nil, true)
	body := readAll(t, resp)
	if !strings.Contains(body, "<KeyCount>0</KeyCount>") || strings.Contains(body, "<Contents>") {
		t.Fatalf("max-keys=0:\n%s", body)
	}
	resp = e.do(t, "GET", "/nope?list-type=2&max-keys=0", nil, nil, true)
	if resp.StatusCode != 404 {
		t.Fatal("max-keys=0 on a missing bucket should 404")
	}
}

func TestUnsupportedAndUnknown(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "misc", map[string]store.PutOptions{"k": {}})
	cases := []struct {
		method, target, code string
	}{
		{"GET", "/misc?policy", "NotImplemented"},
		{"PUT", "/misc/k?tagging", "NotImplemented"},
		{"POST", "/misc", "NotImplemented"},
		{"PATCH", "/misc/k", "MethodNotAllowed"},
		{"DELETE", "/", "MethodNotAllowed"},
	}
	for _, c := range cases {
		resp := e.do(t, c.method, c.target, nil, nil, true)
		if got := errorCode(t, resp); got != c.code {
			t.Errorf("%s %s: %s, want %s", c.method, c.target, got, c.code)
		}
	}
	resp := e.do(t, "PUT", "/misc/k", strings.NewReader("x"), map[string]string{"X-Amz-Acl": "everyone"}, true)
	if errorCode(t, resp) != "InvalidArgument" {
		t.Error("unknown canned ACL accepted")
	}
	resp = e.do(t, "PUT", "/misc/dst", nil, map[string]string{"X-Amz-Copy-Source": "misc"}, true)
	if errorCode(t, resp) != "InvalidArgument" {
		t.Error("copy source without key accepted")
	}
	resp = e.do(t, "PUT", "/misc/dst", nil, map[string]string{"X-Amz-Copy-Source": "misc/k", "X-Amz-Metadata-Directive": "MERGE"}, true)
	if errorCode(t, resp) != "InvalidArgument" {
		t.Error("unknown metadata directive accepted")
	}
	resp = e.do(t, "PUT", "/misc/"+strings.Repeat("k", 1025), strings.NewReader("x"), nil, true)
	if errorCode(t, resp) != "KeyTooLongError" {
		t.Error("over-long key accepted")
	}
	resp = e.do(t, "POST", "/misc?delete", strings.NewReader("<not xml"), nil, true)
	if errorCode(t, resp) != "MalformedXML" {
		t.Error("bad delete XML accepted")
	}
	resp = e.do(t, "PUT", "/misc/k?partNumber=0&uploadId=x", strings.NewReader("x"), nil, true)
	if errorCode(t, resp) != "InvalidArgument" {
		t.Error("part number 0 accepted")
	}
}

func TestPutACLViaGrantHeader(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "grants", map[string]store.PutOptions{"k": {}})
	resp := e.do(t, "PUT", "/grants/k?acl", nil, map[string]string{"X-Amz-Grant-Read": `uri="` + allUsersURI + `"`}, true)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	obj, _ := e.store.HeadObject(t.Context(), "grants", "k")
	if !obj.Public() {
		t.Fatal("grant header ignored")
	}
}

func TestBucketMiscEndpoints(t *testing.T) {
	e := newEnv(t, Config{Region: "eu-west-2"})
	seed(t, e, "misc", nil)
	body := readAll(t, e.do(t, "GET", "/misc?location", nil, nil, true))
	if !strings.Contains(body, ">eu-west-2</LocationConstraint>") {
		t.Fatalf("location: %s", body)
	}
	resp := e.do(t, "HEAD", "/misc", nil, nil, true)
	if resp.Header.Get("X-Amz-Bucket-Region") != "eu-west-2" {
		t.Fatal("bucket region header missing")
	}
	body = readAll(t, e.do(t, "GET", "/misc?versioning", nil, nil, true))
	if !strings.Contains(body, "VersioningConfiguration") {
		t.Fatalf("versioning: %s", body)
	}
	body = readAll(t, e.do(t, "GET", "/misc?acl", nil, nil, true))
	if !strings.Contains(body, "FULL_CONTROL") || strings.Contains(body, "AllUsers") {
		t.Fatalf("bucket acl: %s", body)
	}
	if resp := e.do(t, "PUT", "/misc?acl", nil, map[string]string{"X-Amz-Acl": "public-read"}, true); resp.StatusCode != 200 {
		t.Fatalf("put bucket acl: %d", resp.StatusCode)
	}
}

func TestEventsArePublished(t *testing.T) {
	e := newEnv(t, Config{})
	seed(t, e, "logged", nil)
	e.do(t, "PUT", "/logged/a.txt", strings.NewReader("12345"), map[string]string{"User-Agent": "aws-sdk-php/3.300"}, false)
	e.do(t, "GET", "/logged/missing", nil, nil, true)

	recent := e.events.Recent()
	if len(recent) != 2 {
		t.Fatalf("events = %+v", recent)
	}
	put, get := recent[0], recent[1]
	if put.Op != "PutObject" || put.Bucket != "logged" || put.Key != "a.txt" || put.Status != 200 ||
		put.BytesIn != 5 || put.AccessKey != testAK || put.UserAgent != "aws-sdk-php/3.300" || put.DurationMs < 0 {
		t.Errorf("put event = %+v", put)
	}
	if get.Op != "GetObject" || get.Status != 404 || get.ErrorCode != "NoSuchKey" || get.BytesOut == 0 || get.AccessKey != "" {
		t.Errorf("get event = %+v", get)
	}
}

func TestSplitPath(t *testing.T) {
	for in, want := range map[string][2]string{
		"/":               {"", ""},
		"/bucket":         {"bucket", ""},
		"/bucket/":        {"bucket", ""},
		"/bucket/a/b/":    {"bucket", "a/b/"},
		"/bucket//double": {"bucket", "/double"},
	} {
		b, k := splitPath(in)
		if b != want[0] || k != want[1] {
			t.Errorf("splitPath(%q) = %q, %q", in, b, k)
		}
	}
}

func TestUncleanPathsAreKeys(t *testing.T) {
	// net/http's ServeMux would redirect these; the handler must not.
	e := newEnv(t, Config{})
	seed(t, e, "paths", nil)
	for _, key := range []string{"a//b", "./x", "../y"} {
		req := httptest.NewRequest("PUT", "/paths/placeholder", strings.NewReader(key))
		req.URL.Path = "/paths/" + key
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("put %q: %d", key, rec.Code)
		}
		if _, err := e.store.HeadObject(t.Context(), "paths", key); err != nil {
			t.Fatalf("%q not stored verbatim: %v", key, err)
		}
	}
}
