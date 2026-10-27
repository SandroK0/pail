package s3api

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/SandroK0/pail/internal/events"
	"github.com/SandroK0/pail/internal/store"
)

const (
	testAK     = "local"
	testSecret = "localsecret"
)

type env struct {
	srv    *httptest.Server
	store  *store.Store
	events *events.Broker
	h      *Handler
	client *s3.Client
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if cfg.AccessKey == "" {
		cfg.AccessKey, cfg.SecretKey = testAK, testSecret
	}
	broker := events.NewBroker(100)
	h := New(st, cfg, broker)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{srv: srv, store: st, events: broker, h: h, client: sdkClient(srv.URL, testAK, testSecret)}
}

func sdkClient(endpoint, ak, secret string) *s3.Client {
	return s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(ak, secret, ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
		o.RetryMaxAttempts = 1
	})
}

func apiCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	return ""
}

func mustCreate(t *testing.T, e *env, bucket string) {
	t.Helper()
	if _, err := e.client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
}

func mustPut(t *testing.T, e *env, bucket, key, body string) {
	t.Helper()
	_, err := e.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: strings.NewReader(body)})
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func getBody(t *testing.T, e *env, bucket, key string) string {
	t.Helper()
	out, err := e.client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer out.Body.Close()
	b, _ := io.ReadAll(out.Body)
	return string(b)
}

func verifiedEnv(t *testing.T) *env {
	return newEnv(t, Config{VerifyAuth: true, CORSOrigins: []string{"*"}})
}

func TestSDKBuckets(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "alpha")
	mustCreate(t, e, "beta")

	_, err := e.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("alpha")})
	if apiCode(err) != "BucketAlreadyOwnedByYou" {
		t.Fatalf("duplicate create: %v", err)
	}
	_, err = e.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("Bad_Name")})
	if apiCode(err) != "InvalidBucketName" {
		t.Fatalf("bad name: %v", err)
	}

	list, err := e.client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Buckets) != 2 || *list.Buckets[0].Name != "alpha" || list.Buckets[0].CreationDate == nil {
		t.Fatalf("buckets = %+v", list.Buckets)
	}

	if _, err := e.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("alpha")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("nope")}); err == nil {
		t.Fatal("HeadBucket on missing bucket succeeded")
	}
	loc, err := e.client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String("alpha")})
	if err != nil || loc.LocationConstraint != "" {
		t.Fatalf("location = %+v, %v", loc, err)
	}

	mustPut(t, e, "alpha", "k", "v")
	_, err = e.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String("alpha")})
	if apiCode(err) != "BucketNotEmpty" {
		t.Fatalf("delete non-empty: %v", err)
	}
	if _, err := e.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String("beta")}); err != nil {
		t.Fatal(err)
	}
	_, err = e.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String("beta")})
	if apiCode(err) != "NoSuchBucket" {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestSDKObjectRoundTrip(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "photos")

	key := "dir/with space/ü+%&=.txt"
	put, err := e.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String("photos"),
		Key:                aws.String(key),
		Body:               strings.NewReader("hello world"),
		ContentType:        aws.String("text/plain"),
		CacheControl:       aws.String("max-age=60"),
		ContentDisposition: aws.String(`attachment; filename="hi.txt"`),
		Metadata:           map[string]string{"author": "sandro", "Mixed-Case": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("hello world"))
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`
	if *put.ETag != wantETag {
		t.Fatalf("put etag = %s, want %s", *put.ETag, wantETag)
	}

	head, err := e.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("photos"), Key: aws.String(key)})
	if err != nil {
		t.Fatal(err)
	}
	if *head.ContentLength != 11 || *head.ContentType != "text/plain" || *head.ETag != wantETag ||
		*head.CacheControl != "max-age=60" || *head.ContentDisposition != `attachment; filename="hi.txt"` ||
		head.Metadata["author"] != "sandro" || head.Metadata["mixed-case"] != "x" || head.LastModified == nil {
		t.Fatalf("head = %+v", head)
	}

	if got := getBody(t, e, "photos", key); got != "hello world" {
		t.Fatalf("body = %q", got)
	}

	rng, err := e.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String(key), Range: aws.String("bytes=6-")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rng.Body)
	rng.Body.Close()
	if string(b) != "world" || *rng.ContentRange != "bytes 6-10/11" {
		t.Fatalf("range = %q %s", b, *rng.ContentRange)
	}

	_, err = e.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String(key), IfNoneMatch: aws.String(wantETag)})
	var re interface{ HTTPStatusCode() int }
	if !errors.As(err, &re) || re.HTTPStatusCode() != http.StatusNotModified {
		t.Fatalf("If-None-Match: %v", err)
	}

	override, err := e.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("photos"), Key: aws.String(key), ResponseContentType: aws.String("application/x-custom"),
	})
	if err != nil {
		t.Fatal(err)
	}
	override.Body.Close()
	if *override.ContentType != "application/x-custom" {
		t.Fatalf("response-content-type override = %s", *override.ContentType)
	}

	if _, err := e.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("photos"), Key: aws.String(key)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("photos"), Key: aws.String(key)})
	var nf *types.NotFound
	if !errors.As(err, &nf) {
		t.Fatalf("head after delete: %v", err)
	}
	_, err = e.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String(key)})
	var nsk *types.NoSuchKey
	if !errors.As(err, &nsk) {
		t.Fatalf("get after delete: %v", err)
	}
	if _, err := e.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("photos"), Key: aws.String(key)}); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	_, err = e.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("nope"), Key: aws.String("k"), Body: strings.NewReader("x")})
	if apiCode(err) != "NoSuchBucket" {
		t.Fatalf("put to missing bucket: %v", err)
	}
}

// onlyReader hides Seek so the SDK must stream the body (aws-chunked).
type onlyReader struct{ io.Reader }

// The Go SDK only streams aws-chunked bodies with trailing checksums over
// TLS, so this test runs its own HTTPS server.
func TestSDKUnseekableBody(t *testing.T) {
	e := verifiedEnv(t)
	var encoding string
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			encoding = r.Header.Get("Content-Encoding")
		}
		e.h.ServeHTTP(w, r)
	}))
	defer tlsSrv.Close()
	e.client = s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(testAK, testSecret, ""),
		HTTPClient:  tlsSrv.Client(),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(tlsSrv.URL)
		o.UsePathStyle = true
	})
	mustCreate(t, e, "stream")

	body := strings.Repeat("streamed ", 10000)
	_, err := e.client.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String("stream"), Key: aws.String("k"), Body: onlyReader{strings.NewReader(body)},
		ContentLength: aws.Int64(int64(len(body))), ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
	}, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoding, "aws-chunked") {
		t.Fatalf("SDK did not use aws-chunked (Content-Encoding %q); test is not exercising the decoder", encoding)
	}
	if got := getBody(t, e, "stream", "k"); got != body {
		t.Fatalf("got %d bytes, want %d", len(got), len(body))
	}
	head, _ := e.client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String("stream"), Key: aws.String("k")})
	if head.ContentEncoding != nil && strings.Contains(*head.ContentEncoding, "aws-chunked") {
		t.Fatal("aws-chunked leaked into the stored Content-Encoding")
	}
}

func TestSDKListObjectsV2(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "listing")
	keys := []string{"a.txt", "dir/1", "dir/2", "dir/sub/3", "z z+.txt"}
	for _, k := range keys {
		mustPut(t, e, "listing", k, "x")
	}

	out, err := e.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("listing"), Delimiter: aws.String("/")})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range out.Contents {
		got = append(got, *o.Key)
	}
	if strings.Join(got, ",") != "a.txt,z z+.txt" || len(out.CommonPrefixes) != 1 || *out.CommonPrefixes[0].Prefix != "dir/" || *out.KeyCount != 3 {
		t.Fatalf("root listing = %v %+v", got, out.CommonPrefixes)
	}

	// Paginate one key at a time.
	var all []string
	p := s3.NewListObjectsV2Paginator(e.client, &s3.ListObjectsV2Input{Bucket: aws.String("listing"), MaxKeys: aws.Int32(1)})
	pages := 0
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, o := range page.Contents {
			all = append(all, *o.Key)
		}
	}
	if !sort.StringsAreSorted(all) || len(all) != len(keys) || pages != len(keys) {
		t.Fatalf("paginated = %v in %d pages", all, pages)
	}

	out, err = e.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("listing"), Prefix: aws.String("dir/"), StartAfter: aws.String("dir/1")})
	if err != nil || len(out.Contents) != 2 || *out.Contents[0].Key != "dir/2" {
		t.Fatalf("start-after = %+v, %v", out.Contents, err)
	}

	v1, err := e.client.ListObjects(ctx, &s3.ListObjectsInput{Bucket: aws.String("listing"), Marker: aws.String("dir/2"), MaxKeys: aws.Int32(1)})
	if err != nil || len(v1.Contents) != 1 || *v1.Contents[0].Key != "dir/sub/3" || !*v1.IsTruncated || *v1.NextMarker != "dir/sub/3" {
		t.Fatalf("v1 = %+v, %v", v1, err)
	}
}

func TestSDKCopy(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "copies")
	_, err := e.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("copies"), Key: aws.String("src file.txt"), Body: strings.NewReader("data"),
		ContentType: aws.String("text/plain"), Metadata: map[string]string{"a": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cp, err := e.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String("copies"), Key: aws.String("dst/copy.txt"), CopySource: aws.String("copies/src%20file.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cp.CopyObjectResult == nil || cp.CopyObjectResult.ETag == nil {
		t.Fatalf("copy result = %+v", cp)
	}
	head, _ := e.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("copies"), Key: aws.String("dst/copy.txt")})
	if *head.ContentType != "text/plain" || head.Metadata["a"] != "1" {
		t.Fatalf("copied metadata = %+v", head)
	}

	_, err = e.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String("copies"), Key: aws.String("dst/replaced"), CopySource: aws.String("copies/src%20file.txt"),
		MetadataDirective: types.MetadataDirectiveReplace, ContentType: aws.String("image/png"), Metadata: map[string]string{"b": "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	head, _ = e.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("copies"), Key: aws.String("dst/replaced")})
	if *head.ContentType != "image/png" || head.Metadata["b"] != "2" || head.Metadata["a"] != "" {
		t.Fatalf("replaced metadata = %+v", head)
	}

	_, err = e.client.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("copies"), Key: aws.String("x"), CopySource: aws.String("copies/missing")})
	if apiCode(err) != "NoSuchKey" {
		t.Fatalf("missing source: %v", err)
	}
}

func TestSDKDeleteObjects(t *testing.T) {
	e := verifiedEnv(t)
	mustCreate(t, e, "batch")
	for _, k := range []string{"a", "b", "c"} {
		mustPut(t, e, "batch", k, "x")
	}
	out, err := e.client.DeleteObjects(t.Context(), &s3.DeleteObjectsInput{
		Bucket: aws.String("batch"),
		Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String("a")}, {Key: aws.String("b")}, {Key: aws.String("missing")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Deleted) != 3 || len(out.Errors) != 0 {
		t.Fatalf("deleted = %d, errors = %+v", len(out.Deleted), out.Errors)
	}
	list, _ := e.client.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{Bucket: aws.String("batch")})
	if len(list.Contents) != 1 || *list.Contents[0].Key != "c" {
		t.Fatalf("left = %+v", list.Contents)
	}

	out, err = e.client.DeleteObjects(t.Context(), &s3.DeleteObjectsInput{
		Bucket: aws.String("batch"), Delete: &types.Delete{Quiet: aws.Bool(true), Objects: []types.ObjectIdentifier{{Key: aws.String("c")}}},
	})
	if err != nil || len(out.Deleted) != 0 {
		t.Fatalf("quiet delete = %+v, %v", out, err)
	}
}

func TestSDKMultipart(t *testing.T) {
	e := verifiedEnv(t)
	mustCreate(t, e, "big")
	body := bytes.Repeat([]byte("0123456789abcdef"), 12<<20/16) // 12 MiB → 3 parts of 5 MiB
	up := manager.NewUploader(e.client, func(u *manager.Uploader) { u.PartSize = 5 << 20 })
	res, err := up.Upload(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String("big"), Key: aws.String("video.mp4"), Body: bytes.NewReader(body), ContentType: aws.String("video/mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.UploadID == "" || !strings.HasSuffix(*res.ETag, `-3"`) {
		t.Fatalf("upload result = %+v etag %s", res, *res.ETag)
	}
	if got := getBody(t, e, "big", "video.mp4"); got != string(body) {
		t.Fatalf("body length %d, want %d", len(got), len(body))
	}
	head, _ := e.client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String("big"), Key: aws.String("video.mp4")})
	if *head.ContentType != "video/mp4" {
		t.Fatalf("content type = %s", *head.ContentType)
	}
}

func TestSDKMultipartLowLevel(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "parts")
	create, err := e.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("parts"), Key: aws.String("k")})
	if err != nil {
		t.Fatal(err)
	}
	part, err := e.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket: aws.String("parts"), Key: aws.String("k"), UploadId: create.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("abc"),
	})
	if err != nil {
		t.Fatal(err)
	}

	parts, err := e.client.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String("parts"), Key: aws.String("k"), UploadId: create.UploadId})
	if err != nil || len(parts.Parts) != 1 || *parts.Parts[0].Size != 3 {
		t.Fatalf("ListParts = %+v, %v", parts, err)
	}
	uploads, err := e.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String("parts")})
	if err != nil || len(uploads.Uploads) != 1 {
		t.Fatalf("ListMultipartUploads = %+v, %v", uploads, err)
	}

	_, err = e.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String("parts"), Key: aws.String("k"), UploadId: create.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: aws.String(`"bogus"`)}}},
	})
	if apiCode(err) != "InvalidPart" {
		t.Fatalf("bad etag: %v", err)
	}

	if _, err := e.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String("parts"), Key: aws.String("k"), UploadId: create.UploadId}); err != nil {
		t.Fatal(err)
	}
	_, err = e.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String("parts"), Key: aws.String("k"), UploadId: create.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.ETag}}},
	})
	if apiCode(err) != "NoSuchUpload" {
		t.Fatalf("complete after abort: %v", err)
	}
}

func TestSDKACL(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "acls")
	_, err := e.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("acls"), Key: aws.String("pub"), Body: strings.NewReader("x"), ACL: types.ObjectCannedACLPublicRead})
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, e, "acls", "priv", "x")

	isPublic := func(key string) bool {
		acl, err := e.client.GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: aws.String("acls"), Key: aws.String(key)})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range acl.Grants {
			if g.Grantee != nil && g.Grantee.URI != nil && *g.Grantee.URI == allUsersURI && g.Permission == types.PermissionRead {
				return true
			}
		}
		return false
	}
	if !isPublic("pub") || isPublic("priv") {
		t.Fatal("initial ACLs wrong")
	}

	if _, err := e.client.PutObjectAcl(ctx, &s3.PutObjectAclInput{Bucket: aws.String("acls"), Key: aws.String("priv"), ACL: types.ObjectCannedACLPublicRead}); err != nil {
		t.Fatal(err)
	}
	if !isPublic("priv") {
		t.Fatal("canned ACL not applied")
	}
	_, err = e.client.PutObjectAcl(ctx, &s3.PutObjectAclInput{
		Bucket: aws.String("acls"), Key: aws.String("pub"),
		AccessControlPolicy: &types.AccessControlPolicy{
			Owner:  &types.Owner{ID: aws.String(ownerID)},
			Grants: []types.Grant{{Grantee: &types.Grantee{Type: types.TypeCanonicalUser, ID: aws.String(ownerID)}, Permission: types.PermissionFullControl}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if isPublic("pub") {
		t.Fatal("ACL body without AllUsers should make the object private")
	}
}

func TestSDKPresign(t *testing.T) {
	e := verifiedEnv(t)
	ctx := t.Context()
	mustCreate(t, e, "signed")
	mustPut(t, e, "signed", "doc.pdf", "pdf bytes")
	presigner := s3.NewPresignClient(e.client)

	get, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("signed"), Key: aws.String("doc.pdf")},
		s3.WithPresignExpires(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(get.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "pdf bytes" {
		t.Fatalf("presigned GET = %d %q", resp.StatusCode, b)
	}

	tampered := strings.Replace(get.URL, "doc.pdf", "other.pdf", 1)
	resp, _ = http.Get(tampered)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tampered presign = %d", resp.StatusCode)
	}

	put, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("signed"), Key: aws.String("upload.txt")})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(put.Method, put.URL, strings.NewReader("uploaded via presigned PUT"))
	for k, v := range put.SignedHeader {
		req.Header[k] = v
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("presigned PUT = %d", resp.StatusCode)
	}
	if got := getBody(t, e, "signed", "upload.txt"); got != "uploaded via presigned PUT" {
		t.Fatalf("uploaded body = %q", got)
	}

	// Jump past the expiry.
	e.h.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	resp, _ = http.Get(get.URL)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "Request has expired") {
		t.Fatalf("expired presign = %d %s", resp.StatusCode, body)
	}
}

func TestSDKWrongCredentials(t *testing.T) {
	e := verifiedEnv(t)
	mustCreate(t, e, "secure")

	wrongSecret := sdkClient(e.srv.URL, testAK, "nope")
	_, err := wrongSecret.ListBuckets(t.Context(), &s3.ListBucketsInput{})
	if apiCode(err) != "SignatureDoesNotMatch" {
		t.Fatalf("wrong secret: %v", err)
	}
	wrongKey := sdkClient(e.srv.URL, "someone", testSecret)
	_, err = wrongKey.ListBuckets(t.Context(), &s3.ListBucketsInput{})
	if apiCode(err) != "InvalidAccessKeyId" {
		t.Fatalf("wrong key: %v", err)
	}

	// With verification off, anything goes.
	loose := newEnv(t, Config{})
	if _, err := sdkClient(loose.srv.URL, "x", "y").ListBuckets(t.Context(), &s3.ListBucketsInput{}); err != nil {
		t.Fatalf("auth off: %v", err)
	}
}

func TestSDKAutoCreateBuckets(t *testing.T) {
	e := newEnv(t, Config{AutoCreateBuckets: true})
	mustPut(t, e, "fresh", "k", "v")
	if got := getBody(t, e, "fresh", "k"); got != "v" {
		t.Fatal(got)
	}
	_, err := e.client.PutObject(context.Background(), &s3.PutObjectInput{Bucket: aws.String("Bad_Name"), Key: aws.String("k"), Body: strings.NewReader("v")})
	if err == nil {
		t.Fatal("auto-created an invalid bucket name")
	}
}
