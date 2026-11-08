package tenancy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ArchiveFormat identifies a site archive; ArchiveVersion is bumped when the
// content changes incompatibly.
const (
	ArchiveFormat  = "openbekci.site-archive"
	ArchiveVersion = 1
)

// exportTable is a tenant table carried by a site archive and the query that
// selects the site's rows ($1 organization, $2 site).
type exportTable struct {
	Name  string
	Query string
}

const personsOfSite = `SELECT person_id FROM occupancy WHERE org_id = $1 AND site_id = $2
	UNION SELECT person_id FROM unit_claim WHERE org_id = $1 AND site_id = $2
	UNION SELECT person_id FROM directory_consent WHERE org_id = $1 AND site_id = $2
	UNION SELECT person_id FROM membership WHERE org_id = $1 AND ((scope_type = 'site' AND scope_id = $2)
	      OR (scope_type = 'block' AND scope_id IN (SELECT id FROM block WHERE org_id = $1 AND site_id = $2)))`

// exportTables are the tables of an archive in insertion order (parents first).
// Every site-scoped tenant table belongs here or in excludedTables; the
// manifest test fails when a new tenant table is in neither.
var exportTables = []exportTable{
	{"site", `SELECT to_jsonb(t) FROM site t WHERE t.org_id = $1 AND t.id = $2`},
	{"block", `SELECT to_jsonb(t) FROM block t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"unit", `SELECT to_jsonb(t) FROM unit t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"allocation_key", `SELECT to_jsonb(t) FROM allocation_key t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"allocation_key_value", `SELECT to_jsonb(t) FROM allocation_key_value t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"unit_tag", `SELECT to_jsonb(t) FROM unit_tag t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"vehicle", `SELECT to_jsonb(t) FROM vehicle t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"facility", `SELECT to_jsonb(t) FROM facility t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"person", `SELECT to_jsonb(t) FROM person t WHERE t.org_id = $1 AND (t.site_id = $2 OR t.id IN (` + personsOfSite + `)) ORDER BY t.id`},
	{"occupancy", `SELECT to_jsonb(t) FROM occupancy t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"move_out", `SELECT to_jsonb(t) FROM move_out t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"directory_consent", `SELECT to_jsonb(t) FROM directory_consent t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"vendor", `SELECT to_jsonb(t) FROM vendor t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"unit_claim", `SELECT to_jsonb(t) FROM unit_claim t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"membership", `SELECT to_jsonb(t) FROM membership t WHERE t.org_id = $1 AND ((t.scope_type = 'site' AND t.scope_id = $2)
		OR (t.scope_type = 'block' AND t.scope_id IN (SELECT id FROM block WHERE org_id = $1 AND site_id = $2))) ORDER BY t.id`},
}

// excludedTables are tenant tables a site archive does not carry, with the reason.
var excludedTables = map[string]string{
	"organization":      "the tenant root; the target organization already exists",
	"audit_event":       "the audit trail stays with the organization that made the changes",
	"site_export":       "the record of the hand-over itself",
	"import_batch":      "operational record of spreadsheet imports",
	"import_row":        "operational record of spreadsheet imports",
	"file":              "blobs stay with the organization's storage; a hand-over carries no files yet",
	"org_storage_usage": "the organization's storage counter",
	"invitation":        "pending invitations and the join code are not carried over; invite again from the new organization",
}

// ExportManifest returns every tenant table the archive format knows (carried
// or excluded), for the CI test that fails when a new tenant table is unlisted.
func ExportManifest() map[string]string {
	out := map[string]string{}
	for k, v := range excludedTables {
		out[k] = "excluded: " + v
	}
	for _, t := range exportTables {
		out[t.Name] = "exported"
	}
	return out
}

// Archive is the JSON document of a site export. Identifiers are preserved.
type Archive struct {
	Format      string                      `json:"format"`
	Version     int                         `json:"version"`
	ExportedAt  time.Time                   `json:"exported_at"`
	SourceOrgID uuid.UUID                   `json:"source_org_id"`
	SiteID      uuid.UUID                   `json:"site_id"`
	SiteName    string                      `json:"site_name"`
	Tables      map[string][]map[string]any `json:"tables"`
}

// SiteExport is the record of an export or import.
type SiteExport struct {
	ID          uuid.UUID      `json:"id"`
	Direction   string         `json:"direction"`
	SiteID      *uuid.UUID     `json:"site_id"`
	SiteName    *string        `json:"site_name"`
	SourceOrgID *uuid.UUID     `json:"source_org_id"`
	TargetOrgID *uuid.UUID     `json:"target_org_id"`
	Status      string         `json:"status"`
	Summary     map[string]any `json:"summary"`
	HasArchive  bool           `json:"has_archive"`
	At          time.Time      `json:"at"`
}

const exportColumns = `id, direction, site_id, site_name, source_org_id, target_org_id, status, summary, archive IS NOT NULL, at`

func scanExport(row pgx.Row) (SiteExport, error) {
	var e SiteExport
	var summary []byte
	err := row.Scan(&e.ID, &e.Direction, &e.SiteID, &e.SiteName, &e.SourceOrgID, &e.TargetOrgID, &e.Status, &summary, &e.HasArchive, &e.At)
	e.Summary = map[string]any{}
	_ = json.Unmarshal(summary, &e.Summary)
	e.At = e.At.UTC()
	return e, err
}

func buildArchive(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, now time.Time) (Archive, map[string]any, error) {
	site, err := LoadSite(ctx, tx, orgID, siteID, false, false)
	if err != nil {
		return Archive{}, nil, err
	}
	a := Archive{Format: ArchiveFormat, Version: ArchiveVersion, ExportedAt: now.UTC(), SourceOrgID: orgID, SiteID: siteID, SiteName: site.Name, Tables: map[string][]map[string]any{}}
	counts := map[string]any{}
	for _, t := range exportTables {
		rows, err := tx.Query(ctx, t.Query, orgID, siteID)
		if err != nil {
			return Archive{}, nil, fmt.Errorf("export %s: %w", t.Name, err)
		}
		list := []map[string]any{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return Archive{}, nil, err
			}
			m := map[string]any{}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&m); err != nil {
				rows.Close()
				return Archive{}, nil, err
			}
			list = append(list, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Archive{}, nil, err
		}
		a.Tables[t.Name] = list
		counts[t.Name] = len(list)
	}
	return a, counts, nil
}

// ExportSite builds the archive of a whole site (people, occupancies, units,
// allocation keys, vehicles, memberships of the site, vendors and the like,
// identifiers preserved; see the export manifest for what is left out) and
// records the export. It needs site.export, organization-wide. The archive
// holds national ids still sealed with this instance's encryption key.
func (s *Service) ExportSite(ctx context.Context, orgID, siteID uuid.UUID) (SiteExport, error) {
	if err := authz.Require(ctx, PermSiteExport, authz.Scope{OrgID: orgID}); err != nil {
		return SiteExport{}, err
	}
	return s.exportSite(ctx, orgID, siteID)
}

// SystemExportSite is ExportSite without an authorization check, for the admin
// command (the operator holds the database credentials).
func (s *Service) SystemExportSite(ctx context.Context, orgID, siteID uuid.UUID) (SiteExport, []byte, error) {
	rec, err := s.exportSite(ctx, orgID, siteID)
	if err != nil {
		return SiteExport{}, nil, err
	}
	data, _, err := s.ExportArchive(ctx, orgID, rec.ID, true)
	return rec, data, err
}

func (s *Service) exportSite(ctx context.Context, orgID, siteID uuid.UUID) (SiteExport, error) {
	var out SiteExport
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, counts, err := buildArchive(ctx, tx, orgID, siteID, time.Now())
		if err != nil {
			return err
		}
		data, err := json.Marshal(a)
		if err != nil {
			return err
		}
		var uid *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			uid = &u
		}
		eid := id.New()
		summary, _ := json.Marshal(counts)
		if _, err := tx.Exec(ctx, `INSERT INTO site_export (id, org_id, direction, site_id, site_name, source_org_id, status, summary, archive, created_by)
			VALUES ($1, $2, 'export', $3, $4, $2, 'done', $5, $6, $7)`, eid, orgID, siteID, a.SiteName, summary, data, uid); err != nil {
			return err
		}
		if out, err = scanExport(tx.QueryRow(ctx, `SELECT `+exportColumns+` FROM site_export WHERE org_id = $1 AND id = $2`, orgID, eid)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "site.export", TargetType: "site_export", TargetID: eid})
	})
	if err != nil {
		return SiteExport{}, DBProblem(err, "export site")
	}
	return out, nil
}

// ListSiteExports lists export and import records, newest last by id. It needs
// site.export or site.import.
func (s *Service) ListSiteExports(ctx context.Context, orgID uuid.UUID, p PageParams) ([]SiteExport, string, error) {
	if !authz.Has(ctx, PermSiteExport, authz.Scope{OrgID: orgID}) {
		if err := authz.Require(ctx, PermSiteImport, authz.Scope{OrgID: orgID}); err != nil {
			return nil, "", err
		}
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []SiteExport
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+exportColumns+` FROM site_export WHERE org_id = $1 AND id > $2 ORDER BY id LIMIT $3`, orgID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExport(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", DBProblem(err, "list site exports")
	}
	items, next := Page(out, limit, func(e SiteExport) uuid.UUID { return e.ID })
	return items, next, nil
}

// ExportArchive returns the stored archive of an export and a file name. It
// needs site.export (system skips the check).
func (s *Service) ExportArchive(ctx context.Context, orgID, exportID uuid.UUID, system bool) ([]byte, string, error) {
	if !system {
		if err := authz.Require(ctx, PermSiteExport, authz.Scope{OrgID: orgID}); err != nil {
			return nil, "", err
		}
	}
	var data []byte
	var name string
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var site *string
		if err := tx.QueryRow(ctx, `SELECT archive, site_name FROM site_export WHERE org_id = $1 AND id = $2 AND direction = 'export'`, orgID, exportID).Scan(&data, &site); err != nil {
			return err
		}
		if data == nil {
			return problem.NotFound("this export has no archive")
		}
		name = "site-export-" + exportID.String() + ".json"
		return nil
	})
	if err != nil {
		return nil, "", DBProblem(err, "download export")
	}
	return data, name, nil
}

// ImportSite creates a site in the organization from an archive, preserving
// identifiers. People already present (same account) are linked, not
// duplicated. It needs site.import, organization-wide.
func (s *Service) ImportSite(ctx context.Context, orgID uuid.UUID, data []byte) (SiteExport, error) {
	if err := authz.Require(ctx, PermSiteImport, authz.Scope{OrgID: orgID}); err != nil {
		return SiteExport{}, err
	}
	return s.importSite(ctx, orgID, data)
}

// SystemImportSite is ImportSite without an authorization check, for the admin command.
func (s *Service) SystemImportSite(ctx context.Context, orgID uuid.UUID, data []byte) (SiteExport, error) {
	return s.importSite(ctx, orgID, data)
}

func (s *Service) importSite(ctx context.Context, orgID uuid.UUID, data []byte) (SiteExport, error) {
	var a Archive
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&a); err != nil || a.Format != ArchiveFormat {
		return SiteExport{}, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
	}
	if a.Version != ArchiveVersion {
		return SiteExport{}, problem.Validation(problem.Field{Field: "file", Code: "invalid_value", Params: map[string]any{"version": a.Version, "supported": ArchiveVersion}})
	}
	if len(a.Tables["site"]) != 1 {
		return SiteExport{}, problem.Validation(problem.Field{Field: "file", Code: "invalid_value", Params: map[string]any{"reason": "needs_exactly_one_site"}})
	}
	var out SiteExport
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		summary := map[string]any{}
		remap := map[string]string{} // archived person id -> id in this organization
		var lockDate any             // the site's lock date, set again after the journal is in
		// persons first: link by account
		for _, t := range exportTables {
			rows := a.Tables[t.Name]
			var insert []map[string]any
			linked := 0
			for _, r := range rows {
				r["org_id"] = orgID.String()
				switch t.Name {
				case "person":
					if sid, ok := r["site_id"].(string); ok && sid != a.SiteID.String() {
						r["site_id"] = nil // registered at another site that is not part of this archive
					}
					if uid, ok := r["user_id"].(string); ok && uid != "" {
						var existing uuid.UUID
						err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, uid).Scan(&existing)
						if err == nil {
							remap[fmt.Sprint(r["id"])] = existing.String()
							linked++
							continue
						}
						if !errors.Is(err, pgx.ErrNoRows) {
							return err
						}
					}
				case "unit_claim":
					r["invitation_id"] = nil
				case "site":
					lockDate = r["lock_date"]
					r["lock_date"] = nil
				}
				for _, c := range personColumns {
					if pid, ok := r[c].(string); ok {
						if to, ok := remap[pid]; ok {
							r[c] = to
						}
					}
				}
				if isMoneyTable(t.Name) {
					for _, c := range fileColumns {
						if _, ok := r[c]; ok {
							r[c] = nil
						}
					}
				}
				if pid, ok := r["person_id"].(string); ok {
					if to, ok := remap[pid]; ok {
						r["person_id"] = to
					}
				}
				insert = append(insert, r)
			}
			if len(insert) > 0 {
				blob, err := json.Marshal(insert)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s SELECT * FROM jsonb_populate_recordset(NULL::%s, $1::jsonb)`, t.Name, t.Name), blob); err != nil {
					return fmt.Errorf("import %s: %w", t.Name, err)
				}
			}
			summary[t.Name] = len(insert)
			if t.Name == "person" {
				summary["person_linked"] = linked
			}
		}
		if lockDate != nil {
			if _, err := tx.Exec(ctx, `UPDATE site SET lock_date = $3::date WHERE org_id = $1 AND id = $2`, orgID, a.SiteID, fmt.Sprint(lockDate)); err != nil {
				return err
			}
		}
		eid := id.New()
		sum, _ := json.Marshal(summary)
		var uid *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			uid = &u
		}
		if _, err := tx.Exec(ctx, `INSERT INTO site_export (id, org_id, direction, site_id, site_name, source_org_id, target_org_id, status, summary, created_by)
			VALUES ($1, $2, 'import', $3, $4, $5, $2, 'done', $6, $7)`, eid, orgID, a.SiteID, a.SiteName, a.SourceOrgID, sum, uid); err != nil {
			return err
		}
		var err error
		if out, err = scanExport(tx.QueryRow(ctx, `SELECT `+exportColumns+` FROM site_export WHERE org_id = $1 AND id = $2`, orgID, eid)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &a.SiteID, Action: "site.import", TargetType: "site_export", TargetID: eid})
	})
	if err != nil {
		if IsUnique(err, "") {
			return SiteExport{}, problem.Conflict("this organization already has a site or people with the identifiers of the archive")
		}
		return SiteExport{}, DBProblem(err, "import site")
	}
	return out, nil
}

// OffboardSite closes a site after its hand-over: the organization's access
// to it ends (the site becomes read-only for nobody: every operation on it
// answers 403 site_offboarded, residents lose their derived access, site and
// block memberships are end-dated, open invitations and the join code are
// revoked). The site must have been exported first, and the caller needs a
// recent re-authentication. It needs site.offboard, organization-wide.
func (s *Service) OffboardSite(ctx context.Context, orgID, siteID uuid.UUID) (Site, error) {
	if err := authz.Require(ctx, PermSiteOffboard, authz.Scope{OrgID: orgID}); err != nil {
		return Site{}, err
	}
	if err := authz.PrincipalFromContext(ctx).RequireReauth(time.Now()); err != nil {
		return Site{}, err
	}
	return s.offboard(ctx, orgID, siteID, true)
}

// SystemOffboardSite is OffboardSite for the admin command (no authorization, export not required).
func (s *Service) SystemOffboardSite(ctx context.Context, orgID, siteID uuid.UUID) (Site, error) {
	return s.offboard(ctx, orgID, siteID, false)
}

func (s *Service) offboard(ctx context.Context, orgID, siteID uuid.UUID, needExport bool) (Site, error) {
	var out Site
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := LoadSite(ctx, tx, orgID, siteID, true, false)
		if err != nil {
			return err
		}
		if needExport {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM site_export WHERE org_id = $1 AND site_id = $2 AND direction = 'export' AND status = 'done'`, orgID, siteID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return problem.Conflict("export the site before offboarding it")
			}
		}
		var ended int64
		tag, err := tx.Exec(ctx, `UPDATE membership SET valid_to = GREATEST(now(), valid_from + interval '1 microsecond'), updated_at = now(), version = version + 1
			WHERE org_id = $1 AND (valid_to IS NULL OR valid_to > now())
			  AND ((scope_type = 'site' AND scope_id = $2) OR (scope_type = 'block' AND scope_id IN (SELECT id FROM block WHERE org_id = $1 AND site_id = $2)))`, orgID, siteID)
		if err != nil {
			return err
		}
		ended = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `UPDATE invitation SET status = 'revoked', updated_at = now(), version = version + 1 WHERE org_id = $1 AND site_id = $2 AND status = 'pending'`, orgID, siteID); err != nil {
			return err
		}
		if out, err = scanSite(tx.QueryRow(ctx, `UPDATE site SET status = 'offboarded', updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2 RETURNING `+siteColumns, orgID, siteID)); err != nil {
			return err
		}
		diff, err := audit.Diff("site", map[string]any{"status": before.Status}, map[string]any{"status": "offboarded", "memberships_ended": ended})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "site.offboard", TargetType: "site", TargetID: siteID, Diff: diff})
	})
	if err != nil {
		return Site{}, DBProblem(err, "offboard site")
	}
	return out, nil
}

// ManifestTables lists the manifest's table names, sorted (for tests).
func ManifestTables() []string {
	m := ExportManifest()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
