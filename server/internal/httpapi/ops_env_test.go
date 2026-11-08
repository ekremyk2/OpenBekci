package httpapi

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/announcements"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/dashboards"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// captureNotifier records the notifications the modules enqueue instead of
// queueing them; tests deliver them through the notification service.
type captureNotifier struct {
	mu   sync.Mutex
	sent []jobs.NotifyArgs
}

func (c *captureNotifier) Notify(_ context.Context, _ pgx.Tx, a jobs.NotifyArgs) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.Key == "" {
		a.Key = "test"
	}
	c.sent = append(c.sent, a)
	return nil
}

func (c *captureNotifier) take() []jobs.NotifyArgs {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.sent
	c.sent = nil
	return out
}

// opsTest is the daily-operations services of a test environment.
type opsTest struct {
	deps          OpsDeps
	notes         *captureNotifier
	mail          *mail.Recorder
	sms           *sms.Recorder
	notifications *notifications.Service
	tickets       *tickets.Service
	announcements *announcements.Service
	documents     *documents.Service
	dashboards    *dashboards.Service
}

func newOpsTest(d *db.DB, fs *files.Service) *opsTest {
	o := &opsTest{notes: &captureNotifier{}, mail: &mail.Recorder{}, sms: &sms.Recorder{}}
	o.notifications = notifications.NewService(d, notifications.Config{Mail: o.mail, SMS: o.sms, BaseURL: "https://example.test"})
	o.tickets = tickets.NewService(d, fs, o.notes)
	o.announcements = announcements.NewService(d, fs, o.notes, announcements.Config{SMS: true, Mail: true})
	o.documents = documents.NewService(d, fs)
	o.dashboards = dashboards.NewService(d, nil)
	o.deps = NewOpsDeps(o.tickets, o.announcements, o.documents, o.notifications, o.dashboards, codes.NewService(d))
	return o
}
