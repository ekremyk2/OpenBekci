package people

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// DirectoryEntry is one person in the site directory (S8).
type DirectoryEntry struct {
	PersonID    uuid.UUID `json:"person_id"`
	DisplayName string    `json:"display_name"`
	Phone       *string   `json:"phone"`
	UnitID      uuid.UUID `json:"unit_id"`
	DoorNumber  string    `json:"door_number"`
	Kind        string    `json:"kind"`
	// OwnUnit marks the people of the caller's own unit, always visible to them.
	OwnUnit bool `json:"own_unit"`
}

// Consent is what a person lets the directory show.
type Consent struct {
	SiteID     uuid.UUID `json:"site_id"`
	ShareName  bool      `json:"share_name"`
	SharePhone bool      `json:"share_phone"`
}

// ListDirectory returns the site's directory. Managers, the board and the
// auditor (person.read for the site) see everyone with their phone; a
// resident sees the people of their own unit and, when the site enabled the
// directory, the people who consented (name always, phone if shared). It
// needs directory.read.
func (s *Service) ListDirectory(ctx context.Context, orgID, siteID uuid.UUID, q string) ([]DirectoryEntry, error) {
	if err := authz.RequireAnywhere(ctx, PermDirectoryRead); err != nil {
		return nil, err
	}
	out := []DirectoryEntry{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermDirectoryRead, sc); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false)
		if err != nil {
			return err
		}
		full := authz.Has(ctx, PermPersonRead, sc)
		if !full && !site.TypedSettings().DirectoryEnabled {
			return nil
		}
		var own []string // "unit|side": own-unit entries cover the caller's own side only
		for _, o := range authz.GrantsFromContext(ctx).Current() {
			if o.SiteID == siteID {
				own = append(own, o.UnitID.String()+"|"+string(o.Side))
			}
		}
		qq := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(strings.TrimSpace(q))) + "%"
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.display_name, p.contact->>'phone', u.id, u.door_number, o.kind,
			       COALESCE(c.share_name, false), COALESCE(c.share_phone, false), COALESCE((u.id::text || '|' || o.side) = ANY($3::text[]), false)
			FROM occupancy o
			JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
			JOIN unit u ON u.org_id = o.org_id AND u.id = o.unit_id
			JOIN site s ON s.org_id = o.org_id AND s.id = o.site_id
			LEFT JOIN directory_consent c ON c.org_id = o.org_id AND c.site_id = o.site_id AND c.person_id = o.person_id
			WHERE o.org_id = $1 AND o.site_id = $2
			  AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE s.timezone)::date)
			  AND ($4 = '%%' OR lower(p.display_name) LIKE $4 OR lower(u.door_number) LIKE $4)
			ORDER BY u.door_number, p.display_name`, orgID, siteID, own, qq)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e DirectoryEntry
			var shareName, sharePhone bool
			if err := rows.Scan(&e.PersonID, &e.DisplayName, &e.Phone, &e.UnitID, &e.DoorNumber, &e.Kind, &shareName, &sharePhone, &e.OwnUnit); err != nil {
				return err
			}
			switch {
			case full || e.OwnUnit:
			case shareName:
				if !sharePhone {
					e.Phone = nil
				}
			default:
				continue
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, tenancy.DBProblem(err, "directory")
	}
	return out, nil
}

// SetDirectoryConsent records what the caller lets the site directory show
// about them. It needs directory.consent and a person of the caller in the organization.
func (s *Service) SetDirectoryConsent(ctx context.Context, orgID, siteID uuid.UUID, d Doc) (Consent, error) {
	if err := authz.RequireAnywhere(ctx, PermDirectoryConsent); err != nil {
		return Consent{}, err
	}
	if f := d.Unknown("share_name", "share_phone"); len(f) > 0 {
		return Consent{}, problem.Validation(f...)
	}
	var shareName, sharePhone bool
	if err := d.Get("share_name", &shareName); err != nil {
		return Consent{}, err
	}
	if err := d.Get("share_phone", &sharePhone); err != nil {
		return Consent{}, err
	}
	if sharePhone && !shareName {
		return Consent{}, problem.Validation(problem.Field{Field: "share_phone", Code: "invalid_value", Params: map[string]any{"reason": "needs_share_name"}})
	}
	var out Consent
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermDirectoryConsent, sc); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		pr := authz.PrincipalFromContext(ctx)
		var personID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, pr.UserID).Scan(&personID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return problem.NotFound("you have no person in this organization")
			}
			return err
		}
		var oldName, oldPhone bool
		_ = tx.QueryRow(ctx, `SELECT share_name, share_phone FROM directory_consent WHERE org_id = $1 AND site_id = $2 AND person_id = $3`, orgID, siteID, personID).Scan(&oldName, &oldPhone)
		if _, err := tx.Exec(ctx, `INSERT INTO directory_consent (id, org_id, site_id, person_id, share_name, share_phone) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (org_id, site_id, person_id) DO UPDATE SET share_name = $5, share_phone = $6, updated_at = now(), version = directory_consent.version + 1`,
			id.New(), orgID, siteID, personID, shareName, sharePhone); err != nil {
			return err
		}
		out = Consent{SiteID: siteID, ShareName: shareName, SharePhone: sharePhone}
		diff, err := audit.Diff("directory_consent", map[string]any{"share_name": oldName, "share_phone": oldPhone}, map[string]any{"share_name": shareName, "share_phone": sharePhone})
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			return nil
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "directory.set_consent", TargetType: "person", TargetID: personID, Diff: diff})
	})
	if err != nil {
		return Consent{}, tenancy.DBProblem(err, "directory consent")
	}
	return out, nil
}

// Vendor is a supplier.
type Vendor struct {
	ID                       uuid.UUID      `json:"id"`
	SiteID                   *uuid.UUID     `json:"site_id"`
	Name                     string         `json:"name"`
	TaxID                    *string        `json:"tax_id"`
	Contact                  map[string]any `json:"contact"`
	DefaultExpenseCategoryID *uuid.UUID     `json:"default_expense_category_id"`
	Version                  int            `json:"version"`
	CreatedAt                time.Time      `json:"created_at"`
	UpdatedAt                time.Time      `json:"updated_at"`
}

const vendorColumns = `id, site_id, name, tax_id, contact, default_expense_category_id, version, created_at, updated_at`

func scanVendor(row pgx.Row) (Vendor, error) {
	var v Vendor
	var contact []byte
	err := row.Scan(&v.ID, &v.SiteID, &v.Name, &v.TaxID, &contact, &v.DefaultExpenseCategoryID, &v.Version, &v.CreatedAt, &v.UpdatedAt)
	v.Contact = map[string]any{}
	_ = json.Unmarshal(contact, &v.Contact)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v, err
}

// vendorScope is the scope a vendor needs: its site's, or the organization's.
func vendorScope(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, siteID *uuid.UUID) (authz.Scope, error) {
	if siteID == nil {
		return authz.Scope{OrgID: orgID}, nil
	}
	return authz.ScopeOfSite(ctx, tx, *siteID)
}

// ListVendors lists vendors of the organization and its sites the caller can
// read. It needs vendor.read.
func (s *Service) ListVendors(ctx context.Context, orgID uuid.UUID, p tenancy.PageParams) ([]Vendor, string, error) {
	if err := authz.RequireAnywhere(ctx, PermVendorRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	orgWide, sites := siteGrants(ctx, PermVendorRead)
	var out []Vendor
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+vendorColumns+` FROM vendor WHERE org_id = $1 AND id > $2
			AND ($3 OR site_id IS NULL OR site_id = ANY($4)) ORDER BY id LIMIT $5`, orgID, after, orgWide, sites, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanVendor(rows)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list vendors")
	}
	items, next := tenancy.Page(out, limit, func(v Vendor) uuid.UUID { return v.ID })
	return items, next, nil
}

// GetVendor returns a vendor. It needs vendor.read.
func (s *Service) GetVendor(ctx context.Context, orgID, vendorID uuid.UUID) (Vendor, error) {
	if err := authz.RequireAnywhere(ctx, PermVendorRead); err != nil {
		return Vendor{}, err
	}
	var out Vendor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := scanVendor(tx.QueryRow(ctx, `SELECT `+vendorColumns+` FROM vendor WHERE org_id = $1 AND id = $2`, orgID, vendorID))
		if err != nil {
			return err
		}
		sc, err := vendorScope(ctx, tx, orgID, v.SiteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermVendorRead, sc); err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		return Vendor{}, tenancy.DBProblem(err, "get vendor")
	}
	return out, nil
}

func applyVendor(base Vendor, d Doc, create bool) (Vendor, error) {
	fields := d.Unknown("name", "tax_id", "contact", "default_expense_category_id", "site_id")
	v := base
	if d.Has("name") {
		var n string
		if err := d.Get("name", &n); err != nil {
			return v, err
		}
		if l := len([]rune(strings.TrimSpace(n))); l < 1 || l > 200 {
			fields = append(fields, problem.Field{Field: "name", Code: "invalid_value"})
		}
		v.Name = strings.TrimSpace(n)
	} else if create {
		fields = append(fields, problem.Field{Field: "name", Code: "required"})
	}
	var err error
	if d.Has("tax_id") {
		if v.TaxID, err = d.String("tax_id"); err != nil {
			return v, err
		}
	}
	if d.Has("contact") {
		var m map[string]any
		if err := d.Get("contact", &m); err != nil {
			return v, err
		}
		if m == nil {
			m = map[string]any{}
		}
		v.Contact = m
	}
	if d.Has("default_expense_category_id") {
		v.DefaultExpenseCategoryID = nil
		if !d.IsNull("default_expense_category_id") {
			var u uuid.UUID
			if err := d.Get("default_expense_category_id", &u); err != nil {
				return v, err
			}
			v.DefaultExpenseCategoryID = &u
		}
	}
	if len(fields) > 0 {
		return v, problem.Validation(fields...)
	}
	return v, nil
}

type vendorAudit struct {
	Name                     string         `json:"name"`
	TaxID                    *string        `json:"tax_id"`
	Contact                  map[string]any `json:"contact"`
	DefaultExpenseCategoryID *uuid.UUID     `json:"default_expense_category_id"`
}

func (v Vendor) audit() vendorAudit {
	return vendorAudit{v.Name, v.TaxID, v.Contact, v.DefaultExpenseCategoryID}
}

// CreateVendor adds a vendor, for a site (site_id) or the whole organization.
// It needs vendor.manage for that scope.
func (s *Service) CreateVendor(ctx context.Context, orgID uuid.UUID, d Doc) (Vendor, error) {
	if err := authz.RequireAnywhere(ctx, PermVendorManage); err != nil {
		return Vendor{}, err
	}
	v, err := applyVendor(Vendor{ID: id.New(), Contact: map[string]any{}}, d, true)
	if err != nil {
		return Vendor{}, err
	}
	if d.Has("site_id") && !d.IsNull("site_id") {
		var sid uuid.UUID
		if err := d.Get("site_id", &sid); err != nil {
			return Vendor{}, err
		}
		v.SiteID = &sid
	}
	var out Vendor
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := vendorScope(ctx, tx, orgID, v.SiteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermVendorManage, sc); err != nil {
			return err
		}
		contact, _ := json.Marshal(v.Contact)
		if out, err = scanVendor(tx.QueryRow(ctx, `INSERT INTO vendor (id, org_id, site_id, name, tax_id, contact, default_expense_category_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+vendorColumns, v.ID, orgID, v.SiteID, v.Name, v.TaxID, contact, v.DefaultExpenseCategoryID)); err != nil {
			return err
		}
		diff, err := audit.Diff("vendor", nil, out.audit())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: v.SiteID, Action: "vendor.create", TargetType: "vendor", TargetID: v.ID, Diff: diff})
	})
	if err != nil {
		return Vendor{}, tenancy.DBProblem(err, "vendor")
	}
	return out, nil
}

// UpdateVendor applies d if the vendor is still at expectedVersion. It needs vendor.manage.
func (s *Service) UpdateVendor(ctx context.Context, orgID, vendorID uuid.UUID, expectedVersion int, d Doc) (Vendor, error) {
	if err := authz.RequireAnywhere(ctx, PermVendorManage); err != nil {
		return Vendor{}, err
	}
	if d.Has("site_id") {
		return Vendor{}, problem.Validation(problem.Field{Field: "site_id", Code: "unknown_field"})
	}
	var out Vendor
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanVendor(tx.QueryRow(ctx, `SELECT `+vendorColumns+` FROM vendor WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, vendorID))
		if err != nil {
			return err
		}
		sc, err := vendorScope(ctx, tx, orgID, before.SiteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermVendorManage, sc); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		next, err := applyVendor(before, d, false)
		if err != nil {
			return err
		}
		diff, err := audit.Diff("vendor", before.audit(), next.audit())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		contact, _ := json.Marshal(next.Contact)
		if out, err = scanVendor(tx.QueryRow(ctx, `UPDATE vendor SET name = $3, tax_id = $4, contact = $5, default_expense_category_id = $6, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2 RETURNING `+vendorColumns, orgID, vendorID, next.Name, next.TaxID, contact, next.DefaultExpenseCategoryID)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: before.SiteID, Action: "vendor.update", TargetType: "vendor", TargetID: vendorID, Diff: diff})
	})
	if errors.Is(err, tenancy.ErrVersion) {
		return Vendor{}, tenancy.VersionConflict("vendor")
	}
	if err != nil {
		return Vendor{}, tenancy.DBProblem(err, "update vendor")
	}
	return out, nil
}

// DeleteVendor removes a vendor. It needs vendor.manage.
func (s *Service) DeleteVendor(ctx context.Context, orgID, vendorID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermVendorManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := scanVendor(tx.QueryRow(ctx, `SELECT `+vendorColumns+` FROM vendor WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, vendorID))
		if err != nil {
			return err
		}
		sc, err := vendorScope(ctx, tx, orgID, v.SiteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermVendorManage, sc); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM vendor WHERE org_id = $1 AND id = $2`, orgID, vendorID); err != nil {
			return err
		}
		diff, err := audit.Diff("vendor", v.audit(), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: v.SiteID, Action: "vendor.delete", TargetType: "vendor", TargetID: vendorID, Diff: diff})
	})
	return tenancy.DBProblem(err, "delete vendor")
}
