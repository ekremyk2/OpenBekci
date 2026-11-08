package billing

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// AccrualRow is a unit side's late fee position as of a day (the accrual list).
type AccrualRow struct {
	UnitID        uuid.UUID   `json:"unit_id"`
	DoorNumber    string      `json:"unit_door_number"`
	DebtorKind    string      `json:"debtor_kind"`
	AsOf          string      `json:"as_of"`
	OpenPrincipal money.Money `json:"open_principal"`
	Accrued       money.Money `json:"accrued"`
	Booked        money.Money `json:"booked"`
	Frozen        bool        `json:"frozen"`
	Waived        money.Money `json:"waived"`
}

// ListLateFeeAccruals computes the late fee of every unit side with principal charges, on demand (money.read);
// the cursor is an offset into the list ordered by door number.
func (s *Service) ListLateFeeAccruals(ctx context.Context, orgID, siteID uuid.UUID, unit *uuid.UUID, kind, asOf string, nonzero bool, p ledger.PageParams) ([]AccrualRow, string, error) {
	limit, after, err := offsetPage(p)
	if err != nil {
		return nil, "", err
	}
	var out []AccrualRow
	more := false
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		if asOf == "" {
			asOf = ledger.Today(site.Timezone, s.Now())
		} else if _, err := ledger.ParseDate(asOf); err != nil {
			return bad("as_of", "invalid_format")
		}
		cur := ledger.Currency(site)
		rows, err := tx.Query(ctx, `SELECT DISTINCT c.unit_id, u.door_number, c.debtor_kind FROM charge c JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id
			WHERE c.org_id=$1 AND c.site_id=$2 AND c.kind='principal' AND c.debtor_kind IN ('owner','tenant') AND c.cancel_entry_id IS NULL
			  AND ($3::uuid IS NULL OR c.unit_id = $3) AND ($4 = '' OR c.debtor_kind = $4) ORDER BY u.door_number, c.unit_id, c.debtor_kind`, orgID, siteID, unit, kind)
		if err != nil {
			return err
		}
		type side struct {
			unit       uuid.UUID
			door, kind string
		}
		var sides []side
		for rows.Next() {
			var sd side
			if err := rows.Scan(&sd.unit, &sd.door, &sd.kind); err != nil {
				rows.Close()
				return err
			}
			sides = append(sides, sd)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		skipped := 0
		for _, sd := range sides {
			u := sd.unit
			d := Debtor{UnitID: &u, Kind: sd.kind}
			acc, _, _, err := accrue(ctx, tx, orgID, site, d, asOf)
			if err != nil {
				return err
			}
			if nonzero && acc.Accrued.Amount == 0 && acc.Booked.Amount == 0 {
				continue
			}
			if skipped < after {
				skipped++
				continue
			}
			if len(out) == limit {
				more = true
				break
			}
			charges, err := LoadCharges(ctx, tx, orgID, siteID, d)
			if err != nil {
				return err
			}
			principal, _ := OpenTotal(charges)
			out = append(out, AccrualRow{UnitID: sd.unit, DoorNumber: sd.door, DebtorKind: sd.kind, AsOf: asOf, OpenPrincipal: money.New(principal, cur), Accrued: acc.Accrued,
				Booked: acc.Booked, Frozen: acc.Frozen, Waived: acc.Waived})
		}
		return nil
	})
	next := ""
	if more {
		next = strconv.Itoa(after + len(out))
	}
	return out, next, err
}

func offsetPage(p ledger.PageParams) (limit, after int, err error) {
	limit = p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if p.Cursor != "" {
		if after, err = strconv.Atoi(p.Cursor); err != nil || after < 0 {
			return 0, 0, bad("cursor", "invalid_value")
		}
	}
	return limit, after, nil
}

// BookingRow is one late fee booking.
type BookingRow struct {
	ID            uuid.UUID   `json:"id"`
	SiteID        uuid.UUID   `json:"site_id"`
	UnitID        uuid.UUID   `json:"unit_id"`
	DoorNumber    string      `json:"unit_door_number"`
	UnitLabel     string      `json:"unit_label"`
	BlockName     *string     `json:"block_name"`
	DebtorKind    string      `json:"debtor_kind"`
	Period        string      `json:"period"`
	Kind          string      `json:"kind"`
	Amount        money.Money `json:"amount"`
	ChargeID      *uuid.UUID  `json:"charge_id"`
	ExpectedTo    money.Money `json:"expected_to_date"`
	BookedTo      money.Money `json:"booked_to_date"`
	Delta         money.Money `json:"delta"`
	CreatedAt     time.Time   `json:"created_at"`
	runningBefore int64
}

// ListLateFeeBookings lists the fee bookings of a site (money.read), oldest first.
func (s *Service) ListLateFeeBookings(ctx context.Context, orgID, siteID uuid.UUID, unit *uuid.UUID, kind, period string, p ledger.PageParams) ([]BookingRow, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []BookingRow
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		rows, err := tx.Query(ctx, `SELECT b.id, b.site_id, b.unit_id, u.door_number, u.door_number || COALESCE(' / ' || bl.name, ''), CASE WHEN bl.hidden THEN NULL ELSE bl.name END, b.debtor_kind, b.period, b.kind, b.amount, b.charge_id, b.created_at,
				COALESCE((SELECT SUM(x.amount) FROM late_fee_booking x WHERE x.org_id = b.org_id AND x.unit_id = b.unit_id AND x.debtor_kind = b.debtor_kind AND (x.created_at, x.id) <= (b.created_at, b.id)), 0)
			FROM late_fee_booking b JOIN unit u ON u.org_id = b.org_id AND u.id = b.unit_id LEFT JOIN block bl ON bl.org_id = u.org_id AND bl.id = u.block_id
			WHERE b.org_id=$1 AND b.site_id=$2 AND b.debtor_kind IN ('owner','tenant') AND b.id > $3 AND ($4::uuid IS NULL OR b.unit_id = $4) AND ($5 = '' OR b.debtor_kind = $5) AND ($6 = '' OR b.period = $6)
			ORDER BY b.id LIMIT $7`, orgID, siteID, after, unit, kind, period, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b BookingRow
			var amt, cum int64
			if err := rows.Scan(&b.ID, &b.SiteID, &b.UnitID, &b.DoorNumber, &b.UnitLabel, &b.BlockName, &b.DebtorKind, &b.Period, &b.Kind, &amt, &b.ChargeID, &b.CreatedAt, &cum); err != nil {
				return err
			}
			b.CreatedAt = b.CreatedAt.UTC()
			b.Amount, b.BookedTo, b.ExpectedTo, b.Delta = money.New(amt, cur), money.New(cum, cur), money.New(cum, cur), money.New(amt, cur)
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(b BookingRow) uuid.UUID { return b.ID })
	return items, next, nil
}

// WaiverRow is a recorded waiver as the API shows it.
type WaiverRow struct {
	ID         uuid.UUID   `json:"id"`
	SiteID     uuid.UUID   `json:"site_id"`
	UnitID     uuid.UUID   `json:"unit_id"`
	DebtorKind string      `json:"debtor_kind"`
	Amount     money.Money `json:"amount"`
	Reason     string      `json:"reason"`
	By         uuid.UUID   `json:"by"`
	EntryID    uuid.UUID   `json:"journal_entry_id"`
	CreatedAt  time.Time   `json:"created_at"`
}

const waiverSel = `SELECT w.id, w.site_id, w.unit_id, w.debtor_kind, w.amount, w.reason, COALESCE(w.by_user_id, '00000000-0000-0000-0000-000000000000'::uuid), w.entry_id, w.created_at, s.currency
	FROM late_fee_waiver w JOIN site s ON s.org_id = w.org_id AND s.id = w.site_id`

func scanWaiver(r pgx.Row) (w WaiverRow, err error) {
	var amt int64
	var cur string
	err = r.Scan(&w.ID, &w.SiteID, &w.UnitID, &w.DebtorKind, &amt, &w.Reason, &w.By, &w.EntryID, &w.CreatedAt, &cur)
	w.Amount = money.New(amt, money.Code(cur))
	w.CreatedAt = w.CreatedAt.UTC()
	return
}

// ListLateFeeWaivers lists the waivers of a site (money.read).
func (s *Service) ListLateFeeWaivers(ctx context.Context, orgID, siteID uuid.UUID, unit *uuid.UUID, kind string, p ledger.PageParams) ([]WaiverRow, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []WaiverRow
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, waiverSel+` WHERE w.org_id=$1 AND w.site_id=$2 AND w.id > $3 AND ($4::uuid IS NULL OR w.unit_id = $4) AND ($5 = '' OR w.debtor_kind = $5) ORDER BY w.id LIMIT $6`,
			orgID, siteID, after, unit, kind, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			w, err := scanWaiver(rows)
			if err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(w WaiverRow) uuid.UUID { return w.ID })
	return items, next, nil
}

// GetLateFeeWaiver returns a waiver (money.read).
func (s *Service) GetLateFeeWaiver(ctx context.Context, orgID, waiverID uuid.UUID) (WaiverRow, error) {
	var w WaiverRow
	err := s.run(ctx, orgID, "waiver", func(tx pgx.Tx) (err error) {
		if w, err = scanWaiver(tx.QueryRow(ctx, waiverSel+` WHERE w.org_id=$1 AND w.id=$2`, orgID, waiverID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, w.SiteID)
	})
	return w, err
}

// FreezeRow is a late fee freeze as the API shows it.
type FreezeRow struct {
	ID         uuid.UUID  `json:"id"`
	SiteID     uuid.UUID  `json:"site_id"`
	UnitID     uuid.UUID  `json:"unit_id"`
	DebtorKind string     `json:"debtor_kind"`
	From       string     `json:"from"`
	To         *string    `json:"to"`
	Reason     string     `json:"reason"`
	CaseID     *uuid.UUID `json:"case_id"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Version    int        `json:"version"`
}

const freezeSel = `SELECT id, site_id, unit_id, debtor_kind, from_on::text, to_on::text, reason, case_id, created_at, updated_at, version FROM late_fee_freeze`

func scanFreeze(r pgx.Row) (f FreezeRow, err error) {
	err = r.Scan(&f.ID, &f.SiteID, &f.UnitID, &f.DebtorKind, &f.From, &f.To, &f.Reason, &f.CaseID, &f.CreatedAt, &f.UpdatedAt, &f.Version)
	f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
	return
}

// GetLateFeeFreeze returns a freeze (money.read).
func (s *Service) GetLateFeeFreeze(ctx context.Context, orgID, freezeID uuid.UUID) (FreezeRow, error) {
	var f FreezeRow
	err := s.run(ctx, orgID, "freeze", func(tx pgx.Tx) (err error) {
		if f, err = scanFreeze(tx.QueryRow(ctx, freezeSel+` WHERE org_id=$1 AND id=$2`, orgID, freezeID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, f.SiteID)
	})
	return f, err
}

// ListLateFeeFreezes lists the freezes of a site (money.read); active keeps those not ended.
func (s *Service) ListLateFeeFreezes(ctx context.Context, orgID, siteID uuid.UUID, unit *uuid.UUID, kind string, active *bool, p ledger.PageParams) ([]FreezeRow, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []FreezeRow
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		today := ledger.Today(site.Timezone, s.Now())
		rows, err := tx.Query(ctx, freezeSel+` WHERE org_id=$1 AND site_id=$2 AND id > $3 AND ($4::uuid IS NULL OR unit_id = $4) AND ($5 = '' OR debtor_kind = $5)
			AND ($6::boolean IS NULL OR $6 = (from_on <= $7::date AND (to_on IS NULL OR to_on >= $7::date))) ORDER BY id LIMIT $8`, orgID, siteID, after, unit, kind, active, today, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFreeze(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(f FreezeRow) uuid.UUID { return f.ID })
	return items, next, nil
}

// UpdateLateFeeFreeze ends a freeze or changes its reason (money.manage; version is the If-Match
// value). The start date cannot move: fees already booked stay as they are.
func (s *Service) UpdateLateFeeFreeze(ctx context.Context, orgID, freezeID uuid.UUID, version int, d tenancy.Doc) (FreezeRow, error) {
	var out FreezeRow
	err := s.run(ctx, orgID, "freeze", func(tx pgx.Tx) error {
		before, err := scanFreeze(tx.QueryRow(ctx, freezeSel+` WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, freezeID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != version {
			return tenancy.ErrVersion
		}
		if u := d.Unknown("to", "reason"); len(u) > 0 {
			return problem.Validation(u...)
		}
		next := before
		for k, dst := range map[string]any{"to": &next.To, "reason": &next.Reason} {
			if d.Has(k) {
				if err := d.Get(k, dst); err != nil {
					return err
				}
			}
		}
		if next.To != nil {
			if _, err := ledger.ParseDate(*next.To); err != nil || *next.To < next.From {
				return bad("to", "invalid_value")
			}
		}
		if out, err = scanFreeze(tx.QueryRow(ctx, `UPDATE late_fee_freeze SET to_on=$3::date, reason=$4, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2 RETURNING `+freezeSel[len("SELECT "):len(freezeSel)-len(" FROM late_fee_freeze")],
			orgID, freezeID, next.To, next.Reason)); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, out.SiteID, "late_fee_freeze.update", "late_fee_freeze", freezeID, before, out)
	})
	return out, err
}
