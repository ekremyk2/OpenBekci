package tenancy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ImportSite creates a site in the organization from an archive, preserving
// identifiers. Only archives signed by this instance are accepted; see
// ImportSiteWithOptions for the rest. It needs site.import, organization-wide.
func (s *Service) ImportSite(ctx context.Context, orgID uuid.UUID, data []byte) (SiteExport, error) {
	return s.ImportSiteWithOptions(ctx, orgID, data, ImportOptions{})
}

// ImportSiteWithOptions is ImportSite with options. The archive is never
// trusted for what it grants: see importPlan.
func (s *Service) ImportSiteWithOptions(ctx context.Context, orgID uuid.UUID, data []byte, opts ImportOptions) (SiteExport, error) {
	if err := authz.Require(ctx, PermSiteImport, authz.Scope{OrgID: orgID}); err != nil {
		return SiteExport{}, err
	}
	return s.importSite(ctx, orgID, data, opts)
}

// SystemImportSite is ImportSite without an authorization check, for the admin command.
func (s *Service) SystemImportSite(ctx context.Context, orgID uuid.UUID, data []byte) (SiteExport, error) {
	return s.importSite(ctx, orgID, data, ImportOptions{})
}

// SystemImportSiteWithOptions is SystemImportSite with options (the operator's --untrusted).
func (s *Service) SystemImportSiteWithOptions(ctx context.Context, orgID uuid.UUID, data []byte, opts ImportOptions) (SiteExport, error) {
	return s.importSite(ctx, orgID, data, opts)
}

// siteScopedTables must carry the archive's site id in every row.
var siteScopedTables = map[string]bool{
	"block": true, "unit": true, "allocation_key": true, "allocation_key_value": true, "unit_tag": true,
	"vehicle": true, "facility": true, "occupancy": true, "move_out": true, "directory_consent": true, "vendor": true,
	// operations
	"file": true, "ticket_workflow": true, "ticket_status": true, "ticket_category": true, "ticket": true,
	"ticket_comment": true, "ticket_attachment": true, "ticket_status_history": true, "announcement": true,
	"folder": true, "document": true, "site_notification_config": true, "site_channel_default": true,
	// governance
	"governance_settings": true, "meeting": true, "agenda_item": true, "meeting_document": true, "meeting_notice": true,
	"poll": true, "poll_answer": true, "attendance": true, "vote_count": true, "decision_counter": true, "decision": true,
}

// childTables have no site column: each row hangs off a site-scoped parent of
// the archive (table, column), which must be one of the archive's own rows.
var childTables = map[string][2]string{
	"ticket_vote":            {"ticket", "ticket_id"},
	"announcement_file":      {"announcement", "announcement_id"},
	"announcement_recipient": {"announcement", "announcement_id"},
	"announcement_read":      {"announcement", "announcement_id"},
}

// personColumns are the columns of tables, besides occupancy, directory consent
// and membership, that point at a person of the archive.
var personColumns = map[string][]string{
	"ticket": {"reporter_person_id", "assignee_person_id"}, "ticket_comment": {"author_person_id"},
	"ticket_vote": {"person_id"}, "announcement_recipient": {"person_id"}, "announcement_read": {"person_id"},
	"document": {"addressed_person_id"},
	"attendance": {"person_id", "proxy_for_person_id"}, "meeting_notice": {"person_id"}, "poll_answer": {"answered_by"},
}

// neverFromArchive are columns that name an account or an action of the source
// instance. They are cleared on import: an archive never links a person to a
// login, and records of who did something do not carry over.
var neverFromArchive = []string{"user_id", "created_by", "decided_by", "invited_by", "accepted_by", "invitation_id", "uploaded_by", "changed_by", "recorded_by", "checked_in_by"}

func invalidArchive(reason string) error {
	return problem.Validation(problem.Field{Field: "file", Code: "invalid_value", Params: map[string]any{"reason": reason}})
}

// importPlan is the archive after validation and filtering: the rows to insert
// per table and what was dropped.
type importPlan struct {
	rows    map[string][]map[string]any
	summary map[string]any
}

// planImport validates the archive and drops everything that must not be
// imported (C1): unknown tables and columns are refused; every site-scoped row
// must belong to the archive's site; only site-owned board/auditor memberships
// inside the site survive (role memberships of the outgoing management and any
// organization-wide membership are dropped); user links are cleared; claims
// (tied to accounts) are not carried; persons nobody refers to are dropped.
func planImport(a Archive, cols map[string]map[string]bool, orgID uuid.UUID, keepSealed bool) (importPlan, error) {
	known := map[string]bool{}
	for _, t := range exportTables {
		known[t.Name] = true
	}
	for name := range a.Tables {
		if !known[name] {
			return importPlan{}, invalidArchive("unknown_table_" + name)
		}
	}
	site := a.SiteID.String()
	plan := importPlan{rows: map[string][]map[string]any{}, summary: map[string]any{}}
	warnings := []string{}

	// shape: every key must be a real column of the table
	for _, t := range exportTables {
		for _, r := range a.Tables[t.Name] {
			for k := range r {
				if !cols[t.Name][k] {
					return importPlan{}, invalidArchive("unknown_column_" + t.Name + "." + k)
				}
			}
			r["org_id"] = orgID.String()
			for _, c := range neverFromArchive {
				if _, ok := r[c]; ok {
					r[c] = nil
				}
			}
		}
	}

	siteRow := a.Tables["site"][0]
	if fmt.Sprint(siteRow["id"]) != site {
		return importPlan{}, invalidArchive("site_id_mismatch")
	}
	plan.rows["site"] = []map[string]any{siteRow}

	blockIDs := map[string]bool{}
	for _, t := range exportTables {
		if !siteScopedTables[t.Name] {
			continue
		}
		for _, r := range a.Tables[t.Name] {
			if fmt.Sprint(r["site_id"]) != site {
				return importPlan{}, invalidArchive("foreign_site_row_" + t.Name)
			}
			if t.Name == "block" {
				blockIDs[fmt.Sprint(r["id"])] = true
			}
			plan.rows[t.Name] = append(plan.rows[t.Name], r)
		}
	}

	for tn, parent := range childTables {
		ids := map[string]bool{}
		for _, r := range plan.rows[parent[0]] {
			ids[fmt.Sprint(r["id"])] = true
		}
		for _, r := range a.Tables[tn] {
			if !ids[fmt.Sprint(r[parent[1]])] {
				return importPlan{}, invalidArchive("foreign_site_row_" + tn)
			}
			plan.rows[tn] = append(plan.rows[tn], r)
		}
	}

	// decisions travel with the site: agenda items and elected memberships keep
	// their link when the decision is in the archive (a link to anything else is
	// an invalid archive for agenda items and is cleared for memberships)
	decisionIDs := map[string]bool{}
	for _, r := range plan.rows["decision"] {
		decisionIDs[fmt.Sprint(r["id"])] = true
	}
	for _, r := range plan.rows["agenda_item"] {
		if r["decision_id"] != nil && !decisionIDs[fmt.Sprint(r["decision_id"])] {
			return importPlan{}, invalidArchive("unknown_decision_in_agenda_item")
		}
	}

	// memberships: site-owned board/auditor inside the site only
	keptMembers := []map[string]any{}
	droppedMembers := 0
	for _, r := range a.Tables["membership"] {
		role := fmt.Sprint(r["role"])
		scope, _ := r["scope_type"].(string)
		sid := fmt.Sprint(r["scope_id"])
		owned, _ := r["site_owned"].(bool)
		inside := (scope == "site" && sid == site) || (scope == "block" && blockIDs[sid])
		if owned && (role == "board_member" || role == "auditor") && inside {
			if r["decision_id"] != nil && !decisionIDs[fmt.Sprint(r["decision_id"])] {
				r["decision_id"] = nil
			}
			keptMembers = append(keptMembers, r)
		} else {
			droppedMembers++
		}
	}
	plan.rows["membership"] = keptMembers
	plan.summary["membership_dropped"] = droppedMembers
	if droppedMembers > 0 {
		warnings = append(warnings, fmt.Sprintf("%d memberships were not imported: only the site's own board and auditor memberships are carried over", droppedMembers))
	}

	// persons: referenced ones only; the archive never links an account
	personIDs := map[string]bool{}
	sealedDropped := 0
	for _, r := range a.Tables["person"] {
		personIDs[fmt.Sprint(r["id"])] = true
	}
	referenced := map[string]bool{}
	for _, tn := range []string{"occupancy", "directory_consent", "membership"} {
		for _, r := range plan.rows[tn] {
			pid := fmt.Sprint(r["person_id"])
			if !personIDs[pid] {
				return importPlan{}, invalidArchive("unknown_person_in_" + tn)
			}
			referenced[pid] = true
		}
	}
	for tn, columns := range personColumns {
		for _, r := range plan.rows[tn] {
			for _, c := range columns {
				if r[c] == nil {
					continue
				}
				pid := fmt.Sprint(r[c])
				if !personIDs[pid] {
					return importPlan{}, invalidArchive("unknown_person_in_" + tn)
				}
				referenced[pid] = true
			}
		}
	}
	for _, r := range a.Tables["person"] {
		pid := fmt.Sprint(r["id"])
		if fmt.Sprint(r["site_id"]) != site {
			r["site_id"] = nil // registered at another site that is not part of this archive
		} else {
			referenced[pid] = true
		}
		if !referenced[pid] {
			continue
		}
		if !keepSealed && r["national_id_enc"] != nil {
			r["national_id_enc"], r["national_id_country"] = nil, nil
			sealedDropped++
		}
		plan.rows["person"] = append(plan.rows["person"], r)
	}
	if sealedDropped > 0 {
		plan.summary["national_id_dropped"] = sealedDropped
		warnings = append(warnings, fmt.Sprintf("%d national ids were not imported: they are sealed with the source instance's key, which this import does not have; enter them again", sealedDropped))
	}
	plan.summary["person_dropped"] = len(a.Tables["person"]) - len(plan.rows["person"])
	plan.summary["person_unlinked"] = len(plan.rows["person"]) // no account link comes from an archive
	if n := len(a.Tables["unit_claim"]); n > 0 {
		plan.summary["unit_claim_dropped"] = n
		warnings = append(warnings, fmt.Sprintf("%d unit claims were not imported: claims belong to accounts; residents claim again", n))
	}
	warnings = append(warnings, "people are imported without a login link; each resident links their account again (confirmation by email or phone)")
	plan.summary["warnings"] = warnings
	return plan, nil
}

func tableColumns(ctx context.Context, tx pgx.Tx) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	names := make([]string, 0, len(exportTables))
	for _, t := range exportTables {
		names = append(names, t.Name)
	}
	rows, err := tx.Query(ctx, `SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ANY($1) AND is_generated = 'NEVER'`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		if out[t] == nil {
			out[t] = map[string]bool{}
		}
		out[t][c] = true
	}
	return out, rows.Err()
}

func (s *Service) importSite(ctx context.Context, orgID uuid.UUID, data []byte, opts ImportOptions) (SiteExport, error) {
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
	if a.SourceOrgID == orgID {
		return SiteExport{}, problem.Conflict("the archive comes from this organization; a site cannot be imported into the organization it left")
	}
	trusted := s.archiveTrusted(a)
	if !trusted && !opts.AllowUntrusted {
		return SiteExport{}, problem.Validation(problem.Field{Field: "file", Code: "untrusted_archive"})
	}
	var out SiteExport
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		cols, err := tableColumns(ctx, tx)
		if err != nil {
			return err
		}
		plan, err := planImport(a, cols, orgID, trusted && a.FieldKeyID == crypto.CurrentKeyID)
		if err != nil {
			return err
		}
		for _, t := range exportTables {
			rows := plan.rows[t.Name]
			if t.Name == "folder" && len(rows) > 0 {
				// inserting the units created their folders (trigger); the archive
				// carries the originals, with their ids and the documents in them
				if _, err := tx.Exec(ctx, `DELETE FROM folder WHERE org_id = $1 AND site_id = $2 AND kind = 'unit'`, orgID, a.SiteID); err != nil {
					return err
				}
			}
			if len(rows) > 0 {
				blob, err := json.Marshal(rows)
				if err != nil {
					return err
				}
				names := make([]string, 0, len(cols[t.Name]))
				for c := range cols[t.Name] {
					names = append(names, pgx.Identifier{c}.Sanitize())
				}
				list := strings.Join(names, ", ")
				if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM jsonb_populate_recordset(NULL::%s, $1::jsonb)`, t.Name, list, list, t.Name), blob); err != nil {
					return fmt.Errorf("import %s: %w", t.Name, err)
				}
			}
			plan.summary[t.Name] = len(rows)
		}
		plan.summary["untrusted"] = !trusted
		eid := id.New()
		sum, _ := json.Marshal(plan.summary)
		var uid *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			uid = &u
		}
		if _, err := tx.Exec(ctx, `INSERT INTO site_export (id, org_id, direction, site_id, site_name, source_org_id, target_org_id, status, summary, created_by)
			VALUES ($1, $2, 'import', $3, $4, $5, $2, 'done', $6, $7)`, eid, orgID, a.SiteID, a.SiteName, a.SourceOrgID, sum, uid); err != nil {
			return err
		}
		if out, err = scanExport(tx.QueryRow(ctx, `SELECT `+exportColumns+` FROM site_export WHERE org_id = $1 AND id = $2`, orgID, eid)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &a.SiteID, Action: "site.import", TargetType: "site_export", TargetID: eid})
	})
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) {
			return SiteExport{}, err
		}
		if IsUnique(err, "") {
			return SiteExport{}, problem.Conflict("this organization already has a site or people with the identifiers of the archive")
		}
		return SiteExport{}, DBProblem(err, "import site")
	}
	return out, nil
}
