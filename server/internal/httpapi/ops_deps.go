package httpapi

import (
	"github.com/openbekci/openbekci/server/internal/core/announcements"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/dashboards"
	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
)

// OpsDeps are the daily-operations services the API serves.
type OpsDeps struct {
	tickets       *tickets.Service
	announcements *announcements.Service
	documents     *documents.Service
	notifications *notifications.Service
	dashboards    *dashboards.Service
	codes         *codes.Service
}

// NewOpsDeps groups the daily-operations services for Deps.Ops.
func NewOpsDeps(t *tickets.Service, a *announcements.Service, d *documents.Service, n *notifications.Service, dash *dashboards.Service, c *codes.Service) OpsDeps {
	return OpsDeps{tickets: t, announcements: a, documents: d, notifications: n, dashboards: dash, codes: c}
}
