// Package governance is the 0.2 governance module (backend plan 4.14, decisions
// G1 to G8): the owners' assembly run end to end. It keeps the site's rules
// (quorum, thresholds, vote weight, proxy caps, notice timing) as settings with
// Law 634 defaults, meetings with an eligibility snapshot taken at the call,
// the per-owner delivery record of the call (meeting_notice), non-binding
// polls, attendance with QR check-in and proxies, in-room vote counts,
// numbered decisions that can create the change they describe, minutes, and
// reminders before elected terms end.
//
// The module does not know the money or documents modules: decision effects
// that need money go through DecisionEffects, and generated documents are filed
// through DocumentFiler. Both have defaults that work on their own.
package governance

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// PlatformTx lists the organizations for the jobs, satisfied by *db.DB.
type PlatformTx interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// DecisionRef identifies the decision an effect belongs to; the created
// record carries its id (decision_id).
type DecisionRef struct {
	OrgID      uuid.UUID
	SiteID     uuid.UUID
	DecisionID uuid.UUID
	Text       string
}

// EffectRecord is the record an effect created.
type EffectRecord struct {
	RecordType string
	RecordID   uuid.UUID
}

// DecisionEffects creates the money records a decision can describe (G5).
// Implementations run inside the apply transaction (tx is the tenant
// transaction) so the decision and its effect commit together, and must
// create records that carry the decision id. Preview returns what Apply would
// create, without writing. The money module implements it; DecisionEffectsUnavailable
// is the default until then. The elected_role effect is implemented by this
// module directly.
type DecisionEffects interface {
	ApplyDuesRate(ctx context.Context, tx pgx.Tx, d DecisionRef, in map[string]any) (EffectRecord, error)
	ApplySpecialAssessment(ctx context.Context, tx pgx.Tx, d DecisionRef, in map[string]any) (EffectRecord, error)
	FinalizeBudget(ctx context.Context, tx pgx.Tx, d DecisionRef, budgetID uuid.UUID) (EffectRecord, error)
	Preview(ctx context.Context, tx pgx.Tx, d DecisionRef, kind string, in map[string]any) (map[string]any, error)
}

// Document is a generated document ready to be filed.
type Document struct {
	Kind      string // call_letter, attendance_sheet, minutes, and the pack-only delivery_checklist, proxy_form, decision_book_draft
	FileName  string
	MIME      string
	Content   []byte
	SiteID    uuid.UUID
	MeetingID uuid.UUID
}

// FiledDocument is what filing returns: the document and its file.
type FiledDocument struct {
	DocumentID uuid.UUID
	FileID     uuid.UUID
}

// DocumentFiler files a generated document into the site's documents (O8).
// Implementations run inside the transaction. The default (LocalFiler) keeps
// the bytes in meeting_document and returns that row's id for both ids; the
// operations module replaces it with one that files into the right folder
// with the right visibility.
type DocumentFiler interface {
	File(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, doc Document) (FiledDocument, error)
}

// Notifier enqueues a notification job in the caller's transaction. The
// wiring implements it with the job client; nil means notifications are not
// sent (the legal record, meeting_notice, is kept either way).
//
// Notify takes the shared notification job (jobs.NotifyArgs, kind
// notifications.deliver) so the operations module's dispatcher serves it; Key
// makes a redelivery idempotent.
type Notifier interface {
	Notify(ctx context.Context, tx pgx.Tx, a jobs.NotifyArgs) error
	// EnqueueNotifyAbsentees schedules the governance.notify_absentees job of a
	// meeting: the delivery of its decisions to the owners who were absent.
	EnqueueNotifyAbsentees(ctx context.Context, tx pgx.Tx, orgID, meetingID uuid.UUID, channels []string) error
}

// recipients names people as notification recipients.
func recipients(people []uuid.UUID) []jobs.Recipient {
	out := make([]jobs.Recipient, 0, len(people))
	for _, p := range people {
		out = append(out, jobs.Recipient{PersonID: ptr(p)})
	}
	return out
}

// Config configures the Service.
type Config struct {
	// BaseURL is the public URL (QR check-in links).
	BaseURL      string
	InstanceName string
	Log          *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Effects and Filer default to DecisionEffectsUnavailable and LocalFiler.
	Effects DecisionEffects
	Filer   DocumentFiler
}

// Service is the governance module.
type Service struct {
	db          TxRunner
	platform    PlatformTx
	cfg         Config
	effects     DecisionEffects
	filer       DocumentFiler
	files       FileChecker
	notifier    Notifier
	docResolver doctpl.Resolver
	log         *slog.Logger
	now         func() time.Time
}

// NewService returns a Service. platform may be nil (the jobs then do nothing).
func NewService(db TxRunner, platform PlatformTx, cfg Config) *Service {
	s := &Service{db: db, platform: platform, cfg: cfg, effects: cfg.Effects, filer: cfg.Filer, log: cfg.Log, now: cfg.Now}
	if s.effects == nil {
		s.effects = DecisionEffectsUnavailable{}
	}
	if s.filer == nil {
		s.filer = LocalFiler{}
	}
	if s.files == nil {
		s.files = noFiles{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.cfg.InstanceName == "" {
		s.cfg.InstanceName = "OpenBekci"
	}
	return s
}

// SetNotifier sets the notification enqueuer (the job client exists only after the service).
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// SetEffects replaces the decision effects implementation (the money module registers here).
func (s *Service) SetEffects(e DecisionEffects) {
	if e != nil {
		s.effects = e
	}
}

// SetFiler replaces the document filer (the operations module registers here).
func (s *Service) SetFiler(f DocumentFiler) {
	if f != nil {
		s.filer = f
	}
}

// DecisionEffectsUnavailable is the default DecisionEffects: the money module
// is not installed, so effects that need it answer 409 with a clear reason.
type DecisionEffectsUnavailable struct{}

func (DecisionEffectsUnavailable) unavailable() error {
	return problem.Conflict("this effect needs the money module, which is not installed")
}

// ApplyDuesRate implements DecisionEffects.
func (d DecisionEffectsUnavailable) ApplyDuesRate(context.Context, pgx.Tx, DecisionRef, map[string]any) (EffectRecord, error) {
	return EffectRecord{}, d.unavailable()
}

// ApplySpecialAssessment implements DecisionEffects.
func (d DecisionEffectsUnavailable) ApplySpecialAssessment(context.Context, pgx.Tx, DecisionRef, map[string]any) (EffectRecord, error) {
	return EffectRecord{}, d.unavailable()
}

// FinalizeBudget implements DecisionEffects.
func (d DecisionEffectsUnavailable) FinalizeBudget(context.Context, pgx.Tx, DecisionRef, uuid.UUID) (EffectRecord, error) {
	return EffectRecord{}, d.unavailable()
}

// Preview implements DecisionEffects.
func (d DecisionEffectsUnavailable) Preview(context.Context, pgx.Tx, DecisionRef, string, map[string]any) (map[string]any, error) {
	return nil, d.unavailable()
}

// LocalFiler is the default DocumentFiler: the document stays in meeting_document.
type LocalFiler struct{}

// File implements DocumentFiler.
func (LocalFiler) File(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, doc Document) (FiledDocument, error) {
	id := newID()
	// one document per meeting and kind: regenerating the minutes replaces the
	// previous bytes (the old row is pruned) instead of piling them up
	if _, err := tx.Exec(ctx, `DELETE FROM meeting_document WHERE org_id = $1 AND meeting_id = $2 AND kind = $3`, orgID, doc.MeetingID, doc.Kind); err != nil {
		return FiledDocument{}, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO meeting_document (id, org_id, site_id, meeting_id, kind, file_name, mime, content, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, orgID, doc.SiteID, doc.MeetingID, doc.Kind, doc.FileName, doc.MIME, doc.Content, principalID(ctx))
	return FiledDocument{DocumentID: id, FileID: id}, err
}

func newID() uuid.UUID { return id.New() }

func principalID(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // values here are ids, numbers and strings
	}
	return b
}

func fieldProblem(field, code string) *problem.Error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

func ptr[T any](v T) *T { return &v }

// errVersion is returned inside a transaction when If-Match does not match.
var errVersion = tenancy.ErrVersion

// dbErr maps a write error to a problem.
func dbErr(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tenancy.ErrVersion) {
		return tenancy.VersionConflict(what)
	}
	return tenancy.DBProblem(err, what)
}

// viewer is how the caller reaches a site's governance records.
type viewer struct {
	staff bool // managers, board, auditor: everything
	owner bool // holds an owner occupancy at the site
}

// viewerOf classifies the caller at a site: role holders (anybody with
// site.read) see everything; the others come through an occupancy and see
// what concerns owners or, for tenants, residents.
func viewerOf(ctx context.Context, sc authz.Scope) viewer {
	v := viewer{staff: authz.GrantsFromContext(ctx).HasViaRole(tenancy.PermSiteRead, sc)}
	if sc.SiteID != nil {
		for _, o := range authz.GrantsFromContext(ctx).Current() {
			if o.SiteID == *sc.SiteID && o.Kind == authz.OccOwner {
				v.owner = true
			}
		}
	}
	return v
}

// requireSite checks perm at the site's own scope (loaded from its row).
func requireSite(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) (authz.Scope, error) {
	sc, err := authz.ScopeOfSite(ctx, tx, siteID)
	if err != nil {
		return sc, err
	}
	return sc, authz.Require(ctx, perm, sc)
}

// today is the current date in UTC at midnight.
func (s *Service) today() time.Time {
	n := s.now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// callerPerson is the person linked to the signed-in user in the organization.
func callerPerson(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (uuid.UUID, bool) {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return uuid.Nil, false
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, p.UserID).Scan(&id); err != nil {
		return uuid.Nil, false
	}
	return id, true
}
