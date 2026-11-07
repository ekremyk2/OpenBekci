package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/announcements"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/dashboards"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoOps = errors.New("daily operations module not configured")

func (s *apiServer) tk() (*tickets.Service, error) {
	if s.tickets == nil {
		return nil, problem.Internal(errNoOps)
	}
	return s.tickets, nil
}

func (s *apiServer) an() (*announcements.Service, error) {
	if s.announcements == nil {
		return nil, problem.Internal(errNoOps)
	}
	return s.announcements, nil
}

func (s *apiServer) dc() (*documents.Service, error) {
	if s.documents == nil {
		return nil, problem.Internal(errNoOps)
	}
	return s.documents, nil
}

func (s *apiServer) nt() (*notifications.Service, error) {
	if s.notifications == nil {
		return nil, problem.Internal(errNoOps)
	}
	return s.notifications, nil
}

func (s *apiServer) db() (*dashboards.Service, error) {
	if s.dashboards == nil {
		return nil, problem.Internal(errNoOps)
	}
	return s.dashboards, nil
}

func deref[T any](p *T) T {
	var z T
	if p == nil {
		return z
	}
	return *p
}

// body reads a required JSON body as a Doc.
func bodyDoc(isNil bool, body any) (tenancy.Doc, error) {
	if err := hasBody(isNil); err != nil {
		return nil, err
	}
	return docOf(body)
}

// ---- notifications ----

func (s *apiServer) OperationsListNotifications(ctx context.Context, req gen.OperationsListNotificationsRequestObject) (gen.OperationsListNotificationsResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	items, next, err := n.List(ctx, req.OrgId, notifications.ListFilter{SiteID: req.Params.SiteId, Unread: deref(req.Params.Unread), Type: deref(req.Params.Type)}, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.NotificationList](items, next)
	return gen.OperationsListNotifications200JSONResponse(out), err
}

func (s *apiServer) OperationsGetUnreadCount(ctx context.Context, req gen.OperationsGetUnreadCountRequestObject) (gen.OperationsGetUnreadCountResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	u, err := n.UnreadCount(ctx, req.OrgId, req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnreadCount](u)
	return gen.OperationsGetUnreadCount200JSONResponse(out), err
}

func (s *apiServer) OperationsMarkNotificationRead(ctx context.Context, req gen.OperationsMarkNotificationReadRequestObject) (gen.OperationsMarkNotificationReadResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	v, err := n.MarkRead(ctx, req.OrgId, req.NotificationId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Notification](v)
	return gen.OperationsMarkNotificationRead200JSONResponse(out), err
}

func (s *apiServer) OperationsMarkAllNotificationsRead(ctx context.Context, req gen.OperationsMarkAllNotificationsReadRequestObject) (gen.OperationsMarkAllNotificationsReadResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	var site *uuid.UUID
	if req.Body != nil {
		site = req.Body.SiteId
	}
	if err := n.MarkAllRead(ctx, req.OrgId, site); err != nil {
		return nil, err
	}
	return gen.OperationsMarkAllNotificationsRead204Response{}, nil
}

func (s *apiServer) OperationsGetNotificationMutes(ctx context.Context, req gen.OperationsGetNotificationMutesRequestObject) (gen.OperationsGetNotificationMutesResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	m, err := n.GetMutes(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.NotificationMutes](m)
	return gen.OperationsGetNotificationMutes200JSONResponse(out), err
}

func (s *apiServer) OperationsSetNotificationMutes(ctx context.Context, req gen.OperationsSetNotificationMutesRequestObject) (gen.OperationsSetNotificationMutesResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	m, err := n.SetMutes(ctx, req.OrgId, req.Body.MutedTypes)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.NotificationMutes](m)
	return gen.OperationsSetNotificationMutes200JSONResponse(out), err
}

func defaultsResponse(d notifications.Defaults) (gen.NotificationDefaults, error) {
	return conv[gen.NotificationDefaults](d)
}

func (s *apiServer) OperationsGetNotificationDefaults(ctx context.Context, req gen.OperationsGetNotificationDefaultsRequestObject) (gen.OperationsGetNotificationDefaultsResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	d, err := n.GetDefaults(ctx, req.OrgId, site)
	if err != nil {
		return nil, err
	}
	out, err := defaultsResponse(d)
	return gen.OperationsGetNotificationDefaults200JSONResponse(out), err
}

func (s *apiServer) OperationsSetNotificationDefaults(ctx context.Context, req gen.OperationsSetNotificationDefaultsRequestObject) (gen.OperationsSetNotificationDefaultsResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	in := make([]notifications.DefaultInput, len(req.Body.Defaults))
	for i, d := range req.Body.Defaults {
		in[i] = notifications.DefaultInput{Type: d.Type, Channels: channelStrings(d.Channels)}
	}
	d, err := n.SetDefaults(ctx, req.OrgId, site, ver, in)
	if err != nil {
		return nil, err
	}
	out, err := defaultsResponse(d)
	return gen.OperationsSetNotificationDefaults200JSONResponse(out), err
}

func channelStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, c := range in {
		out[i] = string(c)
	}
	return out
}

func (s *apiServer) OperationsGetSmsUsage(ctx context.Context, req gen.OperationsGetSmsUsageRequestObject) (gen.OperationsGetSmsUsageResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	u, err := n.GetSMSUsage(ctx, req.OrgId, site, deref(req.Params.Month))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SmsUsage](u)
	return gen.OperationsGetSmsUsage200JSONResponse(out), err
}

// ---- tickets ----

func (s *apiServer) OperationsGetTicketWorkflow(ctx context.Context, req gen.OperationsGetTicketWorkflowRequestObject) (gen.OperationsGetTicketWorkflowResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	w, err := t.GetWorkflow(ctx, req.OrgId, site)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketWorkflow](w)
	return gen.OperationsGetTicketWorkflow200JSONResponse(out), err
}

type workflowBody struct {
	Statuses []struct {
		ID       *uuid.UUID `json:"id"`
		Name     string     `json:"name"`
		Semantic string     `json:"semantic"`
	} `json:"statuses"`
	Replacements map[string]uuid.UUID `json:"replacements"`
}

func replacementsOf(m map[string]uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	for k, v := range m {
		id, err := uuid.Parse(k)
		if err != nil {
			return nil, problem.Validation(problem.Field{Field: "replacements." + k, Code: "invalid_format"})
		}
		out[id] = v
	}
	return out, nil
}

func (s *apiServer) OperationsUpdateTicketWorkflow(ctx context.Context, req gen.OperationsUpdateTicketWorkflowRequestObject) (gen.OperationsUpdateTicketWorkflowResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b, err := conv[workflowBody](req.Body)
	if err != nil {
		return nil, err
	}
	in := make([]tickets.StatusInput, len(b.Statuses))
	for i, st := range b.Statuses {
		in[i] = tickets.StatusInput{ID: st.ID, Name: st.Name, Semantic: st.Semantic}
	}
	repl, err := replacementsOf(b.Replacements)
	if err != nil {
		return nil, err
	}
	w, err := t.UpdateWorkflow(ctx, req.OrgId, site, ver, in, repl)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketWorkflow](w)
	return gen.OperationsUpdateTicketWorkflow200JSONResponse(out), err
}

func (s *apiServer) OperationsRestoreTicketWorkflow(ctx context.Context, req gen.OperationsRestoreTicketWorkflowRequestObject) (gen.OperationsRestoreTicketWorkflowResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	var m map[string]uuid.UUID
	if req.Body != nil && req.Body.Replacements != nil {
		if m, err = conv[map[string]uuid.UUID](*req.Body.Replacements); err != nil {
			return nil, err
		}
	}
	repl, err := replacementsOf(m)
	if err != nil {
		return nil, err
	}
	w, err := t.RestoreDefaultWorkflow(ctx, req.OrgId, site, ver, repl)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketWorkflow](w)
	return gen.OperationsRestoreTicketWorkflow200JSONResponse(out), err
}

func (s *apiServer) OperationsListTicketCategories(ctx context.Context, req gen.OperationsListTicketCategoriesRequestObject) (gen.OperationsListTicketCategoriesResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListCategories(ctx, req.OrgId, site, string(deref(req.Params.Archived)), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.TicketCategoryList](items, next)
	return gen.OperationsListTicketCategories200JSONResponse(out), err
}

func (s *apiServer) OperationsCreateTicketCategory(ctx context.Context, req gen.OperationsCreateTicketCategoryRequestObject) (gen.OperationsCreateTicketCategoryResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	c, err := t.CreateCategory(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketCategory](c)
	return gen.OperationsCreateTicketCategory201JSONResponse(out), err
}

func (s *apiServer) OperationsGetTicketCategory(ctx context.Context, req gen.OperationsGetTicketCategoryRequestObject) (gen.OperationsGetTicketCategoryResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	c, err := t.GetCategory(ctx, req.OrgId, req.CategoryId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketCategory](c)
	return gen.OperationsGetTicketCategory200JSONResponse(out), err
}

func (s *apiServer) OperationsUpdateTicketCategory(ctx context.Context, req gen.OperationsUpdateTicketCategoryRequestObject) (gen.OperationsUpdateTicketCategoryResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	c, err := t.UpdateCategory(ctx, req.OrgId, req.CategoryId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketCategory](c)
	return gen.OperationsUpdateTicketCategory200JSONResponse(out), err
}

func (s *apiServer) OperationsListTickets(ctx context.Context, req gen.OperationsListTicketsRequestObject) (gen.OperationsListTicketsResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := t.ListTickets(ctx, req.OrgId, tickets.Filter{SiteID: site, CategoryID: p.CategoryId, StatusID: p.StatusId, Semantic: deref(p.Semantic),
		AssigneePersonID: p.AssigneePersonId, AssigneeVendorID: p.AssigneeVendorId, UnitID: p.UnitId, Visibility: deref(p.Visibility), ReporterPersonID: p.ReporterPersonId,
		Mine: deref(p.Mine), Queue: deref(p.Queue), Q: deref(p.Q)}, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.TicketList](items, next)
	return gen.OperationsListTickets200JSONResponse(out), err
}

func (s *apiServer) OperationsCreateTicket(ctx context.Context, req gen.OperationsCreateTicketRequestObject) (gen.OperationsCreateTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateTicket(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Ticket](v)
	return gen.OperationsCreateTicket201JSONResponse(out), err
}

func (s *apiServer) OperationsGetTicket(ctx context.Context, req gen.OperationsGetTicketRequestObject) (gen.OperationsGetTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	v, err := t.GetTicket(ctx, req.OrgId, req.TicketId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Ticket](v)
	return gen.OperationsGetTicket200JSONResponse(out), err
}

func (s *apiServer) OperationsUpdateTicket(ctx context.Context, req gen.OperationsUpdateTicketRequestObject) (gen.OperationsUpdateTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := t.UpdateTicket(ctx, req.OrgId, req.TicketId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Ticket](v)
	return gen.OperationsUpdateTicket200JSONResponse(out), err
}

func (s *apiServer) OperationsTransitionTicket(ctx context.Context, req gen.OperationsTransitionTicketRequestObject) (gen.OperationsTransitionTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	v, err := t.Transition(ctx, req.OrgId, req.TicketId, tickets.TransitionInput{StatusID: b.StatusId, Comment: deref(b.Comment), CommentVisibility: string(deref(b.CommentVisibility))})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Ticket](v)
	return gen.OperationsTransitionTicket200JSONResponse(out), err
}

func (s *apiServer) OperationsMergeTicket(ctx context.Context, req gen.OperationsMergeTicketRequestObject) (gen.OperationsMergeTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := t.Merge(ctx, req.OrgId, req.TicketId, req.Body.IntoTicketId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Ticket](v)
	return gen.OperationsMergeTicket200JSONResponse(out), err
}

func (s *apiServer) OperationsVoteTicket(ctx context.Context, req gen.OperationsVoteTicketRequestObject) (gen.OperationsVoteTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	v, err := t.SetVote(ctx, req.OrgId, req.TicketId, true)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketVote](v)
	return gen.OperationsVoteTicket200JSONResponse(out), err
}

func (s *apiServer) OperationsUnvoteTicket(ctx context.Context, req gen.OperationsUnvoteTicketRequestObject) (gen.OperationsUnvoteTicketResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	v, err := t.SetVote(ctx, req.OrgId, req.TicketId, false)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketVote](v)
	return gen.OperationsUnvoteTicket200JSONResponse(out), err
}

func (s *apiServer) OperationsListTicketComments(ctx context.Context, req gen.OperationsListTicketCommentsRequestObject) (gen.OperationsListTicketCommentsResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListComments(ctx, req.OrgId, req.TicketId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.TicketCommentList](items, next)
	return gen.OperationsListTicketComments200JSONResponse(out), err
}

func (s *apiServer) OperationsCreateTicketComment(ctx context.Context, req gen.OperationsCreateTicketCommentRequestObject) (gen.OperationsCreateTicketCommentResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	c, err := t.CreateComment(ctx, req.OrgId, req.TicketId, b.Body, string(deref(b.Visibility)), deref(b.AttachmentFileIds))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketComment](c)
	return gen.OperationsCreateTicketComment201JSONResponse(out), err
}

func (s *apiServer) OperationsCreateTicketAttachment(ctx context.Context, req gen.OperationsCreateTicketAttachmentRequestObject) (gen.OperationsCreateTicketAttachmentResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	a, err := t.CreateAttachment(ctx, req.OrgId, req.TicketId, req.Body.FileId, req.Body.CommentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.TicketAttachment](a)
	return gen.OperationsCreateTicketAttachment201JSONResponse(out), err
}

func (s *apiServer) OperationsDeleteTicketAttachment(ctx context.Context, req gen.OperationsDeleteTicketAttachmentRequestObject) (gen.OperationsDeleteTicketAttachmentResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	if err := t.DeleteAttachment(ctx, req.OrgId, req.TicketId, req.AttachmentId); err != nil {
		return nil, err
	}
	return gen.OperationsDeleteTicketAttachment204Response{}, nil
}

func (s *apiServer) OperationsListTicketHistory(ctx context.Context, req gen.OperationsListTicketHistoryRequestObject) (gen.OperationsListTicketHistoryResponseObject, error) {
	t, err := s.tk()
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListHistory(ctx, req.OrgId, req.TicketId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.TicketStatusChangeList](items, next)
	return gen.OperationsListTicketHistory200JSONResponse(out), err
}

// ---- announcements ----

func (s *apiServer) OperationsListAnnouncements(ctx context.Context, req gen.OperationsListAnnouncementsRequestObject) (gen.OperationsListAnnouncementsResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := announcements.Filter{SiteID: site, Status: deref(p.Status), Pinned: p.Pinned, Unread: deref(p.Unread)}
	if p.From != nil {
		t := p.From.Time
		f.From = &t
	}
	if p.To != nil {
		t := p.To.AddDate(0, 0, 1) // inclusive end date
		f.To = &t
	}
	items, next, err := a.List(ctx, req.OrgId, f, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AnnouncementList](items, next)
	return gen.OperationsListAnnouncements200JSONResponse(out), err
}

func (s *apiServer) OperationsCreateAnnouncement(ctx context.Context, req gen.OperationsCreateAnnouncementRequestObject) (gen.OperationsCreateAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	v, err := a.Create(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Announcement](v)
	return gen.OperationsCreateAnnouncement201JSONResponse(out), err
}

func (s *apiServer) OperationsGetAnnouncement(ctx context.Context, req gen.OperationsGetAnnouncementRequestObject) (gen.OperationsGetAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	v, err := a.Get(ctx, req.OrgId, req.AnnouncementId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Announcement](v)
	return gen.OperationsGetAnnouncement200JSONResponse(out), err
}

func (s *apiServer) OperationsUpdateAnnouncement(ctx context.Context, req gen.OperationsUpdateAnnouncementRequestObject) (gen.OperationsUpdateAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := a.Update(ctx, req.OrgId, req.AnnouncementId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Announcement](v)
	return gen.OperationsUpdateAnnouncement200JSONResponse(out), err
}

func (s *apiServer) OperationsDeleteAnnouncement(ctx context.Context, req gen.OperationsDeleteAnnouncementRequestObject) (gen.OperationsDeleteAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.Delete(ctx, req.OrgId, req.AnnouncementId, ver); err != nil {
		return nil, err
	}
	return gen.OperationsDeleteAnnouncement204Response{}, nil
}

func (s *apiServer) OperationsPreviewAnnouncement(ctx context.Context, req gen.OperationsPreviewAnnouncementRequestObject) (gen.OperationsPreviewAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	aud, err := conv[announcements.Audience](b.Audience)
	if err != nil {
		return nil, err
	}
	v, err := a.PreviewAnnouncement(ctx, req.OrgId, b.SiteId, aud, channelStrings(b.Channels))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AnnouncementPreview](v)
	return gen.OperationsPreviewAnnouncement200JSONResponse(out), err
}

func (s *apiServer) OperationsPublishAnnouncement(ctx context.Context, req gen.OperationsPublishAnnouncementRequestObject) (gen.OperationsPublishAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	var override []string
	if req.Body != nil && req.Body.Channels != nil {
		override = channelStrings(*req.Body.Channels)
	}
	v, err := a.Publish(ctx, req.OrgId, req.AnnouncementId, override)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Announcement](v)
	return gen.OperationsPublishAnnouncement200JSONResponse(out), err
}

func (s *apiServer) OperationsArchiveAnnouncement(ctx context.Context, req gen.OperationsArchiveAnnouncementRequestObject) (gen.OperationsArchiveAnnouncementResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	v, err := a.Archive(ctx, req.OrgId, req.AnnouncementId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Announcement](v)
	return gen.OperationsArchiveAnnouncement200JSONResponse(out), err
}

func (s *apiServer) OperationsListAnnouncementReads(ctx context.Context, req gen.OperationsListAnnouncementReadsRequestObject) (gen.OperationsListAnnouncementReadsResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	r, err := a.ListReads(ctx, req.OrgId, req.AnnouncementId, req.Params.Read, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	var nc any
	if r.Next != "" {
		nc = r.Next
	}
	out, err := conv[gen.AnnouncementReadList](map[string]any{"items": r.Items, "next_cursor": nc, "audience_count": r.AudienceCount, "read_count": r.ReadCount})
	return gen.OperationsListAnnouncementReads200JSONResponse(out), err
}

func (s *apiServer) OperationsMarkAnnouncementRead(ctx context.Context, req gen.OperationsMarkAnnouncementReadRequestObject) (gen.OperationsMarkAnnouncementReadResponseObject, error) {
	a, err := s.an()
	if err != nil {
		return nil, err
	}
	if err := a.MarkRead(ctx, req.OrgId, req.AnnouncementId); err != nil {
		return nil, err
	}
	return gen.OperationsMarkAnnouncementRead204Response{}, nil
}

// ---- documents ----

func (s *apiServer) OperationsListFolders(ctx context.Context, req gen.OperationsListFoldersRequestObject) (gen.OperationsListFoldersResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := d.ListFolders(ctx, req.OrgId, site, p.ParentId, deref(p.Kind), p.UnitId, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.FolderList](items, next)
	return gen.OperationsListFolders200JSONResponse(out), err
}

func (s *apiServer) OperationsCreateFolder(ctx context.Context, req gen.OperationsCreateFolderRequestObject) (gen.OperationsCreateFolderResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	doc, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	v, err := d.CreateFolder(ctx, req.OrgId, doc)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Folder](v)
	return gen.OperationsCreateFolder201JSONResponse(out), err
}

func (s *apiServer) OperationsGetFolder(ctx context.Context, req gen.OperationsGetFolderRequestObject) (gen.OperationsGetFolderResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	v, err := d.GetFolder(ctx, req.OrgId, req.FolderId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Folder](v)
	return gen.OperationsGetFolder200JSONResponse(out), err
}

func (s *apiServer) OperationsUpdateFolder(ctx context.Context, req gen.OperationsUpdateFolderRequestObject) (gen.OperationsUpdateFolderResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	doc, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := d.UpdateFolder(ctx, req.OrgId, req.FolderId, ver, doc)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Folder](v)
	return gen.OperationsUpdateFolder200JSONResponse(out), err
}

func (s *apiServer) OperationsDeleteFolder(ctx context.Context, req gen.OperationsDeleteFolderRequestObject) (gen.OperationsDeleteFolderResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := d.DeleteFolder(ctx, req.OrgId, req.FolderId, ver); err != nil {
		return nil, err
	}
	return gen.OperationsDeleteFolder204Response{}, nil
}

func (s *apiServer) OperationsListDocuments(ctx context.Context, req gen.OperationsListDocumentsRequestObject) (gen.OperationsListDocumentsResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	site, err := needSite(req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := d.ListDocuments(ctx, req.OrgId, documents.DocFilter{SiteID: site, FolderID: p.FolderId, Source: deref(p.Source), Q: deref(p.Q)}, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.DocumentList](items, next)
	return gen.OperationsListDocuments200JSONResponse(out), err
}

func (s *apiServer) OperationsCreateDocument(ctx context.Context, req gen.OperationsCreateDocumentRequestObject) (gen.OperationsCreateDocumentResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	doc, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	v, err := d.CreateDocument(ctx, req.OrgId, doc)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Document](v)
	return gen.OperationsCreateDocument201JSONResponse(out), err
}

func (s *apiServer) OperationsGetDocument(ctx context.Context, req gen.OperationsGetDocumentRequestObject) (gen.OperationsGetDocumentResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	v, err := d.GetDocument(ctx, req.OrgId, req.DocumentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Document](v)
	return gen.OperationsGetDocument200JSONResponse(out), err
}

func (s *apiServer) OperationsUpdateDocument(ctx context.Context, req gen.OperationsUpdateDocumentRequestObject) (gen.OperationsUpdateDocumentResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	doc, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := d.UpdateDocument(ctx, req.OrgId, req.DocumentId, ver, doc)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Document](v)
	return gen.OperationsUpdateDocument200JSONResponse(out), err
}

func (s *apiServer) OperationsDeleteDocument(ctx context.Context, req gen.OperationsDeleteDocumentRequestObject) (gen.OperationsDeleteDocumentResponseObject, error) {
	d, err := s.dc()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := d.DeleteDocument(ctx, req.OrgId, req.DocumentId, ver); err != nil {
		return nil, err
	}
	return gen.OperationsDeleteDocument204Response{}, nil
}

// ---- dashboards and codes ----

func (s *apiServer) DashboardsGetSiteDashboard(ctx context.Context, req gen.DashboardsGetSiteDashboardRequestObject) (gen.DashboardsGetSiteDashboardResponseObject, error) {
	d, err := s.db()
	if err != nil {
		return nil, err
	}
	v, err := d.Site(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteDashboard](v)
	return gen.DashboardsGetSiteDashboard200JSONResponse(out), err
}

func (s *apiServer) DashboardsGetStaffDashboard(ctx context.Context, req gen.DashboardsGetStaffDashboardRequestObject) (gen.DashboardsGetStaffDashboardResponseObject, error) {
	d, err := s.db()
	if err != nil {
		return nil, err
	}
	v, err := d.Staff(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.StaffDashboard](v)
	return gen.DashboardsGetStaffDashboard200JSONResponse(out), err
}

func (s *apiServer) DashboardsGetPortfolioDashboard(ctx context.Context, req gen.DashboardsGetPortfolioDashboardRequestObject) (gen.DashboardsGetPortfolioDashboardResponseObject, error) {
	d, err := s.db()
	if err != nil {
		return nil, err
	}
	v, err := d.Portfolio(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.PortfolioDashboard](v)
	return gen.DashboardsGetPortfolioDashboard200JSONResponse(out), err
}

func (s *apiServer) DashboardsGetMyDashboard(ctx context.Context, req gen.DashboardsGetMyDashboardRequestObject) (gen.DashboardsGetMyDashboardResponseObject, error) {
	d, err := s.db()
	if err != nil {
		return nil, err
	}
	v, err := d.Me(ctx, req.OrgId, req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MeDashboard](v)
	return gen.DashboardsGetMyDashboard200JSONResponse(out), err
}

func (s *apiServer) CodesListCodes(ctx context.Context, req gen.CodesListCodesRequestObject) (gen.CodesListCodesResponseObject, error) {
	if s.codes == nil {
		return nil, problem.Internal(errNoOps)
	}
	v, err := s.codes.List(ctx, req.OrgId, deref(req.Params.Locale), req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.OrgCodes](v)
	return gen.CodesListCodes200JSONResponse(out), err
}

var _ = codes.PermRead

// needSite reads the site_id query parameter every site-bound listing requires.
func needSite(p *gen.SiteIdRequired) (uuid.UUID, error) {
	if p == nil {
		return uuid.Nil, problem.Validation(problem.Field{Field: "site_id", Code: "required"})
	}
	return *p, nil
}
