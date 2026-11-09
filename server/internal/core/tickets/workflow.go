package tickets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/opsx"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Service is the ticket module.
type Service struct {
	db     TxRunner
	files  *files.Service
	notify jobs.Notifier
	now    func() time.Time
}

// MaxPhotos is the number of files one ticket or comment may carry (O3 defaults).
const MaxPhotos = 10

// NewService returns a Service and registers the "ticket" visibility resolver
// with the file module (photos are visible to whoever can see the ticket).
// notify may be nil (notifications are dropped).
func NewService(db TxRunner, fs *files.Service, notify jobs.Notifier) *Service {
	if notify == nil {
		notify = jobs.DiscardNotifier{}
	}
	s := &Service{db: db, files: fs, notify: notify, now: time.Now}
	if fs != nil {
		fs.RegisterResolver("ticket", resolver{s}, files.WithMaxFiles(MaxPhotos))
	}
	return s
}

// Semantics of a status.
const (
	OpenLike   = "open_like"
	ClosedLike = "closed_like"
)

// Status is one step of a site's workflow.
type Status struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Semantic string    `json:"semantic"`
	Position int       `json:"position"`
	Key      *string   `json:"key"`
}

// Workflow is the ordered status list of a site (O1).
type Workflow struct {
	SiteID    uuid.UUID `json:"site_id"`
	Statuses  []Status  `json:"statuses"`
	IsDefault bool      `json:"is_default"`
	Version   int       `json:"version"`
}

type defaultStatus struct {
	key, en, tr, semantic string
}

var defaultStatuses = []defaultStatus{
	{"open", "Open", "Açık", OpenLike},
	{"in_progress", "In progress", "Devam ediyor", OpenLike},
	{"done", "Done", "Tamamlandı", ClosedLike},
}

type defaultCategory struct {
	en, tr, visibility, role string
}

var starterCategories = []defaultCategory{
	{"Plumbing", "Tesisat", "private", "site_manager"},
	{"Electrical", "Elektrik", "private", "site_manager"},
	{"Elevator", "Asansör", "common", "site_manager"},
	{"Cleaning", "Temizlik", "common", "staff"},
	{"Security", "Güvenlik", "common", "site_manager"},
	{"Noise / neighbors", "Gürültü / komşuluk", "private", "site_manager"},
	{"Other", "Diğer", "private", "site_manager"},
}

// ensureWorkflow creates the site's default workflow and starter categories the
// first time its tickets are touched (a site inherits the default until it
// edits it, O1). Safe under concurrency: the primary key lets one caller seed.
func ensureWorkflow(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `INSERT INTO ticket_workflow (org_id, site_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, orgID, siteID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	var locale string
	if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, orgID).Scan(&locale); err != nil {
		return err
	}
	tr := locale == "tr"
	for i, d := range defaultStatuses {
		name := d.en
		if tr {
			name = d.tr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_status (id, org_id, site_id, name, semantic, position, key) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id.New(), orgID, siteID, name, d.semantic, i+1, d.key); err != nil {
			return err
		}
	}
	for i, c := range starterCategories {
		name := c.en
		if tr {
			name = c.tr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_category (id, org_id, site_id, name, default_visibility, default_assignee_role, position) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id.New(), orgID, siteID, name, c.visibility, c.role, i+1); err != nil {
			return err
		}
	}
	return nil
}

func loadWorkflow(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (Workflow, error) {
	w := Workflow{SiteID: siteID, Statuses: []Status{}}
	if err := tx.QueryRow(ctx, `SELECT is_default, version FROM ticket_workflow WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&w.IsDefault, &w.Version); err != nil {
		return w, err
	}
	rows, err := tx.Query(ctx, `SELECT id, name, semantic, position, key FROM ticket_status WHERE org_id = $1 AND site_id = $2 AND NOT archived ORDER BY position, id`, orgID, siteID)
	if err != nil {
		return w, err
	}
	defer rows.Close()
	for rows.Next() {
		var s Status
		if err := rows.Scan(&s.ID, &s.Name, &s.Semantic, &s.Position, &s.Key); err != nil {
			return w, err
		}
		w.Statuses = append(w.Statuses, s)
	}
	return w, rows.Err()
}

// GetWorkflow returns the site's workflow.
func (s *Service) GetWorkflow(ctx context.Context, orgID, siteID uuid.UUID) (Workflow, error) {
	var w Workflow
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := opsx.SiteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if _, err := opsx.RequireAny(ctx, sc, AnyView); err != nil {
			return err
		}
		if err := ensureWorkflow(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		w, err = loadWorkflow(ctx, tx, orgID, siteID)
		return err
	})
	return w, tenancy.DBProblem(err, "workflow")
}

// StatusInput is one entry of the complete ordered list sent to UpdateWorkflow.
type StatusInput struct {
	ID       *uuid.UUID
	Name     string
	Semantic string
}

const maxStatuses = 30

// UpdateWorkflow replaces the site's status list (the complete ordered list).
// A removed status that tickets use needs an entry in replacements naming the
// kept status those tickets move to; entries for any other status are
// refused (422), so a client cannot silently mis-address a move. Removed
// statuses are archived, not deleted: history keeps referring to them.
func (s *Service) UpdateWorkflow(ctx context.Context, orgID, siteID uuid.UUID, expected int, in []StatusInput, replacements map[uuid.UUID]uuid.UUID) (Workflow, error) {
	return s.applyWorkflow(ctx, orgID, siteID, expected, false, in, replacements)
}

// RestoreDefaultWorkflow puts the built-in open, in progress, done flow back.
// Custom statuses are removed, so tickets in them need replacements as in
// UpdateWorkflow.
func (s *Service) RestoreDefaultWorkflow(ctx context.Context, orgID, siteID uuid.UUID, expected int, replacements map[uuid.UUID]uuid.UUID) (Workflow, error) {
	return s.applyWorkflow(ctx, orgID, siteID, expected, true, nil, replacements)
}

func (s *Service) applyWorkflow(ctx context.Context, orgID, siteID uuid.UUID, expected int, restore bool, in []StatusInput, repl map[uuid.UUID]uuid.UUID) (Workflow, error) {
	var out Workflow
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := opsx.SiteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermManage, sc); err != nil {
			return err
		}
		if err := ensureWorkflow(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		var version int
		if err := tx.QueryRow(ctx, `SELECT version FROM ticket_workflow WHERE org_id = $1 AND site_id = $2 FOR UPDATE`, orgID, siteID).Scan(&version); err != nil {
			return err
		}
		if version != expected {
			return tenancy.ErrVersion
		}
		before, err := loadWorkflow(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		if restore {
			var locale string
			if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, orgID).Scan(&locale); err != nil {
				return err
			}
			in = nil
			for _, d := range defaultStatuses {
				name := d.en
				if locale == "tr" {
					name = d.tr
				}
				var sid uuid.UUID
				err := tx.QueryRow(ctx, `SELECT id FROM ticket_status WHERE org_id = $1 AND site_id = $2 AND key = $3`, orgID, siteID, d.key).Scan(&sid)
				if errors.Is(err, pgx.ErrNoRows) {
					sid = id.New()
					_, err = tx.Exec(ctx, `INSERT INTO ticket_status (id, org_id, site_id, name, semantic, position, key, archived) VALUES ($1, $2, $3, $4, $5, 99, $6, true)`,
						sid, orgID, siteID, name, d.semantic, d.key)
				}
				if err != nil {
					return err
				}
				in = append(in, StatusInput{ID: &sid, Name: name, Semantic: d.semantic})
			}
		}
		if err := validateStatuses(in); err != nil {
			return err
		}
		known := map[uuid.UUID]Status{}
		for _, st := range before.Statuses {
			known[st.ID] = st
		}
		keep := map[uuid.UUID]bool{}
		for i, st := range in {
			if st.ID == nil {
				continue
			}
			if _, ok := known[*st.ID]; !ok && !restore {
				return problem.Validation(problem.Field{Field: fmt.Sprintf("statuses[%d].id", i), Code: "invalid_value"})
			}
			keep[*st.ID] = true
		}
		// removed statuses in use need a replacement; others must not get one
		var bad []problem.Field
		inUse := map[uuid.UUID]bool{}
		for sid := range known {
			if keep[sid] {
				continue
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM ticket WHERE org_id = $1 AND status_id = $2`, orgID, sid).Scan(&n); err != nil {
				return err
			}
			inUse[sid] = n > 0
		}
		for sid, used := range inUse {
			to, has := repl[sid]
			switch {
			case used && !has:
				bad = append(bad, problem.Field{Field: "replacements." + sid.String(), Code: "required"})
			case used && !keep[to]:
				bad = append(bad, problem.Field{Field: "replacements." + sid.String(), Code: "invalid_value"})
			}
		}
		for sid := range repl {
			if used, removed := inUse[sid]; !removed || !used {
				bad = append(bad, problem.Field{Field: "replacements." + sid.String(), Code: "unexpected"})
			}
		}
		if len(bad) > 0 {
			slices.SortFunc(bad, func(a, b problem.Field) int { return strings.Compare(a.Field, b.Field) })
			return problem.Validation(bad...)
		}
		// move tickets, archive the removed
		for sid, used := range inUse {
			if used {
				if err := moveTickets(ctx, tx, orgID, siteID, sid, repl[sid], s.now()); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE ticket_status SET archived = true WHERE org_id = $1 AND id = $2`, orgID, sid); err != nil {
				return err
			}
		}
		// positions are shifted out of the way first: none is unique, so plain updates are fine
		for i, st := range in {
			if st.ID == nil {
				if _, err := tx.Exec(ctx, `INSERT INTO ticket_status (id, org_id, site_id, name, semantic, position) VALUES ($1, $2, $3, $4, $5, $6)`,
					id.New(), orgID, siteID, strings.TrimSpace(st.Name), st.Semantic, i+1); err != nil {
					return err
				}
				continue
			}
			old, wasActive := known[*st.ID]
			if _, err := tx.Exec(ctx, `UPDATE ticket_status SET name = $3, semantic = $4, position = $5, archived = false WHERE org_id = $1 AND id = $2`,
				orgID, *st.ID, strings.TrimSpace(st.Name), st.Semantic, i+1); err != nil {
				return err
			}
			if wasActive && old.Semantic != st.Semantic {
				if _, err := tx.Exec(ctx, `UPDATE ticket SET closed_at = CASE WHEN $3 = 'closed_like' THEN coalesce(closed_at, now()) ELSE NULL END
					WHERE org_id = $1 AND status_id = $2`, orgID, *st.ID, st.Semantic); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket_workflow SET version = version + 1, is_default = $3, updated_at = now() WHERE org_id = $1 AND site_id = $2`, orgID, siteID, restore); err != nil {
			return err
		}
		out, err = loadWorkflow(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		action := "tickets.workflow_update"
		if restore {
			action = "tickets.workflow_restore"
		}
		diff, _ := audit.Diff("ticket_workflow", workflowView(before), workflowView(out))
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: action, TargetType: "ticket_workflow", TargetID: siteID, Diff: diff})
	})
	return out, tenancy.DBProblem(err, "workflow")
}

func workflowView(w Workflow) map[string]any {
	names := make([]string, len(w.Statuses))
	for i, s := range w.Statuses {
		names[i] = s.Name + ":" + s.Semantic
	}
	return map[string]any{"statuses": names, "is_default": w.IsDefault}
}

func validateStatuses(in []StatusInput) error {
	var bad []problem.Field
	if len(in) == 0 || len(in) > maxStatuses {
		bad = append(bad, problem.Field{Field: "statuses", Code: "invalid_length"})
	}
	var open, closed bool
	names := map[string]bool{}
	ids := map[uuid.UUID]bool{}
	for i, st := range in {
		f := fmt.Sprintf("statuses[%d]", i)
		name := strings.TrimSpace(st.Name)
		if l := len([]rune(name)); l < 1 || l > 60 {
			bad = append(bad, problem.Field{Field: f + ".name", Code: "invalid_length"})
		}
		if names[strings.ToLower(name)] {
			bad = append(bad, problem.Field{Field: f + ".name", Code: "duplicate"})
		}
		names[strings.ToLower(name)] = true
		switch st.Semantic {
		case OpenLike:
			open = true
		case ClosedLike:
			closed = true
		default:
			bad = append(bad, problem.Field{Field: f + ".semantic", Code: "invalid_value"})
		}
		if st.ID != nil {
			if ids[*st.ID] {
				bad = append(bad, problem.Field{Field: f + ".id", Code: "duplicate"})
			}
			ids[*st.ID] = true
		}
	}
	if len(in) > 0 && (!open || !closed) {
		bad = append(bad, problem.Field{Field: "statuses", Code: "needs_open_and_closed"})
	}
	if len(bad) > 0 {
		return problem.Validation(bad...)
	}
	return nil
}

// moveTickets re-points the tickets of a removed status and records it in
// their history.
func moveTickets(ctx context.Context, tx pgx.Tx, orgID, siteID, from, to uuid.UUID, now time.Time) error {
	var closed bool
	if err := tx.QueryRow(ctx, `SELECT semantic = 'closed_like' FROM ticket_status WHERE org_id = $1 AND id = $2`, orgID, to).Scan(&closed); err != nil {
		return err
	}
	name := opsx.ActorName(ctx, tx, orgID)
	uid := actorUser(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO ticket_status_history (id, org_id, site_id, ticket_id, from_status_id, to_status_id, changed_by, changed_by_name, comment, at)
		SELECT gen_random_uuid(), org_id, site_id, id, status_id, $4, $5, NULLIF($6, ''), 'workflow edited', $7 FROM ticket WHERE org_id = $1 AND site_id = $2 AND status_id = $3`,
		orgID, siteID, from, to, uid, name, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE ticket SET status_id = $4, updated_at = now(), version = version + 1,
		closed_at = CASE WHEN $5 THEN coalesce(closed_at, now()) ELSE NULL END WHERE org_id = $1 AND site_id = $2 AND status_id = $3`, orgID, siteID, from, to, closed)
	return err
}

func actorUser(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

// SiteSeeder is a tenancy.SiteHook that gives a new site its default workflow and
// starter categories when it is created, so residents find them before anybody
// managing the site has opened the tickets screens. Sites that exist already get
// them the first time any ticket operation touches them.
type SiteSeeder struct{}

// SiteCreated implements tenancy.SiteHook.
func (SiteSeeder) SiteCreated(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) error {
	return ensureWorkflow(ctx, tx, orgID, siteID)
}
