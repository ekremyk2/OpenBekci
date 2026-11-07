package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoGovernance = errors.New("governance service not configured")

func (s *apiServer) gv() (*governance.Service, error) {
	if s.governance == nil {
		return nil, problem.Internal(errNoGovernance)
	}
	return s.governance, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *apiServer) GovernanceGetGovernanceSettings(ctx context.Context, req gen.GovernanceGetGovernanceSettingsRequestObject) (gen.GovernanceGetGovernanceSettingsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetSettings(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GovernanceSettings](v)
	return gen.GovernanceGetGovernanceSettings200JSONResponse(out), err
}

func (s *apiServer) GovernanceSetGovernanceSettings(ctx context.Context, req gen.GovernanceSetGovernanceSettingsRequestObject) (gen.GovernanceSetGovernanceSettingsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.Settings](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.PutSettings(ctx, req.OrgId, req.SiteId, ver, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GovernanceSettings](v)
	return gen.GovernanceSetGovernanceSettings200JSONResponse(out), err
}

func (s *apiServer) GovernanceListGovernancePresets(ctx context.Context, req gen.GovernanceListGovernancePresetsRequestObject) (gen.GovernanceListGovernancePresetsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	items, err := g.ListPresets(ctx, req.OrgId, deref(req.Params.CountryCode))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.GovernancePresetList](items, "")
	return gen.GovernanceListGovernancePresets200JSONResponse(out), err
}

func (s *apiServer) GovernanceListMeetings(ctx context.Context, req gen.GovernanceListMeetingsRequestObject) (gen.GovernanceListMeetingsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	f := governance.MeetingFilter{Status: deref(req.Params.Status), Kind: deref(req.Params.Kind), From: req.Params.From, To: req.Params.To}
	items, next, err := g.ListMeetings(ctx, req.OrgId, req.Params.SiteId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.MeetingList](items, next)
	return gen.GovernanceListMeetings200JSONResponse(out), err
}

func (s *apiServer) GovernanceCreateMeeting(ctx context.Context, req gen.GovernanceCreateMeetingRequestObject) (gen.GovernanceCreateMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.MeetingCreate](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.CreateMeeting(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceCreateMeeting201JSONResponse(out), err
}

func (s *apiServer) GovernanceGetMeeting(ctx context.Context, req gen.GovernanceGetMeetingRequestObject) (gen.GovernanceGetMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetMeeting(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceGetMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceUpdateMeeting(ctx context.Context, req gen.GovernanceUpdateMeetingRequestObject) (gen.GovernanceUpdateMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.UpdateMeeting(ctx, req.OrgId, req.MeetingId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceUpdateMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceDeleteMeeting(ctx context.Context, req gen.GovernanceDeleteMeetingRequestObject) (gen.GovernanceDeleteMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := g.DeleteMeeting(ctx, req.OrgId, req.MeetingId, ver); err != nil {
		return nil, err
	}
	return gen.GovernanceDeleteMeeting204Response{}, nil
}

func (s *apiServer) GovernanceCallMeeting(ctx context.Context, req gen.GovernanceCallMeetingRequestObject) (gen.GovernanceCallMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	var in governance.MeetingCall
	if req.Body != nil {
		if in, err = conv[governance.MeetingCall](req.Body); err != nil {
			return nil, err
		}
	}
	v, err := g.CallMeeting(ctx, req.OrgId, req.MeetingId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceCallMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceStartMeeting(ctx context.Context, req gen.GovernanceStartMeetingRequestObject) (gen.GovernanceStartMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.StartMeeting(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceStartMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceCloseMeeting(ctx context.Context, req gen.GovernanceCloseMeetingRequestObject) (gen.GovernanceCloseMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.CloseMeeting(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceCloseMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceCancelMeeting(ctx context.Context, req gen.GovernanceCancelMeetingRequestObject) (gen.GovernanceCancelMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := g.CancelMeeting(ctx, req.OrgId, req.MeetingId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Meeting](v)
	return gen.GovernanceCancelMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceGetMeetingQuorum(ctx context.Context, req gen.GovernanceGetMeetingQuorumRequestObject) (gen.GovernanceGetMeetingQuorumResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetQuorum(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Quorum](v)
	return gen.GovernanceGetMeetingQuorum200JSONResponse(out), err
}

func (s *apiServer) GovernanceGetCallLetter(ctx context.Context, req gen.GovernanceGetCallLetterRequestObject) (gen.GovernanceGetCallLetterResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	pdf, _, err := g.CallLetterPDF(ctx, req.OrgId, req.MeetingId, req.Params.PersonId)
	if err != nil {
		return nil, err
	}
	return gen.GovernanceGetCallLetter200ApplicationpdfResponse{Body: bytes.NewReader(pdf), ContentLength: int64(len(pdf))}, nil
}

func (s *apiServer) GovernanceGetAttendanceSheet(ctx context.Context, req gen.GovernanceGetAttendanceSheetRequestObject) (gen.GovernanceGetAttendanceSheetResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	pdf, _, err := g.AttendanceSheetPDF(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	return gen.GovernanceGetAttendanceSheet200ApplicationpdfResponse{Body: bytes.NewReader(pdf), ContentLength: int64(len(pdf))}, nil
}

func (s *apiServer) GovernanceGetMyMeeting(ctx context.Context, req gen.GovernanceGetMyMeetingRequestObject) (gen.GovernanceGetMyMeetingResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetMyMeeting(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MyMeeting](v)
	return gen.GovernanceGetMyMeeting200JSONResponse(out), err
}

func (s *apiServer) GovernanceGenerateMinutes(ctx context.Context, req gen.GovernanceGenerateMinutesRequestObject) (gen.GovernanceGenerateMinutesResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GenerateMinutes(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MinutesResult](v)
	return gen.GovernanceGenerateMinutes200JSONResponse(out), err
}

func (s *apiServer) GovernanceGetMinutes(ctx context.Context, req gen.GovernanceGetMinutesRequestObject) (gen.GovernanceGetMinutesResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetMinutes(ctx, req.OrgId, req.MeetingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MinutesResult](v)
	return gen.GovernanceGetMinutes200JSONResponse(out), err
}

func (s *apiServer) GovernanceListMeetingNotices(ctx context.Context, req gen.GovernanceListMeetingNoticesRequestObject) (gen.GovernanceListMeetingNoticesResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	f := governance.NoticeFilter{Phase: deref(req.Params.Phase), Status: deref(req.Params.Status), PersonID: req.Params.PersonId}
	items, next, err := g.ListMeetingNotices(ctx, req.OrgId, req.MeetingId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.MeetingNoticeList](items, next)
	return gen.GovernanceListMeetingNotices200JSONResponse(out), err
}

func (s *apiServer) GovernanceRecordMeetingNotice(ctx context.Context, req gen.GovernanceRecordMeetingNoticeRequestObject) (gen.GovernanceRecordMeetingNoticeResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.NoticeRecord](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.RecordMeetingNotice(ctx, req.OrgId, req.MeetingId, req.PersonId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MeetingNotice](v)
	return gen.GovernanceRecordMeetingNotice200JSONResponse(out), err
}

func (s *apiServer) GovernanceRecordMeetingNoticesBulk(ctx context.Context, req gen.GovernanceRecordMeetingNoticesBulkRequestObject) (gen.GovernanceRecordMeetingNoticesBulkResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.NoticeBulk](req.Body)
	if err != nil {
		return nil, err
	}
	n, err := g.RecordMeetingNoticesBulk(ctx, req.OrgId, req.MeetingId, in)
	if err != nil {
		return nil, err
	}
	return gen.GovernanceRecordMeetingNoticesBulk200JSONResponse{Recorded: int64(n)}, nil
}

func (s *apiServer) GovernanceNotifyMeetingDecisions(ctx context.Context, req gen.GovernanceNotifyMeetingDecisionsRequestObject) (gen.GovernanceNotifyMeetingDecisionsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.DecisionNotify](req.Body)
	if err != nil {
		return nil, err
	}
	dry := req.Params.DryRun != nil && bool(*req.Params.DryRun)
	v, err := g.NotifyMeetingDecisions(ctx, req.OrgId, req.MeetingId, in, dry)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DecisionNotifyResult](v)
	return gen.GovernanceNotifyMeetingDecisions200JSONResponse(out), err
}

func (s *apiServer) GovernanceListAgendaItems(ctx context.Context, req gen.GovernanceListAgendaItemsRequestObject) (gen.GovernanceListAgendaItemsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	items, next, err := g.ListAgendaItems(ctx, req.OrgId, req.MeetingId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AgendaItemList](items, next)
	return gen.GovernanceListAgendaItems200JSONResponse(out), err
}

func (s *apiServer) GovernanceCreateAgendaItem(ctx context.Context, req gen.GovernanceCreateAgendaItemRequestObject) (gen.GovernanceCreateAgendaItemResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.AgendaItemCreate](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.CreateAgendaItem(ctx, req.OrgId, req.MeetingId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AgendaItem](v)
	return gen.GovernanceCreateAgendaItem201JSONResponse(out), err
}

func (s *apiServer) GovernanceUpdateAgendaItem(ctx context.Context, req gen.GovernanceUpdateAgendaItemRequestObject) (gen.GovernanceUpdateAgendaItemResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.UpdateAgendaItem(ctx, req.OrgId, req.AgendaItemId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AgendaItem](v)
	return gen.GovernanceUpdateAgendaItem200JSONResponse(out), err
}

func (s *apiServer) GovernanceDeleteAgendaItem(ctx context.Context, req gen.GovernanceDeleteAgendaItemRequestObject) (gen.GovernanceDeleteAgendaItemResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := g.DeleteAgendaItem(ctx, req.OrgId, req.AgendaItemId, ver); err != nil {
		return nil, err
	}
	return gen.GovernanceDeleteAgendaItem204Response{}, nil
}

func (s *apiServer) GovernanceReorderAgendaItems(ctx context.Context, req gen.GovernanceReorderAgendaItemsRequestObject) (gen.GovernanceReorderAgendaItemsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(req.Body.ItemIds))
	copy(ids, req.Body.ItemIds)
	if err := g.ReorderAgendaItems(ctx, req.OrgId, req.MeetingId, ids); err != nil {
		return nil, err
	}
	return gen.GovernanceReorderAgendaItems204Response{}, nil
}

func (s *apiServer) GovernanceGetPoll(ctx context.Context, req gen.GovernanceGetPollRequestObject) (gen.GovernanceGetPollResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetPoll(ctx, req.OrgId, req.AgendaItemId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Poll](v)
	return gen.GovernanceGetPoll200JSONResponse(out), err
}

func (s *apiServer) GovernanceSetPoll(ctx context.Context, req gen.GovernanceSetPollRequestObject) (gen.GovernanceSetPollResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.PollSet](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.SetPoll(ctx, req.OrgId, req.AgendaItemId, ver, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Poll](v)
	return gen.GovernanceSetPoll200JSONResponse(out), err
}

func (s *apiServer) GovernanceAnswerPoll(ctx context.Context, req gen.GovernanceAnswerPollRequestObject) (gen.GovernanceAnswerPollResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := g.AnswerPoll(ctx, req.OrgId, req.AgendaItemId, req.Body.UnitId, string(req.Body.Answer))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.PollAnswer](v)
	return gen.GovernanceAnswerPoll200JSONResponse(out), err
}

func (s *apiServer) GovernanceGetPollResults(ctx context.Context, req gen.GovernanceGetPollResultsRequestObject) (gen.GovernanceGetPollResultsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetPollResults(ctx, req.OrgId, req.AgendaItemId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.PollResults](v)
	return gen.GovernanceGetPollResults200JSONResponse(out), err
}

func (s *apiServer) GovernanceListPollAnswers(ctx context.Context, req gen.GovernanceListPollAnswersRequestObject) (gen.GovernanceListPollAnswersResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	items, next, err := g.ListPollAnswers(ctx, req.OrgId, req.AgendaItemId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.PollAnswerList](items, next)
	return gen.GovernanceListPollAnswers200JSONResponse(out), err
}

func (s *apiServer) GovernanceGetVoteCounts(ctx context.Context, req gen.GovernanceGetVoteCountsRequestObject) (gen.GovernanceGetVoteCountsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetVoteCounts(ctx, req.OrgId, req.AgendaItemId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VoteCount](v)
	return gen.GovernanceGetVoteCounts200JSONResponse(out), err
}

func (s *apiServer) GovernanceRecordVoteCounts(ctx context.Context, req gen.GovernanceRecordVoteCountsRequestObject) (gen.GovernanceRecordVoteCountsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.VoteCountSet](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.RecordVoteCounts(ctx, req.OrgId, req.AgendaItemId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VoteCount](v)
	return gen.GovernanceRecordVoteCounts200JSONResponse(out), err
}

func (s *apiServer) GovernanceListAttendance(ctx context.Context, req gen.GovernanceListAttendanceRequestObject) (gen.GovernanceListAttendanceResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	items, next, err := g.ListAttendance(ctx, req.OrgId, req.MeetingId, deref(req.Params.Mode), deref(req.Params.Source), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AttendanceList](items, next)
	return gen.GovernanceListAttendance200JSONResponse(out), err
}

func (s *apiServer) GovernanceRecordAttendance(ctx context.Context, req gen.GovernanceRecordAttendanceRequestObject) (gen.GovernanceRecordAttendanceResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.AttendanceCreate](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.RecordAttendance(ctx, req.OrgId, req.MeetingId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Attendance](v)
	return gen.GovernanceRecordAttendance201JSONResponse(out), err
}

func (s *apiServer) GovernanceRemoveAttendance(ctx context.Context, req gen.GovernanceRemoveAttendanceRequestObject) (gen.GovernanceRemoveAttendanceResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := g.RemoveAttendance(ctx, req.OrgId, req.MeetingId, req.AttendanceId); err != nil {
		return nil, err
	}
	return gen.GovernanceRemoveAttendance204Response{}, nil
}

func (s *apiServer) GovernanceCheckInAttendance(ctx context.Context, req gen.GovernanceCheckInAttendanceRequestObject) (gen.GovernanceCheckInAttendanceResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := g.CheckIn(ctx, req.OrgId, req.MeetingId, req.Body.Token)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Attendance](v)
	return gen.GovernanceCheckInAttendance201JSONResponse(out), err
}

func (s *apiServer) GovernanceListDecisions(ctx context.Context, req gen.GovernanceListDecisionsRequestObject) (gen.GovernanceListDecisionsResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	f := governance.DecisionFilter{MeetingID: req.Params.MeetingId, Year: req.Params.Year, Outcome: deref(req.Params.Outcome), ConcernsResidents: req.Params.ConcernsResidents, Q: deref(req.Params.Q)}
	items, next, err := g.ListDecisions(ctx, req.OrgId, req.Params.SiteId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.DecisionList](items, next)
	return gen.GovernanceListDecisions200JSONResponse(out), err
}

func (s *apiServer) GovernanceCreateDecision(ctx context.Context, req gen.GovernanceCreateDecisionRequestObject) (gen.GovernanceCreateDecisionResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in, err := conv[governance.DecisionCreate](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.CreateDecision(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Decision](v)
	return gen.GovernanceCreateDecision201JSONResponse(out), err
}

func (s *apiServer) GovernanceGetDecision(ctx context.Context, req gen.GovernanceGetDecisionRequestObject) (gen.GovernanceGetDecisionResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	v, err := g.GetDecision(ctx, req.OrgId, req.DecisionId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Decision](v)
	return gen.GovernanceGetDecision200JSONResponse(out), err
}

func (s *apiServer) GovernanceUpdateDecision(ctx context.Context, req gen.GovernanceUpdateDecisionRequestObject) (gen.GovernanceUpdateDecisionResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := g.UpdateDecision(ctx, req.OrgId, req.DecisionId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Decision](v)
	return gen.GovernanceUpdateDecision200JSONResponse(out), err
}

func (s *apiServer) GovernanceApplyDecision(ctx context.Context, req gen.GovernanceApplyDecisionRequestObject) (gen.GovernanceApplyDecisionResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(req.Body)
	if err != nil {
		return nil, problem.BadRequest("the request body could not be read")
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, problem.BadRequest("the request body could not be read")
	}
	dry := req.Params.DryRun != nil && bool(*req.Params.DryRun)
	v, err := g.ApplyDecision(ctx, req.OrgId, req.DecisionId, in, dry)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DecisionApplyResult](v)
	return gen.GovernanceApplyDecision200JSONResponse(out), err
}

func (s *apiServer) GovernanceListTermReminders(ctx context.Context, req gen.GovernanceListTermRemindersRequestObject) (gen.GovernanceListTermRemindersResponseObject, error) {
	g, err := s.gv()
	if err != nil {
		return nil, err
	}
	items, next, err := g.ListTermReminders(ctx, req.OrgId, req.Params.SiteId, deref(req.Params.Status), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.TermReminderList](items, next)
	return gen.GovernanceListTermReminders200JSONResponse(out), err
}
