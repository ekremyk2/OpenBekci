package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// uploadDigestKey carries the request's *uploadDigest in the context.
type uploadDigestKey struct{}

// uploadDigest is where the upload handler leaves the digest of the form it
// streamed, for the idempotency middleware to store with the key.
type uploadDigest struct {
	mu  sync.Mutex
	sum string
}

func (d *uploadDigest) set(sum string) {
	d.mu.Lock()
	d.sum = sum
	d.mu.Unlock()
}

func (d *uploadDigest) get() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sum
}

// recordUploadDigest hands the digest of an upload form to the idempotency
// middleware (a no-op without an Idempotency-Key).
func recordUploadDigest(ctx context.Context, sum string) {
	if d, ok := ctx.Value(uploadDigestKey{}).(*uploadDigest); ok {
		d.set(sum)
	}
}

// formHasher fingerprints a multipart upload form without buffering it: the
// small fields (order does not matter) and the file part (name and content).
// The upload handler and the idempotency replay check feed it the same way, so
// the same form always gives the same digest.
type formHasher struct {
	fields   map[string]string
	fileName string
	file     hash.Hash
}

func newFormHasher() *formHasher { return &formHasher{fields: map[string]string{}, file: sha256.New()} }

// field adds a small text field.
func (f *formHasher) field(name, value string) { f.fields[name] = value }

// filePart wraps the file part's reader: whatever is read through it is hashed.
func (f *formHasher) filePart(name string, r io.Reader) io.Reader {
	f.fileName = name
	return io.TeeReader(r, f.file)
}

// sum is the hex SHA-256 of the form as read so far.
func (f *formHasher) sum() string {
	h := sha256.New()
	names := make([]string, 0, len(f.fields))
	for n := range f.fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write([]byte(f.fields[n]))
		h.Write([]byte{0})
	}
	h.Write([]byte(f.fileName))
	h.Write([]byte{0})
	h.Write(f.file.Sum(nil))
	return hex.EncodeToString(h.Sum(nil))
}

// formDigest reads the multipart body of r up to and including its file part
// and returns the form's digest, as the upload handler computes it. Used when
// a request replays an earlier upload: nothing is stored, but the content is
// read to prove it is the same file.
func formDigest(r *http.Request) (string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", problem.BadRequest("the request body is not a multipart form")
	}
	fh := newFormHasher()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return fh.sum(), nil
		}
		if err != nil {
			return "", bodyReadError(err)
		}
		if part.FormName() == "file" {
			_, err := io.Copy(io.Discard, fh.filePart(part.FileName(), part))
			_ = part.Close()
			if err != nil {
				return "", bodyReadError(err)
			}
			return fh.sum(), nil // parts after the file are not part of the upload
		}
		v, err := formValue(part, part.FormName())
		_ = part.Close()
		if err != nil {
			return "", err
		}
		fh.field(part.FormName(), v)
	}
}
