package mcpserver

import (
	"fmt"
	"strings"

	"github.com/SandroK0/local-s3/internal/events"
)

// hint explains the likely cause of a failed request and how to fix it, in
// terms of an app's configuration and Storage calls. buckets lists the
// buckets that exist. It returns "" for successful requests.
func hint(e events.Event, cfg Config, buckets []string) string {
	if e.Status < 400 {
		return ""
	}
	presigned := e.Presigned
	anonymous := e.AccessKey == "" && !presigned

	switch e.ErrorCode {
	case "NoSuchBucket":
		existing := "none"
		if len(buckets) > 0 {
			existing = strings.Join(buckets, ", ")
		}
		return fmt.Sprintf("Bucket %q does not exist on pail (existing: %s). Check AWS_BUCKET in the app's .env or the disk's 'bucket' config, create the bucket in pail's UI, or start pail with AUTO_CREATE_BUCKETS=true.", e.Bucket, existing)

	case "NoSuchKey":
		switch e.Op {
		case "HeadObject":
			return "No file at this key. HeadObject 404s are also what Storage::exists()/missing()/fileExists() send for files that are not there, so this only matters if the file was expected."
		case "CopyObject":
			return "The copy source does not exist: Storage::copy()/move() was given a source path that is not stored. Check the path and the disk's 'root' prefix."
		}
		return fmt.Sprintf("No file at %q. Check the path the app passes to Storage (the disk's 'root' prefix, leading slashes, letter case, extension); search_objects can find near matches.", e.Key)

	case "SignatureDoesNotMatch":
		if presigned {
			return "The temporaryUrl()/temporaryUploadUrl() link was altered after signing (path or query changed) or was signed with a different secret than pail's SECRET_KEY."
		}
		return "The request was signed with a different secret: AWS_SECRET_ACCESS_KEY does not match pail's SECRET_KEY. If the secret is right, something rewrote the request after signing, e.g. a proxy changing the Host header, or AWS_ENDPOINT differs from the URL actually used."

	case "InvalidAccessKeyId":
		return fmt.Sprintf("AWS_ACCESS_KEY_ID %q does not match pail's ACCESS_KEY %q.", e.AccessKey, cfg.AccessKey)

	case "RequestTimeTooSkewed":
		return "The client's clock is more than 15 minutes away from pail's; fix the system clock (common in containers or VMs after sleep)."

	case "AuthorizationHeaderMalformed":
		return "pail could not parse the request's Authorization header (" + e.ErrorMessage + "). Use Signature Version 4, the default in aws-sdk-php v3 and other current SDKs."

	case "AccessDenied":
		switch {
		case e.Op == "CORSPreflight":
			return "A browser on an origin not listed in pail's CORS_ORIGINS tried a direct request (e.g. an upload to a temporaryUploadUrl()). Add the app's origin to CORS_ORIGINS."
		case strings.Contains(e.ErrorMessage, "expired"):
			return "The temporaryUrl()/temporaryUploadUrl() link expired. Generate a fresh one, or pass a later expiration when creating it."
		case anonymous && (e.Op == "GetObject" || e.Op == "HeadObject"):
			return "An unsigned request for a private file, typically a Storage::url() link. With AUTH=verify only public files can be fetched that way: use Storage::temporaryUrl(), or store the file with 'public' visibility (put(..., 'public') / setVisibility(..., 'public'))."
		case anonymous:
			return "An unsigned request that is not a plain read. With AUTH=verify the client must send credentials: check AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are set for this disk."
		}
		return "Access denied: " + e.ErrorMessage

	case "InvalidBucketName":
		return "Bucket names must be 3–63 characters of lowercase letters, numbers, dots and hyphens, starting and ending with a letter or number."
	case "BucketNotEmpty":
		return "A bucket can only be deleted once it is empty. Delete its objects first, or use pail's UI, which can force-delete."
	case "BucketAlreadyOwnedByYou":
		return "The bucket already exists. Safe to ignore if the code creates it on every run."
	case "KeyTooLongError":
		return "Object keys must be 1–1024 bytes of valid UTF-8; the path is too long or not valid UTF-8."
	case "BadDigest":
		return "The Content-MD5 sent did not match the uploaded bytes: the body changed after the MD5 was computed, or the MD5 was computed over different data."
	case "InvalidDigest":
		return "The Content-MD5 header is not a base64-encoded 16-byte MD5."
	case "IncompleteBody":
		return "The upload body was shorter or longer than declared (Content-Length or x-amz-decoded-content-length): the client aborted, or a stream was read before being uploaded."
	case "NoSuchUpload":
		return "That multipart upload was already completed or aborted, or never started. Often a retry after a success; harmless if the file exists."
	case "InvalidPart", "InvalidPartOrder":
		return "The multipart completion listed parts that were not uploaded, with wrong ETags, or out of order."
	case "MalformedXML":
		return "The request's XML body (batch delete, multipart completion or ACL) could not be parsed."
	case "NotImplemented":
		return "pail does not implement this S3 feature (" + strings.TrimPrefix(e.Op, "Unsupported:") + "). Unsupported locally: bucket policies, versioning, tagging, lifecycle, website, UploadPartCopy and browser POST uploads."
	case "MethodNotAllowed":
		return "S3 does not allow this HTTP method on this path."
	case "InvalidArgument":
		return "Invalid request parameter: " + e.ErrorMessage
	}

	switch {
	case e.Status == 416:
		return "The Range header asked for bytes past the end of the file."
	case e.Status == 412:
		return "A conditional header (If-Match / If-Unmodified-Since) did not match the stored object."
	case e.Status >= 500:
		return "pail failed internally; check pail's own log output."
	}
	return ""
}
