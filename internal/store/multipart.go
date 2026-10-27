package store

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const MaxPartNumber = 10000

type Upload struct {
	ID        string
	Bucket    string
	Key       string
	Initiated time.Time
}

type Part struct {
	Number       int
	ETag         string
	Size         int64
	LastModified time.Time
}

type CompletedPart struct {
	Number int
	ETag   string
}

func (s *Store) partPath(uploadID string, n int) string {
	return filepath.Join(s.multipartDir(), uploadID, strconv.Itoa(n))
}

func (s *Store) CreateMultipartUpload(ctx context.Context, bucket, key string, opts PutOptions) (string, error) {
	if !validKey(key) {
		return "", ErrInvalidKey
	}
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return "", err
	}
	normalizeOptions(&opts)
	id := newID()
	if err := os.MkdirAll(filepath.Join(s.multipartDir(), id), 0o755); err != nil {
		return "", err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO multipart_uploads(upload_id, bucket, key, content_type, metadata, headers, acl, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		id, bucket, key, opts.ContentType, encodeMap(opts.Metadata), encodeMap(opts.Headers), opts.ACL, time.Now().UnixNano())
	if err != nil {
		os.RemoveAll(filepath.Join(s.multipartDir(), id))
		return "", err
	}
	return id, nil
}

// requireUpload checks that uploadID exists and belongs to bucket/key.
func (s *Store) requireUpload(ctx context.Context, bucket, key, uploadID string) error {
	var b, k string
	err := s.db.QueryRowContext(ctx, `SELECT bucket, key FROM multipart_uploads WHERE upload_id = ?`, uploadID).Scan(&b, &k)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (b != bucket || k != key)) {
		return ErrNoSuchUpload
	}
	return err
}

// UploadPart stores one part and returns its ETag (unquoted hex MD5).
func (s *Store) UploadPart(ctx context.Context, bucket, key, uploadID string, n int, r io.Reader, contentMD5 []byte) (string, error) {
	if n < 1 || n > MaxPartNumber {
		return "", ErrInvalidPart
	}
	if err := s.requireUpload(ctx, bucket, key, uploadID); err != nil {
		return "", err
	}
	tmp, size, sum, err := s.writeTemp(r)
	if err != nil {
		return "", err
	}
	if contentMD5 != nil && !bytes.Equal(sum, contentMD5) {
		os.Remove(tmp)
		return "", ErrBadDigest
	}
	if err := os.Rename(tmp, s.partPath(uploadID, n)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	etag := hex.EncodeToString(sum)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO multipart_parts(upload_id, part_number, etag, size, modified_at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(upload_id, part_number) DO UPDATE SET
			etag = excluded.etag, size = excluded.size, modified_at = excluded.modified_at`,
		uploadID, n, etag, size, time.Now().UnixNano())
	if err != nil {
		return "", err
	}
	return etag, nil
}

func (s *Store) ListParts(ctx context.Context, bucket, key, uploadID string) ([]Part, error) {
	if err := s.requireUpload(ctx, bucket, key, uploadID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT part_number, etag, size, modified_at FROM multipart_parts WHERE upload_id = ? ORDER BY part_number`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := []Part{}
	for rows.Next() {
		var p Part
		var modified int64
		if err := rows.Scan(&p.Number, &p.ETag, &p.Size, &modified); err != nil {
			return nil, err
		}
		p.LastModified = time.Unix(0, modified).UTC()
		parts = append(parts, p)
	}
	return parts, rows.Err()
}

// ListMultipartUploads returns in-progress uploads whose key starts with prefix.
func (s *Store) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]Upload, error) {
	if err := requireBucket(ctx, s.db, bucket); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT upload_id, key, created_at FROM multipart_uploads WHERE bucket = ? AND substr(key, 1, ?) = ? ORDER BY key, created_at`,
		bucket, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uploads := []Upload{}
	for rows.Next() {
		u := Upload{Bucket: bucket}
		var created int64
		if err := rows.Scan(&u.ID, &u.Key, &created); err != nil {
			return nil, err
		}
		u.Initiated = time.Unix(0, created).UTC()
		uploads = append(uploads, u)
	}
	return uploads, rows.Err()
}

func normalizeETag(etag string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(etag), `"`))
}

// CompleteMultipartUpload joins the listed parts into the final object.
// Its ETag is the MD5 of the parts' binary MD5s followed by -N, as in S3.
func (s *Store) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, completed []CompletedPart) (*Object, error) {
	if err := s.requireUpload(ctx, bucket, key, uploadID); err != nil {
		return nil, err
	}
	if len(completed) == 0 {
		return nil, ErrInvalidPart
	}
	stored, err := s.ListParts(ctx, bucket, key, uploadID)
	if err != nil {
		return nil, err
	}
	byNumber := map[int]Part{}
	for _, p := range stored {
		byNumber[p.Number] = p
	}
	for i, c := range completed {
		if i > 0 && c.Number <= completed[i-1].Number {
			return nil, ErrInvalidPartOrder
		}
		p, ok := byNumber[c.Number]
		if !ok || normalizeETag(c.ETag) != p.ETag {
			return nil, ErrInvalidPart
		}
	}

	var opts PutOptions
	var meta, headers string
	err = s.db.QueryRowContext(ctx,
		`SELECT content_type, metadata, headers, acl FROM multipart_uploads WHERE upload_id = ?`, uploadID).
		Scan(&opts.ContentType, &meta, &headers, &opts.ACL)
	if err != nil {
		return nil, err
	}
	obj := &Object{Bucket: bucket, Key: key, ContentType: opts.ContentType, ACL: opts.ACL}
	obj.Metadata, obj.Headers = decodeMap(meta), decodeMap(headers)

	out, err := os.CreateTemp(s.tmpDir(), "complete-*")
	if err != nil {
		return nil, err
	}
	etags := md5.New()
	for _, c := range completed {
		raw, _ := hex.DecodeString(byNumber[c.Number].ETag)
		etags.Write(raw)
		if err := appendFile(out, s.partPath(uploadID, c.Number)); err != nil {
			out.Close()
			os.Remove(out.Name())
			return nil, err
		}
	}
	size, err := out.Seek(0, io.SeekCurrent)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return nil, err
	}
	obj.Size = size
	obj.ETag = fmt.Sprintf("%s-%d", hex.EncodeToString(etags.Sum(nil)), len(completed))
	obj.LastModified = time.Now().UTC()
	if err := s.commitTemp(ctx, obj, out.Name()); err != nil {
		return nil, err
	}
	s.dropUpload(ctx, uploadID)
	return obj, nil
}

func appendFile(dst *os.File, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}

func (s *Store) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	if err := s.requireUpload(ctx, bucket, key, uploadID); err != nil {
		return err
	}
	return s.dropUpload(ctx, uploadID)
}

func (s *Store) dropUpload(ctx context.Context, uploadID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM multipart_uploads WHERE upload_id = ?`, uploadID); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.multipartDir(), uploadID))
}
