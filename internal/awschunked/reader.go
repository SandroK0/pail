// Package awschunked decodes the aws-chunked body encoding that AWS SDKs use
// for streaming uploads and trailing checksums:
//
//	<hex-size>[;chunk-signature=<sig>]\r\n<data>\r\n ... 0[;...]\r\n[<trailer>:<value>\r\n...]\r\n
//
// Chunk and trailer signatures are not verified.
package awschunked

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

var ErrMalformed = errors.New("malformed aws-chunked body")

const maxLine = 4096

// Is reports whether r's body uses aws-chunked encoding.
func Is(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return true
	}
	for enc := range strings.SplitSeq(r.Header.Get("Content-Encoding"), ",") {
		if strings.TrimSpace(enc) == "aws-chunked" {
			return true
		}
	}
	return false
}

type Reader struct {
	br        *bufio.Reader
	remaining int64 // bytes left in the current chunk
	needCRLF  bool  // a chunk's data ended; its CRLF is unread
	done      bool
	err       error

	// Trailers holds trailing headers (such as x-amz-checksum-crc32) once
	// Read has returned io.EOF.
	Trailers http.Header
}

func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r), Trailers: http.Header{}}
}

func (c *Reader) Read(p []byte) (int, error) {
	for c.remaining == 0 {
		if c.err != nil {
			return 0, c.err
		}
		if c.done {
			return 0, io.EOF
		}
		if err := c.nextChunk(); err != nil {
			c.err = err
			return 0, err
		}
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.br.Read(p)
	c.remaining -= int64(n)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		c.err = err
	}
	return n, err
}

func (c *Reader) readLine() (string, error) {
	line, err := c.br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > maxLine {
		return "", fmt.Errorf("%w: line too long", ErrMalformed)
	}
	if err == io.EOF && len(line) > 0 {
		return "", io.ErrUnexpectedEOF
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(line), "\r\n"), nil
}

func (c *Reader) nextChunk() error {
	if c.needCRLF {
		line, err := c.readLine()
		if err != nil {
			return unexpected(err)
		}
		if line != "" {
			return fmt.Errorf("%w: missing CRLF after chunk", ErrMalformed)
		}
		c.needCRLF = false
	}
	line, err := c.readLine()
	if err != nil {
		return unexpected(err)
	}
	sizeHex, _, _ := strings.Cut(line, ";")
	size, err := strconv.ParseInt(strings.TrimSpace(sizeHex), 16, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("%w: bad chunk size %q", ErrMalformed, line)
	}
	if size > 0 {
		c.remaining, c.needCRLF = size, true
		return nil
	}
	// Last chunk: trailers until a blank line. Some clients stop at EOF.
	for {
		line, err := c.readLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("%w: bad trailer %q", ErrMalformed, line)
		}
		c.Trailers.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	c.done = true
	return nil
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
