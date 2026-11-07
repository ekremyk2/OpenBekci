package files

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Status is where a file is in its life.
type Status string

// Statuses.
const (
	// StatusPending: being stored or scanned; invisible to readers.
	StatusPending Status = "pending"
	// StatusReady: stored, scanned if a scanner is configured, readable.
	StatusReady Status = "ready"
	// StatusQuarantined: the virus scan flagged it; only administrators see it.
	StatusQuarantined Status = "quarantined"
	// StatusDeleted: removed; the blobs are purged by maintenance.prune_files.
	StatusDeleted Status = "deleted"
)

// Source says where a file came from.
type Source string

// Sources. Generated files are statements, receipts, certificates and minutes
// the server produced itself (decision O8).
const (
	SourceUploaded  Source = "uploaded"
	SourceGenerated Source = "generated"
)

// ScopeRef names the resource a file is attached to: a scope type registered
// by the module that owns it ("ticket", "document", ...) and the resource id.
type ScopeRef struct {
	Type string
	ID   uuid.UUID
}

// Variant describes a generated rendition of an image.
type Variant struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
	Mime   string `json:"mime"`
}

// Image variants generated on upload.
const (
	VariantThumb  = "thumb"
	VariantMedium = "medium"
)

// File is a stored file.
type File struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	SiteID         *uuid.UUID
	Key            string
	Name           string
	Size           int64
	Mime           string
	SHA256         string
	UploadedBy     *uuid.UUID
	Scope          *ScopeRef
	Status         Status
	Source         Source
	RetentionUntil *time.Time
	Variants       map[string]Variant
	CreatedAt      time.Time
}

// IsImage reports whether the file is an image (it then has variants).
func (f File) IsImage() bool { return isImageMIME(f.Mime) }

const fileColumns = `id, org_id, site_id, key, name, size, mime, coalesce(sha256, ''), uploaded_by, scope_type, scope_id,
	status, source, retention_until, variants, created_at`

func scanFile(row pgx.Row) (File, error) {
	var f File
	var scopeType *string
	var scopeID *uuid.UUID
	var variants []byte
	var status, source string
	err := row.Scan(&f.ID, &f.OrgID, &f.SiteID, &f.Key, &f.Name, &f.Size, &f.Mime, &f.SHA256, &f.UploadedBy, &scopeType, &scopeID,
		&status, &source, &f.RetentionUntil, &variants, &f.CreatedAt)
	if err != nil {
		return File{}, err
	}
	f.Status, f.Source = Status(status), Source(source)
	if scopeType != nil && scopeID != nil {
		f.Scope = &ScopeRef{Type: *scopeType, ID: *scopeID}
	}
	f.CreatedAt = f.CreatedAt.UTC()
	if f.RetentionUntil != nil {
		t := f.RetentionUntil.UTC()
		f.RetentionUntil = &t
	}
	f.Variants = map[string]Variant{}
	if len(variants) > 0 {
		if err := json.Unmarshal(variants, &f.Variants); err != nil {
			return File{}, err
		}
	}
	return f, nil
}

// auditView is what a file event's diff records. The JSON tags are the audit
// field names; name is registered personal data (see package pii).
type auditView struct {
	Name      string  `json:"name"`
	Size      int64   `json:"size"`
	Mime      string  `json:"mime"`
	SHA256    string  `json:"sha256"`
	Status    string  `json:"status"`
	Source    string  `json:"source"`
	SiteID    *string `json:"site_id"`
	ScopeType *string `json:"scope_type"`
	ScopeID   *string `json:"scope_id"`
}

func (f File) auditView() auditView {
	v := auditView{Name: f.Name, Size: f.Size, Mime: f.Mime, SHA256: f.SHA256, Status: string(f.Status), Source: string(f.Source)}
	if f.SiteID != nil {
		s := f.SiteID.String()
		v.SiteID = &s
	}
	if f.Scope != nil {
		t, i := f.Scope.Type, f.Scope.ID.String()
		v.ScopeType, v.ScopeID = &t, &i
	}
	return v
}
