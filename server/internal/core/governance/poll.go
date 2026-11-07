package governance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Poll is the non-binding pre-meeting poll of an agenda item (G2).
type Poll struct {
	AgendaItemID uuid.UUID `json:"agenda_item_id"`
	Question     *string   `json:"question"`
	OpensAt      time.Time `json:"opens_at"`
	ClosesAt     time.Time `json:"closes_at"`
	Status       string    `json:"status"`
	Version      int       `json:"version"`
}

func pollStatus(now, opens, closes time.Time) string {
	switch {
	case now.Before(opens):
		return "draft"
	case now.Before(closes):
		return "open"
	}
	return "closed"
}

func (s *Service) loadPoll(ctx context.Context, tx pgx.Tx, orgID, itemID uuid.UUID, forUpdate bool) (Poll, error) {
	q := `SELECT agenda_item_id, question, opens_at, closes_at, version FROM poll WHERE org_id = $1 AND agenda_item_id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	var p Poll
	err := tx.QueryRow(ctx, q, orgID, itemID).Scan(&p.AgendaItemID, &p.Question, &p.OpensAt, &p.ClosesAt, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, problem.NotFound("this agenda item has no poll")
	}
	p.OpensAt, p.ClosesAt = p.OpensAt.UTC(), p.ClosesAt.UTC()
	p.Status = pollStatus(s.now(), p.OpensAt, p.ClosesAt)
	return p, err
}

// GetPoll returns the poll of an agenda item. It needs meetings.read.
func (s *Service) GetPoll(ctx context.Context, orgID, itemID uuid.UUID) (Poll, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return Poll{}, err
	}
	var out Poll
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		_, m, sc, err := s.itemAt(ctx, tx, PermMeetingsRead, orgID, itemID, false)
		if err != nil {
			return err
		}
		if m.Status == StatusDraft && !viewerOf(ctx, sc).staff {
			return problem.NotFound("no such agenda item")
		}
		out, err = s.loadPoll(ctx, tx, orgID, itemID, false)
		return err
	})
	return out, dbErr(err, "poll")
}

// PollSet is the input of SetPoll.
type PollSet struct {
	Question *string   `json:"question"`
	OpensAt  time.Time `json:"opens_at"`
	ClosesAt time.Time `json:"closes_at"`
}

// SetPoll creates or replaces the poll of an agenda item. expectedVersion is
// the poll's version (0 when it does not exist yet). The poll closes before
// the meeting. It needs meetings.manage.
func (s *Service) SetPoll(ctx context.Context, orgID, itemID uuid.UUID, expectedVersion int, in PollSet) (Poll, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return Poll{}, err
	}
	var fs []problem.Field
	if !in.ClosesAt.After(in.OpensAt) {
		fs = append(fs, problem.Field{Field: "closes_at", Code: "invalid_value", Params: map[string]any{"reason": "before_opens_at"}})
	}
	var out Poll
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, sc, err := s.itemAt(ctx, tx, PermMeetingsManage, orgID, itemID, true)
		if err != nil {
			return err
		}
		if m.Status != StatusDraft && m.Status != StatusCalled {
			return problem.Conflict("polls can only be set before the meeting")
		}
		if in.ClosesAt.After(m.ScheduledAt) {
			fs = append(fs, problem.Field{Field: "closes_at", Code: "invalid_value", Params: map[string]any{"reason": "after_meeting"}})
		}
		if len(fs) > 0 {
			return problem.Validation(fs...)
		}
		cur, perr := s.loadPoll(ctx, tx, orgID, itemID, true)
		have := perr == nil
		if perr != nil && !isNotFound(perr) {
			return perr
		}
		ver := 0
		if have {
			ver = cur.Version
		}
		if ver != expectedVersion {
			return errVersion
		}
		if _, err = tx.Exec(ctx, `INSERT INTO poll (org_id, site_id, agenda_item_id, question, opens_at, closes_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (org_id, agenda_item_id) DO UPDATE SET question = $4, opens_at = $5, closes_at = $6, updated_at = now(), version = poll.version + 1`,
			orgID, a.siteID, itemID, in.Question, in.OpensAt, in.ClosesAt); err != nil {
			return err
		}
		if out, err = s.loadPoll(ctx, tx, orgID, itemID, false); err != nil {
			return err
		}
		diff, err := audit.Diff("poll", map[string]any{"opens_at": cur.OpensAt, "closes_at": cur.ClosesAt}, map[string]any{"opens_at": out.OpensAt, "closes_at": out.ClosesAt, "question": in.Question})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "poll.set", TargetType: "agenda_item", TargetID: itemID, Diff: diff})
	})
	return out, dbErr(err, "poll")
}

func isNotFound(err error) bool {
	var pe *problem.Error
	return errors.As(err, &pe) && pe.Status == 404
}

// PollAnswer is one unit's answer.
type PollAnswer struct {
	AgendaItemID uuid.UUID `json:"agenda_item_id"`
	UnitID       uuid.UUID `json:"unit_id"`
	UnitDoorNo   string    `json:"unit_door_number"`
	Answer       string    `json:"answer"`
	AnsweredBy   uuid.UUID `json:"answered_by"`
	AnsweredAt   time.Time `json:"answered_at"`
}

// AnswerPoll records the unit's answer. The poll is non-binding (G2): the
// answer is only a temperature check and never enters a threshold. One answer
// per unit: the unit's representative (the owner who votes for it in the
// meeting's eligibility snapshot) answers, and may change the answer until the
// poll closes. It needs polls.answer.
func (s *Service) AnswerPoll(ctx context.Context, orgID, itemID, unitID uuid.UUID, answer string) (PollAnswer, error) {
	if err := authz.RequireAnywhere(ctx, PermPollsAnswer); err != nil {
		return PollAnswer{}, err
	}
	if answer != "yes" && answer != "no" && answer != "abstain" {
		return PollAnswer{}, fieldProblem("answer", "invalid_value")
	}
	var out PollAnswer
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, sc, err := s.itemAt(ctx, tx, PermPollsAnswer, orgID, itemID, false)
		if err != nil {
			return err
		}
		poll, err := s.loadPoll(ctx, tx, orgID, itemID, false)
		if err != nil {
			return err
		}
		if m.Status != StatusCalled || poll.Status != "open" {
			return problem.Conflict("the poll is not open")
		}
		me, ok := callerPerson(ctx, tx, orgID)
		if !ok {
			return problem.Forbidden("your account is not linked to a person here")
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return err
		}
		owner, ok := snap.Owner(me)
		var door string
		if ok {
			for _, u := range owner.Units {
				if u.UnitID == unitID {
					door = u.DoorNumber
				}
			}
		}
		if door == "" {
			return problem.Forbidden("you do not vote for this unit")
		}
		at := s.now().UTC()
		if _, err = tx.Exec(ctx, `INSERT INTO poll_answer (org_id, site_id, agenda_item_id, unit_id, answer, answered_by, answered_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (org_id, agenda_item_id, unit_id) DO UPDATE SET answer = $5, answered_by = $6, answered_at = $7`,
			orgID, a.siteID, itemID, unitID, answer, me, at); err != nil {
			return err
		}
		out = PollAnswer{AgendaItemID: itemID, UnitID: unitID, UnitDoorNo: door, Answer: answer, AnsweredBy: me, AnsweredAt: at}
		diff, err := audit.Diff("poll", nil, map[string]any{"unit_id": unitID, "answer": answer})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "poll.answer", TargetType: "agenda_item", TargetID: itemID, Diff: diff})
	})
	return out, dbErr(err, "poll answer")
}

// PollResults are the tallies of a poll; Binding is always false.
type PollResults struct {
	AgendaItemID  uuid.UUID `json:"agenda_item_id"`
	Yes           int64     `json:"yes"`
	No            int64     `json:"no"`
	Abstain       int64     `json:"abstain"`
	UnitsAnswered int64     `json:"units_answered"`
	UnitsTotal    int64     `json:"units_total"`
	Binding       bool      `json:"binding"`
}

// GetPollResults tallies a poll. It needs meetings.manage or polls.answer.
func (s *Service) GetPollResults(ctx context.Context, orgID, itemID uuid.UUID) (PollResults, error) {
	if !authz.GrantsFromContext(ctx).HoldsAnywhere(PermMeetingsManage) && !authz.GrantsFromContext(ctx).HoldsAnywhere(PermPollsAnswer) {
		return PollResults{}, problem.Forbidden("missing permission " + string(PermMeetingsManage))
	}
	out := PollResults{AgendaItemID: itemID}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, m, err := s.itemUnscoped(ctx, tx, orgID, itemID)
		if err != nil {
			return err
		}
		sc, err := authz.ScopeOfSite(ctx, tx, a.siteID)
		if err != nil {
			return err
		}
		if authz.Require(ctx, PermMeetingsManage, sc) != nil && authz.Require(ctx, PermPollsAnswer, sc) != nil {
			return problem.Forbidden("missing permission " + string(PermMeetingsManage))
		}
		if _, err := s.loadPoll(ctx, tx, orgID, itemID, false); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE answer = 'yes'), count(*) FILTER (WHERE answer = 'no'), count(*) FILTER (WHERE answer = 'abstain'), count(*)
			FROM poll_answer WHERE org_id = $1 AND agenda_item_id = $2`, orgID, itemID).Scan(&out.Yes, &out.No, &out.Abstain, &out.UnitsAnswered); err != nil {
			return err
		}
		if snap, err := loadSnapshot(m.EligibilitySnapshot); err == nil {
			out.UnitsTotal = snap.UnitCount
		} else {
			// not called yet: count the units that have an owner of record today
			cur, cerr := computeSnapshot(ctx, tx, orgID, a.siteID, s.today(), VoteWeightPerUnit)
			if cerr != nil {
				return cerr
			}
			out.UnitsTotal = cur.UnitCount
		}
		return nil
	})
	return out, dbErr(err, "poll results")
}

// itemUnscoped loads an item and its meeting without a permission check (the caller checks).
func (s *Service) itemUnscoped(ctx context.Context, tx pgx.Tx, orgID, id uuid.UUID) (AgendaItem, Meeting, error) {
	a, err := loadAgendaItem(ctx, tx, orgID, id, false)
	if err != nil {
		return a, Meeting{}, err
	}
	m, err := loadMeeting(ctx, tx, orgID, a.MeetingID, false)
	return a, m, err
}

// ListPollAnswers lists the answers of a poll by unit. It needs meetings.manage.
func (s *Service) ListPollAnswers(ctx context.Context, orgID, itemID uuid.UUID, p tenancy.PageParams) ([]PollAnswer, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []PollAnswer
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, _, _, err := s.itemAt(ctx, tx, PermMeetingsManage, orgID, itemID, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT a.unit_id, u.door_number, a.answer, a.answered_by, a.answered_at FROM poll_answer a
			JOIN unit u ON u.org_id = a.org_id AND u.id = a.unit_id
			WHERE a.org_id = $1 AND a.agenda_item_id = $2 AND a.unit_id > $3 ORDER BY a.unit_id LIMIT $4`, orgID, itemID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a := PollAnswer{AgendaItemID: itemID}
			if err := rows.Scan(&a.UnitID, &a.UnitDoorNo, &a.Answer, &a.AnsweredBy, &a.AnsweredAt); err != nil {
				return err
			}
			a.AnsweredAt = a.AnsweredAt.UTC()
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "poll answers")
	}
	items, next := tenancy.Page(out, limit, func(a PollAnswer) uuid.UUID { return a.UnitID })
	return items, next, nil
}

var _ = strings.TrimSpace
