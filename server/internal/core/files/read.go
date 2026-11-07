package files

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoFile = problem.NotFound("no such file")

// gate is the first check of a read or management call: the caller needs perm
// somewhere in the organization. orgWide says the grant covers the whole
// organization; otherwise it is limited to sites (sites lists them) and each
// file is checked against its own site.
func gate(ctx context.Context, perm authz.Permission, orgID uuid.UUID) (orgWide bool, sites []uuid.UUID, err error) {
	if authz.PrincipalFromContext(ctx) == nil {
		return false, nil, problem.Unauthenticated("authentication is required")
	}
	if authz.Has(ctx, perm, authz.Scope{OrgID: orgID}) {
		return true, nil, nil
	}
	grants := authz.GrantsFromContext(ctx)
	if grants.OrgID != orgID { // grants resolved for another organization carry no weight here
		return false, nil, problem.Forbidden("you do not have the " + string(perm) + " permission here")
	}
	any := false
	for _, g := range grants.Items {
		if g.Permission != perm {
			continue
		}
		any = true
		if g.ScopeType == authz.ScopeSite && g.ScopeID != nil {
			sites = append(sites, *g.ScopeID)
		}
	}
	if any {
		return false, sites, nil
	}
	return false, nil, problem.Forbidden("you do not have the " + string(perm) + " permission here")
}

func (s *Service) holds(ctx context.Context, perm authz.Permission, f File) bool {
	return authz.Has(ctx, perm, authz.Scope{OrgID: f.OrgID, SiteID: f.SiteID})
}

func (s *Service) isUploader(ctx context.Context, f File) bool {
	pr := authz.PrincipalFromContext(ctx)
	return pr != nil && f.UploadedBy != nil && *f.UploadedBy == pr.UserID
}

// CanSeeUploader reports whether the caller may be told who uploaded f: the
// uploader and holders of files.manage.
func (s *Service) CanSeeUploader(ctx context.Context, f File) bool {
	return s.isUploader(ctx, f) || s.holds(ctx, PermManage, f)
}

// canRead decides whether the caller may see f: files.read is the gate; then
// an administrator (files.manage) always may. A draft (not attached to a
// resource) is visible to its uploader. Once a file is attached, whoever
// uploaded it is treated like anyone else: the attached resource's visibility
// resolver decides, so a file that moved to a resource the uploader may no
// longer read is hidden from them too. Only ready files are visible to
// non-administrators.
func (s *Service) canRead(ctx context.Context, tx pgx.Tx, f File) (bool, error) {
	if f.Status == StatusPending || f.Status == StatusDeleted || !s.holds(ctx, PermRead, f) {
		return false, nil
	}
	if s.holds(ctx, PermManage, f) {
		return true, nil
	}
	if f.Scope == nil {
		return s.isUploader(ctx, f), nil
	}
	if f.Status != StatusReady {
		return false, nil
	}
	e, ok := s.scope(f.Scope.Type)
	if !ok {
		return false, nil
	}
	return e.resolver.CanRead(ctx, tx, *f.Scope)
}

func (s *Service) load(ctx context.Context, tx pgx.Tx, orgID, fileID uuid.UUID) (File, error) {
	return s.loadRow(ctx, tx, orgID, fileID, "")
}

// loadForUpdate is load with the row locked until the transaction ends, for
// the calls that change a file after deciding on what they read.
func (s *Service) loadForUpdate(ctx context.Context, tx pgx.Tx, orgID, fileID uuid.UUID) (File, error) {
	return s.loadRow(ctx, tx, orgID, fileID, " FOR UPDATE")
}

func (s *Service) loadRow(ctx context.Context, tx pgx.Tx, orgID, fileID uuid.UUID, lock string) (File, error) {
	f, err := scanFile(tx.QueryRow(ctx, `SELECT `+fileColumns+` FROM file WHERE org_id = $1 AND id = $2`+lock, orgID, fileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, errNoFile
	}
	return f, err
}

// Get returns a file's metadata. It needs files.read and the file to be
// visible to the caller (see canRead); anything else is 404.
func (s *Service) Get(ctx context.Context, orgID, fileID uuid.UUID) (File, error) {
	if _, _, err := gate(ctx, PermRead, orgID); err != nil {
		return File{}, err
	}
	var f File
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if f, err = s.load(ctx, tx, orgID, fileID); err != nil {
			return err
		}
		ok, err := s.canRead(ctx, tx, f)
		if err != nil {
			return err
		}
		if !ok {
			return errNoFile
		}
		return nil
	})
	if err != nil {
		return File{}, internalOr(err, "get file")
	}
	return f, nil
}

// ListParams selects files for List.
type ListParams struct {
	// Scope lists the files attached to a resource; the caller needs read
	// access to it (administrators always have it). Without a scope an
	// administrator lists every file of the organization and anyone else the
	// drafts they uploaded (a file that is attached is visible through its resource).
	Scope  *ScopeRef
	Cursor string
	Limit  int
}

// Default and maximum page size, as in the API conventions.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

type listCursor struct {
	AtMicro int64     `json:"a"`
	ID      uuid.UUID `json:"i"`
}

// List returns one page of files, newest first, and the cursor of the next
// page ("" on the last). It needs files.read.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, p ListParams) ([]File, string, error) {
	orgWide, sites, err := gate(ctx, PermRead, orgID)
	if err != nil {
		return nil, "", err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		return nil, "", problem.Validation(problem.Field{Field: "limit", Code: "too_large", Params: map[string]any{"max": MaxLimit}})
	}
	var atBound *time.Time
	var idBound *uuid.UUID
	if p.Cursor != "" {
		var c listCursor
		raw, derr := base64.RawURLEncoding.DecodeString(p.Cursor)
		if derr == nil {
			derr = json.Unmarshal(raw, &c)
		}
		if derr != nil || c.ID == uuid.Nil {
			return nil, "", problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
		}
		t := time.UnixMicro(c.AtMicro).UTC()
		atBound, idBound = &t, &c.ID
	}
	if p.Scope != nil && !scopeTypeRE.MatchString(p.Scope.Type) {
		return nil, "", problem.Validation(problem.Field{Field: "scope_type", Code: "invalid_format"})
	}
	manager := authz.Has(ctx, PermManage, authz.Scope{OrgID: orgID})
	me := authz.PrincipalFromContext(ctx).UserID

	var out []File
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var scopeType *string
		var scopeID, uploader *uuid.UUID
		if p.Scope != nil {
			scopeType, scopeID = &p.Scope.Type, &p.Scope.ID
			if !manager {
				e, ok := s.scope(p.Scope.Type)
				if !ok {
					return errNoFile
				}
				allowed, err := e.resolver.CanRead(ctx, tx, *p.Scope)
				if err != nil {
					return err
				}
				if !allowed {
					return errNoFile
				}
			}
		} else if !manager {
			uploader = &me
		}
		var siteFilter []uuid.UUID
		if !orgWide {
			siteFilter = append([]uuid.UUID{}, sites...)
		}
		rows, err := tx.Query(ctx, `
			SELECT `+fileColumns+` FROM file
			WHERE org_id = $1 AND status NOT IN ('deleted', 'pending')
			  AND ($2::text IS NULL OR scope_type = $2)
			  AND ($3::uuid IS NULL OR scope_id = $3)
			  AND ($4::uuid IS NULL OR (uploaded_by = $4 AND scope_id IS NULL))
			  AND ($5::boolean OR status = 'ready')
			  AND ($6::uuid[] IS NULL OR site_id = ANY ($6))
			  AND ($7::timestamptz IS NULL OR (created_at, id) < ($7, $8::uuid))
			ORDER BY created_at DESC, id DESC
			LIMIT $9`,
			orgID, scopeType, scopeID, uploader, manager, siteFilter, atBound, idBound, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", internalOr(err, "list files")
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		b, _ := json.Marshal(listCursor{AtMicro: last.CreatedAt.UnixMicro(), ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, next, nil
}

// Download is an opened file ready to stream. The caller closes Body.
type Download struct {
	File File
	// Name is the file name to offer.
	Name string
	// Mime and Size describe the stream (the variant's, for a variant).
	Mime string
	Size int64
	// Inline is true for images the browser may display; everything else is
	// offered as an attachment.
	Inline bool
	Body   io.ReadCloser
	// RedirectURL is set instead of Body when the store serves the download
	// itself (S3): answer 302 to it.
	RedirectURL string
}

// ContentDisposition is the Content-Disposition header value: inline for
// images, attachment for everything else, with the name encoded safely.
func (d Download) ContentDisposition() string { return disposition(d.Inline, d.Name) }

func disposition(inline bool, name string) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	if v := mime.FormatMediaType(kind, map[string]string{"filename": name}); v != "" {
		return v
	}
	return kind
}

// Open opens a file (or one of its variants: thumb, medium; images only) for
// download. It needs files.read and the file to be visible to the caller; a
// quarantined file cannot be downloaded by anyone.
func (s *Service) Open(ctx context.Context, orgID, fileID uuid.UUID, variant string) (*Download, error) {
	if _, _, err := gate(ctx, PermRead, orgID); err != nil {
		return nil, err
	}
	var f File
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if f, err = s.load(ctx, tx, orgID, fileID); err != nil {
			return err
		}
		ok, err := s.canRead(ctx, tx, f)
		if err != nil {
			return err
		}
		if !ok {
			return errNoFile
		}
		return nil
	})
	if err != nil {
		return nil, internalOr(err, "open file")
	}
	return s.open(ctx, f, variant)
}

// OpenSigned opens a file for a caller that holds a signed download URL
// instead of a session (an image tag, the mobile app's image loader). The
// signature covers the organization, file and variant and expires.
func (s *Service) OpenSigned(ctx context.Context, orgID, fileID uuid.UUID, variant string, expUnix int64, sig string) (*Download, error) {
	if s.cfg.Signer == nil {
		return nil, problem.Unauthenticated("authentication is required")
	}
	msg := storage.DownloadMessage(orgID.String(), fileID.String(), variant)
	if err := s.cfg.Signer.Verify(msg, expUnix, sig, s.now()); err != nil {
		return nil, problem.Unauthenticated("the download link is invalid or has expired")
	}
	var f File
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		f, err = s.load(ctx, tx, orgID, fileID)
		return err
	})
	if err != nil {
		return nil, internalOr(err, "open file")
	}
	if f.Status != StatusReady {
		return nil, errNoFile
	}
	return s.open(ctx, f, variant)
}

func (s *Service) open(ctx context.Context, f File, variant string) (*Download, error) {
	if f.Status != StatusReady {
		return nil, errNoFile // quarantined, pending and deleted files have no readable content
	}
	d := &Download{File: f, Name: f.Name, Mime: f.Mime, Size: f.Size, Inline: isImageMIME(f.Mime)}
	key := f.Key
	if variant != "" {
		v, ok := f.Variants[variant]
		if !ok {
			return nil, problem.NotFound("this file has no such variant")
		}
		key, d.Mime, d.Size = variantKey(f.Key, variant), v.Mime, v.Size
	}
	if storage.DirectDownloads(s.blob) {
		// S3: the caller is authorized, so hand out a short-lived URL on the bucket.
		u, err := s.blob.SignedURL(ctx, key, s.cfg.LinkTTL,
			storage.WithDisposition(disposition(d.Inline, f.Name)), storage.WithContentType(d.Mime))
		if err == nil {
			d.RedirectURL = u
			return d, nil
		}
		s.cfg.Log.WarnContext(ctx, "cannot sign a download url; streaming instead", "file_id", f.ID, "error", err)
	}
	rc, err := s.blob.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		s.cfg.Log.ErrorContext(ctx, "file blob is missing from storage", "file_id", f.ID, "org_id", f.OrgID, "key", key)
		return nil, errNoFile
	}
	if err != nil {
		return nil, problem.Internal(fmt.Errorf("open blob: %w", err))
	}
	d.Body = rc
	return d, nil
}

// Links are short-lived URLs that download a file without a session.
type Links struct {
	Original  string
	Variants  map[string]string
	ExpiresAt time.Time
}

// Links returns download URLs for f, valid for the configured link lifetime.
// Call it for a file the caller may read (after Get, List or Upload). The
// local driver signs paths on this server; the S3 driver presigns GET URLs.
// A URL that cannot be made is left empty.
func (s *Service) Links(ctx context.Context, f File) Links {
	l := Links{Variants: map[string]string{}, ExpiresAt: s.now().Add(s.cfg.LinkTTL)}
	if f.Status != StatusReady {
		return l
	}
	opts := []storage.SignOption{storage.WithDisposition(disposition(isImageMIME(f.Mime), f.Name)), storage.WithContentType(f.Mime)}
	var err error
	if l.Original, err = s.blob.SignedURL(ctx, f.Key, s.cfg.LinkTTL, opts...); err != nil {
		s.cfg.Log.WarnContext(ctx, "cannot sign a download url", "file_id", f.ID, "error", err)
	}
	for name, v := range f.Variants {
		u, err := s.blob.SignedURL(ctx, variantKey(f.Key, name), s.cfg.LinkTTL,
			storage.WithDisposition(disposition(true, f.Name)), storage.WithContentType(v.Mime))
		if err != nil {
			s.cfg.Log.WarnContext(ctx, "cannot sign a download url", "file_id", f.ID, "variant", name, "error", err)
			continue
		}
		l.Variants[name] = u
	}
	return l
}

// Delete marks a file deleted; its blobs are removed by maintenance.prune_files.
//
// Who may: a holder of files.manage (at the file's site, if it has one) may
// delete any file. The uploader of a draft (a file not attached to a resource)
// may discard it with files.upload; the uploader of an attached file gets 409
// conflict, as it belongs to the resource now and the owning module detaches
// it. Someone who may read the file but is neither gets 403, and someone who
// may not read it 404.
func (s *Service) Delete(ctx context.Context, orgID, fileID uuid.UUID) error {
	if _, _, err := gate(ctx, PermUpload, orgID); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		f, err := s.loadForUpdate(ctx, tx, orgID, fileID)
		if err != nil {
			return err
		}
		if f.Status == StatusDeleted || f.Status == StatusPending {
			return errNoFile
		}
		if !s.holds(ctx, PermManage, f) {
			ok, err := s.canRead(ctx, tx, f)
			if err != nil {
				return err
			}
			switch {
			case !ok:
				return errNoFile
			case !s.isUploader(ctx, f) || !s.holds(ctx, PermUpload, f):
				return problem.Forbidden("you may not delete this file")
			case f.Scope != nil:
				return problem.Conflict("the file is attached to a resource; only an administrator or the resource's module can remove it")
			}
		}
		before := f.auditView()
		f.Status = StatusDeleted
		if _, err := tx.Exec(ctx, `UPDATE file SET status = 'deleted', deleted_at = $2, updated_at = $2 WHERE id = $1`, f.ID, s.now()); err != nil {
			return err
		}
		return s.record(ctx, tx, "files.delete", f, before, f.auditView())
	})
	if err != nil {
		return internalOr(err, "delete file")
	}
	return nil
}

// Attach attaches an unattached file to a resource, inside tx (the caller's
// tenant transaction for orgID, so the attachment commits or rolls back with
// the resource, for example a ticket and its photos). The module that owns the
// resource has already authorized the caller; Attach checks the file: it must
// be ready (a file the virus scanner flagged is 409 conflict and keeps its
// expiry) and unattached, and uploaded by the caller (or the caller must
// hold files.manage). Attaching clears a draft's expiry. The scope type needs
// no registered resolver here, but readers cannot see the file through
// visibility until one is registered. Beyond a scope type's WithMaxFiles limit
// it answers 409 conflict.
func (s *Service) Attach(ctx context.Context, tx pgx.Tx, orgID, fileID uuid.UUID, ref ScopeRef) (File, error) {
	if !scopeTypeRE.MatchString(ref.Type) || ref.ID == uuid.Nil {
		return File{}, problem.Validation(problem.Field{Field: "scope", Code: "invalid_value"})
	}
	// The row is locked from here: the maintenance job cannot expire a draft
	// between this read and the update below.
	f, err := s.loadForUpdate(ctx, tx, orgID, fileID)
	if err != nil {
		return File{}, internalOr(err, "attach file")
	}
	if f.Status != StatusReady && f.Status != StatusQuarantined {
		return File{}, errNoFile
	}
	if pr := authz.PrincipalFromContext(ctx); pr != nil && !s.isUploader(ctx, f) && !s.holds(ctx, PermManage, f) {
		return File{}, errNoFile // somebody else's file: do not reveal it exists
	}
	if f.Scope != nil {
		return File{}, problem.Conflict("the file is already attached to a resource")
	}
	if f.Status == StatusQuarantined {
		return File{}, problem.Conflict("the virus scanner flagged this file; it cannot be attached to a resource")
	}
	if e, ok := s.scope(ref.Type); ok {
		if err := s.checkMax(ctx, tx, orgID, ref, e.maxFiles); err != nil {
			return File{}, err
		}
	}
	before := f.auditView()
	f.Scope, f.RetentionUntil = &ref, nil
	tag, err := tx.Exec(ctx, `
		UPDATE file SET scope_type = $2, scope_id = $3, retention_until = NULL, updated_at = $4
		WHERE id = $1 AND status = 'ready' AND scope_type IS NULL`, f.ID, ref.Type, ref.ID, s.now())
	if err != nil {
		return File{}, internalOr(err, "attach file")
	}
	if tag.RowsAffected() != 1 {
		return File{}, problem.Conflict("the file changed while it was being attached")
	}
	if err := s.record(ctx, tx, "files.attach", f, before, f.auditView()); err != nil {
		return File{}, internalOr(err, "attach file")
	}
	return f, nil
}

// Detach detaches a file from its resource inside tx. The file becomes a
// draft again and expires after the unattached lifetime unless attached
// elsewhere; delete it with Delete (or let it expire) to get rid of it. The
// caller (a module) has authorized the change to the resource.
func (s *Service) Detach(ctx context.Context, tx pgx.Tx, orgID, fileID uuid.UUID) error {
	f, err := s.loadForUpdate(ctx, tx, orgID, fileID)
	if err != nil {
		return internalOr(err, "detach file")
	}
	if f.Scope == nil || f.Status == StatusDeleted {
		return errNoFile
	}
	before := f.auditView()
	f.Scope = nil
	until := s.now().Add(s.cfg.UnattachedTTL)
	if _, err := tx.Exec(ctx, `UPDATE file SET scope_type = NULL, scope_id = NULL, retention_until = $2, updated_at = $3 WHERE id = $1`, f.ID, until, s.now()); err != nil {
		return internalOr(err, "detach file")
	}
	if err := s.record(ctx, tx, "files.detach", f, before, f.auditView()); err != nil {
		return internalOr(err, "detach file")
	}
	return nil
}

// Usage is what an organization stores.
type Usage struct {
	Files int
	Bytes int64
}

// Usage returns the organization's storage usage (originals and variants of
// files that were not purged yet). It needs files.manage.
func (s *Service) Usage(ctx context.Context, orgID uuid.UUID) (Usage, error) {
	if err := authz.Require(ctx, PermManage, authz.Scope{OrgID: orgID}); err != nil {
		return Usage{}, err
	}
	var u Usage
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT files, bytes FROM org_storage_usage WHERE org_id = $1`, orgID).Scan(&u.Files, &u.Bytes)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return Usage{}, problem.Internal(fmt.Errorf("storage usage: %w", err))
	}
	return u, nil
}
