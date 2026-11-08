package tenancy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
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
	UNION SELECT person_id FROM membership WHERE org_id = $1 AND site_owned AND ((scope_type = 'site' AND scope_id = $2)
	      OR (scope_type = 'block' AND scope_id IN (SELECT id FROM block WHERE org_id = $1 AND site_id = $2)))
		UNION SELECT reporter_person_id FROM ticket WHERE org_id = $1 AND site_id = $2
		UNION SELECT assignee_person_id FROM ticket WHERE org_id = $1 AND site_id = $2 AND assignee_person_id IS NOT NULL
		UNION SELECT author_person_id FROM ticket_comment WHERE org_id = $1 AND site_id = $2
		UNION SELECT v.person_id FROM ticket_vote v JOIN ticket t ON t.org_id = v.org_id AND t.id = v.ticket_id WHERE v.org_id = $1 AND t.site_id = $2
		UNION SELECT r.person_id FROM announcement_recipient r JOIN announcement a ON a.org_id = r.org_id AND a.id = r.announcement_id WHERE r.org_id = $1 AND a.site_id = $2
		UNION SELECT r.person_id FROM announcement_read r JOIN announcement a ON a.org_id = r.org_id AND a.id = r.announcement_id WHERE r.org_id = $1 AND a.site_id = $2
		UNION SELECT addressed_person_id FROM document WHERE org_id = $1 AND site_id = $2 AND addressed_person_id IS NOT NULL
		UNION SELECT debtor_person_id FROM charge WHERE org_id = $1 AND site_id = $2 AND debtor_person_id IS NOT NULL
		UNION SELECT debtor_person_id FROM payment WHERE org_id = $1 AND site_id = $2 AND debtor_person_id IS NOT NULL
		UNION SELECT payer_person_id FROM payment WHERE org_id = $1 AND site_id = $2 AND payer_person_id IS NOT NULL
		UNION SELECT person_id FROM journal_line WHERE org_id = $1 AND site_id = $2 AND person_id IS NOT NULL
		UNION SELECT collector_person_id FROM cash_box WHERE org_id = $1 AND site_id = $2 AND collector_person_id IS NOT NULL
		UNION SELECT person_id FROM refund WHERE org_id = $1 AND site_id = $2 AND person_id IS NOT NULL
		UNION SELECT person_id FROM late_fee_booking WHERE org_id = $1 AND site_id = $2 AND person_id IS NOT NULL
		UNION SELECT person_id FROM dunning_event WHERE org_id = $1 AND site_id = $2 AND person_id IS NOT NULL
		UNION SELECT debtor_person_id FROM collection_case WHERE org_id = $1 AND site_id = $2 AND debtor_person_id IS NOT NULL
	UNION SELECT person_id FROM attendance WHERE org_id = $1 AND site_id = $2
	UNION SELECT proxy_for_person_id FROM attendance WHERE org_id = $1 AND site_id = $2 AND proxy_for_person_id IS NOT NULL
	UNION SELECT person_id FROM meeting_notice WHERE org_id = $1 AND site_id = $2
	UNION SELECT answered_by FROM poll_answer WHERE org_id = $1 AND site_id = $2`

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
	{"facility_block", `SELECT to_jsonb(t) FROM facility_block t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"person", `SELECT to_jsonb(t) FROM person t WHERE t.org_id = $1 AND (t.site_id = $2 OR t.id IN (` + personsOfSite + `)) ORDER BY t.id`},
	{"occupancy", `SELECT to_jsonb(t) FROM occupancy t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"move_out", `SELECT to_jsonb(t) FROM move_out t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"directory_consent", `SELECT to_jsonb(t) FROM directory_consent t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"vendor", `SELECT to_jsonb(t) FROM vendor t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"unit_claim", `SELECT to_jsonb(t) FROM unit_claim t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"membership", `SELECT to_jsonb(t) FROM membership t WHERE t.org_id = $1 AND t.site_owned AND ((t.scope_type = 'site' AND t.scope_id = $2)
		OR (t.scope_type = 'block' AND t.scope_id IN (SELECT id FROM block WHERE org_id = $1 AND site_id = $2))) ORDER BY t.id`},
	// operations (daily operation): files first, the tables that point at them after
	{"file", `SELECT to_jsonb(t) FROM file t WHERE t.org_id = $1 AND t.site_id = $2 AND t.status <> 'deleted' ORDER BY t.id`},
	{"ticket_workflow", `SELECT to_jsonb(t) FROM ticket_workflow t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.site_id`},
	{"ticket_status", `SELECT to_jsonb(t) FROM ticket_status t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"ticket_category", `SELECT to_jsonb(t) FROM ticket_category t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"ticket", `SELECT to_jsonb(t) FROM ticket t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"ticket_comment", `SELECT to_jsonb(t) FROM ticket_comment t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"ticket_attachment", `SELECT to_jsonb(t) FROM ticket_attachment t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"ticket_vote", `SELECT to_jsonb(v) FROM ticket_vote v JOIN ticket t ON t.org_id = v.org_id AND t.id = v.ticket_id WHERE v.org_id = $1 AND t.site_id = $2 ORDER BY v.ticket_id, v.person_id`},
	{"ticket_status_history", `SELECT to_jsonb(t) FROM ticket_status_history t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"announcement", `SELECT to_jsonb(t) FROM announcement t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"announcement_file", `SELECT to_jsonb(f) FROM announcement_file f JOIN announcement a ON a.org_id = f.org_id AND a.id = f.announcement_id WHERE f.org_id = $1 AND a.site_id = $2 ORDER BY f.announcement_id, f.file_id`},
	{"announcement_recipient", `SELECT to_jsonb(r) FROM announcement_recipient r JOIN announcement a ON a.org_id = r.org_id AND a.id = r.announcement_id WHERE r.org_id = $1 AND a.site_id = $2 ORDER BY r.announcement_id, r.person_id`},
	{"announcement_read", `SELECT to_jsonb(r) FROM announcement_read r JOIN announcement a ON a.org_id = r.org_id AND a.id = r.announcement_id WHERE r.org_id = $1 AND a.site_id = $2 ORDER BY r.announcement_id, r.person_id`},
	{"folder", `SELECT to_jsonb(t) FROM folder t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.created_at, t.id`},
	{"document", `SELECT to_jsonb(t) FROM document t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"site_notification_config", `SELECT to_jsonb(t) FROM site_notification_config t WHERE t.org_id = $1 AND t.site_id = $2`},
	{"site_channel_default", `SELECT to_jsonb(t) FROM site_channel_default t WHERE t.org_id = $1 AND t.site_id = $2`},
	// Governance (0.2): the site's legal paperwork travels with it, with the
	// decision numbers (decision_counter) so the karar defteri continues without
	// gaps. The foreign keys between decision, agenda_item and membership are
	// deferred, so the order within the archive does not matter for them.
	{"governance_settings", `SELECT to_jsonb(t) FROM governance_settings t WHERE t.org_id = $1 AND t.site_id = $2`},
	{"meeting", `SELECT to_jsonb(t) FROM meeting t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"agenda_item", `SELECT to_jsonb(t) FROM agenda_item t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"meeting_document", `SELECT to_jsonb(t) FROM meeting_document t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"meeting_notice", `SELECT to_jsonb(t) FROM meeting_notice t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"poll", `SELECT to_jsonb(t) FROM poll t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.agenda_item_id`},
	{"poll_answer", `SELECT to_jsonb(t) FROM poll_answer t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.agenda_item_id, t.unit_id`},
	{"attendance", `SELECT to_jsonb(t) FROM attendance t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.id`},
	{"vote_count", `SELECT to_jsonb(t) FROM vote_count t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.agenda_item_id`},
	{"decision_counter", `SELECT to_jsonb(t) FROM decision_counter t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.year`},
	{"decision", `SELECT to_jsonb(t) FROM decision t WHERE t.org_id = $1 AND t.site_id = $2 ORDER BY t.year, t.sequence_no`},
}

// excludedTables are tenant tables a site archive does not carry, with the reason.
var excludedTables = map[string]string{ //nolint:gosec // table names, not credentials
	"organization": "the tenant root; the target organization already exists",
	"audit_event":  "the audit trail stays with the organization that made the changes",
	"site_export":  "the record of the hand-over itself",
	"import_batch": "operational record of spreadsheet imports",
	"import_row":   "operational record of spreadsheet imports",
	"invitation":   "pending invitations and the join code are not carried over; invite again from the new organization",

	"notification":          "personal inbox entries of accounts of the old organization; ephemeral and not site data",
	"notification_delivery": "delivery bookkeeping (provider ids, attempts) of messages already sent",
	"notification_mute":     "personal preferences of an account, not site data; people set them again",
	"sms_usage":             "the instance's SMS counter of the old organization; the new site starts at zero",
	"org_storage_usage":     "derived byte counter of the organization; recomputed from its files",
	// Governance: the legal paperwork is exported, these two are not.
	"attendance_token":         "check-in credentials of one meeting (stored hashed); useless after the hand-over",
	"term_reminder":            "operational reminders; recomputed from the memberships by the daily job",
	"assembly_reminder":        "operational record of the yearly ordinary-assembly reminder",
	"booking":                  "booking history and its fee charges stay with the organization that charged them",
	"visitor_entry":            "visitor logs are short-lived personal data (KVKK retention) and are not handed over",
	"recurring_visitor":        "standing visitor permissions and their QR tokens are not handed over; the residents register them again",
	"visitor_approval_request": "belongs to a visitor log entry",
	"delivery":                 "parcel logs are short-lived and are not handed over",
	"gate_pin":                 "guard PINs are credentials of the organization's people and are set again after the hand-over",
	"gate_pin_attempt":         "operational record of PIN attempts",
	"guard_session":            "console sessions end with the hand-over",
	// access control: bound to the hardware on the site and re-derived in the new organization
	"agent":                "agents are enrolled against one organization; enrol again after the hand-over",
	"access_point":         "access points name the agent that drives them; configure them again after the hand-over",
	"access_site_state":    "allow-list bookkeeping of the old organization",
	"credential":           "derived from vehicles and visitor entries; the projector rebuilds them",
	"allowlist_change":     "change log of the old organization's allow-list",
	"agent_command":        "commands for agents of the old organization",
	"access_event":         "the opening log stays with the organization that recorded it",
	"access_dirty":         "outbox of the projector",
	"access_event_counter": "sequence counter of the opening log, which stays with the old organization",
	// packs: per organization, not site data (the site's pack-filled settings travel inside the site row)
	"pack_enablement":      "which packs the old organization enabled, with their settings and sealed secrets; the target organization enables its own",
	"pack_enablement_site": "per-site pack settings and sealed secrets of the old organization's packs",
	"site_pack_default":    "bookkeeping of which settings a pack filled; the settings themselves travel in the site row",
	"provider_settlement":  "payout bookkeeping against the old organization's provider clearing accounts",
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
	// FieldKeyID is the key id national ids in this archive are sealed with; the key
	// itself is the exporting instance's ENCRYPTION_KEY and never travels with it.
	FieldKeyID string `json:"field_key_id,omitempty"`
	// Signature is an HMAC-SHA256 (hex) of the archive with this field empty,
	// keyed from the instance SECRET_KEY: it proves the archive was produced
	// by this instance and not altered since.
	Signature string `json:"signature,omitempty"`
}

// ImportOptions tune ImportSiteWithOptions.
type ImportOptions struct {
	// AllowUntrusted accepts an archive that was not signed by this instance
	// (another instance, or edited by hand). Only the allow-listed content is
	// imported either way; the option exists so the operator decides knowingly.
	AllowUntrusted bool
}

// WithArchiveKey sets the instance key (derived from SECRET_KEY) that signs
// exported archives and verifies imported ones. Without it archives are
// unsigned and every import counts as untrusted.
func (s *Service) WithArchiveKey(secret []byte) *Service {
	if len(secret) == 0 {
		s.archiveKey = nil
		return s
	}
	h := sha256.New()
	h.Write([]byte("openbekci/site-archive/v1\x00"))
	h.Write(secret)
	s.archiveKey = h.Sum(nil)
	return s
}

func (s *Service) archiveMAC(a Archive) (string, bool) {
	if len(s.archiveKey) == 0 {
		return "", false
	}
	a.Signature = ""
	b, err := json.Marshal(a)
	if err != nil {
		return "", false
	}
	m := hmac.New(sha256.New, s.archiveKey)
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil)), true
}

// archiveTrusted reports whether the archive carries a valid signature of this instance.
func (s *Service) archiveTrusted(a Archive) bool {
	want, ok := s.archiveMAC(a)
	if !ok || a.Signature == "" {
		return false
	}
	return hmac.Equal([]byte(want), []byte(a.Signature))
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
	a := Archive{Format: ArchiveFormat, FieldKeyID: crypto.CurrentKeyID, Version: ArchiveVersion, ExportedAt: now.UTC(), SourceOrgID: orgID, SiteID: siteID, SiteName: site.Name, Tables: map[string][]map[string]any{}}
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
		if sig, ok := s.archiveMAC(a); ok {
			a.Signature = sig
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
			var last *time.Time
			if err := tx.QueryRow(ctx, `SELECT max(at) FROM site_export WHERE org_id = $1 AND site_id = $2 AND direction = 'export' AND status = 'done'`, orgID, siteID).Scan(&last); err != nil {
				return err
			}
			if last == nil {
				return problem.Conflict("export the site before offboarding it")
			}
			// the export must be newer than the site's last change, or the hand-over would miss it
			changed, err := siteHighWater(ctx, tx, orgID, siteID)
			if err != nil {
				return err
			}
			if changed != nil && changed.After(*last) {
				return problem.Conflict("stale_export: the site changed after the last export; export it again before offboarding")
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
