package sigv4

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Example credentials and requests from the AWS S3 SigV4 documentation.
const (
	exampleAK     = "AKIAIOSFODNN7EXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	emptySHA256   = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func TestVerifyAWSHeaderExample(t *testing.T) {
	r := httptest.NewRequest("GET", "http://examplebucket.s3.amazonaws.com/test.txt", nil)
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", emptySHA256)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")

	a, err := Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if a.AccessKey != exampleAK || a.Region != "us-east-1" || a.Presigned || !a.Time.Equal(exampleTime) {
		t.Fatalf("auth = %+v", a)
	}
	if err := Verify(r, a, exampleSecret); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify(r, a, "wrong"); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("wrong secret: %v", err)
	}
	r.Header.Set("Range", "bytes=0-10")
	if err := Verify(r, a, exampleSecret); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("tampered header: %v", err)
	}
}

const examplePresigned = "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"

func TestPresignAWSExample(t *testing.T) {
	got, err := Presign("GET", "https://examplebucket.s3.amazonaws.com/test.txt", exampleAK, exampleSecret, "us-east-1", exampleTime, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got != examplePresigned {
		t.Fatalf("presigned URL\n got %s\nwant %s", got, examplePresigned)
	}
}

func TestVerifyAWSPresignedExample(t *testing.T) {
	r := httptest.NewRequest("GET", examplePresigned, nil)
	a, err := Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Presigned || a.Expires != 24*time.Hour {
		t.Fatalf("auth = %+v", a)
	}
	if err := Verify(r, a, exampleSecret); err != nil {
		t.Fatal(err)
	}
	if a.Expired(exampleTime.Add(24 * time.Hour)) {
		t.Error("expired exactly at the deadline")
	}
	if !a.Expired(exampleTime.Add(24*time.Hour + time.Second)) {
		t.Error("not expired after the deadline")
	}

	tampered := httptest.NewRequest("GET", strings.Replace(examplePresigned, "test.txt", "other.txt", 1), nil)
	a, _ = Parse(tampered)
	if err := Verify(tampered, a, exampleSecret); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("tampered path: %v", err)
	}
}

func TestSignRoundTrip(t *testing.T) {
	for _, path := range []string{"/bucket/plain.txt", "/bucket/with space/ü+%.txt", "/bucket", "/"} {
		u := "http://localhost:8080" + (&url.URL{Path: path}).EscapedPath() + "?list-type=2&prefix=a%2Fb&delimiter=%2F"
		client, _ := http.NewRequest("PUT", u, nil)
		Sign(client, "local", "secret", "us-east-1", time.Now())

		// Replay the request as the server would see it.
		server := httptest.NewRequest("PUT", u, nil)
		server.Header = client.Header.Clone()
		a, err := Parse(server)
		if err != nil {
			t.Fatal(err)
		}
		if err := Verify(server, a, "secret"); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestPresignRoundTrip(t *testing.T) {
	signed, err := Presign("PUT", "http://localhost:8080/bucket/dir/a b+c.txt?response-content-type=text/plain", "local", "secret", "us-east-1", time.Now(), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", signed, nil)
	if r.URL.Path != "/bucket/dir/a b+c.txt" || r.URL.Query().Get("response-content-type") != "text/plain" {
		t.Fatalf("presigned URL decodes to %q %v", r.URL.Path, r.URL.Query())
	}
	a, err := Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(r, a, "secret"); err != nil {
		t.Fatal(err)
	}
	r.Method = "DELETE"
	if err := Verify(r, a, "secret"); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatal("method change not detected")
	}
}

func TestPresignRejectsBadExpiry(t *testing.T) {
	for _, d := range []time.Duration{0, MaxPresignExpiry + time.Second} {
		if _, err := Presign("GET", "http://x/b/k", "a", "s", "us-east-1", time.Now(), d); err == nil {
			t.Errorf("expiry %s accepted", d)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		setup func(r *http.Request)
		url   string
		anon  bool
		v2    bool
		ak    string
		errIs error
	}{
		{name: "anonymous", setup: func(*http.Request) {}, anon: true},
		{name: "v2", setup: func(r *http.Request) { r.Header.Set("Authorization", "AWS mykey:sig") }, v2: true, ak: "mykey"},
		{name: "unknown scheme", setup: func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }, errIs: ErrUnsupported},
		{name: "bad credential", setup: func(r *http.Request) {
			r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ak/2013/us-east-1, SignedHeaders=host, Signature=abc")
			r.Header.Set("X-Amz-Date", "20130524T000000Z")
		}, errIs: ErrMalformed},
		{name: "missing date", setup: func(r *http.Request) {
			r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ak/20130524/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc")
		}, errIs: ErrMalformed},
		{name: "date header fallback", setup: func(r *http.Request) {
			r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=ak/20130524/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc")
			r.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
		}, ak: "ak"},
		{name: "presign bad expiry", url: "/b/k?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=ak%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=999999999&X-Amz-SignedHeaders=host&X-Amz-Signature=abc", errIs: ErrMalformed},
		{name: "presign wrong algorithm", url: "/b/k?X-Amz-Algorithm=HMAC&X-Amz-Signature=abc", errIs: ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := tt.url
			if u == "" {
				u = "/b/k"
			}
			r := httptest.NewRequest("GET", u, nil)
			if tt.setup != nil {
				tt.setup(r)
			}
			a, err := Parse(r)
			if tt.errIs != nil {
				if !errors.Is(err, tt.errIs) {
					t.Fatalf("err = %v, want %v", err, tt.errIs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.anon {
				if a != nil {
					t.Fatalf("auth = %+v, want nil", a)
				}
				return
			}
			if a.V2 != tt.v2 || a.AccessKey != tt.ak {
				t.Fatalf("auth = %+v", a)
			}
		})
	}
}

func TestVerifyRejectsV2(t *testing.T) {
	r := httptest.NewRequest("GET", "/b/k", nil)
	if err := Verify(r, &Auth{V2: true}, "s"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestURIEncode(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"a b/c", ""}:      "a%20b/c",
		{"a b/c", "s"}:     "a%20b%2Fc",
		{"~-._AZaz09", ""}: "~-._AZaz09",
		{"+=&?ü", ""}:      "%2B%3D%26%3F%C3%BC",
	} {
		if got := URIEncode(in[0], in[1] != ""); got != want {
			t.Errorf("URIEncode(%q) = %q, want %q", in[0], got, want)
		}
	}
}
