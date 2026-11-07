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
		Status: gen.FileStatus(f.Status), Source: gen.FileSource(f.Source), UploadedBy: nullUUID(f.UploadedBy),
		ScopeType: nullable.NewNullNullable[string](), ScopeId: nullable.NewNullNullable[uuid.UUID](),
		RetentionUntil: nullable.NewNullNullable[time.Time](),
		Variants:       variants, Links: links, CreatedAt: f.CreatedAt,
	}
	if f.Scope != nil {
		out.ScopeType = nullable.NewNullableWithValue(f.Scope.Type)
		out.ScopeId = nullable.NewNullableWithValue(f.Scope.ID)
	}
	if f.RetentionUntil != nil {
		out.RetentionUntil = nullable.NewNullableWithValue(*f.RetentionUntil)
	}
	return out
}

func (s *apiServer) FilesList(ctx context.Context, req gen.FilesListRequestObject) (gen.FilesListResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	p := req.Params
	lp := files.ListParams{}
	switch {
	case p.ScopeType != nil && p.ScopeId == nil:
		return nil, problem.Validation(problem.Field{Field: "scope_id", Code: "required"})
	case p.ScopeType == nil && p.ScopeId != nil:
		return nil, problem.Validation(problem.Field{Field: "scope_type", Code: "required"})
	case p.ScopeType != nil:
		lp.Scope = &files.ScopeRef{Type: *p.ScopeType, ID: *p.ScopeId}
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
	return gen.FilesList200JSONResponse(out), nil
}

func (s *apiServer) FilesGet(ctx context.Context, req gen.FilesGetRequestObject) (gen.FilesGetResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	f, err := s.files.Get(ctx, req.OrgId, req.FileId)
	if err != nil {
		return nil, err
	}
	return gen.FilesGet200JSONResponse(s.fileDTO(ctx, f)), nil
}

func (s *apiServer) FilesDelete(ctx context.Context, req gen.FilesDeleteRequestObject) (gen.FilesDeleteResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	if err := s.files.Delete(ctx, req.OrgId, req.FileId); err != nil {
		return nil, err
	}
	return gen.FilesDelete204Response{}, nil
}

// FilesUpload reads the multipart form as it arrives: the small fields first,
// then the file part, which is streamed through the file module to storage
// without being buffered. Parts after the file are ignored.
func (s *apiServer) FilesUpload(ctx context.Context, req gen.FilesUploadRequestObject) (gen.FilesUploadResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	if req.Body == nil {
		return nil, problem.BadRequest("the request body is missing")
	}
	var (
		scopeType, name string
		scopeID, siteID *uuid.UUID
		stored          *files.File
		fh              = newFormHasher()
	)
	for {
		part, err := req.Body.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, bodyReadError(err)
		}
		if stored != nil {
			_ = part.Close() // fields after the file are ignored
			continue
		}
		switch field := part.FormName(); field {
		case "file":
			fname := part.FileName()
			if fname == "" {
				fname = name
			}
			up := files.UploadParams{OrgID: req.OrgId, SiteID: siteID, Name: fname, Content: fh.filePart(fname, part)}
			switch {
			case scopeType != "" && scopeID == nil:
				return nil, problem.Validation(problem.Field{Field: "scope_id", Code: "required"})
			case scopeType == "" && scopeID != nil:
				return nil, problem.Validation(problem.Field{Field: "scope_type", Code: "required"})
			case scopeType != "":
				up.Scope = &files.ScopeRef{Type: scopeType, ID: *scopeID}
			}
			f, err := s.files.Upload(ctx, up)
			recordUploadDigest(ctx, fh.sum())
			if err != nil {
				return nil, err
			}
			stored = &f
		case "scope_type", "name":
			v, err := formValue(part, field)
			if err != nil {
				return nil, err
			}
			fh.field(field, v)
			if field == "scope_type" {
				scopeType = v
			} else {
				name = v
			}
		case "scope_id", "site_id":
			v, err := formValue(part, field)
			if err != nil {
				return nil, err
			}
			fh.field(field, v)
			id, perr := uuid.Parse(v)
			if perr != nil {
				return nil, problem.Validation(problem.Field{Field: field, Code: "invalid_format"})
			}
			if field == "scope_id" {
				scopeID = &id
			} else {
				siteID = &id
			}
		default:
			_ = part.Close()
			return nil, problem.Validation(problem.Field{Field: field, Code: "unknown_field"})
		}
	}
	if stored == nil {
		return nil, problem.Validation(problem.Field{Field: "file", Code: "required"})
	}
	loc := "/v1/orgs/" + stored.OrgID.String() + "/files/" + stored.ID.String()
	return gen.FilesUpload201JSONResponse{Body: s.fileDTO(ctx, *stored), Headers: gen.FilesUpload201ResponseHeaders{Location: &loc}}, nil
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

// FilesDownload streams the file: with a session through the visibility
// checks, without one through the signature of a links URL.
func (s *apiServer) FilesDownload(ctx context.Context, req gen.FilesDownloadRequestObject) (gen.FilesDownloadResponseObject, error) {
	if s.files == nil {
		return nil, problem.Internal(errNoFiles)
	}
	variant := ""
	if req.Params.Variant != nil {
		variant = string(*req.Params.Variant)
	}
	var d *files.Download
	var err error
	if authz.PrincipalFromContext(ctx) != nil {
		d, err = s.files.Open(ctx, req.OrgId, req.FileId, variant)
	} else {
		if req.Params.Exp == nil || req.Params.Sig == nil {
			return nil, problem.Unauthenticated("authentication is required")
		}
		d, err = s.files.OpenSigned(ctx, req.OrgId, req.FileId, variant, *req.Params.Exp, *req.Params.Sig)
	}
	if err != nil {
		return nil, err
	}
	return gen.FilesDownload200AsteriskResponse{
		Body: d.Body, ContentType: d.Mime, ContentLength: d.Size,
		Headers: gen.FilesDownload200ResponseHeaders{
			CacheControl:        ptr("private, no-store"),
			ContentDisposition:  ptr(d.ContentDisposition()),
			XContentTypeOptions: ptr("nosniff"),
		},
	}, nil
}
