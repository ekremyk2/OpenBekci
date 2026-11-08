// Package storage holds the blob store behind uploaded files (backend plan
// section 13): a local filesystem driver (the default) and an S3 driver.
// Callers deal in opaque keys; they never see paths or buckets.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Errors returned by every driver.
var (
	// ErrNotFound means there is no blob under the key.
	ErrNotFound = errors.New("storage: not found")
	// ErrUnsupported means the driver cannot do what was asked (for example a
	// signed URL for a key that does not follow the file key layout).
	ErrUnsupported = errors.New("storage: unsupported")
)

// Info describes a stored blob. ContentType is empty when the driver does not
// keep one (the local driver).
type Info struct {
	Size        int64
	ContentType string
}

// Blob is the blob store. Keys look like "<org>/<yy>/<mm>/<id>" with an
// optional ".<variant>" suffix; see ValidKey.
type Blob interface {
	// Put stores r under key, replacing any blob there, atomically: readers
	// see the old blob or the whole new one, never a partial write. size is
	// the expected length of r, or -1 when unknown; a known size that does
	// not match what r delivers is an error and stores nothing.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the blob; the caller closes it. ErrNotFound when absent.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the blob. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// Stat reports the blob's size. ErrNotFound when absent.
	Stat(ctx context.Context, key string) (Info, error)
	// SignedURL returns a short-lived URL that downloads the blob without a
	// session. The local driver returns a path on this server signed with the
	// instance secret; the S3 driver returns a presigned GET (downloads only,
	// never uploads: uploads always stream through the server).
	SignedURL(ctx context.Context, key string, ttl time.Duration, opts ...SignOption) (string, error)
}

// DirectDownloader is implemented by a driver whose signed URLs point at the
// store itself (S3), so a download can be answered with a redirect to one
// instead of streaming the bytes through the server.
type DirectDownloader interface {
	DirectDownloads() bool
}

// DirectDownloads reports whether b hands out signed URLs that can be
// redirected to.
func DirectDownloads(b Blob) bool {
	d, ok := b.(DirectDownloader)
	return ok && d.DirectDownloads()
}

// SignOption tunes a signed URL.
type SignOption func(*SignOptions)

// SignOptions are the settings SignOption values produce.
type SignOptions struct {
	// Disposition is a Content-Disposition value the S3 driver asks the
	// store to send. The local driver ignores it: the files endpoint sets its
	// own headers.
	Disposition string
	// ContentType overrides the content type the S3 driver asks the store to send.
	ContentType string
}

// WithDisposition asks for a Content-Disposition header on the download.
func WithDisposition(v string) SignOption { return func(o *SignOptions) { o.Disposition = v } }

// WithContentType asks for a Content-Type header on the download.
func WithContentType(v string) SignOption { return func(o *SignOptions) { o.ContentType = v } }

// ResolveSignOptions applies opts.
func ResolveSignOptions(opts []SignOption) SignOptions {
	var o SignOptions
	for _, f := range opts {
		f(&o)
	}
	return o
}

// ValidKey reports whether key is a safe relative key: slash-separated
// segments of letters, digits, dot, dash and underscore, none empty or "..".
func ValidKey(key string) bool {
	if key == "" || len(key) > 512 || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			default:
				return false
			}
		}
	}
	return true
}

func checkKey(key string) error {
	if !ValidKey(key) {
		return fmt.Errorf("storage: invalid key %q", key)
	}
	return nil
}
