package store

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultipartUpload(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")

	id, err := s.CreateMultipartUpload(t.Context(), "bk1", "big.bin", PutOptions{ContentType: "video/mp4", ACL: "public-read"})
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{"aaa", "bbbb", "c"}
	var completed []CompletedPart
	for i, p := range parts {
		etag, err := s.UploadPart(t.Context(), "bk1", "big.bin", id, i+1, strings.NewReader(p), nil)
		if err != nil {
			t.Fatal(err)
		}
		if etag != md5hex(p) {
			t.Fatalf("part etag = %s", etag)
		}
		completed = append(completed, CompletedPart{Number: i + 1, ETag: `"` + strings.ToUpper(etag) + `"`})
	}
	// Re-uploading a part replaces it.
	if _, err := s.UploadPart(t.Context(), "bk1", "big.bin", id, 2, strings.NewReader("BBBB"), nil); err != nil {
		t.Fatal(err)
	}
	completed[1].ETag = md5hex("BBBB")

	listed, err := s.ListParts(t.Context(), "bk1", "big.bin", id)
	if err != nil || len(listed) != 3 || listed[1].Size != 4 {
		t.Fatalf("ListParts = %+v, %v", listed, err)
	}
	uploads, err := s.ListMultipartUploads(t.Context(), "bk1", "big")
	if err != nil || len(uploads) != 1 || uploads[0].ID != id {
		t.Fatalf("ListMultipartUploads = %+v, %v", uploads, err)
	}

	obj, err := s.CompleteMultipartUpload(t.Context(), "bk1", "big.bin", id, completed)
	if err != nil {
		t.Fatal(err)
	}
	h := md5.New()
	for _, p := range []string{"aaa", "BBBB", "c"} {
		sum := md5.Sum([]byte(p))
		h.Write(sum[:])
	}
	wantETag := fmt.Sprintf("%s-3", hex.EncodeToString(h.Sum(nil)))
	if obj.ETag != wantETag || obj.Size != 8 || obj.ContentType != "video/mp4" || !obj.Public() {
		t.Fatalf("obj = %+v, want etag %s", obj, wantETag)
	}
	if read(t, s, "bk1", "big.bin") != "aaaBBBBc" {
		t.Fatal("body mismatch")
	}
	if _, err := os.Stat(filepath.Join(s.multipartDir(), id)); !os.IsNotExist(err) {
		t.Fatal("part files left behind")
	}
	if _, err := s.ListParts(t.Context(), "bk1", "big.bin", id); !errors.Is(err, ErrNoSuchUpload) {
		t.Fatalf("upload should be gone: %v", err)
	}
}

func TestMultipartSubsetOfParts(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	id, _ := s.CreateMultipartUpload(t.Context(), "bk1", "k", PutOptions{})
	for n, p := range map[int]string{1: "one", 2: "two", 5: "five"} {
		s.UploadPart(t.Context(), "bk1", "k", id, n, strings.NewReader(p), nil)
	}
	_, err := s.CompleteMultipartUpload(t.Context(), "bk1", "k", id, []CompletedPart{
		{1, md5hex("one")}, {5, md5hex("five")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, s, "bk1", "k") != "onefive" {
		t.Fatal("body mismatch")
	}
}

func TestMultipartErrors(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	c := t.Context()

	if _, err := s.CreateMultipartUpload(c, "nope", "k", PutOptions{}); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
	id, _ := s.CreateMultipartUpload(c, "bk1", "k", PutOptions{})
	s.UploadPart(c, "bk1", "k", id, 1, strings.NewReader("one"), nil)
	s.UploadPart(c, "bk1", "k", id, 2, strings.NewReader("two"), nil)

	cases := []struct {
		name string
		err  error
		fn   func() error
	}{
		{"unknown upload", ErrNoSuchUpload, func() error {
			_, err := s.UploadPart(c, "bk1", "k", "nope", 1, strings.NewReader("x"), nil)
			return err
		}},
		{"upload for another key", ErrNoSuchUpload, func() error {
			_, err := s.UploadPart(c, "bk1", "other", id, 1, strings.NewReader("x"), nil)
			return err
		}},
		{"part number 0", ErrInvalidPart, func() error {
			_, err := s.UploadPart(c, "bk1", "k", id, 0, strings.NewReader("x"), nil)
			return err
		}},
		{"part number too big", ErrInvalidPart, func() error {
			_, err := s.UploadPart(c, "bk1", "k", id, MaxPartNumber+1, strings.NewReader("x"), nil)
			return err
		}},
		{"bad part md5", ErrBadDigest, func() error {
			sum := md5.Sum([]byte("y"))
			_, err := s.UploadPart(c, "bk1", "k", id, 3, strings.NewReader("x"), sum[:])
			return err
		}},
		{"no parts", ErrInvalidPart, func() error {
			_, err := s.CompleteMultipartUpload(c, "bk1", "k", id, nil)
			return err
		}},
		{"wrong etag", ErrInvalidPart, func() error {
			_, err := s.CompleteMultipartUpload(c, "bk1", "k", id, []CompletedPart{{1, md5hex("nope")}})
			return err
		}},
		{"missing part", ErrInvalidPart, func() error {
			_, err := s.CompleteMultipartUpload(c, "bk1", "k", id, []CompletedPart{{1, md5hex("one")}, {9, md5hex("one")}})
			return err
		}},
		{"out of order", ErrInvalidPartOrder, func() error {
			_, err := s.CompleteMultipartUpload(c, "bk1", "k", id, []CompletedPart{{2, md5hex("two")}, {1, md5hex("one")}})
			return err
		}},
		{"duplicate part", ErrInvalidPartOrder, func() error {
			_, err := s.CompleteMultipartUpload(c, "bk1", "k", id, []CompletedPart{{1, md5hex("one")}, {1, md5hex("one")}})
			return err
		}},
	}
	for _, tc := range cases {
		if err := tc.fn(); !errors.Is(err, tc.err) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.err)
		}
	}

	// The upload survives failed completes and can still be aborted.
	if err := s.AbortMultipartUpload(c, "bk1", "k", id); err != nil {
		t.Fatal(err)
	}
	if err := s.AbortMultipartUpload(c, "bk1", "k", id); !errors.Is(err, ErrNoSuchUpload) {
		t.Fatalf("double abort: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.multipartDir(), id)); !os.IsNotExist(err) {
		t.Fatal("aborted parts left behind")
	}
	if _, err := s.HeadObject(c, "bk1", "k"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatal("aborted upload created an object")
	}
}

func TestDeleteBucketDropsUploads(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	id, _ := s.CreateMultipartUpload(t.Context(), "bk1", "k", PutOptions{})
	s.UploadPart(t.Context(), "bk1", "k", id, 1, strings.NewReader("x"), nil)
	if err := s.DeleteBucket(t.Context(), "bk1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.multipartDir(), id)); !os.IsNotExist(err) {
		t.Fatal("parts left behind")
	}
}
