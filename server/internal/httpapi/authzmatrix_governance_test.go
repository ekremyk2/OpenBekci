package httpapi

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of the governance module (0.2). Every build seeds a meeting
// through plain SQL (govSeed) so each request of the matrix finds its target.

var govSeq atomic.Int64

// govFix is a seeded site with two owners and a meeting.
type govFix struct {
	f        authztest.SiteFix
	org      authztest.Org
	meeting  uuid.UUID
	item     uuid.UUID
	u1, u2   uuid.UUID
	p1, p2   uuid.UUID
	token1   string
	decision uuid.UUID
	att1     uuid.UUID
}

func (g govFix) path(org authztest.Org, suffix string) string { return orgPath(org, suffix) }

// govSeed seeds a site with owners P1 (unit 1) and P2 (unit 2) and a meeting in
// the given status. Draft has no snapshot; called has the call data (tokens,
// notices, poll); in_session adds both owners' attendance, vote counts and an
// accepted decision; closed has P1 present and P2 absent.
func govSeed(t *testing.T, w *authztest.World, org authztest.Org, status string) govFix {
	t.Helper()
	n := govSeq.Add(1)
	f := w.NewSite(t, org)
	g := govFix{f: f, org: org, meeting: id.New(), item: id.New(), u1: f.Unit, u2: w.NewUnit(t, f), p1: w.NewPerson(t, org), p2: w.NewPerson(t, org), token1: fmt.Sprintf("tok1-%d", n)}
	w.NewOccupancy(t, f, g.u1, g.p1, "owner")
	w.NewOccupancy(t, f, g.u2, g.p2, "owner")
	now := time.Now().UTC()
	scheduled, second := now.Add(40*24*time.Hour), now.Add(50*24*time.Hour)
	if status == "in_session" || status == "closed" {
		scheduled, second = now.Add(-time.Hour), now.Add(10*24*time.Hour)
	}
	var snap, rules any
	var calledAt any
	if status != "draft" {
		sn := governance.Snapshot{AsOf: now, VoteWeight: "per_unit", ShareBasis: "equal", UnitCount: 2, TotalVotes: 2, TotalShare: 2, Unrepresented: []string{},
			Owners: []governance.SnapOwner{
				{PersonID: g.p1, DisplayName: "Owner One", Units: []governance.SnapUnit{{UnitID: g.u1, DoorNumber: f.DoorNum, Share: 1}}, Votes: 1, Share: 1},
				{PersonID: g.p2, DisplayName: "Owner Two", Units: []governance.SnapUnit{{UnitID: g.u2, DoorNumber: "B", Share: 1}}, Votes: 1, Share: 1},
			}}
		b, _ := json.Marshal(sn)
		r, _ := json.Marshal(governance.TurkishPreset())
		snap, rules, calledAt = string(b), string(r), now
	}
	w.Exec(t, `INSERT INTO meeting (id, org_id, site_id, kind, scope, scheduled_at, second_call_at, location, status, eligibility_snapshot, settings_snapshot, called_at)
		VALUES ($1, $2, $3, 'general_assembly', 'site', $4, $5, 'Community room', $6, $7::jsonb, $8::jsonb, $9)`, g.meeting, org.ID, f.Site, scheduled, second, status, snap, rules, calledAt)
	w.Exec(t, `INSERT INTO agenda_item (id, org_id, site_id, meeting_id, position, title, will_be_voted) VALUES ($1, $2, $3, $4, 1, 'Budget', true)`, g.item, org.ID, f.Site, g.meeting)
	if status == "draft" {
		return g
	}
	for i, p := range []uuid.UUID{g.p1, g.p2} {
		tok := fmt.Sprintf("tok%d-%d", i+1, n)
		w.Exec(t, `INSERT INTO attendance_token (org_id, site_id, meeting_id, person_id, token) VALUES ($1, $2, $3, $4, $5)`, org.ID, f.Site, g.meeting, p, tok)
		w.Exec(t, `INSERT INTO meeting_notice (id, org_id, site_id, meeting_id, person_id, phase) VALUES ($1, $2, $3, $4, $5, 'call')`, id.New(), org.ID, f.Site, g.meeting, p)
	}
	if status == "called" {
		w.Exec(t, `INSERT INTO poll (org_id, site_id, agenda_item_id, question, opens_at, closes_at) VALUES ($1, $2, $3, 'Agree?', $4, $5)`,
			org.ID, f.Site, g.item, now.Add(-24*time.Hour), now.Add(24*time.Hour))
		return g
	}
	g.att1 = id.New()
	w.Exec(t, `INSERT INTO attendance (id, org_id, site_id, meeting_id, person_id, unit_ids, mode, source, votes, share) VALUES ($1, $2, $3, $4, $5, $6, 'in_person', 'desk', 1, 1)`,
		g.att1, org.ID, f.Site, g.meeting, g.p1, []uuid.UUID{g.u1})
	if status == "in_session" {
		w.Exec(t, `INSERT INTO attendance (id, org_id, site_id, meeting_id, person_id, unit_ids, mode, source, votes, share) VALUES ($1, $2, $3, $4, $5, $6, 'in_person', 'desk', 1, 1)`,
			id.New(), org.ID, f.Site, g.meeting, g.p2, []uuid.UUID{g.u2})
	}
	g.decision = id.New()
	w.Exec(t, `INSERT INTO decision_counter (org_id, site_id, year, last_no) VALUES ($1, $2, 2026, 1)`, org.ID, f.Site)
	w.Exec(t, `INSERT INTO decision (id, org_id, site_id, meeting_id, agenda_item_id, sequence_no, year, text, outcome) VALUES ($1, $2, $3, $4, $5, 1, 2026, 'Budget approved', 'accepted')`,
		g.decision, org.ID, f.Site, g.meeting, g.item)
	w.Exec(t, `UPDATE agenda_item SET decision_id = $3 WHERE org_id = $1 AND id = $2`, org.ID, g.item, g.decision)
	if status == "closed" {
		w.Exec(t, `UPDATE meeting SET minutes_document_id = $3, minutes_file_id = $3 WHERE org_id = $1 AND id = $2`, org.ID, g.meeting, id.New())
	}
	return g
}

func init() {
	ifm := "If-Match"
	idem := func() string { return fmt.Sprintf("matrix-key-%d", govSeq.Add(1)+1000) }
	onMeeting := func(status, method, suffix string, ps []authz.Permission, code int, body string, h ...string) mrow {
		return mrow{perms: ps, status: code, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			g := govSeed(t, w, org, status)
			return jsonReq(method, orgPath(org, "/meetings/"+g.meeting.String()+suffix), body, h...)
		}}
	}
	onItem := func(status, method, suffix string, ps []authz.Permission, code int, body string, h ...string) mrow {
		return mrow{perms: ps, status: code, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			g := govSeed(t, w, org, status)
			return jsonReq(method, orgPath(org, "/agenda-items/"+g.item.String()+suffix), body, h...)
		}}
	}
	read, manage, decide, attend, answer := governance.PermMeetingsRead, governance.PermMeetingsManage, governance.PermDecisionsManage, governance.PermAttendanceManage, governance.PermPollsAnswer
	future := func(d int) string {
		return time.Now().UTC().Add(time.Duration(d) * 24 * time.Hour).Format(time.RFC3339)
	}
	rows := map[string]mrow{
		"governance.getGovernanceSettings": {perms: perms(read), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+"/governance-settings")}
		}},
		"governance.setGovernanceSettings": {perms: perms(manage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			b, _ := json.Marshal(governance.TurkishPreset())
			return jsonReq("PUT", orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+"/governance-settings"), string(b), ifm, `"1"`)
		}},
		"governance.listGovernancePresets": {perms: perms(read), status: 200, build: mget("/v1/orgs/{org_id}/governance-presets")},
		"governance.listMeetings": {perms: perms(read), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/meetings?site_id="+govSeed(t, w, org, "called").f.Site.String())}
		}},
		"governance.createMeeting": {perms: perms(manage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/meetings"), fmt.Sprintf(`{"site_id":%q,"kind":"general_assembly","scheduled_at":%q,"second_call_at":%q,"location":"Hall"}`,
				w.NewSite(t, org).Site, future(30), future(40)))
		}},
		"governance.getMeeting":    onMeeting("called", "GET", "", perms(read), 200, ""),
		"governance.updateMeeting": onMeeting("draft", "PATCH", "", perms(manage), 200, `{"location":"Garden"}`, ifm, `"1"`),
		"governance.deleteMeeting": onMeeting("draft", "DELETE", "", perms(manage), 204, "", ifm, `"1"`),
		"governance.callMeeting":   onMeeting("draft", "POST", ":call", perms(manage), 200, `{}`),
		"governance.startMeeting":  onMeeting("called", "POST", ":start", perms(manage), 200, ``),
		"governance.closeMeeting":  onMeeting("in_session", "POST", ":close", perms(manage), 200, ``),
		"governance.cancelMeeting": onMeeting("draft", "POST", ":cancel", perms(manage), 200, `{"reason":"no quorum expected"}`),

		"governance.getMeetingQuorum":   onMeeting("in_session", "GET", "/quorum", perms(attend), 200, ""),
		"governance.getCallLetter":      onMeeting("called", "GET", "/call-letter", perms(read), 200, ""),
		"governance.getAttendanceSheet": onMeeting("in_session", "GET", "/attendance-sheet", perms(attend), 200, ""),
		"governance.getMyMeeting":       onMeeting("called", "GET", "/me", perms(read), 200, ""),
		"governance.generateMinutes":    onMeeting("in_session", "POST", "/minutes:generate", perms(manage), 200, ``),
		"governance.getMinutes":         onMeeting("closed", "GET", "/minutes", perms(read), 200, ""),
		"governance.listMeetingNotices": onMeeting("called", "GET", "/notices", perms(manage), 200, ""),
		"governance.listAgendaItems":    onMeeting("called", "GET", "/agenda-items", perms(read), 200, ""),
		"governance.createAgendaItem":   onMeeting("draft", "POST", "/agenda-items", perms(manage), 201, `{"title":"Elections","will_be_voted":true}`),
		"governance.listAttendance":     onMeeting("in_session", "GET", "/attendance", perms(attend), 200, ""),

		"governance.updateAgendaItem": onItem("draft", "PATCH", "", perms(manage), 200, `{"title":"Renamed"}`, ifm, `"1"`),
		"governance.deleteAgendaItem": onItem("draft", "DELETE", "", perms(manage), 204, "", ifm, `"1"`),
		"governance.getPoll":          onItem("called", "GET", "/poll", perms(read), 200, ""),
		"governance.getPollResults":   onItem("called", "GET", "/poll/results", perms(answer), 200, ""),
		"governance.listPollAnswers":  onItem("called", "GET", "/poll/answers", perms(manage), 200, ""),
		"governance.getVoteCounts":    onItem("in_session", "GET", "/vote-counts", perms(read), 200, ""),
		"governance.recordVoteCounts": onItem("in_session", "PUT", "/vote-counts", perms(decide), 200, `{"yes_count":2,"no_count":0,"abstain_count":0}`),
		"governance.setPoll": {perms: perms(manage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			g := govSeed(t, w, org, "called")
			return jsonReq("PUT", orgPath(org, "/agenda-items/"+g.item.String()+"/poll"), fmt.Sprintf(`{"question":"Agree?","opens_at":%q,"closes_at":%q}`, future(1), future(5)), ifm, `"1"`)
		}},
		// managers are not voters: the service refuses them with 403 unless they own a unit of the meeting
		"governance.answerPoll": {perms: perms(answer), status: 403, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			g := govSeed(t, w, org, "called")
			return jsonReq("PUT", orgPath(org, "/agenda-items/"+g.item.String()+"/poll/answer"), fmt.Sprintf(`{"unit_id":%q,"answer":"yes"}`, g.u1))
		}},

		"governance.listDecisions": {perms: perms(read), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/decisions?site_id="+govSeed(t, w, org, "closed").f.Site.String())}
		}},
		"governance.createDecision": {perms: perms(decide), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			g := govSeed(t, w, org, "in_session")
			return jsonReq("POST", orgPath(org, "/decisions"), fmt.Sprintf(`{"site_id":%q,"meeting_id":%q,"text":"Noted","outcome":"noted"}`, g.f.Site, g.meeting))
		}},
		"governance.getDecision": {perms: perms(read), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/decisions/"+govSeed(t, w, org, "closed").decision.String())}
		}},
		"governance.updateDecision": {perms: perms(decide), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/decisions/"+govSeed(t, w, org, "closed").decision.String()), `{"concerns_residents":true}`, ifm, `"1"`)
		}},
		"governance.applyDecision": {perms: perms(decide), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			g := govSeed(t, w, org, "closed")
			return jsonReq("POST", orgPath(org, "/decisions/"+g.decision.String()+":apply"),
				fmt.Sprintf(`{"kind":"membership","person_id":%q,"role":"board_member","scope_type":"site","scope_id":%q,"valid_from":"2026-01-01","valid_to":"2027-01-01"}`, g.p1, g.f.Site),
				"Idempotency-Key", idem())
		}},
		"governance.listTermReminders": {perms: perms(manage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/term-reminders?site_id="+w.NewSite(t, org).Site.String())}
		}},
	}
	rows["governance.recordAttendance"] = mrow{perms: perms(attend), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "called")
		return jsonReq("POST", orgPath(org, "/meetings/"+g.meeting.String()+"/attendance"), fmt.Sprintf(`{"person_id":%q,"unit_ids":[%q],"mode":"in_person"}`, g.p1, g.u1))
	}}
	rows["governance.recordMeetingNotice"] = mrow{perms: perms(manage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "called")
		return jsonReq("PUT", orgPath(org, "/meetings/"+g.meeting.String()+"/notices/"+g.p1.String()), fmt.Sprintf(`{"phase":"call","method":"registered_mail","sent_at":%q,"tracking_no":"PTT123"}`, future(0)))
	}}
	rows["governance.recordMeetingNoticesBulk"] = mrow{perms: perms(manage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "called")
		return jsonReq("POST", orgPath(org, "/meetings/"+g.meeting.String()+"/notices:bulk"), fmt.Sprintf(`{"person_ids":[%q,%q],"phase":"call","method":"signed","sent_at":%q}`, g.p1, g.p2, future(0)))
	}}
	rows["governance.reorderAgendaItems"] = mrow{perms: perms(manage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "draft")
		return jsonReq("PUT", orgPath(org, "/meetings/"+g.meeting.String()+"/agenda-items:order"), fmt.Sprintf(`{"item_ids":[%q]}`, g.item))
	}}
	rows["governance.notifyMeetingDecisions"] = mrow{perms: perms(manage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "closed")
		return jsonReq("POST", orgPath(org, "/meetings/"+g.meeting.String()+":notify-decisions?dry_run=true"), `{"channels":["in_app","sms"]}`, "Idempotency-Key", idem())
	}}
	rows["governance.checkInAttendance"] = mrow{perms: perms(attend), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "called")
		return jsonReq("POST", orgPath(org, "/meetings/"+g.meeting.String()+"/attendance:checkin"), fmt.Sprintf(`{"token":%q}`, g.token1))
	}}
	rows["governance.removeAttendance"] = mrow{perms: perms(attend), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		g := govSeed(t, w, org, "in_session")
		return mreq{method: "DELETE", path: orgPath(org, "/meetings/"+g.meeting.String()+"/attendance/"+g.att1.String())}
	}}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
