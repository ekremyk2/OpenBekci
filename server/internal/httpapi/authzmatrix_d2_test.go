package httpapi

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/announcements"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/dashboards"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of the daily-operations modules. Every build seeds what it needs
// afresh, so the repeated mutating calls of the matrix always find their target.

var d2Seq atomic.Int64

// d2Site is a site with the ticket workflow, a category, and ids for the rest.
type d2Site struct {
	authztest.SiteFix
	Open, Done, Category uuid.UUID
	Staff                uuid.UUID // the person of the staff actor
}

func newD2Site(w *authztest.World, org authztest.Org, t *testing.T) d2Site {
	t.Helper()
	f := w.NewSite(t, org)
	s := d2Site{SiteFix: f, Open: id.New(), Done: id.New(), Category: id.New(), Staff: w.Actor("staff").PersonID}
	w.Exec(t, `INSERT INTO ticket_workflow (org_id, site_id) VALUES ($1, $2)`, org.ID, f.Site)
	w.Exec(t, `INSERT INTO ticket_status (id, org_id, site_id, name, semantic, position, key) VALUES ($1, $2, $3, 'Open', 'open_like', 1, 'open')`, s.Open, org.ID, f.Site)
	w.Exec(t, `INSERT INTO ticket_status (id, org_id, site_id, name, semantic, position, key) VALUES ($1, $2, $3, 'Done', 'closed_like', 2, 'done')`, s.Done, org.ID, f.Site)
	w.Exec(t, `INSERT INTO ticket_category (id, org_id, site_id, name, default_visibility, position) VALUES ($1, $2, $3, 'Plumbing', 'common', 1)`, s.Category, org.ID, f.Site)
	return s
}

// ticket seeds a common ticket assigned to the staff actor, so every role that
// may read or work on tickets reaches it.
func (s d2Site) ticket(w *authztest.World, t *testing.T) uuid.UUID {
	t.Helper()
	tid := id.New()
	w.Exec(t, `INSERT INTO ticket (id, org_id, site_id, reporter_person_id, category_id, title, description, status_id, visibility, assignee_person_id)
		VALUES ($1, $2, $3, $4, $5, 'Leak', 'in the hall', $6, 'common', (SELECT id FROM person WHERE org_id = $2 AND id = $7))`, tid, s.Org.ID, s.Site, w.NewPerson(t, s.Org), s.Category, s.Open, s.Staff)
	return tid
}

func (s d2Site) file(w *authztest.World, t *testing.T) uuid.UUID {
	t.Helper()
	fid := id.New()
	w.Exec(t, `INSERT INTO file (id, org_id, site_id, key, name, size, mime, sha256, status, retention_until)
		VALUES ($1, $2, $3, $4, 'matrix.pdf', 10, 'application/pdf', $5, 'ready', now() + interval '7 days')`, fid, s.Org.ID, s.Site, "matrix/"+fid.String(), strings.Repeat("a", 64))
	return fid
}

// announcement seeds an announcement of status, addressed to every actor.
func (s d2Site) announcement(w *authztest.World, t *testing.T, status string) uuid.UUID {
	t.Helper()
	aid := id.New()
	w.Exec(t, `INSERT INTO announcement (id, org_id, site_id, title, body_md, status, audience, channels, published_at)
		VALUES ($1, $2, $3, 'Water cut', 'on Saturday', $4, '{"kind":"site"}', ARRAY['in_app'], CASE WHEN $4 = 'published' THEN now() END)`, aid, s.Org.ID, s.Site, status)
	if status == "published" {
		for _, a := range w.Actors {
			if a.PersonID != uuid.Nil {
				w.Exec(t, `INSERT INTO announcement_recipient (org_id, announcement_id, person_id) SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM person WHERE org_id = $1 AND id = $3)`, s.Org.ID, aid, a.PersonID)
			}
		}
	}
	return aid
}

func (s d2Site) folder(w *authztest.World, t *testing.T) uuid.UUID {
	t.Helper()
	fid := id.New()
	w.Exec(t, `INSERT INTO folder (id, org_id, site_id, name, visibility_level) VALUES ($1, $2, $3, $4, 'board')`, fid, s.Org.ID, s.Site, fmt.Sprintf("Plans %d", d2Seq.Add(1)))
	return fid
}

func (s d2Site) document(w *authztest.World, t *testing.T) uuid.UUID {
	t.Helper()
	did, fid := id.New(), s.file(w, t)
	w.Exec(t, `UPDATE file SET scope_type = 'document', scope_id = $2, retention_until = NULL WHERE id = $1`, fid, did)
	w.Exec(t, `INSERT INTO document (id, org_id, site_id, folder_id, title, file_id) VALUES ($1, $2, $3, $4, 'Management plan', $5)`, did, s.Org.ID, s.Site, s.folder(w, t), fid)
	return did
}

func init() {
	ifm := "If-Match"
	q := func(org authztest.Org, path string, site uuid.UUID, more string) string {
		return orgPath(org, path) + "?site_id=" + site.String() + more
	}
	// op builds a row from a function that gets the seeded site.
	op := func(ps []authz.Permission, status int, mk func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mk(w, org, newD2Site(w, org, t), t)
		}}
	}
	// 404 for the roles that hold the permission but did not upload the (generated) file
	notOwnFile := func(byActor map[string]int) func(actor, target string) (int, bool) {
		return func(actor, target string) (int, bool) {
			if st, ok := byActor[actor]; ok && target == "own" {
				return st, true
			}
			return 0, false
		}
	}
	read := tickets.AnyView
	rows := map[string]mrow{
		// notifications
		"operations.listNotifications": {perms: perms(notifications.PermRead), status: 200, build: mget("/v1/orgs/{org_id}/me/notifications")},
		"operations.getUnreadCount":    {perms: perms(notifications.PermRead), status: 200, build: mget("/v1/orgs/{org_id}/me/notifications/unread-count")},
		"operations.getNotification": {perms: perms(notifications.PermRead), status: 404, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/me/notifications/"+uuid.NewString())}
		}},
		"operations.markNotificationRead": {perms: perms(notifications.PermRead), status: 404, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return mreq{method: "POST", path: orgPath(org, "/me/notifications/"+uuid.NewString()+":read")}
		}},
		"operations.markAllNotificationsRead": {perms: perms(notifications.PermRead), status: 204, build: fixed("POST", "/v1/orgs/{org_id}/me/notifications:read-all", "")},
		"operations.getNotificationMutes":     {perms: perms(notifications.PermRead), status: 200, build: mget("/v1/orgs/{org_id}/me/notification-mutes")},
		"operations.setNotificationMutes":     {perms: perms(notifications.PermRead), status: 200, build: fixed("PUT", "/v1/orgs/{org_id}/me/notification-mutes", `{"muted_types":["ticket.updated"]}`)},
		"operations.getNotificationDefaults": op(perms(notifications.PermManage), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: q(org, "/notification-defaults", s.Site, "")}
		}),
		"operations.setNotificationDefaults": op(perms(notifications.PermManage), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("PUT", q(org, "/notification-defaults", s.Site, ""), `{"defaults":[{"type":"ticket.updated","channels":["in_app","email"]}]}`, ifm, `"1"`)
		}),
		"operations.getSmsUsage": op(perms(notifications.PermManage), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: q(org, "/sms-usage", s.Site, "")}
		}),

		// tickets
		"operations.getTicketWorkflow": op(perms(read), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: q(org, "/ticket-workflow", s.Site, "")}
		}),
		"operations.updateTicketWorkflow": op(perms(tickets.PermManage), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("PUT", q(org, "/ticket-workflow", s.Site, ""), fmt.Sprintf(`{"statuses":[{"id":%q,"name":"Open","semantic":"open_like"},{"name":"Waiting for parts","semantic":"open_like"},{"id":%q,"name":"Done","semantic":"closed_like"}]}`, s.Open, s.Done), ifm, `"1"`)
		}),
		"operations.restoreTicketWorkflow": op(perms(tickets.PermManage), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("POST", q(org, "/ticket-workflow:restore-default", s.Site, ""), ``, ifm, `"1"`)
		}),
		"operations.listTicketCategories": op(perms(read), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: q(org, "/ticket-categories", s.Site, "")}
		}),
		"operations.createTicketCategory": op(perms(tickets.PermManage), 201, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/ticket-categories"), fmt.Sprintf(`{"site_id":%q,"name":"Parking","default_visibility":"common"}`, s.Site))
		}),
		"operations.getTicketCategory": op(perms(read), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/ticket-categories/"+s.Category.String())}
		}),
		"operations.updateTicketCategory": op(perms(tickets.PermManage), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/ticket-categories/"+s.Category.String()), `{"name":"Heating"}`, ifm, `"1"`)
		}),
		"operations.listTickets": op(perms(read), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: q(org, "/tickets", s.Site, "")}
		}),
		"operations.createTicket": op(perms(tickets.PermOpen), 201, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Broken lamp","description":"entrance","visibility":"common"}`, s.Site, s.Category))
		}),
		"operations.getTicket": op(perms(read), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/tickets/"+s.ticket(w, t).String())}
		}),
		"operations.updateTicket": op(perms(tickets.PermManage), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/tickets/"+s.ticket(w, t).String()), `{"title":"Leak in the hall","assignee_person_id":null}`, ifm, `"1"`)
		}),
		"operations.transitionTicket": op(perms(authz.AnyOf(tickets.PermManage, tickets.PermWork)), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/tickets/"+s.ticket(w, t).String()+":transition"), fmt.Sprintf(`{"status_id":%q,"comment":"fixed"}`, s.Done))
		}),
		"operations.mergeTicket": op(perms(tickets.PermManage), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/tickets/"+s.ticket(w, t).String()+":merge"), fmt.Sprintf(`{"into_ticket_id":%q}`, s.ticket(w, t)))
		}),
		"operations.voteTicket": op(perms(tickets.PermOpen), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "PUT", path: orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/vote")}
		}),
		"operations.unvoteTicket": op(perms(tickets.PermOpen), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "DELETE", path: orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/vote")}
		}),
		"operations.listTicketComments": op(perms(read), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/comments")}
		}),
		"operations.createTicketComment": op(perms(authz.AnyOf(tickets.PermOpen, tickets.PermWork)), 201, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/comments"), `{"body":"Plumber called"}`)
		}),
		"operations.listTicketHistory": op(perms(read), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/history")}
		}),
		"operations.createTicketAttachment": {perms: perms(tickets.PermOpen), status: 201, override: notOwnFile(map[string]int{"board_member": 404, "staff": 403}),
			build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
				s := newD2Site(w, org, t)
				return jsonReq("POST", orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/attachments"), fmt.Sprintf(`{"file_id":%q}`, s.file(w, t)))
			}},
		"operations.deleteTicketAttachment": {perms: perms(tickets.PermOpen), status: 404, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := newD2Site(w, org, t)
			return mreq{method: "DELETE", path: orgPath(org, "/tickets/"+s.ticket(w, t).String()+"/attachments/"+uuid.NewString())}
		}},

		// announcements
		"operations.listAnnouncements": op(perms(announcements.AnyView), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			s.announcement(w, t, "published")
			return mreq{method: "GET", path: q(org, "/announcements", s.Site, "")}
		}),
		"operations.createAnnouncement": op(perms(announcements.PermPublish), 201, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/announcements"), fmt.Sprintf(`{"site_id":%q,"title":"Water cut","body_md":"On Saturday","audience":{"kind":"site"},"channels":["in_app","sms"]}`, s.Site))
		}),
		"operations.getAnnouncement": op(perms(announcements.AnyView), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/announcements/"+s.announcement(w, t, "published").String())}
		}),
		"operations.updateAnnouncement": op(perms(announcements.PermPublish), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/announcements/"+s.announcement(w, t, "draft").String()), `{"pinned":true}`, ifm, `"1"`)
		}),
		"operations.deleteAnnouncement": op(perms(announcements.PermPublish), 204, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("DELETE", orgPath(org, "/announcements/"+s.announcement(w, t, "draft").String()), ``, ifm, `"1"`)
		}),
		"operations.previewAnnouncement": op(perms(announcements.PermPublish), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/announcements:preview"), fmt.Sprintf(`{"site_id":%q,"audience":{"kind":"site"},"channels":["in_app","sms"]}`, s.Site))
		}),
		"operations.publishAnnouncement": op(perms(announcements.PermPublish), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/announcements/"+s.announcement(w, t, "draft").String()+":publish"), ``, "Idempotency-Key", fmt.Sprintf("matrix-publish-%d", d2Seq.Add(1)))
		}),
		"operations.archiveAnnouncement": op(perms(announcements.PermPublish), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "POST", path: orgPath(org, "/announcements/"+s.announcement(w, t, "published").String()+":archive")}
		}),
		"operations.listAnnouncementReads": op(perms(announcements.PermPublish), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/announcements/"+s.announcement(w, t, "published").String()+"/reads")}
		}),
		"operations.markAnnouncementRead": op(perms(announcements.PermRead), 204, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "PUT", path: orgPath(org, "/announcements/"+s.announcement(w, t, "published").String()+"/read")}
		}),

		// documents
		"operations.listFolders": op(perms(documents.PermRead), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: q(org, "/folders", s.Site, "")}
		}),
		"operations.createFolder": op(perms(documents.PermManage), 201, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/folders"), fmt.Sprintf(`{"site_id":%q,"name":"Insurance","visibility":{"level":"owners"}}`, s.Site))
		}),
		"operations.getFolder": op(perms(documents.PermRead), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/folders/"+s.folder(w, t).String())}
		}),
		"operations.updateFolder": op(perms(documents.PermManage), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/folders/"+s.folder(w, t).String()), `{"name":"Contracts"}`, ifm, `"1"`)
		}),
		"operations.deleteFolder": op(perms(documents.PermManage), 204, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("DELETE", orgPath(org, "/folders/"+s.folder(w, t).String()), ``, ifm, `"1"`)
		}),
		"operations.listDocuments": op(perms(documents.AnyRead), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			s.document(w, t)
			return mreq{method: "GET", path: q(org, "/documents", s.Site, "")}
		}),
		"operations.createDocument": {perms: perms(documents.AnyWrite), status: 201, override: notOwnFile(map[string]int{"board_member": 404}),
			build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
				s := newD2Site(w, org, t)
				return jsonReq("POST", orgPath(org, "/documents"), fmt.Sprintf(`{"site_id":%q,"folder_id":%q,"title":"Insurance 2026","file_id":%q}`, s.Site, s.folder(w, t), s.file(w, t)))
			}},
		"operations.getDocument": op(perms(documents.AnyRead), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/documents/"+s.document(w, t).String())}
		}),
		"operations.updateDocument": op(perms(documents.AnyWrite), 200, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/documents/"+s.document(w, t).String()), `{"title":"Management plan 2026"}`, ifm, `"1"`)
		}),
		"operations.deleteDocument": op(perms(documents.AnyWrite), 204, func(w *authztest.World, org authztest.Org, s d2Site, t *testing.T) mreq {
			return jsonReq("DELETE", orgPath(org, "/documents/"+s.document(w, t).String()), ``, ifm, `"1"`)
		}),

		// dashboards and codes
		"dashboards.getSiteDashboard": op(perms(dashboards.PermSite), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/sites/"+s.Site.String()+"/dashboard")}
		}),
		"dashboards.getStaffDashboard": op(perms(dashboards.PermStaff), 200, func(_ *authztest.World, org authztest.Org, s d2Site, _ *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/sites/"+s.Site.String()+"/staff-dashboard")}
		}),
		"dashboards.getPortfolioDashboard": {perms: perms(dashboards.PermPortfolio), status: 200, build: mget("/v1/orgs/{org_id}/dashboard/portfolio")},
		"dashboards.getMyDashboard":        {perms: perms(dashboards.PermMe), status: 200, build: mget("/v1/orgs/{org_id}/me/dashboard")},
		"codes.listCodes":                  {perms: perms(codes.PermRead), status: 200, build: mget("/v1/orgs/{org_id}/codes?locale=tr")},
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
