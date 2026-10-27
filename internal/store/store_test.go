package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var ctx = context.Background()

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newBucket(t *testing.T, s *Store, name string) {
	t.Helper()
	if err := s.CreateBucket(ctx, name); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, s *Store, bucket, key, body string) *Object {
	t.Helper()
	obj, err := s.PutObject(ctx, bucket, key, strings.NewReader(body), PutOptions{})
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return obj
}

func read(t *testing.T, s *Store, bucket, key string) string {
	t.Helper()
	_, f, err := s.GetObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func countBlobs(t *testing.T, s *Store) int {
	t.Helper()
	n := 0
	filepath.WalkDir(s.blobDir(), func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestBucketLifecycle(t *testing.T) {
	s := newStore(t)

	if err := s.CreateBucket(ctx, "Bad_Name"); !errors.Is(err, ErrInvalidBucketName) {
		t.Fatalf("invalid name: got %v", err)
	}
	newBucket(t, s, "photos")
	if err := s.CreateBucket(ctx, "photos"); !errors.Is(err, ErrBucketExists) {
		t.Fatalf("duplicate: got %v", err)
	}
	newBucket(t, s, "avatars")

	put(t, s, "photos", "a.jpg", "12345")
	put(t, s, "photos", "b.jpg", "123")

	buckets, err := s.ListBuckets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || buckets[0].Name != "avatars" || buckets[1].Name != "photos" {
		t.Fatalf("buckets = %+v", buckets)
	}
	if buckets[1].ObjectCount != 2 || buckets[1].Size != 8 {
		t.Fatalf("stats = %+v", buckets[1])
	}

	b, err := s.GetBucket(ctx, "photos")
	if err != nil || b.ObjectCount != 2 {
		t.Fatalf("GetBucket = %+v, %v", b, err)
	}
	if _, err := s.GetBucket(ctx, "nope"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("GetBucket missing: %v", err)
	}

	if err := s.DeleteBucket(ctx, "photos"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Fatalf("delete non-empty: %v", err)
	}
	if err := s.DeleteBucket(ctx, "avatars"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket(ctx, "avatars"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestValidBucketName(t *testing.T) {
	for name, want := range map[string]bool{
		"abc": true, "my-bucket.v2": true, "a1": false, "ab": false,
		"-abc": false, "abc-": false, "a..b": false, "UPPER": false,
		"under_score": false, "192.168.1.1": false, strings.Repeat("a", 63): true,
		strings.Repeat("a", 64): false, "_api": false,
	} {
		if got := ValidBucketName(name); got != want {
			t.Errorf("ValidBucketName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPutGetOverwrite(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")

	obj, err := s.PutObject(ctx, "bk1", "dir/file.txt", strings.NewReader("hello"), PutOptions{
		ContentType: "text/plain",
		Metadata:    map[string]string{"author": "me"},
		Headers:     map[string]string{"Cache-Control": "max-age=60"},
		ACL:         "public-read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obj.ETag != md5hex("hello") || obj.Size != 5 {
		t.Fatalf("obj = %+v", obj)
	}

	got, err := s.HeadObject(ctx, "bk1", "dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentType != "text/plain" || got.Metadata["author"] != "me" ||
		got.Headers["Cache-Control"] != "max-age=60" || !got.Public() {
		t.Fatalf("head = %+v", got)
	}
	if read(t, s, "bk1", "dir/file.txt") != "hello" {
		t.Fatal("body mismatch")
	}

	put(t, s, "bk1", "dir/file.txt", "goodbye")
	if read(t, s, "bk1", "dir/file.txt") != "goodbye" {
		t.Fatal("overwrite body mismatch")
	}
	got, _ = s.HeadObject(ctx, "bk1", "dir/file.txt")
	if got.ContentType != "binary/octet-stream" || got.ACL != "private" {
		t.Fatalf("overwrite should reset metadata: %+v", got)
	}
	if n := countBlobs(t, s); n != 1 {
		t.Fatalf("overwrite left %d blobs, want 1", n)
	}
}

func TestPutErrors(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")

	if _, err := s.PutObject(ctx, "missing", "k", strings.NewReader("x"), PutOptions{}); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
	for _, key := range []string{"", strings.Repeat("k", MaxKeyLength+1), "bad\xff"} {
		if _, err := s.PutObject(ctx, "bk1", key, strings.NewReader("x"), PutOptions{}); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	wrong := md5.Sum([]byte("other"))
	if _, err := s.PutObject(ctx, "bk1", "k", strings.NewReader("x"), PutOptions{ContentMD5: wrong[:]}); !errors.Is(err, ErrBadDigest) {
		t.Fatalf("bad digest: %v", err)
	}
	right := md5.Sum([]byte("x"))
	if _, err := s.PutObject(ctx, "bk1", "k", strings.NewReader("x"), PutOptions{ContentMD5: right[:]}); err != nil {
		t.Fatalf("good digest: %v", err)
	}
	if n := countBlobs(t, s); n != 1 {
		t.Fatalf("failed puts left blobs behind: %d", n)
	}
}

func TestKeysThatAreNotPaths(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")

	// None of these could coexist as plain files on disk.
	keys := map[string]string{
		"a":              "file a",
		"a/b":            "file under a",
		"a/":             "",
		"../../etc/pass": "traversal",
		"a//b":           "double slash",
		"spaces and ü":   "unicode",
	}
	for k, v := range keys {
		put(t, s, "bk1", k, v)
	}
	for k, v := range keys {
		if got := read(t, s, "bk1", k); got != v {
			t.Errorf("%q = %q, want %q", k, got, v)
		}
	}
}

func TestGetMissing(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	if _, _, err := s.GetObject(ctx, "bk1", "nope"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("missing key: %v", err)
	}
	if _, _, err := s.GetObject(ctx, "nope", "k"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	put(t, s, "bk1", "k", "v")

	if err := s.DeleteObject(ctx, "bk1", "k"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObject(ctx, "bk1", "k"); err != nil {
		t.Fatalf("deleting a missing key should succeed: %v", err)
	}
	if err := s.DeleteObject(ctx, "nope", "k"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
	if n := countBlobs(t, s); n != 0 {
		t.Fatalf("blobs left: %d", n)
	}
}

func TestDeletePrefix(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	for _, k := range []string{"logs/1", "logs/2", "logs/sub/3", "logsx", "other"} {
		put(t, s, "bk1", k, "x")
	}
	n, err := s.DeletePrefix(ctx, "bk1", "logs/")
	if err != nil || n != 3 {
		t.Fatalf("DeletePrefix = %d, %v", n, err)
	}
	res, _ := s.ListObjects(ctx, "bk1", ListOptions{})
	if got := keysOf(res); !reflect.DeepEqual(got, []string{"logsx", "other"}) {
		t.Fatalf("left = %v", got)
	}
	if countBlobs(t, s) != 2 {
		t.Fatal("blobs not removed")
	}
}

func TestCopy(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "src")
	newBucket(t, s, "dst")
	s.PutObject(ctx, "src", "k", strings.NewReader("data"), PutOptions{
		ContentType: "text/plain", Metadata: map[string]string{"a": "1"}, ACL: "public-read",
	})

	obj, err := s.CopyObject(ctx, "src", "k", "dst", "copy", CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if obj.ContentType != "text/plain" || obj.Metadata["a"] != "1" || obj.ACL != "private" || obj.ETag != md5hex("data") {
		t.Fatalf("copy = %+v", obj)
	}
	if read(t, s, "dst", "copy") != "data" {
		t.Fatal("copy body")
	}

	obj, err = s.CopyObject(ctx, "src", "k", "src", "k", CopyOptions{
		ReplaceMetadata: true, ContentType: "image/png", Metadata: map[string]string{"b": "2"}, ACL: "public-read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obj.ContentType != "image/png" || obj.Metadata["b"] != "2" || obj.Metadata["a"] != "" {
		t.Fatalf("replace = %+v", obj)
	}
	if read(t, s, "src", "k") != "data" {
		t.Fatal("copy onto itself lost the body")
	}

	if _, err := s.CopyObject(ctx, "src", "missing", "dst", "x", CopyOptions{}); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("missing source: %v", err)
	}
}

func TestRename(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	put(t, s, "bk1", "old", "1")
	put(t, s, "bk1", "taken", "2")

	if err := s.RenameObject(ctx, "bk1", "old", "taken"); err != nil {
		t.Fatal(err)
	}
	if read(t, s, "bk1", "taken") != "1" {
		t.Fatal("rename did not replace target")
	}
	if _, err := s.HeadObject(ctx, "bk1", "old"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatal("old key still exists")
	}
	if countBlobs(t, s) != 1 {
		t.Fatal("replaced blob not removed")
	}
	if err := s.RenameObject(ctx, "bk1", "nope", "x"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("missing source: %v", err)
	}
	if err := s.RenameObject(ctx, "bk1", "taken", "taken"); err != nil {
		t.Fatalf("rename onto itself: %v", err)
	}
}

func TestSetACL(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	put(t, s, "bk1", "k", "v")
	if err := s.SetACL(ctx, "bk1", "k", "public-read"); err != nil {
		t.Fatal(err)
	}
	obj, _ := s.HeadObject(ctx, "bk1", "k")
	if !obj.Public() {
		t.Fatal("not public")
	}
	if err := s.SetACL(ctx, "bk1", "nope", "private"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("missing key: %v", err)
	}
	if err := s.SetACL(ctx, "nope", "k", "private"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
}

func keysOf(res *ListResult) []string {
	keys := []string{}
	for _, o := range res.Objects {
		keys = append(keys, o.Key)
	}
	return keys
}

func TestListObjects(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	for _, k := range []string{
		"a.txt", "b/1.txt", "b/2.txt", "b/c/3.txt", "b/c/4.txt", "b/d/", "b0.txt", "c", "photos/2026/x.jpg",
	} {
		put(t, s, "bk1", k, "x")
	}

	tests := []struct {
		name     string
		opts     ListOptions
		keys     []string
		prefixes []string
	}{
		{"all", ListOptions{}, []string{"a.txt", "b/1.txt", "b/2.txt", "b/c/3.txt", "b/c/4.txt", "b/d/", "b0.txt", "c", "photos/2026/x.jpg"}, []string{}},
		{"root with delimiter", ListOptions{Delimiter: "/"}, []string{"a.txt", "b0.txt", "c"}, []string{"b/", "photos/"}},
		{"folder", ListOptions{Prefix: "b/", Delimiter: "/"}, []string{"b/1.txt", "b/2.txt"}, []string{"b/c/", "b/d/"}},
		{"prefix without delimiter", ListOptions{Prefix: "b"}, []string{"b/1.txt", "b/2.txt", "b/c/3.txt", "b/c/4.txt", "b/d/", "b0.txt"}, []string{}},
		{"folder marker is listed inside itself", ListOptions{Prefix: "b/d/", Delimiter: "/"}, []string{"b/d/"}, []string{}},
		{"start after", ListOptions{StartAfter: "b/c/3.txt"}, []string{"b/c/4.txt", "b/d/", "b0.txt", "c", "photos/2026/x.jpg"}, []string{}},
		{"start after common prefix", ListOptions{Delimiter: "/", StartAfter: "b/"}, []string{"b0.txt", "c"}, []string{"photos/"}},
		{"no matches", ListOptions{Prefix: "zzz"}, []string{}, []string{}},
		{"multi-char delimiter", ListOptions{Delimiter: "/c/"}, []string{"a.txt", "b/1.txt", "b/2.txt", "b/d/", "b0.txt", "c", "photos/2026/x.jpg"}, []string{"b/c/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := s.ListObjects(ctx, "bk1", tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := keysOf(res); !reflect.DeepEqual(got, tt.keys) {
				t.Errorf("keys = %v, want %v", got, tt.keys)
			}
			if !reflect.DeepEqual(res.CommonPrefixes, tt.prefixes) {
				t.Errorf("prefixes = %v, want %v", res.CommonPrefixes, tt.prefixes)
			}
			if res.IsTruncated {
				t.Error("unexpectedly truncated")
			}
		})
	}

	if _, err := s.ListObjects(ctx, "nope", ListOptions{}); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
}

func TestListPagination(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	// More than one internal batch, mixed with folders.
	var want []string
	for i := range 1203 {
		k := fmt.Sprintf("f%04d", i)
		put(t, s, "bk1", k, "")
		want = append(want, k)
		if i%100 == 0 {
			dir := fmt.Sprintf("g%04d/", i)
			for j := range 3 {
				put(t, s, "bk1", fmt.Sprintf("%s%d", dir, j), "")
			}
			want = append(want, dir)
		}
	}
	// Folders sort after every f-key.
	var sorted []string
	for _, k := range want {
		if strings.HasPrefix(k, "f") {
			sorted = append(sorted, k)
		}
	}
	for _, k := range want {
		if strings.HasPrefix(k, "g") {
			sorted = append(sorted, k)
		}
	}

	for _, pageSize := range []int{1, 7, 1000} {
		var got []string
		marker := ""
		pages := 0
		for {
			res, err := s.ListObjects(ctx, "bk1", ListOptions{Delimiter: "/", StartAfter: marker, MaxKeys: pageSize})
			if err != nil {
				t.Fatal(err)
			}
			pages++
			got = append(got, keysOf(res)...)
			got = append(got, res.CommonPrefixes...)
			if !res.IsTruncated {
				break
			}
			marker = res.NextMarker
		}
		if !reflect.DeepEqual(got, sorted) {
			t.Fatalf("page size %d: got %d entries, want %d", pageSize, len(got), len(sorted))
		}
		if wantPages := (len(sorted) + pageSize - 1) / pageSize; pageSize > 1 && pages != wantPages {
			t.Errorf("page size %d: %d pages, want %d", pageSize, pages, wantPages)
		}
	}
}

func TestListExactlyFullPageIsNotTruncated(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	put(t, s, "bk1", "a", "")
	put(t, s, "bk1", "b", "")
	res, _ := s.ListObjects(ctx, "bk1", ListOptions{MaxKeys: 2})
	if res.IsTruncated || len(res.Objects) != 2 {
		t.Fatalf("res = %+v", res)
	}
	res, _ = s.ListObjects(ctx, "bk1", ListOptions{MaxKeys: 1})
	if !res.IsTruncated || res.NextMarker != "a" {
		t.Fatalf("res = %+v", res)
	}
}

func TestPrefixEnd(t *testing.T) {
	for in, want := range map[string]string{
		"":         "",
		"a":        "b",
		"a/":       "a0",
		"a\xff":    "b",
		"\xff\xff": "",
	} {
		if got := prefixEnd(in); got != want {
			t.Errorf("prefixEnd(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	for _, k := range []string{"Avatars/Me.PNG", "avatars/you.png", "docs/readme.md"} {
		put(t, s, "bk1", k, "")
	}
	objs, err := s.SearchObjects(ctx, "bk1", "png", 10)
	if err != nil || len(objs) != 2 {
		t.Fatalf("search = %v, %v", objs, err)
	}
	objs, _ = s.SearchObjects(ctx, "bk1", "AVATARS/me", 10)
	if len(objs) != 1 || objs[0].Key != "Avatars/Me.PNG" {
		t.Fatalf("case-insensitive search = %v", objs)
	}
	objs, _ = s.SearchObjects(ctx, "bk1", "%", 10)
	if len(objs) != 0 {
		t.Fatal("search treated % as a wildcard")
	}
}

func TestCleanupOnOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	newBucket(t, s, "bk1")
	put(t, s, "bk1", "keep", "x")
	id, _ := s.CreateMultipartUpload(ctx, "bk1", "big", PutOptions{})
	s.Close()

	orphan := filepath.Join(dir, "blobs", "ab", "cd", "abcdorphan")
	os.MkdirAll(filepath.Dir(orphan), 0o755)
	os.WriteFile(orphan, []byte("junk"), 0o644)
	os.WriteFile(filepath.Join(dir, "tmp", "put-123"), []byte("junk"), 0o644)
	os.MkdirAll(filepath.Join(dir, "multipart", "deadbeef"), 0o755)

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphan blob survived")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(entries) != 0 {
		t.Error("tmp not emptied")
	}
	if _, err := os.Stat(filepath.Join(dir, "multipart", "deadbeef")); !os.IsNotExist(err) {
		t.Error("stray multipart dir survived")
	}
	if _, err := os.Stat(filepath.Join(dir, "multipart", id)); err != nil {
		t.Error("live multipart dir removed")
	}
	if read(t, s, "bk1", "keep") != "x" {
		t.Error("live object lost")
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := newStore(t)
	newBucket(t, s, "bk1")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			key := fmt.Sprintf("k%d", i%5)
			if _, err := s.PutObject(ctx, "bk1", key, strings.NewReader(strings.Repeat("x", i)), PutOptions{}); err != nil {
				t.Error(err)
			}
			if _, f, err := s.GetObject(ctx, "bk1", key); err == nil {
				f.Close()
			} else {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := countBlobs(t, s); n != 5 {
		t.Fatalf("blobs = %d, want 5", n)
	}
}
