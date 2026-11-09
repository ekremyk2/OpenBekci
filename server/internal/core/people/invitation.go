package people

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Invitation kinds (S4) and channels.
const (
	InviteManager   = "manager"
	InviteHousehold = "household"
	InviteSiteCode  = "site_code"
)

// DefaultInviteDays is how long a personal invitation is valid.
const DefaultInviteDays = 14

// newScopedToken returns a token "<org>.<random>" and its hash. The organization
// prefix is not secret: it tells the server which tenant to open before the
// caller is known (acceptance happens before sign-in).
func newScopedToken(orgID uuid.UUID, randomBytes int) (string, []byte) {
	b := make([]byte, randomBytes)
	_, _ = rand.Read(b) // crypto/rand does not fail since Go 1.24
	tok := base64.RawURLEncoding.EncodeToString(orgID[:]) + "." + base64.RawURLEncoding.EncodeToString(b)
	return tok, crypto.HashToken(tok)
}

func parseScopedToken(tok string) (uuid.UUID, bool) {
	prefix, rest, ok := strings.Cut(tok, ".")
	if !ok || rest == "" || len(tok) > 200 {
		return uuid.Nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(prefix)
	if err != nil || len(b) != 16 {
		return uuid.Nil, false
	}
	return uuid.UUID(b), true
}

// Invitation is an invitation as managers see it.
type Invitation struct {
	ID            uuid.UUID  `json:"id"`
	SiteID        uuid.UUID  `json:"site_id"`
	Kind          string     `json:"kind"`
	UnitID        *uuid.UUID `json:"unit_id"`
	DoorNumber    *string    `json:"door_number"`
	PersonID      *uuid.UUID `json:"person_id"`
	OccupancyKind *string    `json:"occupancy_kind"`
	Channel       string     `json:"channel"`
	Email         *string    `json:"email"`
	Phone         *string    `json:"phone"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at"`
	SendCount     int        `json:"send_count"`
	LastSentAt    *time.Time `json:"last_sent_at"`
	Uses          int        `json:"uses"`
	AcceptedAt    *time.Time `json:"accepted_at"`
	// Delivery is the outcome of the send that this request made: sent, failed or none.
	Delivery  string    `json:"delivery"`
	CreatedAt time.Time `json:"created_at"`
	Version   int       `json:"version"`
	// Token is the one-time secret and Link its URL; present only in the
	// response that created or re-sent the invitation (never stored).
	Token *string `json:"token,omitempty"`
	Link  *string `json:"link,omitempty"`
}

const invSelect = `SELECT i.id, i.site_id, i.kind, i.unit_id, u.door_number, i.person_id, i.occupancy_kind, i.channel, i.email, i.phone,
	CASE WHEN i.status = 'pending' AND i.expires_at IS NOT NULL AND i.expires_at <= now() THEN 'expired' ELSE i.status END,
	i.expires_at, i.send_count, i.last_sent_at, i.uses, i.accepted_at, i.created_at, i.version
	FROM invitation i LEFT JOIN unit u ON u.org_id = i.org_id AND u.id = i.unit_id`

func scanInv(row pgx.Row) (Invitation, error) {
	var i Invitation
	err := row.Scan(&i.ID, &i.SiteID, &i.Kind, &i.UnitID, &i.DoorNumber, &i.PersonID, &i.OccupancyKind, &i.Channel, &i.Email, &i.Phone,
		&i.Status, &i.ExpiresAt, &i.SendCount, &i.LastSentAt, &i.Uses, &i.AcceptedAt, &i.CreatedAt, &i.Version)
	i.CreatedAt = i.CreatedAt.UTC()
	i.Delivery = "none"
	return i, err
}

// ListInvitations lists personal invitations (and the site codes), optionally
// of one site or status. It needs invitation.manage for the sites.
func (s *Service) ListInvitations(ctx context.Context, orgID uuid.UUID, siteID *uuid.UUID, status string, p tenancy.PageParams) ([]Invitation, string, error) {
	if err := authz.RequireAnywhere(ctx, PermInvitationManage); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	if status != "" && !slices.Contains([]string{"pending", "accepted", "revoked", "expired"}, status) {
		return nil, "", fieldProblem("status", "invalid_value")
	}
	orgWide, sites := siteGrants(ctx, PermInvitationManage)
	var out []Invitation
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if siteID != nil {
			if _, err := authz.ScopeOfSite(ctx, tx, *siteID); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT * FROM (`+invSelect+` WHERE i.org_id = $1 AND i.id > $2 AND ($3 OR i.site_id = ANY($4))
			AND ($5::uuid IS NULL OR i.site_id = $5) AND i.kind <> 'site_code') x WHERE ($6 = '' OR x.status = $6) ORDER BY x.id LIMIT $7`,
			orgID, after, orgWide, sites, siteID, status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInv(rows)
			if err != nil {
				return err
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list invitations")
	}
	items, next := tenancy.Page(out, limit, func(i Invitation) uuid.UUID { return i.ID })
	return items, next, nil
}

func (s *Service) inviteText(locale, instance, site, link string) (subject, body string) {
	if locale == "en" {
		return instance + ": you are invited to " + site,
			fmt.Sprintf("%s: you have been invited to %s. Accept the invitation: %s", instance, site, link)
	}
	return instance + ": " + site + " davetiyesi",
		fmt.Sprintf("%s: %s sitesine davet edildiniz. Davetiyeyi kabul etmek icin: %s", instance, site, link)
}

// deliver sends the invitation link over its channel and records the attempt.
func (s *Service) deliver(ctx context.Context, orgID, invID uuid.UUID, channel string, email, phone *string, locale, site, link string) string {
	subject, body := s.inviteText(locale, s.cfg.InstanceName, site, link)
	var err error
	switch channel {
	case "sms":
		if s.cfg.SMS == nil || phone == nil {
			return "failed"
		}
		_, err = s.cfg.SMS.Send(sms.WithOrg(ctx, orgID), *phone, body)
	case "email":
		if s.cfg.Mail == nil || email == nil {
			return "failed"
		}
		err = s.cfg.Mail.Send(ctx, mail.Message{To: *email, Subject: subject, Body: body})
	default:
		return "none"
	}
	if err != nil {
		s.log.Warn("invitation delivery failed", "invitation", invID, "channel", channel, "error", err)
		return "failed"
	}
	if derr := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE invitation SET send_count = send_count + 1, last_sent_at = now(), updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, invID)
		return e
	}); derr != nil {
		s.log.Warn("record invitation delivery", "invitation", invID, "error", derr)
	}
	return "sent"
}

func (s *Service) channelAvailable(channel string) error {
	switch channel {
	case "sms":
		if s.cfg.SMS == nil {
			return problem.New(503, problem.CodeSMSUnavailable, "SMS delivery is not configured on this server")
		}
	case "email":
		if s.cfg.Mail == nil {
			return problem.New(503, problem.CodeMailUnavailable, "email delivery is not configured on this server")
		}
	case "link":
	default:
		return fieldProblem("channel", "invalid_value")
	}
	return nil
}

// inviteInput is the validated body of a manager or household invitation.
type inviteInput struct {
	siteID, unitID, personID *uuid.UUID
	person                   personInput
	kind                     string // occupancy kind
	side                     string
	sharePct                 *float64
	correspondent, resides   bool
	from                     *time.Time
	channel                  string
	days                     int
	client                   string
	locale                   string
}

func (s *Service) parseInvite(d Doc, household bool) (inviteInput, error) {
	allowed := []string{"unit_id", "person_id", "display_name", "email", "phone", "occupancy_kind", "side", "share_pct", "is_correspondent", "resides", "valid_from", "channel", "expires_in_days", "client", "locale"}
	if !household {
		allowed = append(allowed, "site_id")
	}
	fields := d.Unknown(allowed...)
	in := inviteInput{resides: true, days: DefaultInviteDays, client: "web", locale: "tr"}
	getID := func(k string) (*uuid.UUID, error) {
		if !d.Has(k) || d.IsNull(k) {
			return nil, nil //nolint:nilnil // absent
		}
		var v uuid.UUID
		if err := d.Get(k, &v); err != nil {
			return nil, err
		}
		return &v, nil
	}
	var err error
	if in.siteID, err = getID("site_id"); err != nil {
		return in, err
	}
	if in.unitID, err = getID("unit_id"); err != nil {
		return in, err
	}
	if in.personID, err = getID("person_id"); err != nil {
		return in, err
	}
	if in.personID == nil {
		p, perr := s.parsePerson(d, true, "TR", append(append([]string{}, allowed...), personFields...)...)
		if perr != nil {
			// only report the person fields that belong to it
			return in, perr
		}
		in.person = p
		if p.email == nil && p.phone == nil {
			fields = append(fields, problem.Field{Field: "phone", Code: "required"})
		}
	}
	if household {
		in.kind = KindHousehold
	} else if err := d.Get("occupancy_kind", &in.kind); err != nil {
		return in, err
	} else if in.kind == "" && in.personID != nil {
		// an invitation of an existing person without an occupancy only links their account
	} else if !slices.Contains([]string{KindOwner, KindTenant, KindHousehold}, in.kind) {
		fields = append(fields, problem.Field{Field: "occupancy_kind", Code: map[bool]string{true: "required", false: "invalid_value"}[in.kind == ""]})
	}
	if in.unitID == nil && !household && in.kind != "" {
		fields = append(fields, problem.Field{Field: "unit_id", Code: "required"})
	}
	if err := d.Get("side", &in.side); err != nil {
		return in, err
	}
	if d.Has("share_pct") && !d.IsNull("share_pct") {
		var v float64
		if err := d.Get("share_pct", &v); err != nil {
			return in, err
		}
		if v <= 0 || v > 100 || in.kind != KindOwner {
			fields = append(fields, problem.Field{Field: "share_pct", Code: "invalid_value"})
		}
		in.sharePct = &v
	}
	if err := d.Get("is_correspondent", &in.correspondent); err != nil {
		return in, err
	}
	if err := d.Get("resides", &in.resides); err != nil {
		return in, err
	}
	if in.from, err = parseDate(d, "valid_from", nil); err != nil {
		return in, err
	}
	if err := d.Get("channel", &in.channel); err != nil {
		return in, err
	}
	if err := d.Get("expires_in_days", &in.days); err != nil {
		return in, err
	}
	if in.days < 1 || in.days > 90 {
		fields = append(fields, problem.Field{Field: "expires_in_days", Code: "invalid_value"})
	}
	if err := d.Get("locale", &in.locale); err != nil {
		return in, err
	}
	if err := d.Get("client", &in.client); err != nil {
		return in, err
	}
	if len(fields) > 0 {
		return in, problem.Validation(fields...)
	}
	return in, nil
}
