package documents

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// GeneratedFrom names the record a generated document was made from.
type GeneratedFrom struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
}

// Document is a filed file.
type Document struct {
	ID            uuid.UUID      `json:"id"`
	OrgID         uuid.UUID      `json:"org_id"`
	SiteID        uuid.UUID      `json:"site_id"`
	FolderID      uuid.UUID      `json:"folder_id"`
	Title         string         `json:"title"`
	FileID        uuid.UUID      `json:"file_id"`
	Mime          string         `json:"mime"`
	Size          int64          `json:"size"`
	Visibility    *Visibility    `json:"visibility"`
	Source        string         `json:"source"`
	GeneratedFrom *GeneratedFrom `json:"generated_from"`
	UploadedBy    *uuid.UUID     `json:"uploaded_by"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Version       int            `json:"version"`
	addressed     *uuid.UUID
}

const docSelect = `SELECT d.id, d.org_id, d.site_id, d.folder_id, d.title, d.file_id, f.mime, f.size, d.visibility_level, d.visibility_unit_ids, d.source,
	d.generated_type, d.generated_id, d.uploaded_by, d.created_at, d.updated_at, d.version, d.addressed_person_id
	FROM document d JOIN file f ON f.org_id = d.org_id AND f.id = d.file_id`

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	var level, gtype *string
	var units []uuid.UUID
	var gid *uuid.UUID
	err := row.Scan(&d.ID, &d.OrgID, &d.SiteID, &d.FolderID, &d.Title, &d.FileID, &d.Mime, &d.Size, &level, &units, &d.Source, &gtype, &gid, &d.UploadedBy,
		&d.CreatedAt, &d.UpdatedAt, &d.Version, &d.addressed)
	if err != nil {
		return d, err
	}
	if level != nil {
		d.Visibility = &Visibility{Level: *level, UnitIDs: units}
	}
	if gtype != nil && gid != nil {
		d.GeneratedFrom = &GeneratedFrom{Type: *gtype, ID: *gid}
	}
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	return d, nil
}

// canSee: documents in a unit folder follow the folder; others use their own
// visibility or the folder's. A document addressed to the caller is always
// visible to them (the former occupant's right, O8).
func (a access) canSee(t tree, d Document) bool {
	if d.addressed != nil && a.person != uuid.Nil && *d.addressed == a.person {
		return true
	}
	if (!a.read && !a.manage) || !a.folderVisible(t, d.FolderID) {
		return false
	}
	f := t[d.FolderID]
	if f.Kind != "unit" && d.Visibility != nil {
		return a.levelOK(*d.Visibility)
	}
	return true
}

// canWrite: management anywhere it can see; a resident only in their own
// unit's folder, on documents they uploaded.
func (a access) canWrite(t tree, folderID uuid.UUID, d *Document, uid *uuid.UUID) bool {
	f, ok := t[folderID]
	if !ok {
		return false
	}
	if a.manage && a.folderVisible(t, folderID) {
		return true
	}
	if a.uploadOwn && f.Kind == "unit" && f.UnitID != nil && contains(a.ownUnits, *f.UnitID) {
		if d == nil {
			return true
		}
		return d.UploadedBy != nil && uid != nil && *d.UploadedBy == *uid
	}
	return false
}

func contains(s []uuid.UUID, u uuid.UUID) bool {
	for _, x := range s {
		if x == u {
			return true
		}
	}
	return false
}

func (s *Service) loadDoc(ctx context.Context, tx pgx.Tx, orgID, docID uuid.UUID) (Document, access, tree, error) {
	var site uuid.UUID
	err := tx.QueryRow(ctx, `SELECT site_id FROM document WHERE org_id = $1 AND id = $2`, orgID, docID).Scan(&site)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, access{}, nil, problem.NotFound("no such document")
	}
	if err != nil {
		return Document{}, access{}, nil, err
	}
	a, err := s.accessOf(ctx, tx, orgID, site)
	if err != nil {
		return Document{}, a, nil, err
	}
	t, err := loadTree(ctx, tx, orgID, site)
	if err != nil {
		return Document{}, a, t, err
	}
	d, err := scanDoc(tx.QueryRow(ctx, docSelect+` WHERE d.org_id = $1 AND d.id = $2`, orgID, docID))
	if err != nil {
		return d, a, t, err
	}
	if !a.canSee(t, d) {
		return d, a, t, problem.NotFound("no such document")
	}
	return d, a, t, nil
}

// GetDocument returns a document the caller may see.
func (s *Service) GetDocument(ctx context.Context, orgID, docID uuid.UUID) (Document, error) {
	var d Document
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		d, _, _, err = s.loadDoc(ctx, tx, orgID, docID)
		return err
	})
	return d, tenancy.DBProblem(err, "document")
}

// DocFilter narrows ListDocuments.
type DocFilter struct {
	SiteID   uuid.UUID
	FolderID *uuid.UUID
	Source   string
	Q        string
}

// ListDocuments lists the documents of a site the caller may see, newest
// first. A former occupant (documents.read_addressed) sees only the documents
// addressed to them.
func (s *Service) ListDocuments(ctx context.Context, orgID uuid.UUID, f DocFilter, p tenancy.PageParams) ([]Document, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	if f.Source != "" && f.Source != "uploaded" && f.Source != "generated" {
		return nil, "", problem.Validation(problem.Field{Field: "source", Code: "invalid_value"})
	}
	var out []Document
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, err := s.accessOf(ctx, tx, orgID, f.SiteID)
		if err != nil {
			return err
		}
		t, err := loadTree(ctx, tx, orgID, f.SiteID)
		if err != nil {
			return err
		}
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(f.Q)) + "%"
		cursor := after
		for len(out) < limit+1 {
			rows, err := tx.Query(ctx, docSelect+` WHERE d.org_id = $1 AND d.site_id = $2 AND ($3 = '00000000-0000-0000-0000-000000000000'::uuid OR d.id < $3)
				AND ($4::uuid IS NULL OR d.folder_id = $4) AND ($5 = '' OR d.source = $5) AND ($6 = '' OR d.title ILIKE $7)
				ORDER BY d.id DESC LIMIT $8`, orgID, f.SiteID, cursor, f.FolderID, f.Source, strings.TrimSpace(f.Q), like, limit*3)
			if err != nil {
				return err
			}
			var batch []Document
			for rows.Next() {
				d, err := scanDoc(rows)
				if err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, d)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, d := range batch {
				if a.canSee(t, d) {
					out = append(out, d)
					if len(out) == limit+1 {
						break
					}
				}
			}
			if len(batch) < limit*3 || len(batch) == 0 {
				break
			}
			cursor = batch[len(batch)-1].ID
		}
		return nil
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "documents")
	}
	items, next := tenancy.Page(out, limit, func(d Document) uuid.UUID { return d.ID })
	return items, next, nil
}

type docAudit struct {
	FolderID   uuid.UUID `json:"folder_id"`
	Title      string    `json:"title"`
	FileID     uuid.UUID `json:"file_id"`
	Visibility string    `json:"visibility"`
	Source     string    `json:"source"`
}

func recordDoc(ctx context.Context, tx pgx.Tx, action string, before *Document, after Document) error {
	view := func(d Document) docAudit {
		v := "inherit"
		if d.Visibility != nil {
			v = d.Visibility.Level
		}
		return docAudit{d.FolderID, d.Title, d.FileID, v, d.Source}
	}
	var b any
	if before != nil {
		b = view(*before)
	}
	diff, _ := audit.Diff("document", b, view(after))
	site := after.SiteID
	return audit.Record(ctx, tx, audit.Event{OrgID: after.OrgID, SiteID: &site, Action: action, TargetType: "document", TargetID: after.ID, Diff: diff})
}

func actorUser(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

// CreateDocument files an uploaded file (files.upload first, then this) in a
// folder. Management files anywhere it can open; an owner or tenant files into
// their own unit's private folder (documents.upload_own).
func (s *Service) CreateDocument(ctx context.Context, orgID uuid.UUID, d tenancy.Doc) (Document, error) {
	if bad := d.Unknown("site_id", "folder_id", "title", "file_id", "visibility"); len(bad) > 0 {
		return Document{}, problem.Validation(bad...)
	}
	siteID, _, err := uuidOf(d, "site_id")
	if err != nil {
		return Document{}, err
	}
	folderID, _, err := uuidOf(d, "folder_id")
	if err != nil {
		return Document{}, err
	}
	fileID, _, err := uuidOf(d, "file_id")
	if err != nil {
		return Document{}, err
	}
	var bad []problem.Field
	for k, v := range map[string]*uuid.UUID{"site_id": siteID, "folder_id": folderID, "file_id": fileID} {
		if v == nil {
			bad = append(bad, problem.Field{Field: k, Code: "required"})
		}
	}
	tp, err := d.String("title")
	if err != nil {
		return Document{}, err
	}
	title := strings.TrimSpace(deref(tp))
	if l := len([]rune(title)); l < 1 || l > 200 {
		bad = append(bad, problem.Field{Field: "title", Code: "invalid_length"})
	}
	vis, _, err := parseVisibility(d, "visibility")
	if err != nil {
		return Document{}, err
	}
	if len(bad) > 0 {
		return Document{}, problem.Validation(bad...)
	}
	var out Document
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, err := s.accessOf(ctx, tx, orgID, *siteID)
		if err != nil {
			return err
		}
		if err := ensureUnitFolders(ctx, tx, orgID, *siteID); err != nil {
			return err
		}
		t, err := loadTree(ctx, tx, orgID, *siteID)
		if err != nil {
			return err
		}
		if _, ok := t[*folderID]; !ok {
			return problem.Validation(problem.Field{Field: "folder_id", Code: "invalid_value"})
		}
		if !a.canWrite(t, *folderID, nil, nil) {
			return problem.Forbidden("you may not file documents in this folder")
		}
		if vis != nil {
			if !a.manage || t[*folderID].Kind == "unit" {
				return problem.Validation(problem.Field{Field: "visibility", Code: "not_allowed"})
			}
			if err := s.unitsExist(ctx, tx, orgID, *siteID, vis.UnitIDs, "visibility"); err != nil {
				return err
			}
		}
		did := id.New()
		if _, err := s.files.Attach(ctx, tx, orgID, *fileID, files.ScopeRef{Type: "document", ID: did}); err != nil {
			return err
		}
		var lvl *string
		var units []uuid.UUID
		if vis != nil {
			lvl, units = &vis.Level, vis.UnitIDs
		}
		if _, err := tx.Exec(ctx, `INSERT INTO document (id, org_id, site_id, folder_id, title, file_id, visibility_level, visibility_unit_ids, uploaded_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, did, orgID, *siteID, *folderID, title, *fileID, lvl, nilIfEmpty(units), actorUser(ctx)); err != nil {
			return err
		}
		out, err = scanDoc(tx.QueryRow(ctx, docSelect+` WHERE d.org_id = $1 AND d.id = $2`, orgID, did))
		if err != nil {
			return err
		}
		return recordDoc(ctx, tx, "documents.create", nil, out)
	})
	return out, tenancy.DBProblem(err, "document")
}

// UpdateDocument renames, moves, re-visibilizes or replaces the file of a
// document (replacing overwrites: there are no versions).
func (s *Service) UpdateDocument(ctx context.Context, orgID, docID uuid.UUID, expected int, d tenancy.Doc) (Document, error) {
	if bad := d.Unknown("folder_id", "title", "file_id", "visibility"); len(bad) > 0 {
		return Document{}, problem.Validation(bad...)
	}
	var out Document
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, a, t, err := s.loadDoc(ctx, tx, orgID, docID)
		if err != nil {
			return err
		}
		if !a.canWrite(t, before.FolderID, &before, actorUser(ctx)) {
			return problem.Forbidden("you may not change this document")
		}
		if err := tx.QueryRow(ctx, `SELECT version FROM document WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, docID).Scan(&before.Version); err != nil {
			return err
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		next := before
		if d.Has("title") {
			tp, err := d.String("title")
			if err != nil {
				return err
			}
			next.Title = strings.TrimSpace(deref(tp))
			if l := len([]rune(next.Title)); l < 1 || l > 200 {
				return problem.Validation(problem.Field{Field: "title", Code: "invalid_length"})
			}
		}
		if f, ok, err := uuidOf(d, "folder_id"); err != nil {
			return err
		} else if ok {
			if f == nil {
				return problem.Validation(problem.Field{Field: "folder_id", Code: "required"})
			}
			if _, found := t[*f]; !found || !a.canWrite(t, *f, nil, nil) && !a.canWrite(t, *f, &before, actorUser(ctx)) {
				return problem.Validation(problem.Field{Field: "folder_id", Code: "invalid_value"})
			}
			next.FolderID = *f
		}
		if v, ok, err := parseVisibility(d, "visibility"); err != nil {
			return err
		} else if ok {
			if !a.manage || t[next.FolderID].Kind == "unit" {
				return problem.Validation(problem.Field{Field: "visibility", Code: "not_allowed"})
			}
			if v != nil {
				if err := s.unitsExist(ctx, tx, orgID, before.SiteID, v.UnitIDs, "visibility"); err != nil {
					return err
				}
			}
			next.Visibility = v
		}
		if nf, ok, err := uuidOf(d, "file_id"); err != nil {
			return err
		} else if ok && nf != nil && *nf != before.FileID {
			if before.Source == "generated" {
				return problem.Conflict("a generated document cannot be replaced")
			}
			if _, err := s.files.Attach(ctx, tx, orgID, *nf, files.ScopeRef{Type: "document", ID: docID}); err != nil {
				return err
			}
			if err := s.files.Detach(ctx, tx, orgID, before.FileID); err != nil {
				return err
			}
			next.FileID = *nf
		}
		var lvl *string
		var units []uuid.UUID
		if next.Visibility != nil {
			lvl, units = &next.Visibility.Level, next.Visibility.UnitIDs
		}
		if _, err := tx.Exec(ctx, `UPDATE document SET folder_id = $3, title = $4, file_id = $5, visibility_level = $6, visibility_unit_ids = $7, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, docID, next.FolderID, next.Title, next.FileID, lvl, nilIfEmpty(units)); err != nil {
			return err
		}
		out, err = scanDoc(tx.QueryRow(ctx, docSelect+` WHERE d.org_id = $1 AND d.id = $2`, orgID, docID))
		if err != nil {
			return err
		}
		return recordDoc(ctx, tx, "documents.update", &before, out)
	})
	return out, tenancy.DBProblem(err, "document")
}

// DeleteDocument removes a document; its file expires as an unattached draft.
func (s *Service) DeleteDocument(ctx context.Context, orgID, docID uuid.UUID, expected int) error {
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, a, t, err := s.loadDoc(ctx, tx, orgID, docID)
		if err != nil {
			return err
		}
		if !a.canWrite(t, before.FolderID, &before, actorUser(ctx)) {
			return problem.Forbidden("you may not delete this document")
		}
		if err := tx.QueryRow(ctx, `SELECT version FROM document WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, docID).Scan(&before.Version); err != nil {
			return err
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		if _, err := tx.Exec(ctx, `DELETE FROM document WHERE org_id = $1 AND id = $2`, orgID, docID); err != nil {
			return err
		}
		if err := s.files.Detach(ctx, tx, orgID, before.FileID); err != nil {
			return err
		}
		return recordDoc(ctx, tx, "documents.delete", &before, Document{ID: docID, OrgID: orgID, SiteID: before.SiteID, FolderID: before.FolderID, Title: before.Title, FileID: before.FileID, Source: before.Source})
	})
	return tenancy.DBProblem(err, "document")
}

// GeneratedParams describes a document the server made (statement, receipt,
// closing statement, minutes ...) to be filed without anybody uploading it.
type GeneratedParams struct {
	OrgID, SiteID uuid.UUID
	// Title is shown in the folder; FileName is the stored name (with extension).
	Title, FileName string
	Content         io.Reader
	// UnitID files it in that unit's private folder. Otherwise it goes to the
	// site folder named FolderName (SystemKey is its stable key: "statements",
	// "receipts", "minutes" ...), created with FolderVisibility the first time.
	UnitID           *uuid.UUID
	SystemKey        string
	FolderName       string
	FolderVisibility Visibility
	// Visibility overrides the folder's for this document (nil inherits).
	Visibility *Visibility
	// GeneratedType and GeneratedID name the source record; filing the same
	// record twice returns the first document.
	GeneratedType string
	GeneratedID   uuid.UUID
	// AddressedPersonID makes the document readable by that person after they
	// moved out (documents.read_addressed).
	AddressedPersonID *uuid.UUID
}

// Filer is what modules that generate documents depend on.
type Filer interface {
	FileGenerated(ctx context.Context, tx pgx.Tx, p GeneratedParams) (Document, error)
}

var _ Filer = (*Service)(nil)

// FileGenerated stores generated content as a file (source generated) and
// files it in the right folder with the right visibility, inside the caller's
// tenant transaction tx. The file blob is written first through its own
// transaction; a rolled back caller leaves an orphan file that retention
// removes. It checks no permission: the caller authorized its own action.
func (s *Service) FileGenerated(ctx context.Context, tx pgx.Tx, p GeneratedParams) (Document, error) {
	if p.GeneratedType == "" || p.GeneratedID == uuid.Nil {
		return Document{}, problem.Internal(errors.New("documents: generated document without its source record"))
	}
	var existing uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM document WHERE org_id = $1 AND generated_type = $2 AND generated_id = $3`, p.OrgID, p.GeneratedType, p.GeneratedID).Scan(&existing)
	if err == nil {
		return scanDoc(tx.QueryRow(ctx, docSelect+` WHERE d.org_id = $1 AND d.id = $2`, p.OrgID, existing))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Document{}, err
	}
	var folderID uuid.UUID
	if p.UnitID != nil {
		if err := ensureUnitFolders(ctx, tx, p.OrgID, p.SiteID); err != nil {
			return Document{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM folder WHERE org_id = $1 AND unit_id = $2`, p.OrgID, *p.UnitID).Scan(&folderID); err != nil {
			return Document{}, err
		}
	} else {
		if p.SystemKey == "" {
			return Document{}, problem.Internal(errors.New("documents: generated document without a folder"))
		}
		v := p.FolderVisibility
		if v.Level == "" {
			v.Level = LevelBoard
		}
		name := p.FolderName
		if name == "" {
			name = p.SystemKey
		}
		fid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO folder (id, org_id, site_id, name, visibility_level, visibility_unit_ids, is_system, system_key) VALUES ($1, $2, $3, $4, $5, $6, true, $7)
			ON CONFLICT (org_id, site_id, system_key) WHERE system_key IS NOT NULL DO NOTHING`, fid, p.OrgID, p.SiteID, name, v.Level, nilIfEmpty(v.UnitIDs), p.SystemKey); err != nil {
			return Document{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM folder WHERE org_id = $1 AND site_id = $2 AND system_key = $3`, p.OrgID, p.SiteID, p.SystemKey).Scan(&folderID); err != nil {
			return Document{}, err
		}
	}
	did := id.New()
	site := p.SiteID
	f, err := s.files.UploadGenerated(ctx, files.UploadParams{OrgID: p.OrgID, SiteID: &site, Scope: &files.ScopeRef{Type: "document", ID: did}, Name: p.FileName, Content: p.Content})
	if err != nil {
		return Document{}, err
	}
	var lvl *string
	var units []uuid.UUID
	if p.Visibility != nil {
		lvl, units = &p.Visibility.Level, p.Visibility.UnitIDs
	}
	if _, err := tx.Exec(ctx, `INSERT INTO document (id, org_id, site_id, folder_id, title, file_id, visibility_level, visibility_unit_ids, source, generated_type, generated_id, addressed_person_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'generated', $9, $10, $11)`,
		did, p.OrgID, p.SiteID, folderID, p.Title, f.ID, lvl, nilIfEmpty(units), p.GeneratedType, p.GeneratedID, p.AddressedPersonID); err != nil {
		return Document{}, err
	}
	out, err := scanDoc(tx.QueryRow(ctx, docSelect+` WHERE d.org_id = $1 AND d.id = $2`, p.OrgID, did))
	if err != nil {
		return out, err
	}
	return out, recordDoc(ctx, tx, "documents.generated", nil, out)
}

// resolver is the file visibility resolver of the "document" scope.
type resolver struct{ s *Service }

func (r resolver) CanRead(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
	orgID := authz.GrantsFromContext(ctx).OrgID
	_, _, _, err := r.s.loadDoc(ctx, tx, orgID, ref.ID)
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r resolver) CanAttach(context.Context, pgx.Tx, files.ScopeRef) (bool, error) { return false, nil }
