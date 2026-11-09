package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Attendance is one attendance record: a person in the room, for their own
// units or as proxy for another owner's (G4).
type Attendance struct {
	ID               uuid.UUID   `json:"id"`
	OrgID            uuid.UUID   `json:"org_id"`
	MeetingID        uuid.UUID   `json:"meeting_id"`
	PersonID         uuid.UUID   `json:"person_id"`
	PersonName       string      `json:"person_display_name"`
	UnitIDs          []uuid.UUID `json:"unit_ids"`
	UnitLabels       []string    `json:"unit_labels"`
	Mode             string      `json:"mode"`
	Call             string      `json:"call"`
	ProxyForPersonID *uuid.UUID  `json:"proxy_for_person_id"`
	Source           string      `json:"source"`
	CheckedInAt      time.Time   `json:"checked_in_at"`
	CheckedInBy      *uuid.UUID  `json:"checked_in_by"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`

	votes int64
	share float64
}

const attendanceSelect = `SELECT a.id, a.org_id, a.meeting_id, a.person_id, p.display_name, a.unit_ids,
	(SELECT COALESCE(array_agg(u.door_number ORDER BY u.door_number), '{}') FROM unit u WHERE u.org_id = a.org_id AND u.id = ANY(a.unit_ids)),
	a.mode, a.call, a.proxy_for_person_id, a.source, a.checked_in_at, a.checked_in_by, a.created_at, a.updated_at, a.votes, a.share::float8
	FROM attendance a JOIN person p ON p.org_id = a.org_id AND p.id = a.person_id`

func scanAttendance(row pgx.Row) (Attendance, error) {
	var a Attendance
	err := row.Scan(&a.ID, &a.OrgID, &a.MeetingID, &a.PersonID, &a.PersonName, &a.UnitIDs, &a.UnitLabels, &a.Mode, &a.Call, &a.ProxyForPersonID, &a.Source,
		&a.CheckedInAt, &a.CheckedInBy, &a.CreatedAt, &a.UpdatedAt, &a.votes, &a.share)
	a.CheckedInAt, a.CreatedAt, a.UpdatedAt = a.CheckedInAt.UTC(), a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a, err
}

func (a Attendance) auditView() map[string]any {
	return map[string]any{"person_id": a.PersonID, "unit_ids": a.UnitIDs, "mode": a.Mode, "proxy_for_person_id": a.ProxyForPersonID, "source": a.Source}
}

// rules are the settings in force for a meeting: the snapshot taken when it
// was called, else the current ones.
func (m Meeting) rules() Settings {
	set := GenericPreset()
	if len(m.settings) > 0 {
		_ = json.Unmarshal(m.settings, &set)
	}
	return set
}

// heldCall is the sitting that took place, recorded by the open-first-call and
// open-second-call transitions and never read from the clock; before the
// session opens it is the first.
func (m Meeting) heldCall() string {
	if m.HeldCall != nil {
		return *m.HeldCall
	}
	return callFirst
}

// presence is who is in the room, with the per-person cap applied.
type presence struct {
	Raw    Weight
	Capped Weight
	People int
}

func loadPresence(ctx context.Context, tx pgx.Tx, orgID, meetingID uuid.UUID, call string, snap Snapshot, set Settings) (presence, error) {
	rows, err := tx.Query(ctx, `SELECT person_id, votes, share::float8 FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $3 ORDER BY checked_in_at, id`, orgID, meetingID, call)
	if err != nil {
		return presence{}, err
	}
	defer rows.Close()
	byPerson := map[uuid.UUID]*PersonVotes{}
	var order []uuid.UUID
	for rows.Next() {
		var p uuid.UUID
		var v int64
		var sh float64
		if err := rows.Scan(&p, &v, &sh); err != nil {
			return presence{}, err
		}
		pv := byPerson[p]
		if pv == nil {
			pv = &PersonVotes{PersonID: p.String()}
			byPerson[p] = pv
			order = append(order, p)
		}
		pv.Votes += v
		pv.Share += sh
	}
	if err := rows.Err(); err != nil {
		return presence{}, err
	}
	raw := make([]PersonVotes, 0, len(order))
	for _, p := range order {
		raw = append(raw, *byPerson[p])
	}
	capped := ApplyPersonCap(raw, snap.TotalVotes, set.MaxVotesPerPersonPct)
	return presence{Raw: SumWeights(raw...), Capped: SumWeights(capped...), People: len(order)}, nil
}

// Quorum is the live quorum of a meeting.
type Quorum struct {
	MeetingID          uuid.UUID `json:"meeting_id"`
	Call               string    `json:"call"`
	PresentCount       int64     `json:"present_count"`
	EligibleCount      int64     `json:"eligible_count"`
	PresentShare       float64   `json:"present_share"`
	EligibleShare      float64   `json:"eligible_share"`
	RequiredByCountPct *float64  `json:"required_by_count_pct"`
	RequiredBySharePct *float64  `json:"required_by_share_pct"`
	Reached            bool      `json:"reached"`
	AsOf               time.Time `json:"as_of"`
}

func (s *Service) quorumOf(ctx context.Context, tx pgx.Tx, m Meeting) (Quorum, QuorumResult, presence, Snapshot, error) {
	snap, err := loadSnapshot(m.EligibilitySnapshot)
	if err != nil {
		return Quorum{}, QuorumResult{}, presence{}, snap, problem.Conflict("the meeting has not been called yet")
	}
	set := m.rules()
	call := m.heldCall()
	pr, err := loadPresence(ctx, tx, m.OrgID, m.ID, call, snap, set)
	if err != nil {
		return Quorum{}, QuorumResult{}, pr, snap, err
	}
	now := s.now().UTC()
	res := EvalQuorum(set.Quorum, call, snap.Total(), pr.Capped)
	return Quorum{MeetingID: m.ID, Call: call, PresentCount: pr.Capped.Votes, EligibleCount: snap.TotalVotes, PresentShare: pr.Capped.Share, EligibleShare: snap.TotalShare,
		RequiredByCountPct: res.RequiredCountPct, RequiredBySharePct: res.RequiredSharePct, Reached: res.Reached, AsOf: now}, res, pr, snap, nil
}

// GetQuorum computes the live quorum (by number and by share, with the
// per-person cap) against the eligibility snapshot. It needs meetings.manage
// or attendance.manage.
func (s *Service) GetQuorum(ctx context.Context, orgID, meetingID uuid.UUID) (Quorum, error) {
	g := authz.GrantsFromContext(ctx)
	if !g.HoldsAnywhere(PermMeetingsManage) && !g.HoldsAnywhere(PermAttendanceManage) {
		return Quorum{}, problem.Forbidden("missing permission " + string(PermAttendanceManage))
	}
	var out Quorum
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, err := loadMeeting(ctx, tx, orgID, meetingID, false)
		if err != nil {
			return err
		}
		sc, err := authz.ScopeOfSite(ctx, tx, m.SiteID)
		if err != nil {
			return err
		}
		if authz.Require(ctx, PermAttendanceManage, sc) != nil && authz.Require(ctx, PermMeetingsManage, sc) != nil {
			return problem.Forbidden("missing permission " + string(PermAttendanceManage))
		}
		out, _, _, _, err = s.quorumOf(ctx, tx, m)
		return err
	})
	return out, dbErr(err, "quorum")
}

// ListAttendance lists the attendance records of a meeting. It needs attendance.manage.
func (s *Service) ListAttendance(ctx context.Context, orgID, meetingID uuid.UUID, mode, source string, p tenancy.PageParams) ([]Attendance, string, error) {
	if err := authz.RequireAnywhere(ctx, PermAttendanceManage); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Attendance
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, _, err := s.meetingAt(ctx, tx, PermAttendanceManage, orgID, meetingID, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, attendanceSelect+` WHERE a.org_id = $1 AND a.meeting_id = $2 AND a.id > $3 AND ($4 = '' OR a.mode = $4) AND ($5 = '' OR a.source = $5)
			ORDER BY a.id LIMIT $6`, orgID, meetingID, after, mode, source, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAttendance(rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "list attendance")
	}
	items, next := tenancy.Page(out, limit, func(a Attendance) uuid.UUID { return a.ID })
	return items, next, nil
}

// AttendanceCreate is the input of RecordAttendance.
type AttendanceCreate struct {
	PersonID         uuid.UUID   `json:"person_id"`
	UnitIDs          []uuid.UUID `json:"unit_ids"`
	Mode             string      `json:"mode"`
	ProxyForPersonID *uuid.UUID  `json:"proxy_for_person_id"`
}

func attendanceOpen(m Meeting) error {
	if m.Status != StatusInSession || m.HeldCall == nil {
		return problem.Conflict("attendance is recorded while the session is open: open the first call first")
	}
	return nil
}

// represented returns the units already represented at the call held.
func represented(ctx context.Context, tx pgx.Tx, orgID, meetingID uuid.UUID, call string) (map[uuid.UUID]bool, error) {
	rows, err := tx.Query(ctx, `SELECT unnest(unit_ids) FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $3`, orgID, meetingID, call)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out[u] = true
	}
	return out, rows.Err()
}

// insertAttendance validates and writes one record; the caller holds the meeting row lock.
func (s *Service) insertAttendance(ctx context.Context, tx pgx.Tx, m Meeting, sc authz.Scope, in AttendanceCreate, source string) (Attendance, error) {
	if err := attendanceOpen(m); err != nil {
		return Attendance{}, err
	}
	snap, err := loadSnapshot(m.EligibilitySnapshot)
	if err != nil {
		return Attendance{}, err
	}
	set := m.rules()
	var fs []problem.Field
	if in.Mode != "in_person" && in.Mode != "proxy" {
		return Attendance{}, fieldProblem("mode", "invalid_value")
	}
	if (in.Mode == "proxy") != (in.ProxyForPersonID != nil) {
		return Attendance{}, fieldProblem("proxy_for_person_id", map[bool]string{true: "required", false: "invalid_value"}[in.Mode == "proxy"])
	}
	holder := in.PersonID
	ownerID := in.PersonID
	if in.ProxyForPersonID != nil {
		ownerID = *in.ProxyForPersonID
		if ownerID == holder {
			return Attendance{}, fieldProblem("proxy_for_person_id", "invalid_value")
		}
	}
	owner, ok := snap.Owner(ownerID)
	if !ok {
		fs = append(fs, problem.Field{Field: "person_id", Code: "invalid_value", Params: map[string]any{"reason": "not_an_owner_of_record"}})
		return Attendance{}, problem.Validation(fs...)
	}
	units := in.UnitIDs
	if len(units) == 0 {
		for _, u := range owner.Units {
			units = append(units, u.UnitID)
		}
	}
	for _, u := range units {
		if !slices.ContainsFunc(owner.Units, func(x SnapUnit) bool { return x.UnitID == u }) {
			return Attendance{}, problem.Validation(problem.Field{Field: "unit_ids", Code: "invalid_value", Params: map[string]any{"reason": "unit_not_voted_by_this_owner"}})
		}
	}
	call := m.heldCall()
	already, err := represented(ctx, tx, m.OrgID, m.ID, call)
	if err != nil {
		return Attendance{}, err
	}
	for _, u := range units {
		if already[u] {
			return Attendance{}, problem.Conflict("a unit is already represented in this meeting")
		}
	}
	w := snap.WeightOf(owner, units)
	if in.Mode == "proxy" {
		var held ProxyHeld
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(votes), 0) FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $4 AND person_id = $3 AND mode = 'proxy'`,
			m.OrgID, m.ID, holder, call).Scan(&held.Grantors, &held.Votes); err != nil {
			return Attendance{}, err
		}
		if reason := CheckProxyCap(set, snap.UnitCount, snap.TotalVotes, held, w.Votes); reason != "" {
			return Attendance{}, problem.Validation(problem.Field{Field: "proxy_for_person_id", Code: "invalid_value", Params: map[string]any{"reason": reason}})
		}
	}
	aid := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO attendance (id, org_id, site_id, meeting_id, person_id, unit_ids, mode, proxy_for_person_id, source, votes, share, checked_in_by, call)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`, aid, m.OrgID, m.SiteID, m.ID, holder, units, in.Mode, in.ProxyForPersonID, source, w.Votes, w.Share, principalID(ctx), call); err != nil {
		if tenancy.IsUnique(err, "attendance_one_per_represented") {
			return Attendance{}, problem.Conflict("this person is already recorded for this owner")
		}
		return Attendance{}, err
	}
	out, err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.org_id = $1 AND a.id = $2`, m.OrgID, aid))
	if err != nil {
		return out, err
	}
	diff, err := audit.Diff("attendance", nil, out.auditView())
	if err != nil {
		return out, err
	}
	return out, audit.Record(ctx, tx, audit.Event{OrgID: m.OrgID, SiteID: sc.SiteID, Action: "attendance.record", TargetType: "attendance", TargetID: aid, Diff: diff})
}

// RecordAttendance ticks an owner off at the desk: present in person, or a
// proxy holder representing another owner. Proxy caps and the unit's single
// representation are enforced. It needs attendance.manage.
func (s *Service) RecordAttendance(ctx context.Context, orgID, meetingID uuid.UUID, in AttendanceCreate) (Attendance, error) {
	if err := authz.RequireAnywhere(ctx, PermAttendanceManage); err != nil {
		return Attendance{}, err
	}
	var out Attendance
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermAttendanceManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		out, err = s.insertAttendance(ctx, tx, m, sc, in, "desk")
		return err
	})
	return out, dbErr(err, "attendance")
}

// RemoveAttendance deletes an attendance record (a mistaken tick). It needs attendance.manage.
func (s *Service) RemoveAttendance(ctx context.Context, orgID, meetingID, attendanceID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermAttendanceManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermAttendanceManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		if err := attendanceOpen(m); err != nil {
			return err
		}
		a, err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.org_id = $1 AND a.meeting_id = $2 AND a.id = $3 AND a.call = $4`, orgID, meetingID, attendanceID, m.heldCall()))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such attendance record")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM attendance WHERE org_id = $1 AND id = $2`, orgID, attendanceID); err != nil {
			return err
		}
		diff, err := audit.Diff("attendance", a.auditView(), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "attendance.remove", TargetType: "attendance", TargetID: attendanceID, Diff: diff})
	})
	return dbErr(err, "attendance")
}

// CheckIn checks in the owner whose QR token was scanned at the desk (or sent
// from the app): the same record as the tick-off, with source qr, for all of
// the owner's units that are not represented yet. Scanning twice returns the
// existing record. It needs attendance.manage.
func (s *Service) CheckIn(ctx context.Context, orgID, meetingID uuid.UUID, token string) (Attendance, error) {
	if err := authz.RequireAnywhere(ctx, PermAttendanceManage); err != nil {
		return Attendance{}, err
	}
	var out Attendance
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermAttendanceManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		if err := attendanceOpen(m); err != nil {
			return err
		}
		var person uuid.UUID
		err = tx.QueryRow(ctx, `SELECT person_id FROM attendance_token WHERE org_id = $1 AND meeting_id = $2 AND token_hash = $3`, orgID, meetingID, HashToken(token)).Scan(&person)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("this check-in code does not belong to the meeting")
		}
		if err != nil {
			return err
		}
		existing, err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.org_id = $1 AND a.meeting_id = $2 AND a.call = $4 AND a.person_id = $3 AND a.mode = 'in_person'`, orgID, meetingID, person, m.heldCall()))
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return err
		}
		owner, _ := snap.Owner(person)
		already, err := represented(ctx, tx, orgID, meetingID, m.heldCall())
		if err != nil {
			return err
		}
		var units []uuid.UUID
		for _, u := range owner.Units {
			if !already[u.UnitID] {
				units = append(units, u.UnitID)
			}
		}
		if len(units) == 0 {
			return problem.Conflict("all of this owner's units are already represented")
		}
		out, err = s.insertAttendance(ctx, tx, m, sc, AttendanceCreate{PersonID: person, UnitIDs: units, Mode: "in_person"}, "qr")
		return err
	})
	return out, dbErr(err, "attendance")
}

// sheetData collects the attendance list's content.
func (s *Service) sheetData(ctx context.Context, tx pgx.Tx, m Meeting) (AttendanceSheetData, error) {
	q, res, pr, snap, err := s.quorumOf(ctx, tx, m)
	if err != nil {
		return AttendanceSheetData{}, err
	}
	si, err := loadSiteInfo(ctx, tx, m.OrgID, m.SiteID)
	if err != nil {
		return AttendanceSheetData{}, err
	}
	rows, err := tx.Query(ctx, attendanceSelect+` WHERE a.org_id = $1 AND a.meeting_id = $2 AND a.call = $3 ORDER BY a.checked_in_at, a.id`, m.OrgID, m.ID, m.heldCall())
	if err != nil {
		return AttendanceSheetData{}, err
	}
	var atts []Attendance
	for rows.Next() {
		a, err := scanAttendance(rows)
		if err != nil {
			rows.Close()
			return AttendanceSheetData{}, err
		}
		atts = append(atts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AttendanceSheetData{}, err
	}
	d := AttendanceSheetData{SiteName: si.Name, Locale: si.Locale, Loc: si.Loc, ScheduledAt: m.ScheduledAt, Location: m.Location, Call: q.Call,
		Eligible: snap.Total(), Present: pr.Capped, Quorum: res}
	for _, a := range atts {
		row := SheetRow{Name: a.PersonName, Units: a.UnitLabels, Votes: a.votes, Share: a.share}
		if a.ProxyForPersonID != nil {
			if o, ok := snap.Owner(*a.ProxyForPersonID); ok {
				row.ProxyFor = o.DisplayName
			}
		}
		d.Rows = append(d.Rows, row)
	}
	return d, nil
}

// AttendanceSheetPDF renders the hazirun cetveli. It needs attendance.manage.
func (s *Service) AttendanceSheetPDF(ctx context.Context, orgID, meetingID uuid.UUID) ([]byte, string, error) {
	if err := authz.RequireAnywhere(ctx, PermAttendanceManage); err != nil {
		return nil, "", err
	}
	var pdf []byte
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, _, err := s.meetingAt(ctx, tx, PermAttendanceManage, orgID, meetingID, false)
		if err != nil {
			return err
		}
		d, err := s.sheetData(ctx, tx, m)
		if err != nil {
			return err
		}
		pdf, err = s.attendanceModel(ctx, orgID, d).PDF()
		return err
	})
	return pdf, "attendance-sheet.pdf", dbErr(err, "attendance sheet")
}

// CallLetterPDF returns the call letter. Without a person it is the generic
// letter filed at the call; with person_id it is that owner's letter with their
// check-in QR (an owner may fetch their own, a manager anyone's). It needs meetings.read.
func (s *Service) CallLetterPDF(ctx context.Context, orgID, meetingID uuid.UUID, person *uuid.UUID) ([]byte, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return nil, "", err
	}
	var pdf []byte
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsRead, orgID, meetingID, false)
		if err != nil {
			return err
		}
		v := viewerOf(ctx, sc)
		if m.Status == StatusDraft || m.Status == StatusCancelled && m.CalledAt == nil {
			return problem.Conflict("the meeting has not been called, so there is no call letter")
		}
		if person == nil {
			if !v.staff && !v.owner {
				return problem.Forbidden("the call letter goes to owners")
			}
			if m.CallLetterDocumentID != nil {
				err = tx.QueryRow(ctx, `SELECT content FROM meeting_document WHERE org_id = $1 AND id = $2`, orgID, *m.CallLetterDocumentID).Scan(&pdf)
				if err == nil {
					return nil
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
		}
		doc, err := s.callLetterDoc(ctx, tx, m, person, v)
		if err != nil {
			return err
		}
		pdf, err = doc.PDF()
		return err
	})
	return pdf, "call-letter.pdf", dbErr(err, "call letter")
}

func (s *Service) callLetterDoc(ctx context.Context, tx pgx.Tx, m Meeting, person *uuid.UUID, v viewer) (DocModel, error) {
	si, err := loadSiteInfo(ctx, tx, m.OrgID, m.SiteID)
	if err != nil {
		return DocModel{}, err
	}
	rows, err := tx.Query(ctx, `SELECT position, title, COALESCE(description, ''), will_be_voted FROM agenda_item WHERE org_id = $1 AND meeting_id = $2 ORDER BY position`, m.OrgID, m.ID)
	if err != nil {
		return DocModel{}, err
	}
	var agenda []AgendaLine
	for rows.Next() {
		var a AgendaLine
		if err := rows.Scan(&a.Position, &a.Title, &a.Description, &a.WillBeVoted); err != nil {
			rows.Close()
			return DocModel{}, err
		}
		agenda = append(agenda, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DocModel{}, err
	}
	title := ""
	if m.Title != nil {
		title = *m.Title
	}
	d := CallLetterData{SiteName: si.Name, Kind: m.Kind, Title: title, Locale: si.Locale, Loc: si.Loc, ScheduledAt: m.ScheduledAt, SecondCall: m.SecondCallAt,
		Location: m.Location, Agenda: agenda, Instance: s.cfg.InstanceName, IssuedAt: m.CalledAt0(m.CreatedAt)}
	if person != nil {
		if me, ok := callerPerson(ctx, tx, m.OrgID); !v.staff && (!ok || me != *person) {
			return DocModel{}, problem.Forbidden("you can only fetch your own call letter")
		}
		var name string
		err := tx.QueryRow(ctx, `SELECT p.display_name FROM attendance_token t JOIN person p ON p.org_id = t.org_id AND p.id = t.person_id
			WHERE t.org_id = $1 AND t.meeting_id = $2 AND t.person_id = $3`, m.OrgID, m.ID, *person).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return DocModel{}, problem.NotFound("this person is not an owner called to the meeting")
		}
		if err != nil {
			return DocModel{}, err
		}
		// only the hash is stored: the letter issues the owner's current token (an earlier QR stops working)
		token, err := rotateToken(ctx, tx, m.OrgID, m.ID, *person)
		if err != nil {
			return DocModel{}, err
		}
		d.Recipient, d.QRToken, d.QRPayload = name, token, s.checkinPayload(token)
		if d.QRPNG, err = QRPNG(d.QRPayload); err != nil {
			return DocModel{}, fmt.Errorf("render qr: %w", err)
		}
	}
	return s.callLetterModel(ctx, m.OrgID, d), nil
}

// CalledAt0 returns the call time, or def when the meeting has none.
func (m Meeting) CalledAt0(def time.Time) time.Time {
	if m.CalledAt != nil {
		return *m.CalledAt
	}
	return def
}

// MyPoll is a poll the caller can answer.
type MyPoll struct {
	AgendaItemID  uuid.UUID   `json:"agenda_item_id"`
	Title         string      `json:"title"`
	AnsweredUnits []uuid.UUID `json:"answered_units"`
	// Answers is what the caller answered per unit (their own answers only).
	Answers []MyPollAnswer `json:"answers"`
}

// MyPollAnswer is the caller's answer for one unit.
type MyPollAnswer struct {
	UnitID     uuid.UUID `json:"unit_id"`
	Answer     string    `json:"answer"`
	AnsweredAt time.Time `json:"answered_at"`
}

// MyMeeting is the meeting as the caller sees it.
type MyMeeting struct {
	Meeting         Meeting     `json:"meeting"`
	EligibleUnitIDs []uuid.UUID `json:"eligible_unit_ids"`
	Notice          *Notice     `json:"notice"`
	AttendanceToken *string     `json:"attendance_token"`
	AttendanceQR    *string     `json:"attendance_qr"`
	CheckedIn       bool        `json:"checked_in"`
	Polls           []MyPoll    `json:"polls"`
}

// GetMyMeeting shows the caller their part of a meeting: the units they vote
// for, their call notice, their check-in QR and the polls to answer. Tenants
// see only the meeting itself. It needs meetings.read.
func (s *Service) GetMyMeeting(ctx context.Context, orgID, meetingID uuid.UUID) (MyMeeting, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return MyMeeting{}, err
	}
	out := MyMeeting{EligibleUnitIDs: []uuid.UUID{}, Polls: []MyPoll{}}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsRead, orgID, meetingID, false)
		if err != nil {
			return err
		}
		v := viewerOf(ctx, sc)
		if m.Status == StatusDraft && !v.staff {
			return problem.NotFound("no such meeting")
		}
		out.Meeting = m.redact(v)
		me, ok := callerPerson(ctx, tx, orgID)
		if !ok {
			return nil
		}
		snap, serr := loadSnapshot(m.EligibilitySnapshot)
		owner, isOwner := Snapshot{}.Owner(me)
		if serr == nil {
			owner, isOwner = snap.Owner(me)
		}
		if !isOwner {
			return nil
		}
		for _, u := range owner.Units {
			out.EligibleUnitIDs = append(out.EligibleUnitIDs, u.UnitID)
		}
		if n, err := loadNotice(ctx, tx, orgID, meetingID, me, "call"); err == nil {
			out.Notice = &n
		} else if !isNotFound(err) {
			return err
		}
		if m.Status == StatusCalled || m.Status == StatusInSession {
			// only the hash is stored: viewing issues the owner's current token
			if tok, err := rotateToken(ctx, tx, orgID, meetingID, me); err == nil {
				out.AttendanceToken, out.AttendanceQR = &tok, ptr(s.checkinPayload(tok))
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $4 AND (person_id = $3 OR proxy_for_person_id = $3))`, orgID, meetingID, me, m.heldCall()).Scan(&out.CheckedIn); err != nil {
			return err
		}
		prows, err := tx.Query(ctx, `SELECT a.id, a.title FROM agenda_item a JOIN poll p ON p.org_id = a.org_id AND p.agenda_item_id = a.id
			WHERE a.org_id = $1 AND a.meeting_id = $2 ORDER BY a.position`, orgID, meetingID)
		if err != nil {
			return err
		}
		defer prows.Close()
		var ids []uuid.UUID
		for prows.Next() {
			mp := MyPoll{AnsweredUnits: []uuid.UUID{}, Answers: []MyPollAnswer{}}
			if err := prows.Scan(&mp.AgendaItemID, &mp.Title); err != nil {
				return err
			}
			out.Polls = append(out.Polls, mp)
			ids = append(ids, mp.AgendaItemID)
		}
		if err := prows.Err(); err != nil {
			return err
		}
		prows.Close()
		// the caller's own answers per unit, one query for all polls (answered_by = me: never another owner's)
		arows, err := tx.Query(ctx, `SELECT agenda_item_id, unit_id, answer, answered_at FROM poll_answer WHERE org_id = $1 AND answered_by = $2 AND agenda_item_id = ANY($3) ORDER BY unit_id`,
			orgID, me, ids)
		if err != nil {
			return err
		}
		defer arows.Close()
		byItem := map[uuid.UUID]*MyPoll{}
		for i := range out.Polls {
			byItem[out.Polls[i].AgendaItemID] = &out.Polls[i]
		}
		for arows.Next() {
			var item uuid.UUID
			var a MyPollAnswer
			if err := arows.Scan(&item, &a.UnitID, &a.Answer, &a.AnsweredAt); err != nil {
				return err
			}
			a.AnsweredAt = a.AnsweredAt.UTC()
			mp := byItem[item]
			mp.Answers = append(mp.Answers, a)
			mp.AnsweredUnits = append(mp.AnsweredUnits, a.UnitID)
		}
		return arows.Err()
	})
	return out, dbErr(err, "my meeting")
}
