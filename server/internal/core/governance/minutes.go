package governance

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// MinutesResult points at the filed minutes document.
type MinutesResult struct {
	MeetingID  uuid.UUID `json:"meeting_id"`
	DocumentID uuid.UUID `json:"document_id"`
	FileID     uuid.UUID `json:"file_id"`
}

// minutesData collects what the minutes print: quorum, attendance, every agenda
// item with its numbered decision, dissents included, and decisions taken
// without an agenda item.
func (s *Service) minutesData(ctx context.Context, tx pgx.Tx, m Meeting) (MinutesData, error) {
	q, res, pr, snap, err := s.quorumOf(ctx, tx, m)
	if err != nil {
		return MinutesData{}, err
	}
	si, err := loadSiteInfo(ctx, tx, m.OrgID, m.SiteID)
	if err != nil {
		return MinutesData{}, err
	}
	var attended int
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT person_id) FROM attendance WHERE org_id = $1 AND meeting_id = $2 AND call = $3`, m.OrgID, m.ID, m.heldCall()).Scan(&attended); err != nil {
		return MinutesData{}, err
	}
	d := MinutesData{SiteName: si.Name, Kind: m.Kind, Locale: si.Locale, Loc: si.Loc, ScheduledAt: m.ScheduledAt, SecondCall: m.SecondCallAt, Location: m.Location,
		Call: q.Call, Attended: attended, Eligible: snap.Total(), Present: pr.Capped, Quorum: res}
	decs := map[uuid.UUID]Decision{}
	var loose []Decision
	rows, err := tx.Query(ctx, `SELECT `+decisionColumns+` FROM decision WHERE org_id = $1 AND meeting_id = $2 ORDER BY sequence_no`, m.OrgID, m.ID)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		dec, err := scanDecision(rows)
		if err != nil {
			rows.Close()
			return d, err
		}
		if dec.AgendaItemID != nil {
			decs[*dec.AgendaItemID] = dec
		} else {
			loose = append(loose, dec)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	conv := func(dec Decision) *MinutesDecision {
		md := &MinutesDecision{Year: dec.Year, Seq: dec.SequenceNo, Text: dec.Text, Outcome: dec.Outcome}
		if dec.VoteCount != nil {
			md.HasVotes, md.Yes, md.No, md.Abstain = true, dec.VoteCount.YesCount, dec.VoteCount.NoCount, dec.VoteCount.AbstainCount
		}
		for _, ds := range dec.Dissents {
			name := ""
			if ds.DisplayName != nil {
				name = *ds.DisplayName
			}
			md.Dissents = append(md.Dissents, MinutesDissent{Name: name, Text: ds.Text})
		}
		return md
	}
	irows, err := tx.Query(ctx, `SELECT id, position, title, will_be_voted FROM agenda_item WHERE org_id = $1 AND meeting_id = $2 ORDER BY position`, m.OrgID, m.ID)
	if err != nil {
		return d, err
	}
	pos := 0
	for irows.Next() {
		var id uuid.UUID
		it := MinutesItem{}
		if err := irows.Scan(&id, &it.Position, &it.Title, &it.WillBeVoted); err != nil {
			irows.Close()
			return d, err
		}
		if dec, ok := decs[id]; ok {
			it.Decision = conv(dec)
		}
		pos = it.Position
		d.Items = append(d.Items, it)
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return d, err
	}
	for _, dec := range loose {
		pos++
		d.Items = append(d.Items, MinutesItem{Position: pos, Title: dec.Text, Decision: conv(dec)})
	}
	return d, nil
}

// GenerateMinutes builds the minutes (tutanak) PDF from the meeting's data and
// files it through the DocumentFiler; regenerating replaces the reference. The
// minutes carry numbered decisions for transcription into the karar defteri
// and never claim to be it. It needs meetings.manage.
func (s *Service) GenerateMinutes(ctx context.Context, orgID, meetingID uuid.UUID) (MinutesResult, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return MinutesResult{}, err
	}
	var out MinutesResult
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsManage, orgID, meetingID, true)
		if err != nil {
			return err
		}
		if m.Status != StatusInSession && m.Status != StatusClosed {
			return problem.Conflict("minutes are written for a meeting that took place")
		}
		data, err := s.minutesData(ctx, tx, m)
		if err != nil {
			return err
		}
		pdf, err := Minutes(data).PDF()
		if err != nil {
			return err
		}
		filed, err := s.filer.File(ctx, tx, orgID, Document{Kind: "minutes", FileName: "minutes.pdf", MIME: "application/pdf", Content: pdf, SiteID: m.SiteID, MeetingID: m.ID})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE meeting SET minutes_document_id = $3, minutes_file_id = $4, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, meetingID, filed.DocumentID, filed.FileID); err != nil {
			return err
		}
		out = MinutesResult{MeetingID: meetingID, DocumentID: filed.DocumentID, FileID: filed.FileID}
		diff, err := audit.Diff("meeting", nil, map[string]any{"minutes_document_id": filed.DocumentID})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "meeting.minutes_generate", TargetType: "meeting", TargetID: meetingID, Diff: diff})
	})
	return out, dbErr(err, "minutes")
}

// GetMinutes returns the reference to the filed minutes. It needs meetings.read;
// owners and staff only (tenants see the decisions that concern them instead).
func (s *Service) GetMinutes(ctx context.Context, orgID, meetingID uuid.UUID) (MinutesResult, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return MinutesResult{}, err
	}
	var out MinutesResult
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, sc, err := s.meetingAt(ctx, tx, PermMeetingsRead, orgID, meetingID, false)
		if err != nil {
			return err
		}
		if v := viewerOf(ctx, sc); !v.staff && !v.owner {
			return problem.Forbidden("minutes are shown to owners")
		}
		if m.MinutesDocumentID == nil || m.minutesFileID == nil {
			return problem.NotFound("no minutes were generated for this meeting")
		}
		out = MinutesResult{MeetingID: meetingID, DocumentID: *m.MinutesDocumentID, FileID: *m.minutesFileID}
		return nil
	})
	return out, dbErr(err, "minutes")
}

var _ = errors.New
