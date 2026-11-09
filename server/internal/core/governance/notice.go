package governance

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Notice is the legal record of delivering the call (phase call) or the
// decisions (phase decisions) to one owner (G3). Status is pending until the
// manager records how and when it was delivered.
type Notice struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	MeetingID   uuid.UUID  `json:"meeting_id"`
	PersonID    uuid.UUID  `json:"person_id"`
	PersonName  string     `json:"person_display_name"`
	Phase       string     `json:"phase"`
	Method      *string    `json:"method"`
	Status      string     `json:"status"`
	SentAt      *time.Time `json:"sent_at"`
	TrackingNo  *string    `json:"tracking_no"`
	ProofFileID *uuid.UUID `json:"proof_file_id"`
	Note        *string    `json:"note"`
	// Late says the call reached the owner less than call_min_days before the meeting.
	Late      bool      `json:"late"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const noticeSelect = `SELECT n.id, n.org_id, n.meeting_id, n.person_id, p.display_name, n.phase, n.method, n.status, n.sent_at, n.tracking_no, n.proof_file_id, n.note, n.late, n.created_at, n.updated_at
	FROM meeting_notice n JOIN person p ON p.org_id = n.org_id AND p.id = n.person_id`

func scanNotice(row pgx.Row) (Notice, error) {
	var n Notice
	err := row.Scan(&n.ID, &n.OrgID, &n.MeetingID, &n.PersonID, &n.PersonName, &n.Phase, &n.Method, &n.Status, &n.SentAt, &n.TrackingNo, &n.ProofFileID, &n.Note, &n.Late, &n.CreatedAt, &n.UpdatedAt)
	n.CreatedAt, n.UpdatedAt = n.CreatedAt.UTC(), n.UpdatedAt.UTC()
	if n.SentAt != nil {
		t := n.SentAt.UTC()
		n.SentAt = &t
	}
	return n, err
}

func loadNotice(ctx context.Context, tx pgx.Tx, orgID, meetingID, person uuid.UUID, phase string) (Notice, error) {
	n, err := scanNotice(tx.QueryRow(ctx, noticeSelect+` WHERE n.org_id = $1 AND n.meeting_id = $2 AND n.person_id = $3 AND n.phase = $4`, orgID, meetingID, person, phase))
	if errors.Is(err, pgx.ErrNoRows) {
		return n, problem.NotFound("no such notice")
	}
	return n, err
}

func (n Notice) auditView() map[string]any {
	return map[string]any{"person_id": n.PersonID, "phase": n.Phase, "method": n.Method, "status": n.Status, "sent_at": n.SentAt,
		"tracking_no": n.TrackingNo, "proof_file_id": n.ProofFileID, "note": n.Note, "late": n.Late}
}

// NoticeFilter narrows ListMeetingNotices.
type NoticeFilter struct {
	Phase, Status string
	PersonID      *uuid.UUID
}

// ListMeetingNotices is the per-owner delivery checklist of a meeting. It needs meetings.manage.
func (s *Service) ListMeetingNotices(ctx context.Context, orgID, meetingID uuid.UUID, f NoticeFilter, p tenancy.PageParams) ([]Notice, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Notice
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, _, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, noticeSelect+` WHERE n.org_id = $1 AND n.meeting_id = $2 AND n.id > $3 AND ($4 = '' OR n.phase = $4) AND ($5 = '' OR n.status = $5)
			AND ($6::uuid IS NULL OR n.person_id = $6) ORDER BY n.id LIMIT $7`, orgID, meetingID, after, f.Phase, f.Status, f.PersonID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			n, err := scanNotice(rows)
			if err != nil {
				return err
			}
			out = append(out, n)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "list notices")
	}
	items, next := tenancy.Page(out, limit, func(n Notice) uuid.UUID { return n.ID })
	return items, next, nil
}

// NoticeRecord is the input of RecordMeetingNotice.
type NoticeRecord struct {
	Phase       string     `json:"phase"`
	Method      string     `json:"method"`
	SentAt      time.Time  `json:"sent_at"`
	TrackingNo  *string    `json:"tracking_no"`
	ProofFileID *uuid.UUID `json:"proof_file_id"`
	Note        *string    `json:"note"`
}

var noticeMethods = []string{"registered_mail", "signed", "email", "sms", "other"}

func (r NoticeRecord) validate() []problem.Field {
	var fs []problem.Field
	if r.Phase != "call" && r.Phase != "decisions" {
		fs = append(fs, problem.Field{Field: "phase", Code: "invalid_value"})
	}
	if !slices.Contains(noticeMethods, r.Method) {
		fs = append(fs, problem.Field{Field: "method", Code: "invalid_value"})
	}
	if r.SentAt.IsZero() {
		fs = append(fs, problem.Field{Field: "sent_at", Code: "required"})
	}
	return fs
}

// checkDelivery validates what a manager records about a delivery against the
// meeting: the date cannot be in the future nor before the call, an electronic
// method needs the site's setting, and the proof must be a file of the
// organization.
func (s *Service) checkDelivery(ctx context.Context, tx pgx.Tx, m Meeting, r NoticeRecord) error {
	var fs []problem.Field
	day := func(t time.Time) time.Time {
		t = t.UTC()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	if day(r.SentAt).After(s.today()) {
		fs = append(fs, problem.Field{Field: "sent_at", Code: "invalid_value", Params: map[string]any{"reason": "in_the_future"}})
	}
	floor, what := m.CalledAt0(m.CreatedAt), "call"
	if r.Phase == "decisions" {
		floor, what = m.ScheduledAt, "meeting"
	}
	if day(r.SentAt).Before(day(floor)) {
		fs = append(fs, problem.Field{Field: "sent_at", Code: "invalid_value", Params: map[string]any{"reason": "before_the_" + what}})
	}
	if (r.Method == "email" || r.Method == "sms") && !m.rules().NoticeElectronicAllowed {
		fs = append(fs, problem.Field{Field: "method", Code: "invalid_value", Params: map[string]any{"reason": "electronic_notice_not_allowed"}})
	}
	if r.ProofFileID != nil {
		ok, err := s.files.FileExists(ctx, tx, m.OrgID, *r.ProofFileID)
		if err != nil {
			return err
		}
		if !ok {
			fs = append(fs, problem.Field{Field: "proof_file_id", Code: "invalid_value", Params: map[string]any{"reason": "unknown_file"}})
		}
	}
	if len(fs) > 0 {
		return problem.Validation(fs...)
	}
	return nil
}

// recordNotice upserts the notice of one owner and phase as recorded. With
// check the manager's input is validated (RecordMeetingNotice); the delivery
// job records what it did itself.
func (s *Service) recordNotice(ctx context.Context, tx pgx.Tx, m Meeting, sc authz.Scope, snap Snapshot, person uuid.UUID, r NoticeRecord, check bool) (Notice, error) {
	if _, ok := snap.Owner(person); !ok {
		return Notice{}, problem.Validation(problem.Field{Field: "person_id", Code: "invalid_value", Params: map[string]any{"reason": "not_an_owner_called_to_the_meeting"}})
	}
	if check {
		if err := s.checkDelivery(ctx, tx, m, r); err != nil {
			return Notice{}, err
		}
	}
	late := r.Phase == "call" && r.SentAt.After(m.ScheduledAt.Add(-time.Duration(m.rules().CallMinDays)*24*time.Hour))
	before, berr := loadNotice(ctx, tx, m.OrgID, m.ID, person, r.Phase)
	if berr != nil && !isNotFound(berr) {
		return Notice{}, berr
	}
	if _, err := tx.Exec(ctx, `INSERT INTO meeting_notice (id, org_id, site_id, meeting_id, person_id, phase, method, status, sent_at, tracking_no, proof_file_id, note, recorded_by, late)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'recorded', $8, $9, $10, $11, $12, $13)
		ON CONFLICT (org_id, meeting_id, person_id, phase) DO UPDATE SET method = $7, status = 'recorded', sent_at = $8, tracking_no = $9, proof_file_id = $10, note = $11,
			recorded_by = $12, late = $13, updated_at = now()`,
		newID(), m.OrgID, m.SiteID, m.ID, person, r.Phase, r.Method, r.SentAt, r.TrackingNo, r.ProofFileID, r.Note, principalID(ctx), late); err != nil {
		return Notice{}, err
	}
	n, err := loadNotice(ctx, tx, m.OrgID, m.ID, person, r.Phase)
	if err != nil {
		return n, err
	}
	var bv any
	if berr == nil {
		bv = before.auditView()
	}
	diff, err := audit.Diff("meeting_notice", bv, n.auditView())
	if err != nil {
		return n, err
	}
	return n, audit.Record(ctx, tx, audit.Event{OrgID: m.OrgID, SiteID: sc.SiteID, Action: "meeting_notice.record", TargetType: "meeting_notice", TargetID: n.ID, Diff: diff})
}

// RecordMeetingNotice records how and when the call (or the decisions) reached
// one owner: method, date, tracking number, proof. It needs meetings.manage.
func (s *Service) RecordMeetingNotice(ctx context.Context, orgID, meetingID, person uuid.UUID, r NoticeRecord) (Notice, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return Notice{}, err
	}
	if fs := r.validate(); len(fs) > 0 {
		return Notice{}, problem.Validation(fs...)
	}
	var out Notice
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return problem.Conflict("the meeting has not been called yet")
		}
		out, err = s.recordNotice(ctx, tx, m, sc, snap, person, r, true)
		return err
	})
	return out, dbErr(err, "notice")
}

// NoticeBulk is the input of RecordMeetingNoticesBulk.
type NoticeBulk struct {
	PersonIDs []uuid.UUID `json:"person_ids"`
	Phase     string      `json:"phase"`
	Method    string      `json:"method"`
	SentAt    time.Time   `json:"sent_at"`
}

// RecordMeetingNoticesBulk records one method and date for many owners (for
// example one registered-mail batch). It needs meetings.manage.
func (s *Service) RecordMeetingNoticesBulk(ctx context.Context, orgID, meetingID uuid.UUID, in NoticeBulk) (int, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return 0, err
	}
	r := NoticeRecord{Phase: in.Phase, Method: in.Method, SentAt: in.SentAt}
	fs := r.validate()
	if len(in.PersonIDs) == 0 {
		fs = append(fs, problem.Field{Field: "person_ids", Code: "required"})
	}
	if len(fs) > 0 {
		return 0, problem.Validation(fs...)
	}
	n := 0
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return problem.Conflict("the meeting has not been called yet")
		}
		seen := map[uuid.UUID]bool{}
		for _, p := range in.PersonIDs {
			if seen[p] {
				continue
			}
			seen[p] = true
			if _, err := s.recordNotice(ctx, tx, m, sc, snap, p, r, true); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, dbErr(err, "notices")
}

// DecisionNotify is the input of NotifyMeetingDecisions.
type DecisionNotify struct {
	Channels []string `json:"channels"`
}

// DecisionNotifyResult is what a notification run did (or would do).
type DecisionNotifyResult struct {
	DryRun         bool  `json:"dry_run"`
	RecipientCount int64 `json:"recipient_count"`
	SMSCount       int64 `json:"sms_count"`
}

// NotifyMeetingDecisions sends the decisions to the owners who were absent, by
// the channels the sender chose, and records the delivery like the call (a
// decisions notice per owner). dry_run only counts. It needs meetings.manage.
func (s *Service) NotifyMeetingDecisions(ctx context.Context, orgID, meetingID uuid.UUID, in DecisionNotify, dryRun bool) (DecisionNotifyResult, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return DecisionNotifyResult{}, err
	}
	channels, err := cleanChannels(in.Channels, false)
	if err != nil {
		return DecisionNotifyResult{}, err
	}
	if len(channels) == 0 {
		return DecisionNotifyResult{}, fieldProblem("channels", "required")
	}
	var out DecisionNotifyResult
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		out, err = s.notifyAbsentees(ctx, tx, m, sc, channels, dryRun)
		return err
	})
	return out, dbErr(err, "decision notices")
}

// absentees are the owners of the snapshot who were neither present nor represented.
func absentees(ctx context.Context, tx pgx.Tx, m Meeting) ([]SnapOwner, Snapshot, error) {
	snap, err := loadSnapshot(m.EligibilitySnapshot)
	if err != nil {
		return nil, snap, problem.Conflict("the meeting was never called, so there is nobody to notify")
	}
	rows, err := tx.Query(ctx, `SELECT person_id FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $3 AND mode = 'in_person'
		UNION SELECT proxy_for_person_id FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $3 AND proxy_for_person_id IS NOT NULL`, m.OrgID, m.ID, m.heldCall())
	if err != nil {
		return nil, snap, err
	}
	defer rows.Close()
	present := map[uuid.UUID]bool{}
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			return nil, snap, err
		}
		present[p] = true
	}
	if err := rows.Err(); err != nil {
		return nil, snap, err
	}
	var out []SnapOwner
	for _, o := range snap.Owners {
		if !present[o.PersonID] {
			out = append(out, o)
		}
	}
	return out, snap, nil
}
