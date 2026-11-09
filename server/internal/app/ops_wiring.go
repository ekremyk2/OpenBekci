package app

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/announcements"
	chatcore "github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/dashboards"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// lateNotifier is the jobs.Notifier the modules are built with before the
// River client exists (the client's workers need the modules): it queues
// through the client once start has set it.
type lateNotifier struct {
	c   atomic.Pointer[jobs.Client]
	log *slog.Logger
}

// Notify implements jobs.Notifier. Before the client exists (the start-up
// window) a notification is dropped, loudly.
func (l *lateNotifier) Notify(ctx context.Context, tx pgx.Tx, a jobs.NotifyArgs) error {
	c := l.c.Load()
	if c == nil {
		if l.log != nil {
			l.log.WarnContext(ctx, "notification dropped: the job client is not started yet", "type", a.Type, "key", a.Key)
		}
		return nil
	}
	return jobs.QueueNotifier{Client: c}.Notify(ctx, tx, a)
}

// opsServices are the daily-operations modules (backend plan 4.9 to 4.12).
type opsServices struct {
	late          *lateNotifier
	notifications *notifications.Service
	// chat is nil without OPENBEKCI_ENCRYPTION_KEY; chatPacks gets the pack host once it exists
	chat          *chatcore.Service
	chatPacks     *lazyChatPacks
	tickets       *tickets.Service
	announcements *announcements.Service
	documents     *documents.Service
	dashboards    *dashboards.Service
	codes         *codes.Service
}

// newOps builds the daily-operations modules. They register their file
// visibility resolvers on fs.
func newOps(cfg Config, log *slog.Logger, d *db.DB, fs *files.Service) (*opsServices, error) {
	gw, err := newSMSGateway(cfg, log)
	if err != nil {
		return nil, err
	}
	mailer, err := newMailer(cfg, log)
	if err != nil {
		return nil, err
	}
	late := &lateNotifier{log: log}
	ncfg := notifications.Config{Mail: mailer, SMS: gw, Log: log, BaseURL: cfg.BaseURL}
	var chatSvc *chatcore.Service
	chatPacks := &lazyChatPacks{}
	if len(cfg.EncryptionKey) > 0 {
		dev, err := newDevices(cfg, d)
		if err != nil {
			return nil, err
		}
		sender, err := newPushSender(cfg, log)
		if err != nil {
			return nil, err
		}
		ncfg.Push, ncfg.Devices, ncfg.InstanceID = sender, dev, instanceIDFunc(d)
		chatSvc, err = chatcore.NewService(d, chatcore.Config{EncryptionKey: cfg.EncryptionKey, Packs: chatPacks, BaseURL: cfg.BaseURL})
		if err != nil {
			return nil, err
		}
		ncfg.Chat = chatSvc // only when built: a nil *Service in the interface would not be nil
	}
	return &opsServices{
		chat:          chatSvc,
		chatPacks:     chatPacks,
		late:          late,
		notifications: notifications.NewService(d, ncfg),
		tickets:       tickets.NewService(d, fs, late),
		announcements: announcements.NewService(d, fs, late, announcements.Config{SMS: gw != nil, Mail: mailer != nil, BaseURL: cfg.BaseURL}),
		documents:     documents.NewService(d, fs),
		dashboards:    dashboards.NewService(d, nil),
		codes:         codes.NewService(d),
	}, nil
}

// tasks are the catch-up jobs the worker runs periodically.
func (o *opsServices) tasks() []jobs.Task {
	return []jobs.Task{{Name: "announcements.publish_due", Every: time.Minute, Run: o.announcements.PublishDue}}
}

func (o *opsServices) deps() httpapi.OpsDeps {
	return httpapi.NewOpsDeps(o.tickets, o.announcements, o.documents, o.notifications, o.dashboards, o.codes)
}
