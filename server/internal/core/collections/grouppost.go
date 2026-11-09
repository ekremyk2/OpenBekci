package collections

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// splitGroup takes telegram_group out of a run's channels: it is one generic
// post in the site's group, not a reminder per debtor.
func splitGroup(channels []string) (rest []string, group bool) {
	group = slices.Contains(channels, jobs.ChannelTelegramGroup)
	return slices.DeleteFunc(slices.Clone(channels), func(c string) bool { return c == jobs.ChannelTelegramGroup }), group
}

// postDues posts "a dues reminder was sent, check your statement" in the
// site's group. A group is a public room: no name, no amount, no balance (KVKK).
func (s *Service) postDues(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site) error {
	linked := false
	if s.Groups != nil {
		var err error
		if _, linked, err = s.Groups.GroupTitle(ctx, tx, orgID, site.ID); err != nil {
			return err
		}
	}
	if !linked {
		return problem.Conflict("no Telegram group is linked to this site")
	}
	var locale string
	if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, orgID).Scan(&locale); err != nil {
		return err
	}
	return s.Groups.PostToGroup(ctx, tx, jobs.GroupPostArgs{OrgID: orgID, SiteID: site.ID, Type: jobs.GroupDues, HTML: chat.DuesHTML(locale, site.Name, "")})
}
