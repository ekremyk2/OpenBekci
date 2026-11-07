package community

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// VisitorCredential is one visitor credential as the access module projects
// it into its allow-list (access design 2.2): a visitor QR (the hash of the
// token, never the token) or a visitor plate. The visitor side owns the
// source rows; the access module derives credentials from this list and
// never edits them.
type VisitorCredential struct {
	// SourceType is "visitor_entry" or "recurring_visitor"; with SourceID it is
	// the credential's source key.
	SourceType string
	SourceID   uuid.UUID
	SiteID     uuid.UUID
	// Kind is "visitor_qr" (Value: hex SHA-256 of the token) or "plate"
	// (Value: the normalized plate).
	Kind  string
	Value string
	// ValidFrom and ValidTo are the window with the 30 minute slack applied;
	// for a recurring visitor they are the validity dates (start of the first
	// day, end of the last day, in the site zone) and ValidTo is nil while open.
	ValidFrom time.Time
	ValidTo   *time.Time
	// Recurring is the weekly rule of a recurring visitor, evaluated in
	// Timezone; nil for single-entry visitors.
	Recurring *RecurringRule
	Timezone  string
	// MaxUses is 1 for a single-entry visitor, nil when recurring.
	MaxUses *int
	// PointScope is "all" for a QR and "all_vehicle" for a plate.
	PointScope string
	// Active is false for a credential the access module must revoke:
	// cancelled, denied, expired or revoked sources.
	Active    bool
	UpdatedAt time.Time
}

// VisitorCredentials lists the visitor credentials of a site for the access
// module's access.project job. With a zero since it returns the active ones
// (the allow-list snapshot); otherwise every credential whose source changed
// at or after since, active or not (the delta, so revocations propagate). It
// is a system call without a signed-in caller; the access module calls it
// from its job in its own tenant transaction through VisitorCredentialsTx.
func (s *Service) VisitorCredentials(ctx context.Context, orgID, siteID uuid.UUID, since time.Time) ([]VisitorCredential, error) {
	var out []VisitorCredential
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = VisitorCredentialsTx(ctx, tx, orgID, siteID, since, s.now())
		return err
	})
	if err != nil {
		return nil, problem.Internal(err)
	}
	return out, nil
}

// VisitorCredentialsTx is VisitorCredentials inside the caller's tenant transaction.
func VisitorCredentialsTx(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, since, now time.Time) ([]VisitorCredential, error) {
	var tz string
	if err := tx.QueryRow(ctx, `SELECT timezone FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&tz); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	var out []VisitorCredential
	// single entries: pre-registered with a QR, walk-ins approved on the spot (plate only)
	rows, err := tx.Query(ctx, `SELECT id, qr_token_hash, plate, window_from, window_to, approval_status, cancelled, arrived_at, updated_at, recurring_visitor_id
		FROM visitor_entry WHERE org_id = $1 AND site_id = $2 AND recurring_visitor_id IS NULL AND ($3::timestamptz IS NULL OR updated_at >= $3) ORDER BY updated_at, id`,
		orgID, siteID, nilTime(since))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			id         uuid.UUID
			hash       []byte
			plate      *string
			from, to   time.Time
			status     string
			cancelled  bool
			arrived    *time.Time
			updated    time.Time
			recurrence *uuid.UUID
		)
		if err := rows.Scan(&id, &hash, &plate, &from, &to, &status, &cancelled, &arrived, &updated, &recurrence); err != nil {
			rows.Close()
			return nil, err
		}
		approved := status == "pre_approved" || status == "approved"
		active := approved && !cancelled && arrived == nil && now.Before(to.Add(visitorSlack))
		if since.IsZero() && !active {
			continue
		}
		vf, vt := from.Add(-visitorSlack).UTC(), to.Add(visitorSlack).UTC()
		one := 1
		base := VisitorCredential{SourceType: "visitor_entry", SourceID: id, SiteID: siteID, ValidFrom: vf, ValidTo: &vt, MaxUses: &one, Timezone: tz, Active: active, UpdatedAt: updated.UTC()}
		if len(hash) > 0 {
			c := base
			c.Kind, c.Value, c.PointScope = "visitor_qr", hex.EncodeToString(hash), "all"
			out = append(out, c)
		}
		if plate != nil {
			c := base
			c.Kind, c.Value, c.PointScope = "plate", *plate, "all_vehicle"
			out = append(out, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// recurring visitors: their own long-lived QR, plus the plate
	rrows, err := tx.Query(ctx, `SELECT id, qr_token_hash, plate, days, start_time, end_time, valid_from, valid_to, status, updated_at
		FROM recurring_visitor WHERE org_id = $1 AND site_id = $2 AND ($3::timestamptz IS NULL OR updated_at >= $3) ORDER BY updated_at, id`, orgID, siteID, nilTime(since))
	if err != nil {
		return nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var (
			id      uuid.UUID
			hash    []byte
			plate   *string
			days    []int16
			start   string
			end     string
			vfrom   time.Time
			vto     *time.Time
			status  string
			updated time.Time
		)
		if err := rrows.Scan(&id, &hash, &plate, &days, &start, &end, &vfrom, &vto, &status, &updated); err != nil {
			return nil, err
		}
		from := time.Date(vfrom.Year(), vfrom.Month(), vfrom.Day(), 0, 0, 0, 0, loc).UTC()
		var to *time.Time
		active := status == "active"
		if vto != nil {
			t := time.Date(vto.Year(), vto.Month(), vto.Day()+1, 0, 0, 0, 0, loc).UTC()
			to = &t
			if !now.Before(t) {
				active = false
			}
		}
		if since.IsZero() && !active {
			continue
		}
		rule := &RecurringRule{Days: make([]int, len(days)), Start: start, End: end}
		for i, d := range days {
			rule.Days[i] = int(d)
		}
		base := VisitorCredential{SourceType: "recurring_visitor", SourceID: id, SiteID: siteID, ValidFrom: from, ValidTo: to, Recurring: rule, Timezone: tz, Active: active, UpdatedAt: updated.UTC()}
		c := base
		c.Kind, c.Value, c.PointScope = "visitor_qr", hex.EncodeToString(hash), "all"
		out = append(out, c)
		if plate != nil {
			c := base
			c.Kind, c.Value, c.PointScope = "plate", *plate, "all_vehicle"
			out = append(out, c)
		}
	}
	return out, rrows.Err()
}

func nilTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
