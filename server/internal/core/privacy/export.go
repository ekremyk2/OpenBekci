package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/pdf"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ExportArgs is the arguments of privacy.data_export.
type ExportArgs struct {
	OrgID     uuid.UUID `json:"org_id"`
	PersonID  uuid.UUID `json:"person_id"`
	SiteID    uuid.UUID `json:"site_id"`
	RequestID uuid.UUID `json:"request_id"`
	// RequestedBy is the user who asked (told when the export is ready).
	RequestedBy uuid.UUID `json:"requested_by"`
}

// Kind implements river.JobArgs.
func (ExportArgs) Kind() string { return "privacy.data_export" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (ExportArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// RequestExport queues the export of a person's data and returns the request id.
// A manager with people.export for the person may ask, and the person
// themselves (me.manage). The bundle is filed as private documents and the
// person is notified when it is ready.
func (s *Service) RequestExport(ctx context.Context, orgID, personID uuid.UUID) (uuid.UUID, error) {
	p := authz.PrincipalFromContext(ctx)
	if err := authz.RequireInOrg(ctx, people.PermMeManage, orgID); err != nil {
		if authz.RequireInOrg(ctx, people.PermPersonExport, orgID) != nil {
			return uuid.Nil, err
		}
	}
	ins := s.insert.Load()
	if ins == nil {
		return uuid.Nil, problem.Internal(errors.New("privacy: no job queue"))
	}
	reqID := id.New()
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var userID, siteID *uuid.UUID
		var erased bool
		err := tx.QueryRow(ctx, `SELECT p.user_id, p.erased_at IS NOT NULL, COALESCE(p.site_id,
				(SELECT o.site_id FROM occupancy o WHERE o.org_id = p.org_id AND o.person_id = p.id ORDER BY o.valid_from DESC LIMIT 1),
				(SELECT s.id FROM site s WHERE s.org_id = p.org_id ORDER BY s.id LIMIT 1))
			FROM person p WHERE p.org_id = $1 AND p.id = $2`, orgID, personID).Scan(&userID, &erased, &siteID)
		if isNoRows(err) {
			return problem.NotFound("no such person")
		}
		if err != nil {
			return err
		}
		mgr, err := people.CanSee(ctx, tx, orgID, personID, people.PermPersonExport)
		if err != nil {
			return err
		}
		if !mgr && (userID == nil || *userID != p.UserID) {
			return problem.Forbidden("only the person or a manager may request the export")
		}
		if erased {
			return problem.Conflict("the person is erased; there is nothing to export")
		}
		if siteID == nil {
			return errNoSite
		}
		if _, err := tx.Exec(ctx, `INSERT INTO data_export (org_id, id, person_id, requested_by) VALUES ($1, $2, $3, $4)`, orgID, reqID, personID, p.UserID); err != nil {
			return err
		}
		if err := (*ins)(ctx, tx, ExportArgs{OrgID: orgID, PersonID: personID, SiteID: *siteID, RequestID: reqID, RequestedBy: p.UserID}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: siteID, Action: "privacy.export_request", TargetType: "person", TargetID: personID})
	})
	if err != nil {
		return uuid.Nil, tenancy.DBProblem(err, "data export")
	}
	return reqID, nil
}

// exportSections are the parts of the bundle: each query returns one jsonb
// value for ($1 organization, $2 person). Sealed or provider-side columns are
// left out; audit rows carry keyed hashes of personal values, never the values.
var exportSections = []struct{ name, sql string }{
	{"person", `SELECT COALESCE(to_jsonb(p) - 'national_id_enc' - 'org_id', 'null') FROM person p WHERE p.org_id = $1 AND p.id = $2`},
	{"occupancies", `SELECT COALESCE(jsonb_agg(to_jsonb(o) - 'org_id' ORDER BY o.valid_from, o.id), '[]') FROM occupancy o WHERE o.org_id = $1 AND o.person_id = $2`},
	{"charges", `SELECT COALESCE(jsonb_agg(to_jsonb(c) - 'org_id' ORDER BY c.due_date, c.id), '[]') FROM charge c WHERE c.org_id = $1 AND (c.debtor_person_id = $2 OR
		(c.debtor_kind IN ('owner', 'tenant') AND EXISTS (SELECT 1 FROM occupancy o WHERE o.org_id = c.org_id AND o.unit_id = c.unit_id AND o.person_id = $2 AND o.kind <> 'household' AND o.side = c.debtor_kind)))`},
	{"payments", `SELECT COALESCE(jsonb_agg(to_jsonb(p) - 'org_id' ORDER BY p.received_on, p.id), '[]') FROM payment p WHERE p.org_id = $1 AND (p.debtor_person_id = $2 OR p.payer_person_id = $2)`},
	{"tickets", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'org_id' ORDER BY t.id), '[]') FROM ticket t WHERE t.org_id = $1 AND t.reporter_person_id = $2`},
	{"ticket_comments", `SELECT COALESCE(jsonb_agg(to_jsonb(c) - 'org_id' ORDER BY c.id), '[]') FROM ticket_comment c WHERE c.org_id = $1 AND c.author_person_id = $2`},
	{"consents", `SELECT COALESCE(jsonb_agg(to_jsonb(c) - 'org_id' - 'user_id' ORDER BY c.purpose), '[]') FROM consent c WHERE c.org_id = $1 AND c.user_id = (SELECT user_id FROM person WHERE org_id = $1 AND id = $2)`},
	{"audit", `SELECT COALESCE(jsonb_agg(to_jsonb(a) - 'org_id' ORDER BY a.at, a.id), '[]') FROM audit_event a WHERE a.org_id = $1 AND a.target_type = 'person' AND a.target_id = $2`},
}

// Bundle is the content of an export: section name to its JSON.
type Bundle map[string]json.RawMessage

// BuildBundle reads a person's data in the organization.
func BuildBundle(ctx context.Context, tx pgx.Tx, orgID, personID uuid.UUID) (Bundle, error) {
	b := Bundle{}
	for _, sec := range exportSections {
		var raw json.RawMessage
		if err := tx.QueryRow(ctx, sec.sql, orgID, personID).Scan(&raw); err != nil {
			return nil, fmt.Errorf("export section %s: %w", sec.name, err)
		}
		b[sec.name] = raw
	}
	return b, nil
}

// PDF renders the bundle as a readable summary: one paragraph per record.
func (b Bundle) PDF(title string) ([]byte, error) {
	doc, err := pdf.NewUnicode(title)
	if err != nil {
		return nil, err
	}
	doc.Paragraph(title, 14, true)
	names := make([]string, 0, len(b))
	for n := range b {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var rows []map[string]any
		var one map[string]any
		if json.Unmarshal(b[n], &rows) != nil {
			if json.Unmarshal(b[n], &one) == nil && one != nil {
				rows = []map[string]any{one}
			}
		}
		doc.Advance(8)
		doc.Paragraph(fmt.Sprintf("%s (%d)", n, len(rows)), 11, true)
		for _, r := range rows {
			doc.Paragraph(line(r), 8, false)
		}
	}
	return doc.Bytes(), nil
}

// line renders a record as "key: value, ..." in key order, skipping nulls.
func line(r map[string]any) string {
	keys := make([]string, 0, len(r))
	for k, v := range r {
		if v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %v", k, r[k])
	}
	return strings.Join(parts, ", ")
}

type exportWorker struct {
	river.WorkerDefaults[ExportArgs]
	s *Service
}

func (w *exportWorker) Work(ctx context.Context, job *river.Job[ExportArgs]) error {
	return w.s.RunExport(ctx, job.Args)
}

// Scope is the file scope of an export; its id is the request id. Only the person
// and the user who asked can read the files (FileResolver), and they expire after ExportTTL.
const Scope = "privacy_export"

// ExportTTL is how long the export files are kept.
const ExportTTL = 7 * 24 * time.Hour

// Uploader stores generated files (*files.Service).
type Uploader interface {
	UploadGenerated(ctx context.Context, p files.UploadParams) (files.File, error)
}

// FileResolver decides who may read the files of an export: the person whose data it
// holds and the user who requested it, nobody else (a reader other than the person is
// audited as privacy.export_read). Administrators with files.manage still read every
// file, as everywhere in the file module.
func FileResolver() files.VisibilityResolver {
	return files.ResolverFuncs{Read: func(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
		p := authz.PrincipalFromContext(ctx)
		if p == nil {
			return false, nil
		}
		var org, person uuid.UUID
		var owner *uuid.UUID
		var asker uuid.UUID
		err := tx.QueryRow(ctx, `SELECT e.org_id, e.person_id, p.user_id, e.requested_by FROM data_export e JOIN person p ON p.org_id = e.org_id AND p.id = e.person_id WHERE e.id = $1`, ref.ID).
			Scan(&org, &person, &owner, &asker)
		if isNoRows(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if owner != nil && *owner == p.UserID {
			return true, nil
		}
		if asker != p.UserID {
			return false, nil
		}
		return true, audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "privacy.export_read", TargetType: "person", TargetID: person})
	}}
}

// RunExport builds the bundle and stores the JSON and the PDF as private files
// (scope privacy_export, readable by the person and the requester only, kept 7 days),
// then notifies the person and the requester.
func (s *Service) RunExport(ctx context.Context, a ExportArgs) error {
	return s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		bundle, err := BuildBundle(ctx, tx, a.OrgID, a.PersonID)
		if err != nil {
			return err
		}
		day := s.now().UTC().Format("2006-01-02")
		js, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return err
		}
		pd, err := bundle.PDF("Personal data export " + day)
		if err != nil {
			return err
		}
		until := s.now().Add(ExportTTL)
		var pdfFile files.File
		for _, f := range []struct {
			ext     string
			content []byte
		}{{"json.txt", js}, {"pdf", pd}} {
			site := a.SiteID
			up, err := s.cfg.Files.UploadGenerated(ctx, files.UploadParams{OrgID: a.OrgID, SiteID: &site, Scope: &files.ScopeRef{Type: Scope, ID: a.RequestID},
				Name: "personal-data-" + day + "." + f.ext, Content: bytes.NewReader(f.content), RetentionUntil: &until})
			if err != nil {
				return err
			}
			if f.ext == "pdf" { // json.txt: JSON is not on the default upload allowlist, plain text is
				pdfFile = up
			}
		}
		// the person, and the manager who asked when it was not the person
		rcpt := []jobs.Recipient{{PersonID: &a.PersonID}}
		var asker *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2 AND id <> $3`, a.OrgID, a.RequestedBy, a.PersonID).Scan(&asker); err == nil {
			rcpt = append(rcpt, jobs.Recipient{PersonID: asker})
		} else if !isNoRows(err) {
			return err
		}
		return s.cfg.Notifier.Notify(ctx, tx, jobs.NotifyArgs{
			OrgID: a.OrgID, SiteID: &a.SiteID, Type: notifications.TypeDataExportReady, Recipients: rcpt,
			Payload: map[string]any{"file_id": pdfFile.ID, "request_id": a.RequestID},
			Key:     "privacy.export:" + a.RequestID.String(),
		})
	})
}
