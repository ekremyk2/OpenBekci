package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Local stores blobs as files under a root directory (DATA_DIR/files). Every
// write goes to a temporary file in the target directory and is renamed into
// place, so a crash never leaves a half-written blob under a real key.
type Local struct {
	root   string
	signer *Signer
}

// NewLocal returns a Local store under root (created on first write). signer
// makes SignedURL work; nil disables signed URLs.
func NewLocal(root string, signer *Signer) *Local { return &Local{root: root, signer: signer} }

var _ Blob = (*Local)(nil)

func (l *Local) path(key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return filepath.Join(l.root, filepath.FromSlash(key)), nil
}

// Put implements Blob.
func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, _ string) (err error) {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("storage: create directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return fmt.Errorf("storage: create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	n, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
	if err != nil {
		return fmt.Errorf("storage: write: %w", err)
	}
	if size >= 0 && n != size {
		err = fmt.Errorf("storage: wrote %d bytes, expected %d", n, size)
		return err
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("storage: sync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("storage: close: %w", err)
	}
	if err = os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("storage: rename into place: %w", err)
	}
	syncDir(filepath.Dir(p))
	return nil
}

// syncDir flushes a directory entry so a rename survives a power cut. It is
// best effort: some platforms cannot fsync a directory, and the blob itself
// was already synced.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // dir is built from a validated key under root
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// SweepTemp removes the ".tmp-*" files that a crash in the middle of Put left
// behind, when they are older than olderThan (a write in progress is younger).
// The server calls it at start-up. It returns how many files it removed.
func (l *Local) SweepTemp(olderThan time.Duration) (int, error) {
	removed := 0
	cutoff := time.Now().Add(-olderThan)
	err := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // no root yet: nothing was ever written
			}
			return err
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			return nil
		}
		if rerr := os.Remove(p); rerr == nil {
			removed++
		}
		return nil
	})
	return removed, err
}

// ctxReader stops a copy when the context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(b)
}

// Get implements Blob.
func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p) //nolint:gosec // p is built from a validated key under root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Delete implements Blob.
func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete: %w", err)
	}
	return nil
}

// Stat implements Blob.
func (l *Local) Stat(_ context.Context, key string) (Info, error) {
	p, err := l.path(key)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return Info{Size: st.Size()}, nil
}

// SignedURL implements Blob. The key must follow the file layout
// "<org uuid>/<yy>/<mm>/<file uuid>[.<variant>]"; the result is the path
// /v1/orgs/<org>/files/<id> with variant, exp and sig query parameters.
func (l *Local) SignedURL(_ context.Context, key string, ttl time.Duration, _ ...SignOption) (string, error) {
	if l.signer == nil {
		return "", ErrUnsupported
	}
	org, id, variant, ok := ParseFileKey(key)
	if !ok {
		return "", ErrUnsupported
	}
	exp := time.Now().Add(ttl)
	q := url.Values{}
	if variant != "" {
		q.Set("variant", variant)
	}
	q.Set("exp", strconv.FormatInt(exp.Unix(), 10))
	q.Set("sig", l.signer.Sign(DownloadMessage(org, id, variant), exp))
	return "/v1/orgs/" + org + "/files/" + id + "?" + q.Encode(), nil
}

// FileKey builds the key of a file (or of one of its variants when variant is
// not empty): "<org>/<yy>/<mm>/<id>[.<variant>]".
func FileKey(org, id uuid.UUID, at time.Time, variant string) string {
	at = at.UTC()
	k := fmt.Sprintf("%s/%02d/%02d/%s", org, at.Year()%100, int(at.Month()), id)
	if variant != "" {
		k += "." + variant
	}
	return k
}

// ParseFileKey splits a key made by FileKey.
func ParseFileKey(key string) (org, id, variant string, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		return "", "", "", false
	}
	last, variant, _ := strings.Cut(parts[3], ".")
	if _, err := uuid.Parse(parts[0]); err != nil {
		return "", "", "", false
	}
	if _, err := uuid.Parse(last); err != nil {
		return "", "", "", false
	}
	return parts[0], last, variant, true
}
