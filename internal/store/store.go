// Package store keeps buckets and objects: bodies live in a blob directory
// under random IDs, and the key → blob mapping plus metadata lives in SQLite.
// Keys are never used as filesystem paths.
package store

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var (
	ErrNoSuchBucket      = errors.New("no such bucket")
	ErrNoSuchKey         = errors.New("no such key")
	ErrBucketExists      = errors.New("bucket already exists")
	ErrBucketNotEmpty    = errors.New("bucket not empty")
	ErrInvalidBucketName = errors.New("invalid bucket name")
	ErrInvalidKey        = errors.New("invalid key")
	ErrBadDigest         = errors.New("content-md5 mismatch")
	ErrNoSuchUpload      = errors.New("no such upload")
	ErrInvalidPart       = errors.New("invalid part")
	ErrInvalidPartOrder  = errors.New("invalid part order")
)

// MaxKeyLength is the longest key S3 accepts, in bytes.
const MaxKeyLength = 1024

type Bucket struct {
	Name        string
	CreatedAt   time.Time
	ObjectCount int64
	Size        int64
}

type Object struct {
	Bucket       string
	Key          string
	Size         int64
	ETag         string // unquoted hex; multipart uploads end in -N
	ContentType  string
	Metadata     map[string]string // user metadata (x-amz-meta-*), lowercase names without the prefix
	Headers      map[string]string // stored response headers such as Cache-Control
	ACL          string
	LastModified time.Time

	blobID string
}

// Public reports whether anonymous clients may read the object.
func (o *Object) Public() bool {
	return o.ACL == "public-read" || o.ACL == "public-read-write"
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
	Headers     map[string]string
	ACL         string
	ContentMD5  []byte // when set, the body's MD5 must match
}

type Store struct {
	db  *sql.DB
	dir string
}

const schema = `
CREATE TABLE IF NOT EXISTS buckets (
	name       TEXT PRIMARY KEY,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS objects (
	bucket        TEXT NOT NULL REFERENCES buckets(name),
	key           TEXT NOT NULL,
	blob_id       TEXT NOT NULL,
	size          INTEGER NOT NULL,
	etag          TEXT NOT NULL,
	content_type  TEXT NOT NULL,
	metadata      TEXT NOT NULL,
	headers       TEXT NOT NULL,
	acl           TEXT NOT NULL,
	modified_at   INTEGER NOT NULL,
	PRIMARY KEY (bucket, key)
);
CREATE TABLE IF NOT EXISTS multipart_uploads (
	upload_id    TEXT PRIMARY KEY,
	bucket       TEXT NOT NULL REFERENCES buckets(name),
	key          TEXT NOT NULL,
	content_type TEXT NOT NULL,
	metadata     TEXT NOT NULL,
	headers      TEXT NOT NULL,
	acl          TEXT NOT NULL,
	created_at   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS multipart_parts (
	upload_id   TEXT NOT NULL REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE,
	part_number INTEGER NOT NULL,
	etag        TEXT NOT NULL,
	size        INTEGER NOT NULL,
	modified_at INTEGER NOT NULL,
	PRIMARY KEY (upload_id, part_number)
);
`

// Open opens (creating if needed) a store rooted at dir. It clears leftover
// temp files and removes blobs no object references.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	for _, d := range []string{dir, s.blobDir(), s.tmpDir(), s.multipartDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := filepath.Join(dir, "meta.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serialises writers and avoids SQLITE_BUSY. Every
	// statement is short, and blob I/O happens outside the database.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	s.db = db
	if err := s.cleanup(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("cleanup: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) blobDir() string      { return filepath.Join(s.dir, "blobs") }
func (s *Store) tmpDir() string       { return filepath.Join(s.dir, "tmp") }
func (s *Store) multipartDir() string { return filepath.Join(s.dir, "multipart") }

func (s *Store) blobPath(id string) string {
	return filepath.Join(s.blobDir(), id[:2], id[2:4], id)
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// cleanup empties tmp/, deletes unreferenced blobs and multipart part
// directories without an upload row. It only runs before serving starts.
func (s *Store) cleanup(ctx context.Context) error {
	if err := os.RemoveAll(s.tmpDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(s.tmpDir(), 0o755); err != nil {
		return err
	}

	referenced := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT blob_id FROM objects`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		referenced[id] = true
	}
	rows.Close()
	err = filepath.WalkDir(s.blobDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !referenced[d.Name()] {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	uploads := map[string]bool{}
	rows, err = s.db.QueryContext(ctx, `SELECT upload_id FROM multipart_uploads`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		uploads[id] = true
	}
	rows.Close()
	entries, err := os.ReadDir(s.multipartDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !uploads[e.Name()] {
			if err := os.RemoveAll(filepath.Join(s.multipartDir(), e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeTemp streams r into a new temp file, returning its path, size and MD5.
func (s *Store) writeTemp(r io.Reader) (path string, size int64, sum []byte, err error) {
	f, err := os.CreateTemp(s.tmpDir(), "put-*")
	if err != nil {
		return "", 0, nil, err
	}
	h := md5.New()
	size, err = io.Copy(io.MultiWriter(f, h), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", 0, nil, err
	}
	return f.Name(), size, h.Sum(nil), nil
}

// promote moves a temp file into the blob directory under a new ID.
func (s *Store) promote(tmp string) (string, error) {
	id := newID()
	dst := s.blobPath(id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return id, nil
}

func (s *Store) removeBlob(id string) {
	if id != "" {
		os.Remove(s.blobPath(id))
	}
}

// --- buckets ---------------------------------------------------------------

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var ipLikeRe = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

// ValidBucketName applies S3's bucket naming rules.
func ValidBucketName(name string) bool {
	return bucketNameRe.MatchString(name) && !strings.Contains(name, "..") && !ipLikeRe.MatchString(name)
}

func validKey(key string) bool {
	return key != "" && len(key) <= MaxKeyLength && utf8.ValidString(key)
}

func (s *Store) CreateBucket(ctx context.Context, name string) error {
	if !ValidBucketName(name) {
		return ErrInvalidBucketName
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO buckets(name, created_at) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`,
		name, time.Now().UnixNano())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBucketExists
	}
	return nil
}

func (s *Store) DeleteBucket(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireBucket(ctx, tx, name); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE bucket = ?`, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrBucketNotEmpty
	}
	rows, err := tx.QueryContext(ctx, `SELECT upload_id FROM multipart_uploads WHERE bucket = ?`, name)
	if err != nil {
		return err
	}
	var uploads []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		uploads = append(uploads, id)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `DELETE FROM multipart_uploads WHERE bucket = ?`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM buckets WHERE name = ?`, name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, id := range uploads {
		os.RemoveAll(filepath.Join(s.multipartDir(), id))
	}
	return nil
}

const bucketSelect = `
SELECT b.name, b.created_at, COUNT(o.key), COALESCE(SUM(o.size), 0)
FROM buckets b LEFT JOIN objects o ON o.bucket = b.name`

func scanBucket(sc interface{ Scan(...any) error }) (Bucket, error) {
	var b Bucket
	var created int64
	err := sc.Scan(&b.Name, &created, &b.ObjectCount, &b.Size)
	b.CreatedAt = time.Unix(0, created).UTC()
	return b, err
}

// ListBuckets returns every bucket with its object count and total size.
func (s *Store) ListBuckets(ctx context.Context) ([]Bucket, error) {
	rows, err := s.db.QueryContext(ctx, bucketSelect+` GROUP BY b.name ORDER BY b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := []Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

func (s *Store) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	row := s.db.QueryRowContext(ctx, bucketSelect+` WHERE b.name = ? GROUP BY b.name`, name)
	b, err := scanBucket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSuchBucket
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func requireBucket(ctx context.Context, q querier, name string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM buckets WHERE name = ?`, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchBucket
	}
	return err
}

// --- objects ---------------------------------------------------------------

const objectColumns = `bucket, key, blob_id, size, etag, content_type, metadata, headers, acl, modified_at`

func scanObject(sc interface{ Scan(...any) error }) (*Object, error) {
	var o Object
	var meta, headers string
	var modified int64
	if err := sc.Scan(&o.Bucket, &o.Key, &o.blobID, &o.Size, &o.ETag, &o.ContentType, &meta, &headers, &o.ACL, &modified); err != nil {
		return nil, err
	}
	o.LastModified = time.Unix(0, modified).UTC()
	o.Metadata, o.Headers = decodeMap(meta), decodeMap(headers)
	return &o, nil
}

func decodeMap(s string) map[string]string {
	m := map[string]string{}
	json.Unmarshal([]byte(s), &m)
	return m
}

func encodeMap(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func normalizeOptions(opts *PutOptions) {
	if opts.ContentType == "" {
		opts.ContentType = "binary/octet-stream"
	}
	if opts.ACL == "" {
		opts.ACL = "private"
	}
	if opts.Metadata == nil {
		opts.Metadata = map[string]string{}
	}
	if opts.Headers == nil {
		opts.Headers = map[string]string{}
	}
}

// PutObject stores r under bucket/key, replacing any existing object.
func (s *Store) PutObject(ctx context.Context, bucket, key string, r io.Reader, opts PutOptions) (*Object, error) {
	if !validKey(key) {
		return nil, ErrInvalidKey
	}
	// Check before reading the body so a typo'd bucket fails fast.
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return nil, err
	}
	tmp, size, sum, err := s.writeTemp(r)
	if err != nil {
		return nil, err
	}
	if opts.ContentMD5 != nil && !bytes.Equal(sum, opts.ContentMD5) {
		os.Remove(tmp)
		return nil, ErrBadDigest
	}
	normalizeOptions(&opts)
	obj := &Object{
		Bucket:       bucket,
		Key:          key,
		Size:         size,
		ETag:         hex.EncodeToString(sum),
		ContentType:  opts.ContentType,
		Metadata:     opts.Metadata,
		Headers:      opts.Headers,
		ACL:          opts.ACL,
		LastModified: time.Now().UTC(),
	}
	if err := s.commitTemp(ctx, obj, tmp); err != nil {
		return nil, err
	}
	return obj, nil
}

// commitTemp promotes tmp to a blob and points obj at it.
func (s *Store) commitTemp(ctx context.Context, obj *Object, tmp string) error {
	id, err := s.promote(tmp)
	if err != nil {
		return err
	}
	obj.blobID = id
	if err := s.commit(ctx, obj); err != nil {
		s.removeBlob(id)
		return err
	}
	return nil
}

// commit upserts obj's row and frees the blob it replaced.
func (s *Store) commit(ctx context.Context, obj *Object) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireBucket(ctx, tx, obj.Bucket); err != nil {
		return err
	}
	var old string
	err = tx.QueryRowContext(ctx, `SELECT blob_id FROM objects WHERE bucket = ? AND key = ?`, obj.Bucket, obj.Key).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO objects(`+objectColumns+`) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket, key) DO UPDATE SET
			blob_id = excluded.blob_id, size = excluded.size, etag = excluded.etag,
			content_type = excluded.content_type, metadata = excluded.metadata,
			headers = excluded.headers, acl = excluded.acl, modified_at = excluded.modified_at`,
		obj.Bucket, obj.Key, obj.blobID, obj.Size, obj.ETag, obj.ContentType,
		encodeMap(obj.Metadata), encodeMap(obj.Headers), obj.ACL, obj.LastModified.UnixNano())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeBlob(old)
	return nil
}

// HeadObject returns an object's metadata.
func (s *Store) HeadObject(ctx context.Context, bucket, key string) (*Object, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+objectColumns+` FROM objects WHERE bucket = ? AND key = ?`, bucket, key)
	obj, err := scanObject(row)
	if errors.Is(err, sql.ErrNoRows) {
		if err := requireBucket(ctx, s.db, bucket); err != nil {
			return nil, err
		}
		return nil, ErrNoSuchKey
	}
	return obj, err
}

// GetObject returns an object's metadata and an open handle on its body.
// The caller closes the file.
func (s *Store) GetObject(ctx context.Context, bucket, key string) (*Object, *os.File, error) {
	// A concurrent overwrite can free the blob between the lookup and the
	// open; looking it up again finds the new one.
	for range 3 {
		obj, err := s.HeadObject(ctx, bucket, key)
		if err != nil {
			return nil, nil, err
		}
		f, err := os.Open(s.blobPath(obj.blobID))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		return obj, f, nil
	}
	return nil, nil, ErrNoSuchKey
}

// DeleteObject removes bucket/key. Deleting a missing key is not an error,
// matching S3.
func (s *Store) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return err
	}
	var blob string
	err := s.db.QueryRowContext(ctx, `DELETE FROM objects WHERE bucket = ? AND key = ? RETURNING blob_id`, bucket, key).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.removeBlob(blob)
	return nil
}

// DeletePrefix removes every object whose key starts with prefix and
// returns how many were removed.
func (s *Store) DeletePrefix(ctx context.Context, bucket, prefix string) (int, error) {
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return 0, err
	}
	query := `DELETE FROM objects WHERE bucket = ? AND key >= ?`
	args := []any{bucket, prefix}
	if end := prefixEnd(prefix); end != "" {
		query += ` AND key < ?`
		args = append(args, end)
	}
	rows, err := s.db.QueryContext(ctx, query+` RETURNING blob_id`, args...)
	if err != nil {
		return 0, err
	}
	var blobs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		blobs = append(blobs, id)
	}
	rows.Close()
	for _, id := range blobs {
		s.removeBlob(id)
	}
	return len(blobs), rows.Err()
}

type CopyOptions struct {
	// ReplaceMetadata uses ContentType, Metadata and Headers below instead
	// of the source's.
	ReplaceMetadata bool
	ContentType     string
	Metadata        map[string]string
	Headers         map[string]string
	ACL             string // defaults to private, as in S3
}

// CopyObject copies src to dst and returns the new object.
func (s *Store) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, opts CopyOptions) (*Object, error) {
	if !validKey(dstKey) {
		return nil, ErrInvalidKey
	}
	src, f, err := s.GetObject(ctx, srcBucket, srcKey)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	put := PutOptions{ContentType: src.ContentType, Metadata: src.Metadata, Headers: src.Headers, ACL: opts.ACL}
	if opts.ReplaceMetadata {
		put.ContentType, put.Metadata, put.Headers = opts.ContentType, opts.Metadata, opts.Headers
	}
	return s.PutObject(ctx, dstBucket, dstKey, f, put)
}

// RenameObject moves src to dst within a bucket without copying the body.
// An existing object at dst is replaced.
func (s *Store) RenameObject(ctx context.Context, bucket, src, dst string) error {
	if !validKey(dst) {
		return ErrInvalidKey
	}
	if src == dst {
		_, err := s.HeadObject(ctx, bucket, src)
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireBucket(ctx, tx, bucket); err != nil {
		return err
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM objects WHERE bucket = ? AND key = ?`, bucket, src).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchKey
	}
	if err != nil {
		return err
	}
	var old string
	err = tx.QueryRowContext(ctx, `DELETE FROM objects WHERE bucket = ? AND key = ? RETURNING blob_id`, bucket, dst).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE objects SET key = ?, modified_at = ? WHERE bucket = ? AND key = ?`,
		dst, time.Now().UnixNano(), bucket, src)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeBlob(old)
	return nil
}

func (s *Store) SetACL(ctx context.Context, bucket, key, acl string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE objects SET acl = ? WHERE bucket = ? AND key = ?`, acl, bucket, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if err := requireBucket(ctx, s.db, bucket); err != nil {
			return err
		}
		return ErrNoSuchKey
	}
	return nil
}

// --- listing ---------------------------------------------------------------

type ListOptions struct {
	Prefix     string
	Delimiter  string
	StartAfter string // exclusive; also skips common prefixes ≤ StartAfter
	MaxKeys    int    // objects + common prefixes per page; ≤0 means 1000
}

type ListResult struct {
	Objects        []*Object
	CommonPrefixes []string
	IsTruncated    bool
	// NextMarker is the last key or common prefix returned; pass it as
	// StartAfter to fetch the next page.
	NextMarker string
}

// prefixEnd returns the smallest string greater than every string with the
// given prefix, or "" when there is none.
func prefixEnd(prefix string) string {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}

const listBatch = 500

// ListObjects lists keys in byte order, rolling keys that contain Delimiter
// after Prefix up into common prefixes, like S3's ListObjects.
func (s *Store) ListObjects(ctx context.Context, bucket string, opts ListOptions) (*ListResult, error) {
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return nil, err
	}
	maxKeys := opts.MaxKeys
	if maxKeys <= 0 || maxKeys > 1000 {
		maxKeys = 1000
	}
	upper := prefixEnd(opts.Prefix)

	cursor, inclusive := opts.Prefix, true
	if opts.StartAfter >= cursor {
		cursor, inclusive = opts.StartAfter, false
	}

	res := &ListResult{Objects: []*Object{}, CommonPrefixes: []string{}}
	count := 0
	full := func() bool {
		if count == maxKeys {
			res.IsTruncated = true
			return true
		}
		return false
	}

	for {
		query := `SELECT ` + objectColumns + ` FROM objects WHERE bucket = ?`
		args := []any{bucket}
		if inclusive {
			query += ` AND key >= ?`
		} else {
			query += ` AND key > ?`
		}
		args = append(args, cursor)
		if upper != "" {
			query += ` AND key < ?`
			args = append(args, upper)
		}
		query += ` ORDER BY key LIMIT ?`
		args = append(args, listBatch)

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		n, done, seek := 0, false, ""
		for rows.Next() {
			n++
			obj, err := scanObject(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if opts.Delimiter != "" {
				rest := obj.Key[len(opts.Prefix):]
				if i := strings.Index(rest, opts.Delimiter); i >= 0 {
					cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
					if cp > opts.StartAfter {
						if done = full(); done {
							break
						}
						res.CommonPrefixes = append(res.CommonPrefixes, cp)
						res.NextMarker = cp
						count++
					}
					// Skip everything else under this prefix.
					if seek = prefixEnd(cp); seek == "" {
						done = true
					}
					break
				}
			}
			if done = full(); done {
				break
			}
			res.Objects = append(res.Objects, obj)
			res.NextMarker = obj.Key
			count++
			cursor, inclusive = obj.Key, false
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		switch {
		case done:
			return res, nil
		case seek != "":
			cursor, inclusive = seek, true
		case n < listBatch:
			return res, nil
		}
	}
}

// SearchObjects returns up to limit objects whose key contains q,
// case-insensitively.
func (s *Store) SearchObjects(ctx context.Context, bucket, q string, limit int) ([]*Object, error) {
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+objectColumns+` FROM objects WHERE bucket = ? AND instr(lower(key), lower(?)) > 0 ORDER BY key LIMIT ?`,
		bucket, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objs := []*Object{}
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
	}
	return objs, rows.Err()
}
