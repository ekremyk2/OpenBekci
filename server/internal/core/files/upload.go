package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var scopeTypeRE = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,39}$`)

var errTooLarge = errors.New("files: file too large")

// UploadParams describes one file to store.
type UploadParams struct {
	OrgID uuid.UUID
	// SiteID is the site the file belongs to; it limits who may read it to
	// holders of site-scoped grants. No foreign key yet (phase B adds one).
	SiteID *uuid.UUID
	// Scope attaches the file to a resource at upload time. Without it the
	// file is a draft owned by the uploader that expires after the unattached
	// lifetime unless a module calls Attach.
	Scope *ScopeRef
	// Name is the file name; its extension must be on the allowlist.
	Name string
	// Content is read once, streamed to storage.
	Content io.Reader
	// RetentionUntil sets an expiry time; nil keeps the file until it is deleted
	// (drafts get the unattached lifetime).
	RetentionUntil *time.Time
}

// Upload stores a file for the caller. It needs files.upload (at the site, when
// SiteID is set) and, with a Scope, the scope's resolver must allow the caller
// to attach. The content is sniffed, never trusted by its name or the client's
// Content-Type; images are decoded, stripped of metadata by re-encoding and
// given thumb and medium variants. It answers 413 for a file over the limit,
// 415 for a type that is not allowed or does not match its extension, and 503
// scan_unavailable when a virus scanner is configured but unreachable. A file
// the scanner flags comes back with status quarantined.
func (s *Service) Upload(ctx context.Context, p UploadParams) (File, error) {
	if p.SiteID == nil {
		if err := authz.Require(ctx, PermUpload, authz.Scope{OrgID: p.OrgID}); err != nil {
			return File{}, err
		}
	} else if authz.PrincipalFromContext(ctx) == nil {
		return File{}, problem.Unauthenticated("authentication is required")
	} // for a site the scope is derived from the stored site inside the reservation transaction (see ingest)
	uid := authz.PrincipalFromContext(ctx).UserID
	if ok, retry := s.uploads.Allow(uid.String()); !ok {
		return File{}, problem.RateLimited(retry)
	}
	return s.ingest(ctx, p, ingestMode{uploadedBy: &uid, source: SourceUploaded, authorize: true})
}

// Discard removes a file the caller has just uploaded with Upload, for the
// HTTP layer to call when the rest of the request turns out to be invalid
// (a form field after the file part): the file is marked deleted at once and
// its blobs go with the next maintenance run. Only the uploader may.
func (s *Service) Discard(ctx context.Context, f File) error {
	if !s.isUploader(ctx, f) {
		return errNoFile
	}
	err := s.db.InTenantTx(ctx, f.OrgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE file SET status = 'deleted', deleted_at = $2, updated_at = $2
			WHERE id = $1 AND org_id = $3 AND status IN ('ready', 'quarantined')`, f.ID, s.now(), f.OrgID)
		if err != nil || tag.RowsAffected() != 1 {
			return err
		}
		before := f.auditView()
		f.Status = StatusDeleted
		return s.record(ctx, tx, "files.delete", f, before, f.auditView())
	})
	if err != nil {
		return internalOr(err, "discard file")
	}
	return nil
}

// UploadGenerated stores a file the server produced itself (a statement, a
// receipt, minutes; decision O8) with source generated. It is for server-side
// code only (jobs, other modules after they authorized their own action): it
// checks no permission and no resolver, and records a system event. Never
// call it with content or a scope taken straight from a request.
func (s *Service) UploadGenerated(ctx context.Context, p UploadParams) (File, error) {
	return s.ingest(ctx, p, ingestMode{source: SourceGenerated})
}

type ingestMode struct {
	uploadedBy *uuid.UUID
	source     Source
	// authorize makes the scope's resolver decide whether the caller may attach.
	authorize bool
}

func (s *Service) ingest(ctx context.Context, p UploadParams, m ingestMode) (File, error) {
	name := cleanName(p.Name)
	var bad []problem.Field
	if name == "" {
		bad = append(bad, problem.Field{Field: "file", Code: "required"})
	}
	if p.Scope != nil {
		if !scopeTypeRE.MatchString(p.Scope.Type) {
			bad = append(bad, problem.Field{Field: "scope", Code: "invalid_format"})
		} else if _, ok := s.scope(p.Scope.Type); !ok && m.authorize {
			bad = append(bad, problem.Field{Field: "scope", Code: "invalid_value"})
		}
		if p.Scope.ID == uuid.Nil {
			bad = append(bad, problem.Field{Field: "scope", Code: "invalid_value"})
		}
	}
	if len(bad) > 0 {
		return File{}, problem.Validation(bad...)
	}
	if p.Content == nil {
		return File{}, problem.Internal(errors.New("files: upload without content"))
	}

	fileID, now := id.New(), s.now()
	f := File{
		ID: fileID, OrgID: p.OrgID, SiteID: p.SiteID, Key: storage.FileKey(p.OrgID, fileID, now, ""),
		Name: name, Mime: "application/octet-stream", Status: StatusPending, Source: m.source,
		UploadedBy: m.uploadedBy, Scope: p.Scope, RetentionUntil: p.RetentionUntil, CreatedAt: now, Variants: map[string]Variant{},
	}
	if f.RetentionUntil == nil && p.Scope == nil && m.source == SourceUploaded {
		t := now.Add(s.cfg.UnattachedTTL)
		f.RetentionUntil = &t
	}

	// 1. Reserve the row, so a crash leaves a pending row the maintenance job
	// sweeps instead of an orphan blob nobody knows about.
	err := s.db.InIndependentTenantTx(ctx, p.OrgID, func(tx pgx.Tx) error {
		if m.authorize && p.SiteID != nil {
			sc, err := authz.ScopeOfSite(ctx, tx, *p.SiteID)
			switch {
			case errors.Is(err, authz.ErrScopeUnavailable):
				return problem.Validation(problem.Field{Field: "site_id", Code: "invalid_value"}) // no sites in this build
			case err != nil:
				return err
			}
			if err := authz.Require(ctx, PermUpload, sc); err != nil {
				return err
			}
		}
		if p.Scope != nil {
			if e, ok := s.scope(p.Scope.Type); ok {
				if m.authorize {
					allowed, err := e.resolver.CanAttach(ctx, tx, *p.Scope)
					if err != nil {
						return err
					}
					if !allowed {
						return problem.Forbidden("you may not add files here")
					}
				}
				if err := s.checkMax(ctx, tx, p.OrgID, *p.Scope, e.maxFiles); err != nil {
					return err
				}
			}
		}
		var scopeType *string
		var scopeID *uuid.UUID
		if f.Scope != nil {
			scopeType, scopeID = &f.Scope.Type, &f.Scope.ID
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO file (id, org_id, site_id, key, name, mime, uploaded_by, scope_type, scope_id, status, source, retention_until, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending', $10, $11, $12)`,
			f.ID, f.OrgID, f.SiteID, f.Key, f.Name, f.Mime, f.UploadedBy, scopeType, scopeID, string(f.Source), f.RetentionUntil, f.CreatedAt)
		return err
	})
	if err != nil {
		return File{}, internalOr(err, "reserve file")
	}

	// 2. Store the content.
	res, err := s.store(ctx, f, p.Content)
	if err != nil {
		s.discard(f)
		return File{}, ingestError(err, s.cfg.MaxUploadBytes)
	}

	// 3. Publish: ready (or quarantined), with usage and the audit event.
	f.Name, f.Mime, f.Size, f.SHA256, f.Status, f.Variants = res.name, res.mime, res.size, res.sha, res.status, res.variants
	if f.Status == StatusQuarantined && f.RetentionUntil == nil {
		// a flagged file never stays forever, whatever it is attached to
		t := now.Add(s.cfg.UnattachedTTL)
		f.RetentionUntil = &t
	}
	err = s.db.InIndependentTenantTx(ctx, p.OrgID, func(tx pgx.Tx) error {
		variants, err := json.Marshal(f.Variants)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE file SET name = $2, size = $3, mime = $4, sha256 = $5, status = $6, variants = $7,
			       retention_until = $8, updated_at = $9
			WHERE id = $1 AND status = 'pending'`,
			f.ID, f.Name, f.Size, f.Mime, f.SHA256, string(f.Status), variants, f.RetentionUntil, s.now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("the pending file row disappeared (swept as stale?)")
		}
		if err := addUsage(ctx, tx, f.OrgID, f.storedBytes(), 1); err != nil {
			return err
		}
		action := "files.upload"
		if f.Source == SourceGenerated {
			action = "files.generate"
		}
		if err := s.record(ctx, tx, action, f, nil, f.auditView()); err != nil {
			return err
		}
		if f.Status == StatusQuarantined {
			return s.record(ctx, tx, "files.quarantine", f, nil, f.auditView())
		}
		return nil
	})
	if err != nil {
		s.discard(f)
		return File{}, internalOr(err, "publish file")
	}
	return f, nil
}

// record writes a file audit event in tx.
func (s *Service) record(ctx context.Context, tx pgx.Tx, action string, f File, before, after any) error {
	diff, err := audit.Diff("file", before, after)
	if err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{OrgID: f.OrgID, SiteID: f.SiteID, Action: action, TargetType: "file", TargetID: f.ID, Diff: diff})
}

// storedBytes is what the file occupies in storage: original plus variants.
func (f File) storedBytes() int64 {
	n := f.Size
	for _, v := range f.Variants {
		n += v.Size
	}
	return n
}

func addUsage(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, bytes int64, files int) error {
	if bytes < 0 || files < 0 { // purged blobs give their storage back
		_, err := tx.Exec(ctx, `
			UPDATE org_storage_usage SET bytes = GREATEST(bytes + $2, 0), files = GREATEST(files + $3, 0), updated_at = now()
			WHERE org_id = $1`, orgID, bytes, files)
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO org_storage_usage (org_id, bytes, files) VALUES ($1, $2, $3)
		ON CONFLICT (org_id) DO UPDATE
		   SET bytes = org_storage_usage.bytes + EXCLUDED.bytes,
		       files = org_storage_usage.files + EXCLUDED.files,
		       updated_at = now()`, orgID, bytes, files)
	return err
}

// checkMax enforces a scope type's file limit; the advisory lock serializes
// concurrent uploads to the same resource so the limit holds under a race.
func (s *Service) checkMax(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, ref ScopeRef, limit int) error {
	if limit <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "files:"+ref.Type+":"+ref.ID.String()); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM file WHERE org_id = $1 AND scope_type = $2 AND scope_id = $3 AND status IN ('pending', 'ready')`,
		orgID, ref.Type, ref.ID).Scan(&n); err != nil {
		return err
	}
	if n >= limit {
		return problem.Conflict(fmt.Sprintf("this resource already has the maximum of %d files", limit))
	}
	return nil
}

// stored is the outcome of storing the content.
type stored struct {
	name     string
	mime     string
	size     int64
	sha      string
	status   Status
	variants map[string]Variant
}

func (s *Service) store(ctx context.Context, f File, content io.Reader) (stored, error) {
	lr := &limitReader{r: content, max: s.cfg.MaxUploadBytes}
	head := make([]byte, sniffHeadLen)
	n, err := io.ReadFull(lr, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return stored{}, err
	}
	head = head[:n]
	if n == 0 {
		return stored{}, problem.Validation(problem.Field{Field: "file", Code: "too_short"})
	}
	rule, extAllowed, ok := match(s.cfg.Types, f.Name, head)
	switch {
	case !extAllowed:
		return stored{}, problem.New(http.StatusUnsupportedMediaType, problem.CodeUnsupportedMediaType, "files of this type are not accepted")
	case !ok:
		return stored{}, problem.New(http.StatusUnsupportedMediaType, problem.CodeUnsupportedMediaType, "the content of the file does not match its extension")
	}

	// Spool the upload to a temporary file: the scanner and the image decoder
	// read it from there (no io.ReadAll of the whole upload, no read-back from
	// the blob store), and nothing reaches storage before it passed both.
	tmp, err := os.CreateTemp("", spoolPrefix+"*")
	if err != nil {
		return stored{}, fmt.Errorf("spool upload: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.MultiReader(bytes.NewReader(head), lr))
	if err != nil {
		return stored{}, err
	}
	rawSum := hex.EncodeToString(h.Sum(nil))

	status := StatusReady
	if s.cfg.Scanner != nil {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return stored{}, err
		}
		v, err := s.cfg.Scanner.Scan(ctx, tmp)
		if err != nil {
			return stored{}, scanError(err)
		}
		if v.Infected {
			s.cfg.Log.WarnContext(ctx, "upload quarantined by the virus scanner", "file_id", f.ID, "org_id", f.OrgID, "signature", v.Signature)
			status = StatusQuarantined
		}
	}
	if rule.Image && status == StatusReady {
		return s.storeImage(ctx, f, tmp, size)
	}
	// A non-image, or a flagged image (kept as uploaded for an administrator
	// to look at; no variants): the bytes go to storage as they are.
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return stored{}, err
	}
	if err := s.blob.Put(ctx, f.Key, tmp, size, rule.MIME); err != nil {
		return stored{}, err
	}
	return stored{name: f.Name, mime: rule.MIME, size: size, sha: rawSum, status: status, variants: map[string]Variant{}}, nil
}

// spoolPrefix names the temporary files an upload is spooled to.
const spoolPrefix = "openbekci-upload-"

// SweepSpool removes spooled uploads that a crash left in the temp directory,
// when they are older than olderThan (an upload in progress is younger). The
// server calls it at start-up. It returns how many files it removed.
func SweepSpool(olderThan time.Duration) int {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return 0
	}
	removed, cutoff := 0, time.Now().Add(-olderThan)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), spoolPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			if os.Remove(filepath.Join(os.TempDir(), e.Name())) == nil {
				removed++
			}
		}
	}
	return removed
}

// storeImage re-encodes the spooled image and stores the original and its
// variants. At most Config.ImageWorkers images are processed at once in the
// process, and an image over Config.MaxImagePixels is refused (422) after
// reading only its header.
func (s *Service) storeImage(ctx context.Context, f File, tmp *os.File, size int64) (stored, error) {
	select {
	case s.imageSem <- struct{}{}:
		defer func() { <-s.imageSem }()
	case <-ctx.Done():
		return stored{}, ctx.Err()
	}
	put := func(name, mime string, w, h int, enc func(io.Writer) error) (encoded, error) {
		key := f.Key
		if name != "" {
			key = variantKey(f.Key, name)
		}
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(enc(pw)) }()
		sum := sha256.New()
		cr := &countReader{r: io.TeeReader(pr, sum)}
		err := s.blob.Put(ctx, key, cr, -1, mime)
		_ = pr.CloseWithError(err) // lets the encoder stop if Put gave up early
		if err != nil {
			return encoded{}, err
		}
		return encoded{v: Variant{Width: w, Height: h, Size: cr.n, Mime: mime}, sha: hex.EncodeToString(sum.Sum(nil))}, nil
	}
	orig, variants, err := renderImage(tmp, size, s.cfg.MaxImagePixels, s.cfg.MaxImageDimension, put)
	switch {
	case errors.Is(err, errImageTooBig):
		return stored{}, problem.Validation(problem.Field{Field: "file", Code: "too_large"})
	case errors.Is(err, errBadImage), errors.Is(err, errImageNoSize):
		return stored{}, problem.Validation(problem.Field{Field: "file", Code: "invalid_value"})
	case err != nil:
		return stored{}, err
	}
	out := stored{name: renameForMIME(f.Name, orig.v.Mime), mime: orig.v.Mime, size: orig.v.Size, sha: orig.sha, status: StatusReady, variants: map[string]Variant{}}
	for name, v := range variants {
		out.variants[name] = v.v
	}
	return out, nil
}

func variantKey(key, variant string) string { return key + "." + variant }

// renameForMIME fixes the extension of a re-encoded image (a WebP stored as
// JPEG becomes .jpg) so the name still matches the content.
func renameForMIME(name, mime string) string {
	want := map[string]string{"image/jpeg": ".jpg", "image/png": ".png"}[mime]
	ext := strings.ToLower(path.Ext(name))
	if want == "" || ext == want || (want == ".jpg" && ext == ".jpeg") {
		return name
	}
	return strings.TrimSuffix(name, path.Ext(name)) + want
}

// discard removes whatever an aborted upload left behind: the blobs and the
// pending row. It uses a context of its own so a cancelled request still cleans up.
func (s *Service) discard(f File) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, k := range []string{f.Key, variantKey(f.Key, VariantThumb), variantKey(f.Key, VariantMedium)} {
		if err := s.blob.Delete(ctx, k); err != nil {
			s.cfg.Log.Error("discard upload blob", "key", k, "error", err)
		}
	}
	err := s.db.InIndependentTenantTx(ctx, f.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM file WHERE id = $1 AND status = 'pending'`, f.ID)
		return err
	})
	if err != nil {
		s.cfg.Log.Error("discard upload row", "file_id", f.ID, "error", err)
	}
}

// limitReader fails with errTooLarge once more than max bytes were read.
type limitReader struct {
	r      io.Reader
	max, n int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return 0, errTooLarge
	}
	return n, err
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func scanError(err error) error {
	pe := problem.Wrap(http.StatusServiceUnavailable, problem.CodeScanUnavailable, "the virus scanner is not available; try again later", err)
	pe.Expose = true
	return pe
}

// ingestError maps what storing a file can fail with to a problem.
func ingestError(err error, limit int64) error {
	var mbe *http.MaxBytesError
	switch {
	case errors.Is(err, errTooLarge):
		return problem.Wrap(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, fmt.Sprintf("the file exceeds the limit of %d bytes", limit), err)
	case errors.As(err, &mbe):
		return problem.Wrap(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, fmt.Sprintf("the request exceeds the limit of %d bytes", mbe.Limit), err)
	}
	return problem.From(err)
}

// internalOr keeps a problem that came out of a transaction and wraps anything
// else as an internal error.
func internalOr(err error, what string) error {
	var pe *problem.Error
	if errors.As(err, &pe) {
		return err
	}
	return problem.Internal(fmt.Errorf("%s: %w", what, err))
}
