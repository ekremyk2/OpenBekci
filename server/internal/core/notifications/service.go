package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/opsx"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Config configures the Service. A nil Mail or SMS disables that channel: its
// deliveries are recorded as skipped.
type Config struct {
	Mail mail.Sender
	SMS  sms.Gateway
	Log  *slog.Logger
	Now  func() time.Time
	// BaseURL is the public address of the web app; a notification's link is
	// appended to emails as an absolute URL.
	BaseURL string
}

// Service is the notifications module. It implements jobs.Deliverer.
type Service struct {
	db  TxRunner
	cfg Config
}

// NewService returns a Service.
func NewService(db TxRunner, cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{db: db, cfg: cfg}
}

// Notification is an in-app notification of the caller.
type Notification struct {
	ID        uuid.UUID      `json:"id"`
	SiteID    *uuid.UUID     `json:"site_id"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	ReadAt    *time.Time     `json:"read_at"`
	Link      *string        `json:"link"`
	CreatedAt time.Time      `json:"created_at"`
}

const notifColumns = `n.id, n.site_id, n.type, n.payload, n.read_at, n.link, n.created_at`
const notifFrom = ` FROM notification n JOIN notification_delivery d ON d.org_id = n.org_id AND d.notification_id = n.id AND d.channel = 'in_app'`

func scanNotification(row pgx.Row) (Notification, error) {
	var n Notification
	var payload []byte
	if err := row.Scan(&n.ID, &n.SiteID, &n.Type, &payload, &n.ReadAt, &n.Link, &n.CreatedAt); err != nil {
		return n, err
	}
	n.Payload = map[string]any{}
	_ = json.Unmarshal(payload, &n.Payload)
	n.CreatedAt = n.CreatedAt.UTC()
	if n.ReadAt != nil {
		t := n.ReadAt.UTC()
		n.ReadAt = &t
	}
	return n, nil
}

// ListFilter narrows List.
type ListFilter struct {
	SiteID *uuid.UUID
	Unread bool
	Type   string
}

// List returns the caller's in-app notifications, newest first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, f ListFilter, p tenancy.PageParams) ([]Notification, string, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Notification
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		person, ok, err := opsx.PersonOf(ctx, tx, orgID)
		if err != nil || !ok {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+notifColumns+notifFrom+`
			WHERE n.org_id = $1 AND n.person_id = $2 AND ($3 = '00000000-0000-0000-0000-000000000000'::uuid OR n.id < $3)
			  AND ($4::uuid IS NULL OR n.site_id = $4) AND (NOT $5 OR n.read_at IS NULL) AND ($6 = '' OR n.type = $6)
			ORDER BY n.id DESC LIMIT $7`, orgID, person, after, f.SiteID, f.Unread, f.Type, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			n, err := scanNotification(rows)
			if err != nil {
				return err
			}
			out = append(out, n)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "notifications")
	}
	items, next := tenancy.Page(out, limit, func(n Notification) uuid.UUID { return n.ID })
	return items, next, nil
}

// Unread is the cheap poll behind the bell.
type Unread struct {
	Unread                  int `json:"unread"`
	VisitorApprovalsPending int `json:"visitor_approvals_pending"`
}

// UnreadCount counts the caller's unread in-app notifications. The visitor
// approvals counter is 0 until release 0.3 (gate control) fills it.
func (s *Service) UnreadCount(ctx context.Context, orgID uuid.UUID, siteID *uuid.UUID) (Unread, error) {
	var u Unread
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return u, err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		person, ok, err := opsx.PersonOf(ctx, tx, orgID)
		if err != nil || !ok {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*)`+notifFrom+` WHERE n.org_id = $1 AND n.person_id = $2 AND n.read_at IS NULL
			AND ($3::uuid IS NULL OR n.site_id = $3)`, orgID, person, siteID).Scan(&u.Unread)
	})
	return u, tenancy.DBProblem(err, "notifications")
}

// MarkRead marks one of the caller's notifications read (idempotent).
func (s *Service) MarkRead(ctx context.Context, orgID, id uuid.UUID) (Notification, error) {
	var n Notification
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return n, err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		person, err := opsx.MustPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE notification SET read_at = now() WHERE org_id = $1 AND id = $2 AND person_id = $3 AND read_at IS NULL`, orgID, id, person); err != nil {
			return err
		}
		n, err = scanNotification(tx.QueryRow(ctx, `SELECT `+notifColumns+notifFrom+` WHERE n.org_id = $1 AND n.id = $2 AND n.person_id = $3`, orgID, id, person))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such notification")
		}
		return err
	})
	return n, tenancy.DBProblem(err, "notification")
}

// MarkAllRead marks all of the caller's notifications read, optionally of one site.
func (s *Service) MarkAllRead(ctx context.Context, orgID uuid.UUID, siteID *uuid.UUID) error {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		person, ok, err := opsx.PersonOf(ctx, tx, orgID)
		if err != nil || !ok {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE notification SET read_at = now() WHERE org_id = $1 AND person_id = $2 AND read_at IS NULL AND ($3::uuid IS NULL OR site_id = $3)`, orgID, person, siteID)
		return err
	})
	return tenancy.DBProblem(err, "notifications")
}

// Mutes are a person's muted types.
type Mutes struct {
	MutedTypes     []string `json:"muted_types"`
	UnmutableTypes []string `json:"unmutable_types"`
	AvailableTypes []string `json:"available_types"`
}

func mutesOf(muted []string) Mutes {
	slices.Sort(muted)
	if muted == nil {
		muted = []string{}
	}
	return Mutes{MutedTypes: muted, UnmutableTypes: slices.Clone(Unmutable), AvailableTypes: slices.Clone(Vocabulary)}
}

// GetMutes returns the caller's muted types, the ones that cannot be muted and
// the ones that can.
func (s *Service) GetMutes(ctx context.Context, orgID uuid.UUID) (Mutes, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return Mutes{}, err
	}
	var muted []string
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		person, ok, err := opsx.PersonOf(ctx, tx, orgID)
		if err != nil || !ok {
			return err
		}
		muted, err = mutedTypes(ctx, tx, orgID, person)
		return err
	})
	return mutesOf(muted), tenancy.DBProblem(err, "notification mutes")
}

func mutedTypes(ctx context.Context, tx pgx.Tx, orgID, person uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT type FROM notification_mute WHERE org_id = $1 AND person_id = $2 ORDER BY type`, orgID, person)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetMutes replaces the caller's muted types. Sign-in codes and invitations
// cannot be muted (422).
func (s *Service) SetMutes(ctx context.Context, orgID uuid.UUID, types []string) (Mutes, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return Mutes{}, err
	}
	var bad []problem.Field
	for i, t := range types {
		switch {
		case IsUnmutable(t):
			bad = append(bad, problem.Field{Field: fmt.Sprintf("muted_types[%d]", i), Code: "not_mutable"})
		case !slices.Contains(Vocabulary, t):
			bad = append(bad, problem.Field{Field: fmt.Sprintf("muted_types[%d]", i), Code: "invalid_value"})
		}
	}
	if len(bad) > 0 {
		return Mutes{}, problem.Validation(bad...)
	}
	types = slices.Compact(slices.Sorted(slices.Values(types)))
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		person, err := opsx.MustPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM notification_mute WHERE org_id = $1 AND person_id = $2`, orgID, person); err != nil {
			return err
		}
		for _, t := range types {
			if _, err := tx.Exec(ctx, `INSERT INTO notification_mute (org_id, person_id, type) VALUES ($1, $2, $3)`, orgID, person, t); err != nil {
				return err
			}
		}
		return nil
	})
	return mutesOf(types), tenancy.DBProblem(err, "notification mutes")
}

// Default is the channel set of one automatic message type at a site.
type Default struct {
	Type     string   `json:"type"`
	Channels []string `json:"channels"`
	Locked   bool     `json:"locked"`
}

// Defaults are a site's channel sets for automatic messages.
type Defaults struct {
	SiteID   uuid.UUID `json:"site_id"`
	Defaults []Default `json:"defaults"`
	Version  int       `json:"version"`
}

func loadDefaults(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (Defaults, error) {
	d := Defaults{SiteID: siteID, Version: 1}
	if err := tx.QueryRow(ctx, `SELECT version FROM site_notification_config WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&d.Version); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}
	over := map[string][]string{}
	rows, err := tx.Query(ctx, `SELECT type, channels FROM site_channel_default WHERE org_id = $1 AND site_id = $2`, orgID, siteID)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var ch []string
		if err := rows.Scan(&t, &ch); err != nil {
			return d, err
		}
		over[t] = ch
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	for _, t := range Vocabulary {
		ch, ok := over[t]
		if !ok {
			ch = DefaultChannels(t)
		}
		d.Defaults = append(d.Defaults, Default{Type: t, Channels: ch})
	}
	for _, t := range Unmutable {
		d.Defaults = append(d.Defaults, Default{Type: t, Channels: DefaultChannels(t), Locked: true})
	}
	return d, nil
}

// GetDefaults returns the site's channel defaults for automatic messages.
func (s *Service) GetDefaults(ctx context.Context, orgID, siteID uuid.UUID) (Defaults, error) {
	var d Defaults
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermManage, sc); err != nil {
			return err
		}
		d, err = loadDefaults(ctx, tx, orgID, siteID)
		return err
	})
	return d, tenancy.DBProblem(err, "notification defaults")
}

// DefaultInput is one entry of SetDefaults.
type DefaultInput struct {
	Type     string
	Channels []string
}

// SetDefaults replaces the site's overrides. expected is the version read
// last (If-Match); a type that is left out goes back to its built-in default.
func (s *Service) SetDefaults(ctx context.Context, orgID, siteID uuid.UUID, expected int, in []DefaultInput) (Defaults, error) {
	var bad []problem.Field
	seen := map[string]bool{}
	for i, e := range in {
		f := fmt.Sprintf("defaults[%d]", i)
		switch {
		case IsUnmutable(e.Type):
			bad = append(bad, problem.Field{Field: f + ".type", Code: "locked"})
		case !slices.Contains(Vocabulary, e.Type):
			bad = append(bad, problem.Field{Field: f + ".type", Code: "invalid_value"})
		case seen[e.Type]:
			bad = append(bad, problem.Field{Field: f + ".type", Code: "duplicate"})
		}
		seen[e.Type] = true
		ch := NormalizeChannels(e.Channels)
		if len(ch) == 0 || len(ch) != len(slices.Compact(slices.Sorted(slices.Values(e.Channels)))) {
			bad = append(bad, problem.Field{Field: f + ".channels", Code: "invalid_value"})
		}
	}
	if len(bad) > 0 {
		return Defaults{}, problem.Validation(bad...)
	}
	var out Defaults
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermManage, sc); err != nil {
			return err
		}
		before, err := loadDefaults(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		if _, err := tx.Exec(ctx, `INSERT INTO site_notification_config (org_id, site_id, version) VALUES ($1, $2, 2)
			ON CONFLICT (org_id, site_id) DO UPDATE SET version = site_notification_config.version + 1, updated_at = now()`, orgID, siteID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM site_channel_default WHERE org_id = $1 AND site_id = $2`, orgID, siteID); err != nil {
			return err
		}
		for _, e := range in {
			if _, err := tx.Exec(ctx, `INSERT INTO site_channel_default (org_id, site_id, type, channels) VALUES ($1, $2, $3, $4)`, orgID, siteID, e.Type, NormalizeChannels(e.Channels)); err != nil {
				return err
			}
		}
		out, err = loadDefaults(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "notifications.defaults_set", TargetType: "notification_defaults", TargetID: siteID,
			Diff: mustDiff(before, out)})
	})
	return out, tenancy.DBProblem(err, "notification defaults")
}

func mustDiff(before, after Defaults) audit.Changes {
	flat := func(d Defaults) map[string]any {
		m := map[string]any{}
		for _, e := range d.Defaults {
			m[e.Type] = e.Channels
		}
		return m
	}
	c, err := audit.Diff("notification_defaults", flat(before), flat(after))
	if err != nil {
		return nil
	}
	return c
}

// SMSUsage is a site's SMS counter for a month.
type SMSUsage struct {
	SiteID       uuid.UUID `json:"site_id"`
	Month        string    `json:"month"`
	Sent         int       `json:"sent"`
	Delivered    int       `json:"delivered"`
	Failed       int       `json:"failed"`
	MonthlyLimit *int      `json:"monthly_limit"`
}

// GetSMSUsage returns the site's SMS counter for month ("YYYY-MM", default the
// current month in the site's time zone). Delivered counts accepted messages:
// the gateway reports no final delivery status yet.
func (s *Service) GetSMSUsage(ctx context.Context, orgID, siteID uuid.UUID, month string) (SMSUsage, error) {
	u := SMSUsage{SiteID: siteID}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermManage, sc); err != nil {
			return err
		}
		var first time.Time
		if month == "" {
			var tz string
			if err := tx.QueryRow(ctx, `SELECT timezone FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&tz); err != nil {
				return err
			}
			loc, lerr := time.LoadLocation(tz)
			if lerr != nil {
				loc = time.UTC
			}
			n := s.cfg.Now().In(loc)
			first = time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
		} else {
			first, err = time.Parse("2006-01", month)
			if err != nil {
				return problem.Validation(problem.Field{Field: "month", Code: "invalid_format"})
			}
		}
		u.Month = first.Format("2006-01")
		var segments int
		err = tx.QueryRow(ctx, `SELECT messages, segments FROM sms_usage WHERE org_id = $1 AND site_id = $2 AND month = $3`, orgID, siteID, first).Scan(&u.Sent, &segments)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		u.Delivered = u.Sent
		return tx.QueryRow(ctx, `SELECT count(*) FROM notification_delivery d JOIN notification n ON n.org_id = d.org_id AND n.id = d.notification_id
			WHERE d.org_id = $1 AND n.site_id = $2 AND d.channel = 'sms' AND d.status = 'failed' AND date_trunc('month', d.updated_at) = $3`, orgID, siteID, first).Scan(&u.Failed)
	})
	return u, tenancy.DBProblem(err, "sms usage")
}
