package adminapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SandroK0/pail/internal/sigv4"
)

type stepJSON struct {
	Name       string  `json:"name"`
	OK         bool    `json:"ok"`
	Status     int     `json:"status"`
	DurationMs float64 `json:"durationMs"`
	Error      string  `json:"error,omitempty"`
}

// selftest makes signed S3 requests to the public endpoint with the
// configured credentials, exactly as Laravel would, and reports each step.
func (a *API) selftest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Bucket string `json:"bucket"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Bucket == "" {
		writeError(w, http.StatusBadRequest, "bucket is required")
		return
	}
	b := make([]byte, 6)
	rand.Read(b)
	objectURL := a.objectURL(body.Bucket, ".pail-selftest/"+hex.EncodeToString(b)+".txt")
	const payload = "hello from pail"

	steps := []struct {
		name, method, url string
		body              string
		check             func(*http.Response, string) string
	}{
		{"HeadBucket", http.MethodHead, a.cfg.PublicURL + "/" + body.Bucket, "", nil},
		{"PutObject", http.MethodPut, objectURL, payload, nil},
		{"GetObject", http.MethodGet, objectURL, "", func(_ *http.Response, got string) string {
			if got != payload {
				return fmt.Sprintf("read back %q", got)
			}
			return ""
		}},
		{"DeleteObject", http.MethodDelete, objectURL, "", nil},
	}

	out := []stepJSON{}
	for _, s := range steps {
		start := time.Now()
		step := stepJSON{Name: s.name}
		req, err := http.NewRequestWithContext(r.Context(), s.method, s.url, strings.NewReader(s.body))
		if err == nil {
			req.Header.Set("User-Agent", "pail-selftest")
			sigv4.Sign(req, a.cfg.AccessKey, a.cfg.SecretKey, a.cfg.Region, a.now())
			var resp *http.Response
			if resp, err = a.client.Do(req); err == nil {
				got, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				step.Status = resp.StatusCode
				switch {
				case resp.StatusCode >= 300:
					step.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
					if code := between(string(got), "<Code>", "</Code>"); code != "" {
						step.Error += " " + code
					}
				case s.check != nil:
					step.Error = s.check(resp, string(got))
				}
			}
		}
		if err != nil {
			step.Error = err.Error()
		}
		step.OK = step.Error == ""
		step.DurationMs = float64(time.Since(start).Microseconds()) / 1000
		out = append(out, step)
		if !step.OK {
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func between(s, open, close string) string {
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, close)
	return v
}
