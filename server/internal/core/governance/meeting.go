package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Meeting statuses.
const (
	StatusDraft     = "draft"
	StatusCalled    = "called"
	StatusInSession = "in_session"
	StatusClosed    = "closed"
	StatusCancelled = "cancelled"
)

// Meeting is an assembly as the API shows it.
type Meeting struct {
	ID                   uuid.UUID       `json:"id"`
	OrgID                uuid.UUID       `json:"org_id"`
	SiteID               uuid.UUID       `json:"site_id"`
	Kind                 string          `json:"kind"`
	Scope                string          `json:"scope"`
	Title                *string         `json:"title"`
	ScheduledAt          time.Time       `json:"scheduled_at"`
	SecondCallAt         time.Time       `json:"second_call_at"`
	Location             string          `json:"location"`
	Status               string          `json:"status"`
	EligibilitySnapshot  json.RawMessage `json:"eligibility_snapshot"`
	CallLetterDocumentID *uuid.UUID      `json:"call_letter_document_id"`
	MinutesDocumentID    *uuid.UUID      `json:"minutes_document_id"`
	CalledAt             *time.Time      `json:"called_at"`
	// HeldCall is the sitting that actually took place (first or second); nil until the session is opened.
	HeldCall          *string    `json:"held_call"`
	FirstCallFailedAt *time.Time `json:"first_call_failed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Version           int        `json:"version"`

	settings      json.RawMessage
	minutesFileID *uuid.UUID
}

const meetingColumns = `id, org_id, site_id, kind, scope, title, scheduled_at, second_call_at, location, status, eligibility_snapshot, settings_snapshot,
	call_letter_document_id, minutes_document_id, minutes_file_id, called_at, held_call, first_call_failed_at, created_at, updated_at, version`

func scanMeeting(row pgx.Row) (Meeting, error) {
	var m Meeting
	var snap, set []byte
	err := row.Scan(&m.ID, &m.OrgID, &m.SiteID, &m.Kind, &m.Scope, &m.Title, &m.ScheduledAt, &m.SecondCallAt, &m.Location, &m.Status, &snap, &set,
		&m.CallLetterDocumentID, &m.MinutesDocumentID, &m.minutesFileID, &m.CalledAt, &m.HeldCall, &m.FirstCallFailedAt, &m.CreatedAt, &m.UpdatedAt, &m.Version)
	m.ScheduledAt, m.SecondCallAt, m.CreatedAt, m.UpdatedAt = m.ScheduledAt.UTC(), m.SecondCallAt.UTC(), m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	if m.CalledAt != nil {
		t := m.CalledAt.UTC()
		m.CalledAt = &t
	}
	if m.FirstCallFailedAt != nil {
		t := m.FirstCallFailedAt.UTC()
		m.FirstCallFailedAt = &t
	}
	m.EligibilitySnapshot, m.settings = normalizeSnapshot(snap), set
	return m, err
}

// loadMeeting reads a meeting under RLS; ErrNoRows becomes 404.
func loadMeeting(ctx context.Context, tx pgx.Tx, orgID, id uuid.UUID, forUpdate bool) (Meeting, error) {
	q := `SELECT ` + meetingColumns + ` FROM meeting WHERE org_id = $1 AND id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	m, err := scanMeeting(tx.QueryRow(ctx, q, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, problem.NotFound("no such meeting")
	}
	return m, err
}

// meetingAt loads a meeting and checks perm at its site's scope.
func (s *Service) meetingAt(ctx context.Context, tx pgx.Tx, perm authz.Permission, orgID, id uuid.UUID, forUpdate bool) (Meeting, authz.Scope, error) {
	m, err := loadMeeting(ctx, tx, orgID, id, forUpdate)
	if err != nil {
		return m, authz.Scope{}, err
	}
	sc, err := requireSite(ctx, tx, perm, m.SiteID)
	return m, sc, err
}

// redact hides the eligibility snapshot (who owns what) from callers who are not staff.
func (m Meeting) redact(v viewer) Meeting {
	if !v.staff {
		m.EligibilitySnapshot = nil
	}
	return m
}

func (m Meeting) auditView() map[string]any {
	return map[string]any{"kind": m.Kind, "scope": m.Scope, "title": m.Title, "scheduled_at": m.ScheduledAt, "second_call_at": m.SecondCallAt,
		"location": m.Location, "status": m.Status, "held_call": m.HeldCall}
}

// MeetingFilter narrows ListMeetings.
type MeetingFilter struct {
	Status, Kind string
	From, To     *time.Time
}

// ListMeetings lists a site's meetings (newest first by creation). It needs meetings.read.
func (s *Service) ListMeetings(ctx context.Context, orgID, siteID uuid.UUID, f MeetingFilter, p tenancy.PageParams) ([]Meeting, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Meeting
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := requireSite(ctx, tx, PermMeetingsRead, siteID)
		if err != nil {
			return err
		}
		v := viewerOf(ctx, sc)
		rows, err := tx.Query(ctx, `SELECT `+meetingColumns+` FROM meeting WHERE org_id = $1 AND site_id = $2 AND id > $3
			AND ($4 = '' OR status = $4) AND ($5 = '' OR kind = $5) AND ($6::timestamptz IS NULL OR scheduled_at >= $6) AND ($7::timestamptz IS NULL OR scheduled_at <= $7)
			AND ($8 OR status <> 'draft') ORDER BY id LIMIT $9`, orgID, siteID, after, f.Status, f.Kind, f.From, f.To, v.staff, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMeeting(rows)
			if err != nil {
				return err
			}
			out = append(out, m.redact(v))
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "list meetings")
	}
	items, next := tenancy.Page(out, limit, func(m Meeting) uuid.UUID { return m.ID })
	return items, next, nil
}

// MeetingCreate is the input of CreateMeeting.
type MeetingCreate struct {
	SiteID       uuid.UUID `json:"site_id"`
	Kind         string    `json:"kind"`
	Scope        string    `json:"scope"`
	Title        *string   `json:"title"`
	ScheduledAt  time.Time `json:"scheduled_at"`
	SecondCallAt time.Time `json:"second_call_at"`
	Location     string    `json:"location"`
}

// validateWindow checks the second call against the site's window.
func validateWindow(set Settings, scheduled, second time.Time) []problem.Field {
	var fs []problem.Field
	days := second.Sub(scheduled).Hours() / 24
	if days < float64(set.SecondCallMinDays) || days > float64(set.SecondCallMaxDays) {
		fs = append(fs, problem.Field{Field: "second_call_at", Code: "invalid_value",
			Params: map[string]any{"reason": "second_call_window", "min_days": set.SecondCallMinDays, "max_days": set.SecondCallMaxDays}})
	}
	return fs
}

func validateMeetingKind(kind, scope string) []problem.Field {
	var fs []problem.Field
	if kind != "general_assembly" && kind != "extraordinary" {
		fs = append(fs, problem.Field{Field: "kind", Code: "invalid_value"})
	}
	if scope != "site" {
		fs = append(fs, problem.Field{Field: "scope", Code: "invalid_value", Params: map[string]any{"reason": "only_site_scope_is_supported"}})
	}
	return fs
}

// CreateMeeting creates a draft meeting. The second call must fall in the
// site's window. It needs meetings.manage.
func (s *Service) CreateMeeting(ctx context.Context, orgID uuid.UUID, in MeetingCreate) (Meeting, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return Meeting{}, err
	}
	if in.Scope == "" {
		in.Scope = "site"
	}
	fs := validateMeetingKind(in.Kind, in.Scope)
	if strings.TrimSpace(in.Location) == "" {
		fs = append(fs, problem.Field{Field: "location", Code: "required"})
	}
	if !in.ScheduledAt.After(s.now()) {
		fs = append(fs, problem.Field{Field: "scheduled_at", Code: "invalid_value", Params: map[string]any{"reason": "in_the_past"}})
	}
	var out Meeting
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := requireSite(ctx, tx, PermMeetingsManage, in.SiteID)
		if err != nil {
			return err
		}
		if _, err = tenancy.LoadSite(ctx, tx, orgID, in.SiteID, false, false); err != nil {
			return err
		}
		set, err := loadSettings(ctx, tx, orgID, in.SiteID, false)
		if err != nil {
			return err
		}
		fs = append(fs, validateWindow(set.Settings, in.ScheduledAt, in.SecondCallAt)...)
		if len(fs) > 0 {
			return problem.Validation(fs...)
		}
		mid := newID()
		if _, err = tx.Exec(ctx, `INSERT INTO meeting (id, org_id, site_id, kind, scope, title, scheduled_at, second_call_at, location, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, mid, orgID, in.SiteID, in.Kind, in.Scope, in.Title, in.ScheduledAt, in.SecondCallAt, in.Location, principalID(ctx)); err != nil {
			return err
		}
		if out, err = loadMeeting(ctx, tx, orgID, mid, false); err != nil {
			return err
		}
		diff, err := audit.Diff("meeting", nil, out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "meeting.create", TargetType: "meeting", TargetID: mid, Diff: diff})
	})
	return out, dbErr(err, "meeting")
}

// GetMeeting returns a meeting. It needs meetings.read; callers who are not
// staff do not see the eligibility snapshot or drafts.
func (s *Service) GetMeeting(ctx context.Context, orgID, id uuid.UUID) (Meeting, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return Meeting{}, err
	}
	var out Meeting
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsRead, orgID, id, false)
		if err != nil {
			return err
		}
		v := viewerOf(ctx, sc)
		if m.Status == StatusDraft && !v.staff {
			return problem.NotFound("no such meeting")
		}
		out = m.redact(v)
		return nil
	})
	return out, dbErr(err, "meeting")
}

// UpdateMeeting changes a draft meeting if it is still at expectedVersion. It
// needs meetings.manage. A called meeting can only be cancelled.
func (s *Service) UpdateMeeting(ctx context.Context, orgID, id uuid.UUID, expectedVersion int, d tenancy.Doc) (Meeting, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return Meeting{}, err
	}
	fs := d.Unknown("title", "scheduled_at", "second_call_at", "location")
	var out Meeting
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if m.Version != expectedVersion {
			return errVersion
		}
		if m.Status != StatusDraft {
			return problem.Conflict("only a draft meeting can be changed; cancel a called meeting and create a new one")
		}
		next := m
		if d.Has("title") {
			if next.Title, err = d.String("title"); err != nil {
				return err
			}
		}
		if err := d.Get("scheduled_at", &next.ScheduledAt); err != nil {
			return err
		}
		if err := d.Get("second_call_at", &next.SecondCallAt); err != nil {
			return err
		}
		if err := d.Get("location", &next.Location); err != nil {
			return err
		}
		if strings.TrimSpace(next.Location) == "" {
			fs = append(fs, problem.Field{Field: "location", Code: "required"})
		}
		if !next.ScheduledAt.After(s.now()) {
			fs = append(fs, problem.Field{Field: "scheduled_at", Code: "invalid_value", Params: map[string]any{"reason": "in_the_past"}})
		}
		set, err := loadSettings(ctx, tx, orgID, m.SiteID, false)
		if err != nil {
			return err
		}
		fs = append(fs, validateWindow(set.Settings, next.ScheduledAt, next.SecondCallAt)...)
		if len(fs) > 0 {
			return problem.Validation(fs...)
		}
		if _, err = tx.Exec(ctx, `UPDATE meeting SET title = $3, scheduled_at = $4, second_call_at = $5, location = $6, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, id, next.Title, next.ScheduledAt, next.SecondCallAt, next.Location); err != nil {
			return err
		}
		if out, err = loadMeeting(ctx, tx, orgID, id, false); err != nil {
			return err
		}
		diff, err := audit.Diff("meeting", m.auditView(), out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "meeting.update", TargetType: "meeting", TargetID: id, Diff: diff})
	})
	return out, dbErr(err, "meeting")
}

// DeleteMeeting deletes a draft meeting with its agenda. It needs meetings.manage.
func (s *Service) DeleteMeeting(ctx context.Context, orgID, id uuid.UUID, expectedVersion int) error {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if m.Version != expectedVersion {
			return errVersion
		}
		if m.Status != StatusDraft {
			return problem.Conflict("only a draft meeting can be deleted; cancel it instead")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM meeting WHERE org_id = $1 AND id = $2`, orgID, id); err != nil {
			return err
		}
		diff, err := audit.Diff("meeting", m.auditView(), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "meeting.delete", TargetType: "meeting", TargetID: id, Diff: diff})
	})
	return dbErr(err, "meeting")
}

// siteInfo is what documents need to know about the site.
type siteInfo struct {
	Name   string
	Loc    *time.Location
	Locale string
}

func loadSiteInfo(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (siteInfo, error) {
	site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
	if err != nil {
		return siteInfo{}, err
	}
	loc, lerr := time.LoadLocation(site.Timezone)
	if lerr != nil {
		loc = time.UTC
	}
	var locale string
	if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, orgID).Scan(&locale); err != nil {
		return siteInfo{}, err
	}
	return siteInfo{Name: site.Name, Loc: loc, Locale: locale}, nil
}

func newToken() string {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// HashToken is how a check-in token is stored: only its SHA-256 is kept, so a
// database read does not give anybody a working QR. A personal call letter or
// the owner's /me view issues the current token by replacing the stored hash
// (an earlier printed QR then stops working), see rotateToken.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// rotateToken issues a fresh check-in token for an owner of a called meeting
// and stores its hash; it returns pgx.ErrNoRows when the owner has no token.
func rotateToken(ctx context.Context, tx pgx.Tx, orgID, meetingID, person uuid.UUID) (string, error) {
	tok := newToken()
	tag, err := tx.Exec(ctx, `UPDATE attendance_token SET token_hash = $4 WHERE org_id = $1 AND meeting_id = $2 AND person_id = $3`, orgID, meetingID, person, HashToken(tok))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", pgx.ErrNoRows
	}
	return tok, nil
}

// checkinPayload is what the QR code encodes.
func (s *Service) checkinPayload(token string) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/checkin/" + token
}

// MeetingCall is the optional announcement of the call.
type MeetingCall struct {
	NotifyChannels []string `json:"notify_channels"`
}

// CallMeeting calls a draft meeting: it checks the notice lead time and the
// agenda, takes the eligibility snapshot from the owners of record, issues
// each owner a check-in QR token and a pending call notice (the legal
// delivery record), generates the call letter and files it. The call may also
// be announced through the notification channels given; announcing is not
// the legal delivery. It needs meetings.manage.
func (s *Service) CallMeeting(ctx context.Context, orgID, id uuid.UUID, in MeetingCall) (Meeting, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return Meeting{}, err
	}
	channels, err := cleanChannels(in.NotifyChannels)
	if err != nil {
		return Meeting{}, err
	}
	var out Meeting
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if m.Status != StatusDraft {
			return problem.Conflict("only a draft meeting can be called")
		}
		set, err := loadSettings(ctx, tx, orgID, m.SiteID, false)
		if err != nil {
			return err
		}
		now := s.now()
		var fs []problem.Field
		if lead := m.ScheduledAt.Sub(now); lead < time.Duration(set.Settings.CallMinDays)*24*time.Hour {
			fs = append(fs, problem.Field{Field: "scheduled_at", Code: "invalid_value", Params: map[string]any{"reason": "call_too_late", "min_days": set.Settings.CallMinDays}})
		}
		fs = append(fs, validateWindow(set.Settings, m.ScheduledAt, m.SecondCallAt)...)
		var agenda []AgendaLine
		arows, err := tx.Query(ctx, `SELECT position, title, COALESCE(description, ''), will_be_voted FROM agenda_item WHERE org_id = $1 AND meeting_id = $2 ORDER BY position`, orgID, id)
		if err != nil {
			return err
		}
		for arows.Next() {
			var a AgendaLine
			if err := arows.Scan(&a.Position, &a.Title, &a.Description, &a.WillBeVoted); err != nil {
				arows.Close()
				return err
			}
			agenda = append(agenda, a)
		}
		arows.Close()
		if err := arows.Err(); err != nil {
			return err
		}
		if len(agenda) == 0 {
			fs = append(fs, problem.Field{Field: "agenda", Code: "required"})
		}
		if len(fs) > 0 {
			return problem.Validation(fs...)
		}
		snap, err := computeSnapshot(ctx, tx, orgID, m.SiteID, s.today(), set.Settings.VoteWeight)
		if err != nil {
			return err
		}
		snap.TakenAt = now.UTC()
		if len(snap.Owners) == 0 {
			return problem.Conflict("the site has no owners of record to call; add owner occupancies first")
		}
		si, err := loadSiteInfo(ctx, tx, orgID, m.SiteID)
		if err != nil {
			return err
		}
		snapRaw, setRaw := mustJSON(snap), mustJSON(set.Settings)
		if _, err = tx.Exec(ctx, `UPDATE meeting SET status = 'called', called_at = $3, eligibility_snapshot = $4, call_snapshot = $4, settings_snapshot = $5, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, id, now, snapRaw, setRaw); err != nil {
			return err
		}
		people := make([]uuid.UUID, 0, len(snap.Owners))
		for _, o := range snap.Owners {
			people = append(people, o.PersonID)
			if _, err = tx.Exec(ctx, `INSERT INTO attendance_token (org_id, site_id, meeting_id, person_id, token_hash) VALUES ($1, $2, $3, $4, $5)`, orgID, m.SiteID, id, o.PersonID, HashToken(newToken())); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO meeting_notice (id, org_id, site_id, meeting_id, person_id, phase) VALUES ($1, $2, $3, $4, $5, 'call')`, newID(), orgID, m.SiteID, id, o.PersonID); err != nil {
				return err
			}
		}
		title := ""
		if m.Title != nil {
			title = *m.Title
		}
		letter := CallLetter(CallLetterData{SiteName: si.Name, Kind: m.Kind, Title: title, Locale: si.Locale, Loc: si.Loc, ScheduledAt: m.ScheduledAt,
			SecondCall: m.SecondCallAt, Location: m.Location, Agenda: agenda, Instance: s.cfg.InstanceName, IssuedAt: now})
		pdf, err := letter.PDF()
		if err != nil {
			return err
		}
		filed, err := s.filer.File(ctx, tx, orgID, Document{Kind: "call_letter", FileName: "call-letter.pdf", MIME: "application/pdf", Content: pdf, SiteID: m.SiteID, MeetingID: id})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE meeting SET call_letter_document_id = $3 WHERE org_id = $1 AND id = $2`, orgID, id, filed.DocumentID); err != nil {
			return err
		}
		if len(channels) > 0 && s.notifier != nil {
			body := fmt.Sprintf("%s: %s, %s", si.Name, fmtTime(m.ScheduledAt, si.Loc, si.Locale), m.Location)
			if err := s.notifier.Notify(ctx, tx, jobs.NotifyArgs{OrgID: orgID, SiteID: &m.SiteID, Type: "meeting.called", Recipients: recipients(people), Channels: channels,
				Payload: map[string]any{"title": letter.Title, "body": body, "date": fmtTime(m.ScheduledAt, si.Loc, si.Locale), "meeting_id": id}, Link: "/meetings/" + id.String(), Key: "governance.call:" + id.String()}); err != nil {
				return err
			}
		}
		if out, err = loadMeeting(ctx, tx, orgID, id, false); err != nil {
			return err
		}
		diff, err := audit.Diff("meeting", m.auditView(), out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "meeting.call", TargetType: "meeting", TargetID: id, Diff: diff})
	})
	return out, dbErr(err, "meeting")
}

func cleanChannels(in []string) ([]string, error) {
	var out []string
	for _, c := range in {
		if !slices.Contains([]string{"in_app", "email", "sms"}, c) {
			return nil, fieldProblem("notify_channels", "invalid_value")
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// transition moves a meeting between statuses with an audit event.
func (s *Service) transition(ctx context.Context, orgID, id uuid.UUID, from []string, to, action string, extra func(tx pgx.Tx, m Meeting) error) (Meeting, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return Meeting{}, err
	}
	var out Meeting
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if !slices.Contains(from, m.Status) {
			return problem.Conflict(fmt.Sprintf("a %s meeting cannot be moved to %s", m.Status, to))
		}
		if extra != nil {
			if err := extra(tx, m); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE meeting SET status = $3, updated_at = now(), version = version + 1,
			started_at = CASE WHEN $3 = 'in_session' AND started_at IS NULL THEN now() ELSE started_at END, closed_at = CASE WHEN $3 = 'closed' THEN now() ELSE closed_at END
			WHERE org_id = $1 AND id = $2`, orgID, id, to); err != nil {
			return err
		}
		if out, err = loadMeeting(ctx, tx, orgID, id, false); err != nil {
			return err
		}
		diff, err := audit.Diff("meeting", m.auditView(), out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: action, TargetType: "meeting", TargetID: id, Diff: diff})
	})
	return out, dbErr(err, "meeting")
}

// OpenFirstCall opens the session of a called meeting at its first call (the
// attendance desk opens). The call held is recorded, never inferred from the
// clock. It needs meetings.manage.
func (s *Service) OpenFirstCall(ctx context.Context, orgID, id uuid.UUID) (Meeting, error) {
	return s.transition(ctx, orgID, id, []string{StatusCalled}, StatusInSession, "meeting.open_first_call", func(tx pgx.Tx, m Meeting) error {
		if err := s.refreshSnapshot(ctx, tx, m); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE meeting SET held_call = 'first' WHERE org_id = $1 AND id = $2`, orgID, m.ID)
		return err
	})
}

// refreshSnapshot retakes the eligibility snapshot as of the day the session
// opens (G1): an owner who sold after the call loses the vote and the buyer of
// record can check in. The snapshot taken at the call stays on the meeting as
// call_snapshot (the preview); the difference is audited. Owners who left lose
// their check-in token, owners who arrived get a token and a pending call
// notice (they were not noticed, and the record says so).
func (s *Service) refreshSnapshot(ctx context.Context, tx pgx.Tx, m Meeting) error {
	prev, err := loadSnapshot(m.EligibilitySnapshot)
	if err != nil {
		return problem.Conflict("the meeting has not been called yet")
	}
	snap, err := computeSnapshot(ctx, tx, m.OrgID, m.SiteID, s.today(), m.rules().VoteWeight)
	if err != nil {
		return err
	}
	snap.TakenAt = s.now().UTC()
	if len(snap.Owners) == 0 {
		return problem.Conflict("the site has no owners of record on the day of the meeting")
	}
	var added, removed []uuid.UUID
	for _, o := range snap.Owners {
		if _, ok := prev.Owner(o.PersonID); !ok {
			added = append(added, o.PersonID)
		}
	}
	for _, o := range prev.Owners {
		if _, ok := snap.Owner(o.PersonID); !ok {
			removed = append(removed, o.PersonID)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE meeting SET eligibility_snapshot = $3 WHERE org_id = $1 AND id = $2`, m.OrgID, m.ID, mustJSON(snap)); err != nil {
		return err
	}
	for _, p := range removed {
		if _, err := tx.Exec(ctx, `DELETE FROM attendance_token WHERE org_id = $1 AND meeting_id = $2 AND person_id = $3`, m.OrgID, m.ID, p); err != nil {
			return err
		}
	}
	for _, p := range added {
		if _, err := tx.Exec(ctx, `INSERT INTO attendance_token (org_id, site_id, meeting_id, person_id, token_hash) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`, m.OrgID, m.SiteID, m.ID, p, HashToken(newToken())); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO meeting_notice (id, org_id, site_id, meeting_id, person_id, phase) VALUES ($1, $2, $3, $4, $5, 'call')
			ON CONFLICT DO NOTHING`, newID(), m.OrgID, m.SiteID, m.ID, p); err != nil {
			return err
		}
	}
	if len(added) == 0 && len(removed) == 0 && prev.TotalVotes == snap.TotalVotes {
		return nil
	}
	diff, err := audit.Diff("eligibility_snapshot",
		map[string]any{"owners": len(prev.Owners), "total_votes": prev.TotalVotes, "as_of": prev.AsOf},
		map[string]any{"owners": len(snap.Owners), "total_votes": snap.TotalVotes, "as_of": snap.AsOf, "owners_added": added, "owners_removed": removed})
	if err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{OrgID: m.OrgID, SiteID: &m.SiteID, Action: "meeting.snapshot_refresh", TargetType: "meeting", TargetID: m.ID, Diff: diff})
}

// OpenSecondCall moves a session whose first call failed its quorum to the
// second call. It is refused while the first call has its quorum; the failure
// is recorded as first_call_failed_at. Attendance of the first call does not
// carry over: everybody checks in again. It needs meetings.manage.
func (s *Service) OpenSecondCall(ctx context.Context, orgID, id uuid.UUID) (Meeting, error) {
	return s.transition(ctx, orgID, id, []string{StatusInSession}, StatusInSession, "meeting.open_second_call", func(tx pgx.Tx, m Meeting) error {
		if m.heldCall() != callFirst {
			return problem.Conflict("the second call is already open")
		}
		q, _, _, _, err := s.quorumOf(ctx, tx, m)
		if err != nil {
			return err
		}
		if q.Reached {
			return problem.Conflict("the first call reached its quorum; the second call cannot be opened")
		}
		_, err = tx.Exec(ctx, `UPDATE meeting SET held_call = 'second', first_call_failed_at = $3 WHERE org_id = $1 AND id = $2`, orgID, m.ID, s.now())
		return err
	})
}

// CloseMeeting closes a session. Closing does not notify anybody: the decisions
// may not exist yet, so they go to the absent owners with :notify-decisions.
// It needs meetings.manage.
func (s *Service) CloseMeeting(ctx context.Context, orgID, id uuid.UUID) (Meeting, error) {
	return s.transition(ctx, orgID, id, []string{StatusInSession}, StatusClosed, "meeting.close", nil)
}

// CancelMeeting cancels a meeting that has not closed; the reason is kept. It needs meetings.manage.
func (s *Service) CancelMeeting(ctx context.Context, orgID, id uuid.UUID, reason string) (Meeting, error) {
	if strings.TrimSpace(reason) == "" {
		return Meeting{}, fieldProblem("reason", "required")
	}
	return s.transition(ctx, orgID, id, []string{StatusDraft, StatusCalled, StatusInSession}, StatusCancelled, "meeting.cancel", func(tx pgx.Tx, m Meeting) error {
		_, err := tx.Exec(ctx, `UPDATE meeting SET cancel_reason = $3 WHERE org_id = $1 AND id = $2`, orgID, m.ID, reason)
		return err
	})
}
