package governance

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// TermRemindersArgs is the arguments of governance.term_reminders (daily).
type TermRemindersArgs struct{}

// Kind implements river.JobArgs.
func (TermRemindersArgs) Kind() string { return "governance.term_reminders" }

// NotifyAbsenteesArgs is the arguments of governance.notify_absentees, enqueued
// when a meeting closes.
type NotifyAbsenteesArgs struct {
	OrgID     uuid.UUID `json:"org_id"`
	MeetingID uuid.UUID `json:"meeting_id"`
}

// Kind implements river.JobArgs.
func (NotifyAbsenteesArgs) Kind() string { return "governance.notify_absentees" }

type termRemindersWorker struct {
	river.WorkerDefaults[TermRemindersArgs]
	s *Service
}

func (w *termRemindersWorker) Work(ctx context.Context, _ *river.Job[TermRemindersArgs]) error {
	return w.s.RunTermReminders(ctx)
}

type notifyAbsenteesWorker struct {
	river.WorkerDefaults[NotifyAbsenteesArgs]
	s *Service
}

func (w *notifyAbsenteesWorker) Work(ctx context.Context, job *river.Job[NotifyAbsenteesArgs]) error {
	return w.s.RunNotifyAbsentees(ctx, job.Args.OrgID, job.Args.MeetingID)
}

// TermRemindersInterval is how often the term reminder job runs.
const TermRemindersInterval = 24 * time.Hour

// JobOptions returns the module's River workers and its daily periodic job,
// for jobs.Options.Register and jobs.Options.Periodic.
func (s *Service) JobOptions() (func(*river.Workers), []*river.PeriodicJob) {
	register := func(w *river.Workers) {
		river.AddWorker(w, &termRemindersWorker{s: s})
		river.AddWorker(w, &notifyAbsenteesWorker{s: s})
	}
	periodic := []*river.PeriodicJob{river.NewPeriodicJob(
		river.PeriodicInterval(TermRemindersInterval),
		func() (river.JobArgs, *river.InsertOpts) { return TermRemindersArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true},
	)}
	return register, periodic
}

// RiverNotifier implements Notifier with the job client: jobs are inserted in
// the caller's transaction, so they exist exactly when the change committed.
type RiverNotifier struct{ Client *jobs.Client }

// Enqueue implements Notifier.
func (r RiverNotifier) Enqueue(ctx context.Context, tx pgx.Tx, n jobs.Notify) error {
	_, err := r.Client.InsertTx(ctx, tx, n, nil)
	return err
}

// EnqueueNotifyAbsentees implements Notifier.
func (r RiverNotifier) EnqueueNotifyAbsentees(ctx context.Context, tx pgx.Tx, orgID, meetingID uuid.UUID) error {
	_, err := r.Client.InsertTx(ctx, tx, NotifyAbsenteesArgs{OrgID: orgID, MeetingID: meetingID}, nil)
	return err
}

// RunNotifyAbsentees sends the decisions of a closed meeting to the owners who
// were absent (in-app by default; the manager can send again through other
// channels with the notify-decisions operation) and records the delivery like
// the call. It does nothing when the decisions were already sent.
func (s *Service) RunNotifyAbsentees(ctx context.Context, orgID, meetingID uuid.UUID) error {
	return s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, err := loadMeeting(ctx, tx, orgID, meetingID, true)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		if m.Status != StatusClosed || len(m.EligibilitySnapshot) == 0 {
			return nil
		}
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meeting_notice WHERE org_id = $1 AND meeting_id = $2 AND phase = 'decisions' AND status = 'recorded')`, orgID, meetingID).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		sc, err := authz.ScopeOfSite(ctx, tx, m.SiteID)
		if err != nil {
			return err
		}
		_, err = s.notifyAbsentees(ctx, tx, m, sc, []string{"in_app"}, false)
		return err
	})
}

// RunTermReminders creates the reminders before elected terms end (G6) in
// every organization: at term_reminder_days, 30 and 7 days before the end of a
// board, auditor or manager term, and once when it has passed. Each lead time
// reminds once per membership. The site's managers and the member are notified.
func (s *Service) RunTermReminders(ctx context.Context) error {
	if s.platform == nil {
		return nil
	}
	var orgs []uuid.UUID
	if err := s.platform.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM organization WHERE status = 'active' ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			orgs = append(orgs, id)
		}
		return rows.Err()
	}); err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}
	var firstErr error
	for _, org := range orgs {
		if err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error { return s.termRemindersOrg(ctx, tx, org) }); err != nil {
			s.log.Error("term reminders", "org", org, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// leadBucket picks the tightest reminder bucket a term with daysLeft days to
// go has reached: the smallest lead >= daysLeft, 0 once it has passed, -1 when
// none applies yet.
func leadBucket(leads []int, daysLeft int) int {
	if daysLeft < 0 {
		return 0
	}
	best := -1
	for _, l := range leads {
		if daysLeft <= l && (best == -1 || l < best) {
			best = l
		}
	}
	return best
}

// reminderLeads are the lead times of a site: its term_reminder_days plus 30 and 7 days (those below it).
func reminderLeads(set Settings) []int {
	leads := []int{int(set.TermReminderDays)}
	for _, l := range []int{30, 7} {
		if l < int(set.TermReminderDays) {
			leads = append(leads, l)
		}
	}
	slices.Sort(leads)
	return leads
}

func (s *Service) termRemindersOrg(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	today := s.today()
	type term struct {
		membership, person, site uuid.UUID
		role, name               string
		validTo                  time.Time
	}
	rows, err := tx.Query(ctx, `SELECT m.id, m.person_id, m.scope_id, m.role, p.display_name, m.valid_to
		FROM membership m JOIN person p ON p.org_id = m.org_id AND p.id = m.person_id
		WHERE m.org_id = $1 AND m.scope_type = 'site' AND m.role IN ('site_manager', 'board_member', 'auditor') AND m.valid_to IS NOT NULL
		AND m.valid_from <= now() AND m.valid_to >= now() - interval '30 days' AND m.valid_to <= now() + interval '366 days'
		AND NOT EXISTS (SELECT 1 FROM membership n WHERE n.org_id = m.org_id AND n.person_id = m.person_id AND n.role = m.role AND n.scope_type = m.scope_type
			AND n.scope_id = m.scope_id AND n.id <> m.id AND n.valid_from <= m.valid_to AND (n.valid_to IS NULL OR n.valid_to > m.valid_to))
		AND EXISTS (SELECT 1 FROM site s WHERE s.org_id = m.org_id AND s.id = m.scope_id AND s.status = 'active')
		ORDER BY m.id`, orgID)
	if err != nil {
		return err
	}
	var terms []term
	for rows.Next() {
		var t term
		if err := rows.Scan(&t.membership, &t.person, &t.site, &t.role, &t.name, &t.validTo); err != nil {
			rows.Close()
			return err
		}
		terms = append(terms, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	leadsOf := map[uuid.UUID][]int{}
	for _, t := range terms {
		leads, ok := leadsOf[t.site]
		if !ok {
			set, err := loadSettings(ctx, tx, orgID, t.site, false)
			if err != nil {
				return err
			}
			leads = reminderLeads(set.Settings)
			leadsOf[t.site] = leads
		}
		end := time.Date(t.validTo.UTC().Year(), t.validTo.UTC().Month(), t.validTo.UTC().Day(), 0, 0, 0, 0, time.UTC)
		days := int(end.Sub(today).Hours() / 24)
		bucket := leadBucket(leads, days)
		if bucket < 0 {
			continue
		}
		rid := newID()
		tag, err := tx.Exec(ctx, `INSERT INTO term_reminder (id, org_id, site_id, membership_id, person_id, role, valid_to, lead_days) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (org_id, membership_id, lead_days) DO NOTHING`, rid, orgID, t.site, t.membership, t.person, t.role, end, bucket)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		recipients, err := termRecipients(ctx, tx, orgID, t.site, t.person)
		if err != nil {
			return err
		}
		if s.notifier != nil && len(recipients) > 0 {
			title := fmt.Sprintf("%s: %s", t.role, t.name)
			body := fmt.Sprintf("%s term ends %s (%d days)", t.role, end.Format("2006-01-02"), days)
			if err := s.notifier.Enqueue(ctx, tx, jobs.Notify{OrgID: orgID, SiteID: &t.site, Topic: "governance.term_expiry", PersonIDs: recipients,
				Channels: []string{"in_app", "email"}, Title: title, Body: body, RefType: "membership", RefID: t.membership}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE term_reminder SET notified_at = now() WHERE org_id = $1 AND id = $2`, orgID, rid); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("term_reminder", nil, map[string]any{"membership_id": t.membership, "role": t.role, "valid_to": end.Format("2006-01-02"), "lead_days": bucket})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &t.site, ActorKind: audit.ActorJob, Action: "term_reminder.create", TargetType: "term_reminder", TargetID: rid, Diff: diff}); err != nil {
			return err
		}
	}
	return nil
}

// termRecipients are the site's current managers and the person whose term ends.
func termRecipients(ctx context.Context, tx pgx.Tx, orgID, siteID, member uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT person_id FROM membership WHERE org_id = $1 AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())
		AND ((role = 'site_manager' AND scope_type = 'site' AND scope_id = $2) OR (role = 'org_admin' AND scope_type = 'org')) ORDER BY person_id`, orgID, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{member}
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if p != member {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// TermReminder is a reminder before an elected term ends.
type TermReminder struct {
	ID           uuid.UUID  `json:"id"`
	OrgID        uuid.UUID  `json:"org_id"`
	SiteID       uuid.UUID  `json:"site_id"`
	MembershipID uuid.UUID  `json:"membership_id"`
	PersonName   string     `json:"person_display_name"`
	Role         string     `json:"role"`
	ValidTo      string     `json:"valid_to"`
	DaysLeft     int        `json:"days_left"`
	Status       string     `json:"status"`
	NotifiedAt   *time.Time `json:"notified_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ListTermReminders lists the latest reminder of each elected term of a site.
// Status is overdue (ended), due (30 days or less) or upcoming. It needs meetings.manage.
func (s *Service) ListTermReminders(ctx context.Context, orgID, siteID uuid.UUID, status string, p tenancy.PageParams) ([]TermReminder, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []TermReminder
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireSite(ctx, tx, PermMeetingsManage, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT r.id, r.org_id, r.site_id, r.membership_id, p.display_name, r.role, r.valid_to, r.valid_to - $4::date, r.notified_at, r.created_at, r.updated_at
			FROM term_reminder r JOIN person p ON p.org_id = r.org_id AND p.id = r.person_id
			WHERE r.org_id = $1 AND r.site_id = $2 AND r.id > $3
			AND r.id = (SELECT r2.id FROM term_reminder r2 WHERE r2.org_id = r.org_id AND r2.membership_id = r.membership_id ORDER BY r2.lead_days LIMIT 1)
			AND ($5 = '' OR CASE WHEN r.valid_to - $4::date < 0 THEN 'overdue' WHEN r.valid_to - $4::date <= 30 THEN 'due' ELSE 'upcoming' END = $5)
			ORDER BY r.id LIMIT $6`, orgID, siteID, after, s.today(), status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t TermReminder
			var vt time.Time
			if err := rows.Scan(&t.ID, &t.OrgID, &t.SiteID, &t.MembershipID, &t.PersonName, &t.Role, &vt, &t.DaysLeft, &t.NotifiedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
				return err
			}
			t.ValidTo = vt.Format("2006-01-02")
			t.CreatedAt, t.UpdatedAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC()
			switch {
			case t.DaysLeft < 0:
				t.Status = "overdue"
			case t.DaysLeft <= 30:
				t.Status = "due"
			default:
				t.Status = "upcoming"
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "term reminders")
	}
	items, next := tenancy.Page(out, limit, func(t TermReminder) uuid.UUID { return t.ID })
	return items, next, nil
}
