package governance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// AgendaItem is an agenda item as the API shows it.
type AgendaItem struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	MeetingID   uuid.UUID  `json:"meeting_id"`
	Position    int        `json:"position"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	WillBeVoted bool       `json:"will_be_voted"`
	Topic       *string    `json:"topic"`
	DecisionID  *uuid.UUID `json:"decision_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Version     int        `json:"version"`

	siteID uuid.UUID
}

const agendaColumns = `id, org_id, meeting_id, position, title, description, will_be_voted, topic, decision_id, created_at, updated_at, version, site_id`

func scanAgenda(row pgx.Row) (AgendaItem, error) {
	var a AgendaItem
	err := row.Scan(&a.ID, &a.OrgID, &a.MeetingID, &a.Position, &a.Title, &a.Description, &a.WillBeVoted, &a.Topic, &a.DecisionID, &a.CreatedAt, &a.UpdatedAt, &a.Version, &a.siteID)
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a, err
}

func loadAgendaItem(ctx context.Context, tx pgx.Tx, orgID, id uuid.UUID, forUpdate bool) (AgendaItem, error) {
	q := `SELECT ` + agendaColumns + ` FROM agenda_item WHERE org_id = $1 AND id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	a, err := scanAgenda(tx.QueryRow(ctx, q, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, problem.NotFound("no such agenda item")
	}
	return a, err
}

func (a AgendaItem) auditView() map[string]any {
	return map[string]any{"position": a.Position, "title": a.Title, "description": a.Description, "will_be_voted": a.WillBeVoted, "topic": a.Topic}
}

// itemAt loads an agenda item with its meeting and checks perm at the site.
func (s *Service) itemAt(ctx context.Context, tx pgx.Tx, perm authz.Permission, orgID, id uuid.UUID, forUpdate bool) (AgendaItem, Meeting, authz.Scope, error) {
	a, err := loadAgendaItem(ctx, tx, orgID, id, forUpdate)
	if err != nil {
		return a, Meeting{}, authz.Scope{}, err
	}
	m, err := loadMeeting(ctx, tx, orgID, a.MeetingID, false)
	if err != nil {
		return a, m, authz.Scope{}, err
	}
	sc, err := requireSite(ctx, tx, perm, a.siteID)
	return a, m, sc, err
}

// ListAgendaItems lists the agenda of a meeting in order. It needs meetings.read.
func (s *Service) ListAgendaItems(ctx context.Context, orgID, meetingID uuid.UUID, p tenancy.PageParams) ([]AgendaItem, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return nil, "", err
	}
	limit, _, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []AgendaItem
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsRead, orgID, meetingID, false)
		if err != nil {
			return err
		}
		if m.Status == StatusDraft && !viewerOf(ctx, sc).staff {
			return problem.NotFound("no such meeting")
		}
		rows, err := tx.Query(ctx, `SELECT `+agendaColumns+` FROM agenda_item WHERE org_id = $1 AND meeting_id = $2 ORDER BY position`, orgID, meetingID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAgenda(rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "list agenda items")
	}
	if len(out) > limit {
		out = out[:limit] // an agenda is short; the cursor is not needed
	}
	if out == nil {
		out = []AgendaItem{}
	}
	return out, "", nil
}

// AgendaItemCreate is the input of CreateAgendaItem.
type AgendaItemCreate struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	WillBeVoted bool    `json:"will_be_voted"`
	Topic       *string `json:"topic"`
}

func (s *Service) checkTopic(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, topic *string) error {
	if topic == nil {
		return nil
	}
	set, err := loadSettings(ctx, tx, orgID, siteID, false)
	if err != nil {
		return err
	}
	if !set.Settings.HasTopic(*topic) {
		return problem.Validation(problem.Field{Field: "topic", Code: "invalid_value", Params: map[string]any{"reason": "unknown_topic"}})
	}
	return nil
}

// CreateAgendaItem appends an item to a draft meeting's agenda. It needs meetings.manage.
func (s *Service) CreateAgendaItem(ctx context.Context, orgID, meetingID uuid.UUID, in AgendaItemCreate) (AgendaItem, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return AgendaItem{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return AgendaItem{}, fieldProblem("title", "required")
	}
	var out AgendaItem
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		if m.Status != StatusDraft {
			return problem.Conflict("the agenda of a called meeting is fixed; cancel it and create a new one")
		}
		if err := s.checkTopic(ctx, tx, orgID, m.SiteID, in.Topic); err != nil {
			return err
		}
		aid := newID()
		if _, err = tx.Exec(ctx, `INSERT INTO agenda_item (id, org_id, site_id, meeting_id, position, title, description, will_be_voted, topic)
			VALUES ($1, $2, $3, $4, (SELECT COALESCE(max(position), 0) + 1 FROM agenda_item WHERE org_id = $2 AND meeting_id = $4), $5, $6, $7, $8)`,
			aid, orgID, m.SiteID, meetingID, in.Title, in.Description, in.WillBeVoted, in.Topic); err != nil {
			return err
		}
		if out, err = loadAgendaItem(ctx, tx, orgID, aid, false); err != nil {
			return err
		}
		diff, err := audit.Diff("agenda_item", nil, out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "agenda_item.create", TargetType: "agenda_item", TargetID: aid, Diff: diff})
	})
	return out, dbErr(err, "agenda item")
}

// UpdateAgendaItem changes an item of a draft meeting. It needs meetings.manage.
func (s *Service) UpdateAgendaItem(ctx context.Context, orgID, id uuid.UUID, expectedVersion int, d tenancy.Doc) (AgendaItem, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return AgendaItem{}, err
	}
	fs := d.Unknown("title", "description", "will_be_voted", "topic")
	var out AgendaItem
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, sc, err := s.itemAt(ctx, tx, PermMeetingsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if a.Version != expectedVersion {
			return errVersion
		}
		if m.Status != StatusDraft {
			return problem.Conflict("the agenda of a called meeting is fixed")
		}
		next := a
		if err := d.Get("title", &next.Title); err != nil {
			return err
		}
		if strings.TrimSpace(next.Title) == "" {
			fs = append(fs, problem.Field{Field: "title", Code: "required"})
		}
		if d.Has("description") {
			if next.Description, err = d.String("description"); err != nil {
				return err
			}
		}
		if err := d.Get("will_be_voted", &next.WillBeVoted); err != nil {
			return err
		}
		if d.Has("topic") {
			if next.Topic, err = d.String("topic"); err != nil {
				return err
			}
		}
		if len(fs) > 0 {
			return problem.Validation(fs...)
		}
		if err := s.checkTopic(ctx, tx, orgID, a.siteID, next.Topic); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE agenda_item SET title = $3, description = $4, will_be_voted = $5, topic = $6, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, id, next.Title, next.Description, next.WillBeVoted, next.Topic); err != nil {
			return err
		}
		if out, err = loadAgendaItem(ctx, tx, orgID, id, false); err != nil {
			return err
		}
		diff, err := audit.Diff("agenda_item", a.auditView(), out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "agenda_item.update", TargetType: "agenda_item", TargetID: id, Diff: diff})
	})
	return out, dbErr(err, "agenda item")
}

// DeleteAgendaItem removes an item of a draft meeting and closes the gap in the numbering. It needs meetings.manage.
func (s *Service) DeleteAgendaItem(ctx context.Context, orgID, id uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, sc, err := s.itemAt(ctx, tx, PermMeetingsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if a.Version != expectedVersion {
			return errVersion
		}
		if m.Status != StatusDraft {
			return problem.Conflict("the agenda of a called meeting is fixed")
		}
		if _, err = tx.Exec(ctx, `DELETE FROM agenda_item WHERE org_id = $1 AND id = $2`, orgID, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE agenda_item SET position = position - 1 WHERE org_id = $1 AND meeting_id = $2 AND position > $3`, orgID, a.MeetingID, a.Position); err != nil {
			return err
		}
		diff, err := audit.Diff("agenda_item", a.auditView(), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "agenda_item.delete", TargetType: "agenda_item", TargetID: id, Diff: diff})
	})
	return dbErr(err, "agenda item")
}

// ReorderAgendaItems sets the order of a draft meeting's agenda; ids must list every item once. It needs meetings.manage.
func (s *Service) ReorderAgendaItems(ctx context.Context, orgID, meetingID uuid.UUID, ids []uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		if m.Status != StatusDraft {
			return problem.Conflict("the agenda of a called meeting is fixed")
		}
		var existing []uuid.UUID
		rows, err := tx.Query(ctx, `SELECT id FROM agenda_item WHERE org_id = $1 AND meeting_id = $2 ORDER BY id`, orgID, meetingID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		given := slices.Clone(ids)
		slices.SortFunc(given, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		if !slices.Equal(existing, given) {
			return problem.Validation(problem.Field{Field: "item_ids", Code: "invalid_value", Params: map[string]any{"reason": "must_list_every_item_once"}})
		}
		for i, id := range ids {
			if _, err := tx.Exec(ctx, `UPDATE agenda_item SET position = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, id, i+1); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("agenda_item", nil, map[string]any{"order": ids})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "agenda_item.reorder", TargetType: "meeting", TargetID: meetingID, Diff: diff})
	})
	return dbErr(err, "agenda")
}
