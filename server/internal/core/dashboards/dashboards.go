// Package dashboards builds the home screens: per site for management, the
// money-free counts of staff, the organization portfolio, and the caller's own
// summary (backend plan section 8). Money figures come from the money module
// through MoneyFigures; until it is wired a stub answers zeros.
package dashboards

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Permissions of the module.
const (
	PermSite      authz.Permission = "dashboard.site"
	PermStaff     authz.Permission = "dashboard.staff"
	PermPortfolio authz.Permission = "dashboard.portfolio"
	PermMe        authz.Permission = "me.dashboard"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	return []authz.PermissionDef{
		{Name: PermSite, Description: "Read the site dashboard (receivables, cash and bank; never for staff or guards)",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant}, Scopes: orgSite},
		{Name: PermStaff, Description: "Read the counts-only staff dashboard (assigned and common tickets, expected visitors, parcels; no money)",
			Roles: []authz.Role{authz.RoleStaff}, Scopes: orgSite},
		{Name: PermPortfolio, Description: "Read the organization portfolio dashboard",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleAuditor, authz.RoleAccountant}, Scopes: orgSite},
		{Name: PermMe, Description: "Read the caller's home summary",
			Roles: authz.BuiltinRoles(), Scopes: []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock},
			Occupants: []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}},
	}
}

// Money is an amount in minor units with its currency.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// SiteMoney are the money figures of one site, computed by the money module.
type SiteMoney struct {
	ReceivableOwner         Money
	ReceivableTenant        Money
	OverdueTotal            Money
	OverdueUnits            int
	Cash                    Money
	Bank                    Money
	CollectionRatePct       float64
	AccruedLateFees         Money
	PendingDeclaredPayments int
}

// MySide is what the caller owes on one side of one unit (the money module's
// MySide of the spec).
type MySide struct {
	UnitID         uuid.UUID      `json:"unit_id"`
	UnitDoorNumber string         `json:"unit_door_number"`
	DebtorKind     string         `json:"debtor_kind"`
	Principal      Money          `json:"principal"`
	AccruedLateFee Money          `json:"accrued_late_fee"`
	Total          Money          `json:"total"`
	Credit         Money          `json:"credit"`
	NextDueDate    *string        `json:"next_due_date"`
	BankDetails    map[string]any `json:"bank_details"`
}

// MoneyFigures is implemented by the money module. Both methods run inside
// the caller's tenant transaction and respect its visibility rules: the
// dashboards check dashboard.site and me.dashboard first, MySides returns
// only the sides the person may see.
type MoneyFigures interface {
	// SiteFigures returns the site's receivables, overdue amounts, cash, bank
	// and collection rate as of asOf, all in the site's currency.
	SiteFigures(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, currency string, asOf time.Time) (SiteMoney, error)
	// MySides returns what person owes today, one entry per unit side, in
	// the given site (nil: all sites).
	MySides(ctx context.Context, tx pgx.Tx, orgID, personID uuid.UUID, siteID *uuid.UUID) ([]MySide, error)
}

// ZeroMoney is the stub used until the money module is wired.
type ZeroMoney struct{}

// SiteFigures implements MoneyFigures with zeros.
func (ZeroMoney) SiteFigures(_ context.Context, _ pgx.Tx, _, _ uuid.UUID, currency string, _ time.Time) (SiteMoney, error) {
	z := Money{Currency: currency}
	return SiteMoney{ReceivableOwner: z, ReceivableTenant: z, OverdueTotal: z, Cash: z, Bank: z, AccruedLateFees: z}, nil
}

// MySides implements MoneyFigures with nothing.
func (ZeroMoney) MySides(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, *uuid.UUID) ([]MySide, error) {
	return []MySide{}, nil
}

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Service builds the dashboards.
type Service struct {
	db    TxRunner
	money MoneyFigures
	now   func() time.Time
}

// NewService returns a Service; a nil money uses ZeroMoney.
func NewService(db TxRunner, money MoneyFigures) *Service {
	if money == nil {
		money = ZeroMoney{}
	}
	return &Service{db: db, money: money, now: time.Now}
}

// Meeting is an upcoming meeting (0.2).
type Meeting struct {
	ID          uuid.UUID `json:"id"`
	Title       *string   `json:"title"`
	ScheduledAt time.Time `json:"scheduled_at"`
	Status      string    `json:"status"`
}

// Term is a role term about to end (0.2).
type Term struct {
	MembershipID      uuid.UUID `json:"membership_id"`
	PersonDisplayName string    `json:"person_display_name"`
	Role              string    `json:"role"`
	ValidTo           string    `json:"valid_to"`
}

// SiteDashboard is the per-site dashboard of management.
type SiteDashboard struct {
	SiteID                  uuid.UUID `json:"site_id"`
	AsOf                    time.Time `json:"as_of"`
	ReceivableOwner         Money     `json:"receivable_owner"`
	ReceivableTenant        Money     `json:"receivable_tenant"`
	OverdueTotal            Money     `json:"overdue_total"`
	OverdueUnits            int       `json:"overdue_units"`
	Cash                    Money     `json:"cash"`
	Bank                    Money     `json:"bank"`
	CollectionRatePct       float64   `json:"collection_rate_pct"`
	AccruedLateFees         Money     `json:"accrued_late_fees"`
	OpenTickets             int       `json:"open_tickets"`
	OpenCommonTickets       int       `json:"open_common_tickets"`
	PendingUnitClaims       int       `json:"pending_unit_claims"`
	PendingDeclaredPayments int       `json:"pending_declared_payments"`
	WaitingDeliveries       int       `json:"waiting_deliveries"`
	PendingVisitorApprovals int       `json:"pending_visitor_approvals"`
	UpcomingMeeting         *Meeting  `json:"upcoming_meeting"`
	ExpiringTerms           []Term    `json:"expiring_terms"`
}

func siteOf(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (currency string, err error) {
	err = tx.QueryRow(ctx, `SELECT currency FROM site WHERE org_id = $1 AND id = $2 AND status = 'active'`, orgID, siteID).Scan(&currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", problem.NotFound("no such site")
	}
	return
}

func ticketCounts(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (open, common int, err error) {
	err = tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE t.visibility = 'common') FROM ticket t
		JOIN ticket_status st ON st.org_id = t.org_id AND st.id = t.status_id
		WHERE t.org_id = $1 AND t.site_id = $2 AND st.semantic = 'open_like' AND t.merged_into_id IS NULL`, orgID, siteID).Scan(&open, &common)
	return
}

// Site builds the site dashboard (dashboard.site).
func (s *Service) Site(ctx context.Context, orgID, siteID uuid.UUID) (SiteDashboard, error) {
	var d SiteDashboard
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermSite, sc); err != nil {
			return err
		}
		cur, err := siteOf(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		d = SiteDashboard{SiteID: siteID, AsOf: s.now().UTC(), ExpiringTerms: []Term{}}
		m, err := s.money.SiteFigures(ctx, tx, orgID, siteID, cur, d.AsOf)
		if err != nil {
			return err
		}
		d.ReceivableOwner, d.ReceivableTenant, d.OverdueTotal, d.OverdueUnits = m.ReceivableOwner, m.ReceivableTenant, m.OverdueTotal, m.OverdueUnits
		d.Cash, d.Bank, d.CollectionRatePct, d.AccruedLateFees, d.PendingDeclaredPayments = m.Cash, m.Bank, m.CollectionRatePct, m.AccruedLateFees, m.PendingDeclaredPayments
		if d.OpenTickets, d.OpenCommonTickets, err = ticketCounts(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM unit_claim WHERE org_id = $1 AND site_id = $2 AND status = 'pending'`, orgID, siteID).Scan(&d.PendingUnitClaims)
	})
	return d, tenancy.DBProblem(err, "dashboard")
}

// StaffDashboard is the counts-only dashboard of staff: no money, no debtors,
// no resident names.
type StaffDashboard struct {
	SiteID                 uuid.UUID `json:"site_id"`
	AsOf                   time.Time `json:"as_of"`
	AssignedOpenTickets    int       `json:"assigned_open_tickets"`
	AssignedOverdueTickets int       `json:"assigned_overdue_tickets"`
	OpenCommonTickets      int       `json:"open_common_tickets"`
	ExpectedVisitorsToday  int       `json:"expected_visitors_today"`
	WaitingDeliveries      int       `json:"waiting_deliveries"`
}

// Staff builds the staff dashboard (dashboard.staff). Overdue tickets, expected
// visitors and parcels are 0 until target times and gate control exist.
func (s *Service) Staff(ctx context.Context, orgID, siteID uuid.UUID) (StaffDashboard, error) {
	var d StaffDashboard
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermStaff, sc); err != nil {
			return err
		}
		if _, err := siteOf(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		d = StaffDashboard{SiteID: siteID, AsOf: s.now().UTC()}
		if _, d.OpenCommonTickets, err = ticketCounts(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM ticket t JOIN ticket_status st ON st.org_id = t.org_id AND st.id = t.status_id
			JOIN person p ON p.org_id = t.org_id AND p.id = t.assignee_person_id
			WHERE t.org_id = $1 AND t.site_id = $2 AND st.semantic = 'open_like' AND p.user_id = $3`, orgID, siteID, authz.PrincipalFromContext(ctx).UserID).Scan(&d.AssignedOpenTickets)
	})
	return d, tenancy.DBProblem(err, "dashboard")
}

// PortfolioSite is one site of the portfolio.
type PortfolioSite struct {
	SiteID            uuid.UUID `json:"site_id"`
	Name              string    `json:"name"`
	Overdue           Money     `json:"overdue"`
	OverdueUnits      int       `json:"overdue_units"`
	Cash              Money     `json:"cash"`
	Bank              Money     `json:"bank"`
	OpenTickets       int       `json:"open_tickets"`
	CollectionRatePct float64   `json:"collection_rate_pct"`
}

// PortfolioTotals sums the sites (in the first site's currency; sites in
// other currencies are left out of the money totals).
type PortfolioTotals struct {
	Overdue     Money `json:"overdue"`
	Cash        Money `json:"cash"`
	Bank        Money `json:"bank"`
	OpenTickets int   `json:"open_tickets"`
}

// Portfolio is the cross-site view of a management company.
type Portfolio struct {
	AsOf   time.Time       `json:"as_of"`
	Sites  []PortfolioSite `json:"sites"`
	Totals PortfolioTotals `json:"totals"`
}

// Portfolio builds the organization dashboard (dashboard.portfolio) over the
// active sites the caller holds the permission for.
func (s *Service) Portfolio(ctx context.Context, orgID uuid.UUID) (Portfolio, error) {
	if err := authz.RequireInOrg(ctx, PermPortfolio, orgID); err != nil {
		return Portfolio{}, err
	}
	reach := authz.GrantsFromContext(ctx).ReachOf(PermPortfolio, orgID)
	out := Portfolio{AsOf: s.now().UTC(), Sites: []PortfolioSite{}}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, currency FROM site WHERE org_id = $1 AND status = 'active' ORDER BY name, id`, orgID)
		if err != nil {
			return err
		}
		type siteRow struct {
			id        uuid.UUID
			name, cur string
		}
		var sites []siteRow
		for rows.Next() {
			var r siteRow
			if err := rows.Scan(&r.id, &r.name, &r.cur); err != nil {
				rows.Close()
				return err
			}
			if reach.OrgWide || slices.Contains(reach.SiteIDs, r.id) {
				sites = append(sites, r)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range sites {
			m, err := s.money.SiteFigures(ctx, tx, orgID, r.id, r.cur, out.AsOf)
			if err != nil {
				return err
			}
			open, _, err := ticketCounts(ctx, tx, orgID, r.id)
			if err != nil {
				return err
			}
			out.Sites = append(out.Sites, PortfolioSite{SiteID: r.id, Name: r.name, Overdue: m.OverdueTotal, OverdueUnits: m.OverdueUnits, Cash: m.Cash, Bank: m.Bank, OpenTickets: open, CollectionRatePct: m.CollectionRatePct})
			out.Totals.OpenTickets += open
			if out.Totals.Cash.Currency == "" {
				out.Totals.Overdue.Currency, out.Totals.Cash.Currency, out.Totals.Bank.Currency = r.cur, r.cur, r.cur
			}
			if r.cur == out.Totals.Cash.Currency {
				out.Totals.Overdue.Amount += m.OverdueTotal.Amount
				out.Totals.Cash.Amount += m.Cash.Amount
				out.Totals.Bank.Amount += m.Bank.Amount
			}
		}
		return nil
	})
	if out.Totals.Cash.Currency == "" {
		out.Totals.Overdue.Currency, out.Totals.Cash.Currency, out.Totals.Bank.Currency = "TRY", "TRY", "TRY"
	}
	return out, tenancy.DBProblem(err, "dashboard")
}

// Me is the caller's home summary.
type Me struct {
	Home                    string    `json:"home"`
	Sides                   []MySide  `json:"sides"`
	UnreadNotifications     int       `json:"unread_notifications"`
	UnreadAnnouncements     int       `json:"unread_announcements"`
	MyOpenTickets           int       `json:"my_open_tickets"`
	UpcomingMeetings        []Meeting `json:"upcoming_meetings"`
	PendingVisitorApprovals int       `json:"pending_visitor_approvals"`
	WaitingDeliveries       int       `json:"waiting_deliveries"`
}

// Me builds the caller's summary (me.dashboard): which home to show, what they
// owe (through the money module), unread notifications and announcements and
// their open tickets; optionally for one site.
func (s *Service) Me(ctx context.Context, orgID uuid.UUID, siteID *uuid.UUID) (Me, error) {
	if err := authz.RequireInOrg(ctx, PermMe, orgID); err != nil {
		return Me{}, err
	}
	g := authz.GrantsFromContext(ctx)
	out := Me{Home: "resident", Sides: []MySide{}, UpcomingMeetings: []Meeting{}}
	switch {
	case g.HoldsAnywhere(PermPortfolio):
		out.Home = "portfolio"
	case g.HoldsAnywhere(PermSite), g.HoldsAnywhere(PermStaff):
		out.Home = "site"
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var person uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, authz.PrincipalFromContext(ctx).UserID).Scan(&person); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if siteID != nil {
			sc, err := authz.ScopeOfSite(ctx, tx, *siteID)
			if err != nil {
				return err
			}
			if err := authz.Require(ctx, PermMe, sc); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification n JOIN notification_delivery d ON d.org_id = n.org_id AND d.notification_id = n.id AND d.channel = 'in_app'
			WHERE n.org_id = $1 AND n.person_id = $2 AND n.read_at IS NULL AND ($3::uuid IS NULL OR n.site_id = $3)`, orgID, person, siteID).Scan(&out.UnreadNotifications); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM announcement a JOIN announcement_recipient rc ON rc.org_id = a.org_id AND rc.announcement_id = a.id AND rc.person_id = $2
			WHERE a.org_id = $1 AND a.status = 'published' AND (a.expires_at IS NULL OR a.expires_at > now()) AND ($3::uuid IS NULL OR a.site_id = $3)
			AND NOT EXISTS (SELECT 1 FROM announcement_read r WHERE r.org_id = a.org_id AND r.announcement_id = a.id AND r.person_id = $2)`, orgID, person, siteID).Scan(&out.UnreadAnnouncements); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ticket t JOIN ticket_status st ON st.org_id = t.org_id AND st.id = t.status_id
			WHERE t.org_id = $1 AND t.reporter_person_id = $2 AND st.semantic = 'open_like' AND t.merged_into_id IS NULL AND ($3::uuid IS NULL OR t.site_id = $3)`, orgID, person, siteID).Scan(&out.MyOpenTickets); err != nil {
			return err
		}
		sides, err := s.money.MySides(ctx, tx, orgID, person, siteID)
		if err != nil {
			return err
		}
		if sides != nil {
			out.Sides = sides
		}
		return nil
	})
	return out, tenancy.DBProblem(err, "dashboard")
}
