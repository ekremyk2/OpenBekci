package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/announcements"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/core/tickets"
	"github.com/openbekci/openbekci/server/internal/platform/db"
)

// chatCommands runs the bot's DM commands (/borcum, /ariza, /duyurular) as the
// linked user: their grants are resolved like a request's, so a command can
// read and write nothing the user could not in the app.
type chatCommands struct {
	db       *db.DB
	resolver *authz.Resolver
	tickets  *tickets.Service
	reports  *reports.Service
	anns     *announcements.Service
	baseURL  string
}

// as returns ctx carrying the user's principal and grants in the organization.
func (c *chatCommands) as(ctx context.Context, org, user uuid.UUID) (context.Context, authz.Grants, error) {
	p := &authz.Principal{ID: user, UserID: user}
	g, member, err := c.resolver.ResolveOrg(ctx, p, org)
	if err != nil {
		return ctx, g, err
	}
	if !member {
		return ctx, g, errors.New("the user is not a member of the organization")
	}
	return authz.WithGrants(authz.WithPrincipal(ctx, p), g), g, nil
}

func (c *chatCommands) Balance(ctx context.Context, org, user uuid.UUID, locale string) ([]string, string, error) {
	ctx, g, err := c.as(ctx, org, user)
	if err != nil {
		return nil, "", err
	}
	tr := strings.HasPrefix(strings.ToLower(locale), "tr")
	type side struct {
		unit, site uuid.UUID
		kind       authz.UnitSide
	}
	var sides []side
	seen := map[side]bool{}
	for _, o := range g.Current() {
		for _, k := range []authz.UnitSide{authz.SideOwner, authz.SideTenant} {
			s := side{o.UnitID, o.SiteID, k}
			if !seen[s] && authz.CanAccessUnit(ctx, o.UnitID, k) == nil {
				seen[s] = true
				sides = append(sides, s)
			}
		}
	}
	var lines []string
	for _, s := range sides {
		unit := s.unit
		st, err := c.reports.DebtorStatement(ctx, org, s.site, billing.Debtor{UnitID: &unit, Kind: string(s.kind)}, "", "")
		if err != nil {
			return nil, "", err
		}
		amount, err := st.Closing.Total.Format(map[bool]string{true: "tr-TR", false: "en-US"}[tr])
		if err != nil {
			return nil, "", err
		}
		var door string
		if err := c.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id = $1 AND id = $2`, org, s.unit).Scan(&door)
		}); err != nil {
			return nil, "", err
		}
		label := map[authz.UnitSide][2]string{authz.SideOwner: {"malik", "owner"}, authz.SideTenant: {"kiracı", "tenant"}}[s.kind]
		if tr {
			lines = append(lines, fmt.Sprintf("Daire %s (%s): %s", door, label[0], amount))
		} else {
			lines = append(lines, fmt.Sprintf("Unit %s (%s): %s", door, label[1], amount))
		}
	}
	return lines, strings.TrimRight(c.baseURL, "/") + "/orgs/" + org.String() + "/money/statements", nil
}

func (c *chatCommands) OpenTicket(ctx context.Context, org, user uuid.UUID, text string) (string, error) {
	ctx, g, err := c.as(ctx, org, user)
	if err != nil {
		return "", err
	}
	var unit, site uuid.UUID
	for _, o := range g.Current() {
		if o.Kind == authz.OccOwner || o.Kind == authz.OccTenant {
			unit, site = o.UnitID, o.SiteID
			break
		}
	}
	if unit == uuid.Nil {
		return "", errors.New("the user has no unit")
	}
	cats, _, err := c.tickets.ListCategories(ctx, org, site, "", tenancy.PageParams{Limit: 1})
	if err != nil || len(cats) == 0 {
		return "", fmt.Errorf("no ticket category: %w", errors.Join(err))
	}
	title := strings.Join(strings.Fields(text), " ")
	if r := []rune(title); len(r) > 80 {
		title = string(r[:79]) + "…"
	}
	doc, err := tenancy.ToDoc(map[string]any{"site_id": site, "unit_id": unit, "category_id": cats[0].ID, "title": title, "description": text})
	if err != nil {
		return "", err
	}
	t, err := c.tickets.CreateTicket(ctx, org, doc)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(t.ID.String()[:8]), nil // a ticket has no running number; its short id is what the app shows and finds
}

func (c *chatCommands) Announcements(ctx context.Context, org, user uuid.UUID, n int) ([]string, error) {
	ctx, g, err := c.as(ctx, org, user)
	if err != nil {
		return nil, err
	}
	cur := g.Current()
	if len(cur) == 0 {
		return nil, nil
	}
	list, _, err := c.anns.List(ctx, org, announcements.Filter{SiteID: cur[0].SiteID, Status: announcements.StatusPublished}, tenancy.PageParams{Limit: n})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Title)
	}
	return out, nil
}
