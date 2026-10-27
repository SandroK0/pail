// Package sigv4 parses, verifies and creates AWS Signature Version 4
// signatures for S3, in both the Authorization-header and presigned-URL forms.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Algorithm       = "AWS4-HMAC-SHA256"
	UnsignedPayload = "UNSIGNED-PAYLOAD"
	TimeFormat      = "20060102T150405Z"
	dateFormat      = "20060102"
	service         = "s3"

	// MaxPresignExpiry is the longest X-Amz-Expires S3 accepts (7 days).
	MaxPresignExpiry = 7 * 24 * time.Hour
)

var (
	ErrMalformed         = errors.New("malformed authorization")
	ErrSignatureMismatch = errors.New("signature does not match")
	ErrUnsupported       = errors.New("unsupported signature version")
)

// Auth is the signature information carried by a request.
type Auth struct {
	AccessKey     string
	Date          string // yyyymmdd from the credential scope
	Region        string
	Service       string
	SignedHeaders []string
	Signature     string
	Time          time.Time // X-Amz-Date
	PayloadHash   string
	Presigned     bool
	Expires       time.Duration // presigned only
	V2            bool          // legacy "AWS key:sig" header; never verifiable here
}

// Expired reports whether a presigned request is past its expiry at now.
func (a *Auth) Expired(now time.Time) bool {
	return a.Presigned && now.After(a.Time.Add(a.Expires))
}

func (a *Auth) scope() string {
	return strings.Join([]string{a.Date, a.Region, a.Service, "aws4_request"}, "/")
}

// Parse extracts signature information from r. It returns nil, nil for an
// anonymous request.
func Parse(r *http.Request) (*Auth, error) {
	q := r.URL.Query()
	if q.Get("X-Amz-Algorithm") != "" || q.Get("X-Amz-Signature") != "" {
		return parseQuery(q)
	}
	h := r.Header.Get("Authorization")
	switch {
	case h == "":
		return nil, nil
	case strings.HasPrefix(h, "AWS "):
		ak, _, _ := strings.Cut(strings.TrimPrefix(h, "AWS "), ":")
		return &Auth{AccessKey: ak, V2: true}, nil
	case strings.HasPrefix(h, Algorithm+" "):
		return parseHeader(r, strings.TrimPrefix(h, Algorithm+" "))
	}
	return nil, ErrUnsupported
}

func parseCredential(a *Auth, cred string) error {
	parts := strings.Split(cred, "/")
	if len(parts) != 5 || parts[4] != "aws4_request" {
		return fmt.Errorf("%w: credential %q", ErrMalformed, cred)
	}
	a.AccessKey, a.Date, a.Region, a.Service = parts[0], parts[1], parts[2], parts[3]
	return nil
}

func parseHeader(r *http.Request, rest string) (*Auth, error) {
	a := &Auth{}
	fields := map[string]string{}
	for part := range strings.SplitSeq(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrMalformed, part)
		}
		fields[k] = v
	}
	if err := parseCredential(a, fields["Credential"]); err != nil {
		return nil, err
	}
	if fields["SignedHeaders"] == "" || fields["Signature"] == "" {
		return nil, fmt.Errorf("%w: missing SignedHeaders or Signature", ErrMalformed)
	}
	a.SignedHeaders = strings.Split(fields["SignedHeaders"], ";")
	a.Signature = fields["Signature"]

	var err error
	if d := r.Header.Get("X-Amz-Date"); d != "" {
		a.Time, err = time.Parse(TimeFormat, d)
	} else {
		a.Time, err = http.ParseTime(r.Header.Get("Date"))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: bad date", ErrMalformed)
	}
	a.PayloadHash = r.Header.Get("X-Amz-Content-Sha256")
	if a.PayloadHash == "" {
		a.PayloadHash = UnsignedPayload
	}
	return a, nil
}

func parseQuery(q url.Values) (*Auth, error) {
	if q.Get("X-Amz-Algorithm") != Algorithm {
		return nil, ErrUnsupported
	}
	a := &Auth{Presigned: true, Signature: q.Get("X-Amz-Signature"), PayloadHash: UnsignedPayload}
	if err := parseCredential(a, q.Get("X-Amz-Credential")); err != nil {
		return nil, err
	}
	var err error
	if a.Time, err = time.Parse(TimeFormat, q.Get("X-Amz-Date")); err != nil {
		return nil, fmt.Errorf("%w: bad X-Amz-Date", ErrMalformed)
	}
	secs, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || secs < 1 || time.Duration(secs)*time.Second > MaxPresignExpiry {
		return nil, fmt.Errorf("%w: bad X-Amz-Expires", ErrMalformed)
	}
	a.Expires = time.Duration(secs) * time.Second
	if a.Signature == "" || q.Get("X-Amz-SignedHeaders") == "" {
		return nil, fmt.Errorf("%w: missing signature", ErrMalformed)
	}
	a.SignedHeaders = strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	if h := q.Get("X-Amz-Content-Sha256"); h != "" {
		a.PayloadHash = h
	}
	return a, nil
}

// Verify checks a's signature for r using secret. The payload hash is taken
// as declared; the body itself is not hashed.
func Verify(r *http.Request, a *Auth, secret string) error {
	if a.V2 {
		return ErrUnsupported
	}
	canonical := canonicalRequest(r.Method, r.URL.Path, r.URL.Query(), a.SignedHeaders, requestHeader(r), a.PayloadHash, a.Presigned)
	want := signature(secret, a, canonical)
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(a.Signature))) {
		return ErrSignatureMismatch
	}
	return nil
}

// requestHeader reads a header as the client sent it. Go moves Host,
// Content-Length and Transfer-Encoding out of r.Header on the server side.
func requestHeader(r *http.Request) func(string) string {
	return func(name string) string {
		switch name {
		case "host":
			if r.Host != "" {
				return r.Host
			}
			return r.URL.Host
		case "content-length":
			if v := r.Header.Get(name); v != "" {
				return v
			}
			if r.ContentLength >= 0 {
				return strconv.FormatInt(r.ContentLength, 10)
			}
		case "transfer-encoding":
			if v := r.Header.Get(name); v != "" {
				return v
			}
			return strings.Join(r.TransferEncoding, ",")
		}
		vals := r.Header.Values(name)
		for i, v := range vals {
			vals[i] = strings.Join(strings.Fields(v), " ")
		}
		return strings.Join(vals, ",")
	}
}

func canonicalRequest(method, path string, query url.Values, signed []string, header func(string) string, payload string, presigned bool) string {
	uri := URIEncode(path, false)
	if uri == "" {
		uri = "/"
	}
	var b strings.Builder
	for _, h := range signed {
		b.WriteString(h)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(header(h)))
		b.WriteByte('\n')
	}
	return strings.Join([]string{
		method,
		uri,
		canonicalQuery(query, presigned),
		b.String(),
		strings.Join(signed, ";"),
		payload,
	}, "\n")
}

func canonicalQuery(q url.Values, presigned bool) string {
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		if presigned && k == "X-Amz-Signature" {
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, URIEncode(k, true)+"="+URIEncode(v, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func signature(secret string, a *Auth, canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	toSign := strings.Join([]string{Algorithm, a.Time.UTC().Format(TimeFormat), a.scope(), hex.EncodeToString(sum[:])}, "\n")
	key := hmacSHA256([]byte("AWS4"+secret), a.Date)
	key = hmacSHA256(key, a.Region)
	key = hmacSHA256(key, a.Service)
	key = hmacSHA256(key, "aws4_request")
	return hex.EncodeToString(hmacSHA256(key, toSign))
}

// URIEncode percent-encodes s the way SigV4 does: everything except
// A-Z a-z 0-9 - . _ ~ (and / unless encodeSlash).
func URIEncode(s string, encodeSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

// Sign adds SigV4 Authorization headers to a client request, signing host,
// x-amz-content-sha256 and x-amz-date with an unsigned payload.
func Sign(r *http.Request, accessKey, secret, region string, t time.Time) {
	t = t.UTC()
	r.Header.Set("X-Amz-Date", t.Format(TimeFormat))
	r.Header.Set("X-Amz-Content-Sha256", UnsignedPayload)
	a := &Auth{
		AccessKey: accessKey, Date: t.Format(dateFormat), Region: region, Service: service,
		SignedHeaders: []string{"host", "x-amz-content-sha256", "x-amz-date"},
		Time:          t, PayloadHash: UnsignedPayload,
	}
	canonical := canonicalRequest(r.Method, r.URL.Path, r.URL.Query(), a.SignedHeaders, requestHeader(r), a.PayloadHash, false)
	r.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		Algorithm, accessKey, a.scope(), strings.Join(a.SignedHeaders, ";"), signature(secret, a, canonical)))
}

// Presign returns rawURL with SigV4 query authentication valid for expires.
func Presign(method, rawURL, accessKey, secret, region string, t time.Time, expires time.Duration) (string, error) {
	if expires < time.Second || expires > MaxPresignExpiry {
		return "", fmt.Errorf("expiry must be between 1s and %s", MaxPresignExpiry)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	t = t.UTC()
	a := &Auth{
		AccessKey: accessKey, Date: t.Format(dateFormat), Region: region, Service: service,
		SignedHeaders: []string{"host"}, Time: t, PayloadHash: UnsignedPayload,
		Presigned: true, Expires: expires,
	}
	q := u.Query()
	q.Set("X-Amz-Algorithm", Algorithm)
	q.Set("X-Amz-Credential", accessKey+"/"+a.scope())
	q.Set("X-Amz-Date", t.Format(TimeFormat))
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")
	host := func(string) string { return u.Host }
	canonical := canonicalRequest(method, u.Path, q, a.SignedHeaders, host, UnsignedPayload, true)
	path := URIEncode(u.Path, false)
	if path == "" {
		path = "/"
	}
	return fmt.Sprintf("%s://%s%s?%s&X-Amz-Signature=%s",
		u.Scheme, u.Host, path, canonicalQuery(q, true), signature(secret, a, canonical)), nil
}
