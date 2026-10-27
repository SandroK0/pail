package awschunked

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     string
		trailers map[string]string
	}{
		{
			name: "signed chunks",
			in: "5;chunk-signature=aaaa\r\nhello\r\n" +
				"6;chunk-signature=bbbb\r\n world\r\n" +
				"0;chunk-signature=cccc\r\n\r\n",
			want: "hello world",
		},
		{
			name: "unsigned with trailing checksum",
			in: "b\r\nhello world\r\n" +
				"0\r\nx-amz-checksum-crc32:DUoRhQ==\r\nx-amz-trailer-signature:abc\r\n\r\n",
			want:     "hello world",
			trailers: map[string]string{"X-Amz-Checksum-Crc32": "DUoRhQ==", "X-Amz-Trailer-Signature": "abc"},
		},
		{
			name: "empty body",
			in:   "0;chunk-signature=cccc\r\n\r\n",
			want: "",
		},
		{
			name: "ends at EOF without final blank line",
			in:   "3\r\nabc\r\n0\r\n",
			want: "abc",
		},
		{
			name: "uppercase hex size",
			in:   "A\r\n0123456789\r\n0\r\n\r\n",
			want: "0123456789",
		},
		{
			name: "binary data containing CRLF",
			in:   "4\r\n\r\n\r\n\r\n0\r\n\r\n",
			want: "\r\n\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, wrap := range []func(io.Reader) io.Reader{
				func(r io.Reader) io.Reader { return r },
				iotest.OneByteReader,
				iotest.HalfReader,
			} {
				r := NewReader(wrap(strings.NewReader(tt.in)))
				got, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tt.want {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
				for k, v := range tt.trailers {
					if r.Trailers.Get(k) != v {
						t.Errorf("trailer %s = %q, want %q", k, r.Trailers.Get(k), v)
					}
				}
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := map[string]string{
		"bad size":       "zz\r\nabc\r\n0\r\n\r\n",
		"truncated data": "a\r\nabc",
		"missing CRLF":   "3\r\nabcX\r\n0\r\n\r\n",
		"no final chunk": "3\r\nabc\r\n",
		"bad trailer":    "0\r\nnot-a-header\r\n\r\n",
		"line too long":  strings.Repeat("1", maxLine+10) + "\r\n",
		"negative size":  "-1\r\n",
		"truncated line": "3",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := io.ReadAll(NewReader(strings.NewReader(in)))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, ErrMalformed) && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestIs(t *testing.T) {
	tests := []struct {
		header, value string
		want          bool
	}{
		{"X-Amz-Content-Sha256", "STREAMING-AWS4-HMAC-SHA256-PAYLOAD", true},
		{"X-Amz-Content-Sha256", "STREAMING-UNSIGNED-PAYLOAD-TRAILER", true},
		{"X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD", false},
		{"Content-Encoding", "aws-chunked", true},
		{"Content-Encoding", "gzip, aws-chunked", true},
		{"Content-Encoding", "gzip", false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("PUT", "/", nil)
		r.Header.Set(tt.header, tt.value)
		if got := Is(r); got != tt.want {
			t.Errorf("%s: %s → %v, want %v", tt.header, tt.value, got, tt.want)
		}
	}
}
