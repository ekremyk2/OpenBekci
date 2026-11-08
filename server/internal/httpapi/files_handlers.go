package httpapi

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoFiles = errors.New("files service not configured")

// maxFieldBytes bounds a small text field of the upload form.
const maxFieldBytes = 4 << 10

// downloadHeaders are sent with every file body: the type is the sniffed one
// and nothing may reinterpret it (nosniff), and a file opened on its own is
// sandboxed (no scripts, no same-origin access) whatever it contains.
func downloadHeaders(d *files.Download) gen.OperationsDownloadFile200ResponseHeaders {
	return gen.OperationsDownloadFile200ResponseHeaders{
		CacheControl:          ptr("private, no-store"),
		ContentDisposition:    ptr(d.ContentDisposition()),
		ContentSecurityPolicy: ptr("sandbox"),
		XContentTypeOptions:   ptr("nosniff"),
	}
}

func (s *apiServer) fileDTO(ctx context.Context, f files.File) gen.File {
	l := s.files.Links(ctx, f)
	links := gen.FileLinks{ExpiresAt: l.ExpiresAt, Variants: l.Variants, Original: nullable.NewNullNullable[string]()}
	if l.Original != "" {
		links.Original = nullable.NewNullableWithValue(l.Original)
	}
	variants := make(map[string]gen.FileVariant, len(f.Variants))
	for name, v := range f.Variants {
		variants[name] = gen.FileVariant{Width: v.Width, Height: v.Height, Size: v.Size, Mime: v.Mime}
	}
	out := gen.File{
		Id: f.ID, OrgId: f.OrgID, SiteId: nullUUID(f.SiteID), Name: f.Name, Size: f.Size, Mime: f.Mime, Sha256: f.SHA256,
		Status: gen.FileStatus(f.Status), Source: gen.FileSource(f.Source), UploadedBy: nullable.NewNullNullable[uuid.UUID](),
		Scope: nullable.NewNullNullable[string](), HasVariants: len(f.Variants) > 0,
		RetentionUntil: nullable.NewNullNullable[time.Time](),
		Variants:       variants, Links: links, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
	}
	// Who uploaded is for the uploader and the administrators only: a global
	// user id is not something every reader of a ticket photo should learn.
	if f.UploadedBy != nil && s.files.CanSeeUploader(ctx, f) {
		out.UploadedBy = nullable.NewNullableWithValue(*f.UploadedBy)
	}
	if f.Scope != nil {
		out.Scope = nullable.NewNullableWithValue(f.Scope.String())
	}
	if f.RetentionUntil != nil {
		out.RetentionUntil = nullable.NewNullableWithValue(*f.RetentionUntil)
	}
	return out
}

func (s *apiServer) OperationsListFiles(ctx context.Context, req gen.OperationsListFilesRequestObject) (gen.OperationsListFilesResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	p := req.Params
	lp := files.ListParams{}
	if p.Scope != nil {
		ref, err := files.ParseScope(*p.Scope)
		if err != nil {
			return nil, problem.Validation(problem.Field{Field: "scope", Code: "invalid_format"})
		}
		lp.Scope = &ref
	}
	if p.Limit != nil {
		lp.Limit = *p.Limit
	}
	if p.Cursor != nil {
		lp.Cursor = *p.Cursor
	}
	list, next, err := s.files.List(ctx, req.OrgId, lp)
	if err != nil {
		return nil, err
	}
	items := make([]gen.File, 0, len(list))
	for _, f := range list {
		items = append(items, s.fileDTO(ctx, f))
	}
	out := gen.FileList{Items: items, NextCursor: nullable.NewNullNullable[string]()}
	if next != "" {
		out.NextCursor = nullable.NewNullableWithValue(next)
	}
	return gen.OperationsListFiles200JSONResponse(out), nil
}

func (s *apiServer) OperationsGetFileMeta(ctx context.Context, req gen.OperationsGetFileMetaRequestObject) (gen.OperationsGetFileMetaResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	f, err := s.files.Get(ctx, req.OrgId, req.FileId)
	if err != nil {
		return nil, err
	}
	return gen.OperationsGetFileMeta200JSONResponse(s.fileDTO(ctx, f)), nil
}

func (s *apiServer) OperationsDeleteFile(ctx context.Context, req gen.OperationsDeleteFileRequestObject) (gen.OperationsDeleteFileResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	if err := s.files.Delete(ctx, req.OrgId, req.FileId); err != nil {
		return nil, err
	}
	return gen.OperationsDeleteFile204Response{}, nil
}

// OperationsUploadFile reads the multipart form as it arrives: the small
// fields first, then the file part, which is streamed through the file module
// to storage without being buffered. Anything after the file part is
// refused: the form is checked to its end and a file that arrived with an
// invalid form is discarded again.
func (s *apiServer) OperationsUploadFile(ctx context.Context, req gen.OperationsUploadFileRequestObject) (gen.OperationsUploadFileResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	if req.Body == nil {
		return nil, problem.BadRequest("the request body is missing")
	}
	var (
		name    string
		scope   *files.ScopeRef
		siteID  *uuid.UUID
		stored  *files.File
		seen    = map[string]bool{}
		hasher  = newFormHasher()
		invalid *problem.Error
	)
	for {
		part, err := req.Body.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return s.abandon(ctx, stored, bodyReadError(err))
		}
		field := part.FormName()
		if stored != nil {
			// a field after the file: the file is already stored, so the whole upload is refused and undone
			_ = part.Close()
			invalid = problem.Validation(problem.Field{Field: field, Code: "multipart_order"})
			break
		}
		if seen[field] {
			_ = part.Close()
			return nil, problem.Validation(problem.Field{Field: field, Code: "duplicate_field"})
		}
		seen[field] = true
		switch field {
		case "file":
			fname := part.FileName()
			if fname == "" {
				fname = name
			}
			up := files.UploadParams{OrgID: req.OrgId, SiteID: siteID, Name: fname, Scope: scope, Content: hasher.filePart(fname, part)}
			f, err := s.files.Upload(ctx, up)
			recordUploadDigest(ctx, hasher.sum())
			if err != nil {
				return nil, err
			}
			stored = &f
		case "scope", "name":
			v, err := formValue(part, field)
			if err != nil {
				return nil, err
			}
			hasher.field(field, v)
			if field == "name" {
				name = v
				break
			}
			ref, perr := files.ParseScope(v)
			if perr != nil {
				return nil, problem.Validation(problem.Field{Field: "scope", Code: "invalid_format"})
			}
			scope = &ref
		case "site_id":
			v, err := formValue(part, field)
			if err != nil {
				return nil, err
			}
			hasher.field(field, v)
			id, perr := uuid.Parse(v)
			if perr != nil {
				return nil, problem.Validation(problem.Field{Field: field, Code: "invalid_format"})
			}
			siteID = &id
		default:
			_ = part.Close()
			return nil, problem.Validation(problem.Field{Field: field, Code: "unknown_field"})
		}
	}
	if invalid != nil {
		return s.abandon(ctx, stored, invalid)
	}
	if stored == nil {
		return nil, problem.Validation(problem.Field{Field: "file", Code: "required"})
	}
	loc := "/v1/orgs/" + stored.OrgID.String() + "/files/" + stored.ID.String() + "/meta"
	return gen.OperationsUploadFile201JSONResponse{Body: s.fileDTO(ctx, *stored), Headers: gen.OperationsUploadFile201ResponseHeaders{Location: &loc}}, nil
}

// abandon undoes an upload whose request turned out to be invalid and returns
// err for the response.
func (s *apiServer) abandon(ctx context.Context, f *files.File, err error) (gen.OperationsUploadFileResponseObject, error) {
	if f != nil {
		if derr := s.files.Discard(context.WithoutCancel(ctx), *f); derr != nil {
			return nil, errors.Join(err, derr)
		}
	}
	return nil, err
}

// formValue reads a small text field of the form.
func formValue(part *multipart.Part, field string) (string, error) {
	b, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
	if err != nil {
		return "", bodyReadError(err)
	}
	if len(b) > maxFieldBytes {
		return "", problem.Validation(problem.Field{Field: field, Code: "too_long", Params: map[string]any{"max": maxFieldBytes}})
	}
	return string(b), nil
}

// OperationsDownloadFile streams the file: with a signed URL (exp and sig) it
// stands on the signature alone, otherwise through the session and the
// visibility checks. With the S3 driver it answers 302 to a presigned URL.
func (s *apiServer) OperationsDownloadFile(ctx context.Context, req gen.OperationsDownloadFileRequestObject) (gen.OperationsDownloadFileResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	variant := ""
	if req.Params.Variant != nil {
		variant = string(*req.Params.Variant)
	}
	var d *files.Download
	var err error
	switch {
	case req.Params.Sig != nil:
		if req.Params.Exp == nil {
			return nil, problem.Unauthenticated("the download link is invalid or has expired")
		}
		d, err = s.files.OpenSigned(ctx, req.OrgId, req.FileId, variant, *req.Params.Exp, *req.Params.Sig)
	case authz.PrincipalFromContext(ctx) != nil:
		d, err = s.files.Open(ctx, req.OrgId, req.FileId, variant)
	default:
		return nil, problem.Unauthenticated("authentication is required")
	}
	if err != nil {
		return nil, err
	}
	if d.RedirectURL != "" {
		return gen.OperationsDownloadFile302Response{Headers: gen.OperationsDownloadFile302ResponseHeaders{Location: &d.RedirectURL}}, nil
	}
	return gen.OperationsDownloadFile200AsteriskResponse{
		Body: d.Body, ContentType: d.Mime, ContentLength: d.Size, Headers: downloadHeaders(d),
	}, nil
}
