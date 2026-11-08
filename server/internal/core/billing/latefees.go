package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/latefee"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SettingWaiverReauthMinor is the site setting (minor units) above which a late fee waiver needs re-authentication.
const SettingWaiverReauthMinor = "late_fee_waiver_reauth_minor"

// DefaultWaiverReauthMinor applies when the site does not set one.
const DefaultWaiverReauthMinor = 100000

// PolicyRow is one entry of the site's late fee schedule.
type PolicyRow struct {
	From            string `json:"from"`
	RateBPSPerMonth int    `json:"rate_bps_per_month"`
	GraceDays       int    `json:"grace_days"`
}

// SetLateFeePolicy adds a schedule entry taking effect on from (money.manage).
// The rate is capped at 500 bps per month (the statutory maximum is applied
// by the pack's preset); a later entry never changes fees already accrued (prospective).
func (s *Service) SetLateFeePolicy(ctx context.Context, orgID, siteID uuid.UUID, row PolicyRow) ([]PolicyRow, error) {
	if _, err := ledger.ParseDate(row.From); err != nil {
		return nil, bad("from", "invalid_format")
	}
	if row.RateBPSPerMonth < 0 || row.RateBPSPerMonth > latefee.MaxRateBPS {
		return nil, bad("rate_bps_per_month", "invalid_value")
	}
	if row.GraceDays < 0 {
		return nil, bad("grace_days", "invalid_value")
	}
	var out []PolicyRow
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, siteID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO late_fee_policy (id, org_id, site_id, from_on, rate_bps_per_month, grace_days) VALUES ($1,$2,$3,$4::date,$5,$6)
			ON CONFLICT (org_id, site_id, from_on) DO UPDATE SET rate_bps_per_month = EXCLUDED.rate_bps_per_month, grace_days = EXCLUDED.grace_days`,
			id.New(), orgID, siteID, row.From, row.RateBPSPerMonth, row.GraceDays); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, siteID, "late_fee_policy.set", "late_fee_policy", siteID, nil, row); err != nil {
			return err
		}
		var err error
		out, err = listPolicy(ctx, tx, orgID, siteID)
		return err
	})
	return out, err
}

func listPolicy(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) ([]PolicyRow, error) {
	rows, err := tx.Query(ctx, `SELECT from_on::text, rate_bps_per_month, grace_days FROM late_fee_policy WHERE org_id=$1 AND site_id=$2 ORDER BY from_on`, orgID, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PolicyRow{}
	for rows.Next() {
		var r PolicyRow
		if err := rows.Scan(&r.From, &r.RateBPSPerMonth, &r.GraceDays); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetLateFeePolicy returns the schedule (money.read).
func (s *Service) GetLateFeePolicy(ctx context.Context, orgID, siteID uuid.UUID) ([]PolicyRow, error) {
	var out []PolicyRow
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) (err error) {
		if err = ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, siteID); err != nil {
			return err
		}
		out, err = listPolicy(ctx, tx, orgID, siteID)
		return err
	})
	return out, err
}

func scheduleOf(rows []PolicyRow, cur money.Code, tz *time.Location) latefee.PolicySchedule {
	var sc latefee.PolicySchedule
	for i, r := range rows {
		from, _ := time.ParseInLocation(ledger.DateLayout, r.From, tz)
		pp := latefee.PolicyPeriod{From: from, Policy: latefee.Policy{Currency: cur, RateBPSPerMonth: int64(r.RateBPSPerMonth), GraceDays: r.GraceDays, Simple: true}}
		if i+1 < len(rows) {
			next, _ := time.ParseInLocation(ledger.DateLayout, rows[i+1].From, tz)
			to := next.AddDate(0, 0, -1)
			pp.To = &to
		}
		sc.Policies = append(sc.Policies, pp)
	}
	return sc
}

// Accrual is a debtor's late fee position.
type Accrual struct {
	Debtor    Debtor      `json:"debtor"`
	AsOf      string      `json:"as_of"`
	Accrued   money.Money `json:"accrued"`
	Waived    money.Money `json:"waived"`
	Expected  money.Money `json:"expected"`
	Booked    money.Money `json:"booked"`
	Unbooked  money.Money `json:"unbooked"`
	PerCharge []ChargeFee `json:"per_charge"`
	Frozen    bool        `json:"frozen"`
}

// ChargeFee is the fee accrued on one charge.
type ChargeFee struct {
	ChargeID uuid.UUID   `json:"charge_id"`
	Fee      money.Money `json:"fee"`
}

type bookingRow struct {
	period, kind string
	amount       int64
}

// accrue computes the fee to date of a unit side from open principal and dates.
func accrue(ctx context.Context, tx pgx.Tx, org uuid.UUID, site tenancy.Site, d Debtor, asOf string) (Accrual, []bookingRow, latefee.Expectation, error) {
	siteID := site.ID
	cur := ledger.Currency(site)
	tz := ledger.Location(site.Timezone)
	acc := Accrual{Debtor: d, AsOf: asOf, Accrued: money.Zero(cur), Waived: money.Zero(cur), Expected: money.Zero(cur), Booked: money.Zero(cur), Unbooked: money.Zero(cur), PerCharge: []ChargeFee{}}
	pol, err := listPolicy(ctx, tx, org, siteID)
	if err != nil {
		return acc, nil, latefee.Expectation{}, err
	}
	if len(pol) == 0 || d.IsPerson() {
		return acc, nil, latefee.Expectation{Fee: acc.Expected, Waived: acc.Waived, Surplus: acc.Waived}, nil
	}
	charges, err := LoadCharges(ctx, tx, org, siteID, d)
	if err != nil {
		return acc, nil, latefee.Expectation{}, err
	}
	var ocs []latefee.OpenCharge
	for _, c := range charges {
		if c.Kind != "principal" || c.Cancelled {
			continue
		}
		due, _ := time.ParseInLocation(ledger.DateLayout, c.DueDate, tz)
		oc := latefee.OpenCharge{ID: c.ID, Kind: latefee.KindPrincipal, DueDate: due, Principal: money.New(c.Amount, cur)}
		for _, a := range c.Allocs {
			on, _ := time.ParseInLocation(ledger.DateLayout, a.On, tz)
			oc.PaidPrincipal = append(oc.PaidPrincipal, latefee.Payment{On: on, Amount: money.New(a.Amount, cur)})
		}
		ocs = append(ocs, oc)
	}
	var freezes []latefee.Freeze
	frows, err := tx.Query(ctx, `SELECT from_on::text, to_on::text FROM late_fee_freeze WHERE org_id=$1 AND site_id=$2 AND unit_id=$3 AND debtor_kind=$4`, org, siteID, *d.UnitID, d.Kind)
	if err != nil {
		return acc, nil, latefee.Expectation{}, err
	}
	for frows.Next() {
		var f string
		var t *string
		if err := frows.Scan(&f, &t); err != nil {
			frows.Close()
			return acc, nil, latefee.Expectation{}, err
		}
		fr := latefee.Freeze{}
		fr.From, _ = time.ParseInLocation(ledger.DateLayout, f, tz)
		if t != nil {
			x, _ := time.ParseInLocation(ledger.DateLayout, *t, tz)
			fr.To = &x
		}
		freezes = append(freezes, fr)
	}
	frows.Close()
	asOfT, _ := time.ParseInLocation(ledger.DateLayout, asOf, tz)
	ac, err := latefee.Accrue(scheduleOf(pol, cur, tz), ocs, asOfT, tz, freezes...)
	if err != nil {
		return acc, nil, latefee.Expectation{}, fmt.Errorf("accrue late fee: %w", err)
	}
	brows, err := tx.Query(ctx, `SELECT period, kind, amount FROM late_fee_booking WHERE org_id=$1 AND site_id=$2 AND unit_id=$3 AND debtor_kind=$4 ORDER BY created_at, id`, org, siteID, *d.UnitID, d.Kind)
	if err != nil {
		return acc, nil, latefee.Expectation{}, err
	}
	var bs []bookingRow
	var lb []latefee.Booking
	for brows.Next() {
		var b bookingRow
		if err := brows.Scan(&b.period, &b.kind, &b.amount); err != nil {
			brows.Close()
			return acc, nil, latefee.Expectation{}, err
		}
		bs = append(bs, b)
		lb = append(lb, latefee.Booking{Period: b.period, Kind: latefee.BookingKind(b.kind), Amount: money.New(b.amount, cur)})
	}
	brows.Close()
	exp, err := latefee.Expected(ac, lb)
	if err != nil {
		return acc, nil, exp, err
	}
	acc.Accrued, acc.Waived, acc.Expected = ac.Total, exp.Waived, exp.Fee
	for _, pc := range ac.PerCharge {
		acc.PerCharge = append(acc.PerCharge, ChargeFee{ChargeID: pc.ChargeID, Fee: pc.Fee})
	}
	acc.Frozen = len(freezes) > 0
	delta, err := latefee.Reconcile(exp.Fee, lb)
	if err != nil {
		return acc, bs, exp, err
	}
	acc.Booked, acc.Unbooked = delta.Booked, delta.Amount
	return acc, bs, exp, nil
}

// AccrueLateFee computes a unit side's late fee to date on demand, without
// writing anything (money.read, rule 8).
func (s *Service) AccrueLateFee(ctx context.Context, orgID, siteID uuid.UUID, d Debtor, asOf string) (Accrual, error) {
	if err := d.Validate(); err != nil {
		return Accrual{}, err
	}
	var out Accrual
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		if asOf == "" {
			asOf = ledger.Today(site.Timezone, s.Now())
		}
		out, _, _, err = accrue(ctx, tx, orgID, site, d, asOf)
		return err
	})
	return out, err
}

// reconcile posts the delta between the fee expected to date and the booked
// fee as a fee charge (positive) or a reversal (negative). It returns the
// journal entry it posted (uuid.Nil when nothing was needed).
func (s *Service) reconcile(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, ch ledger.Chart, d Debtor, today string) (uuid.UUID, int64, error) {
	acc, _, _, err := accrue(ctx, tx, orgID, site, d, today)
	if err != nil || acc.Unbooked.Amount == 0 {
		return uuid.Nil, 0, err
	}
	period := today[:7]
	amt := acc.Unbooked.Amount
	var person *uuid.UUID
	var entry uuid.UUID
	var chargeID *uuid.UUID
	if amt > 0 {
		cid, err := postCharge(ctx, tx, ch, chargeIn{OrgID: orgID, SiteID: site.ID, UnitID: *d.UnitID, FundID: ch.OperatingFund, Period: period, Kind: "fee", ChargeType: "late_fee",
			DebtorKind: d.Kind, PersonID: person, Amount: amt, DueDate: today, EntryDate: today, Description: "Late fee " + period, CreditAccount: ch.Account(ledger.SubLateFeeIncome), SourceType: "late_fee"})
		if err != nil {
			return uuid.Nil, 0, err
		}
		chargeID = &cid
		if err := tx.QueryRow(ctx, `SELECT journal_entry_id FROM charge WHERE org_id=$1 AND id=$2`, orgID, cid).Scan(&entry); err != nil {
			return uuid.Nil, 0, err
		}
	} else {
		// a reversal: income back, receivable down; recorded as a negative fee row
		cid := id.New()
		entry, err = ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: site.ID, EntryDate: today, Memo: "Late fee reversal " + period, SourceType: "late_fee.reversal", SourceID: &cid, Lines: []ledger.Line{
			{AccountID: ch.Account(ledger.SubLateFeeIncome), FundID: ch.OperatingFund, Debit: -amt, UnitID: d.UnitID, DebtorKind: d.Kind},
			{AccountID: ch.Account(ledger.ReceivableSub(d.Kind)), FundID: ch.OperatingFund, Credit: -amt, UnitID: d.UnitID, DebtorKind: d.Kind},
		}})
		if err != nil {
			return uuid.Nil, 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO charge (id, org_id, site_id, unit_id, period, kind, charge_type, debtor_kind, amount, due_date, description, fund_id, journal_entry_id, status)
			VALUES ($1,$2,$3,$4,$5,'fee','late_fee_reversal',$6,$7,$8::date,$9,$10,$11,'paid')`,
			cid, orgID, site.ID, *d.UnitID, period, d.Kind, amt, today, "Late fee reversal "+period, ch.OperatingFund, entry); err != nil {
			return uuid.Nil, 0, err
		}
		chargeID = &cid
	}
	kind := "fee"
	if amt < 0 {
		kind = "reversal"
	}
	_, err = tx.Exec(ctx, `INSERT INTO late_fee_booking (id, org_id, site_id, unit_id, debtor_kind, period, kind, amount, charge_id, entry_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id.New(), orgID, site.ID, *d.UnitID, d.Kind, period, kind, amt, chargeID, entry)
	return entry, amt, err
}

// CrystallizeLateFees reconciles the fee bookings of every unit side of the
// site with the fee expected to date (the hourly job, rule 8): it books new
// fees and posts reversals after backdated payments, and is idempotent. It
// returns the number of postings made.
func (s *Service) CrystallizeLateFees(ctx context.Context, orgID, siteID uuid.UUID) (int, error) {
	n := 0
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, siteID)
		if err != nil {
			return err
		}
		pol, err := listPolicy(ctx, tx, orgID, siteID)
		if err != nil || len(pol) == 0 {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		today := ledger.Today(site.Timezone, s.Now())
		rows, err := tx.Query(ctx, `SELECT DISTINCT unit_id, debtor_kind FROM charge WHERE org_id=$1 AND site_id=$2 AND kind='principal' AND debtor_kind IN ('owner','tenant')
			AND cancel_entry_id IS NULL ORDER BY 1, 2`, orgID, siteID)
		if err != nil {
			return err
		}
		var sides []Debtor
		for rows.Next() {
			var u uuid.UUID
			var k string
			if err := rows.Scan(&u, &k); err != nil {
				rows.Close()
				return err
			}
			uu := u
			sides = append(sides, Debtor{UnitID: &uu, Kind: k})
		}
		rows.Close()
		for _, d := range sides {
			e, _, err := s.reconcile(ctx, tx, orgID, site, ch, d, today)
			if err != nil {
				return err
			}
			if e != uuid.Nil {
				n++
			}
		}
		if n > 0 {
			return ledger.Audit(ctx, tx, orgID, siteID, "late_fee.crystallize", "charge", siteID, nil, map[string]any{"postings": n})
		}
		return nil
	})
	return n, err
}

// WaiveInput waives part of a unit side's unpaid late fee.
type WaiveInput struct {
	SiteID     uuid.UUID `json:"site_id"`
	UnitID     uuid.UUID `json:"unit_id"`
	DebtorKind string    `json:"debtor_kind"`
	Amount     int64     `json:"amount"`
	Reason     string    `json:"reason"`
}

// Waiver is a recorded waiver.
type Waiver struct {
	ID         uuid.UUID `json:"id"`
	UnitID     uuid.UUID `json:"unit_id"`
	DebtorKind string    `json:"debtor_kind"`
	Amount     int64     `json:"amount"`
	Reason     string    `json:"reason"`
	EntryID    uuid.UUID `json:"journal_entry_id"`
}

// WaiveLateFee waives up to the unpaid late fee of a unit side (money.post;
// above the site's threshold it needs re-authentication, section 16). The fee
// is crystallized first; the waiver is recorded and the reversal posted at once.
func (s *Service) WaiveLateFee(ctx context.Context, orgID uuid.UUID, in WaiveInput) (Waiver, error) {
	if in.Amount <= 0 || in.Reason == "" || (in.DebtorKind != "owner" && in.DebtorKind != "tenant") {
		return Waiver{}, bad("amount", "invalid_value")
	}
	var w Waiver
	err := s.run(ctx, orgID, "waiver", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		limit := int64(DefaultWaiverReauthMinor)
		if v, ok := site.Settings[SettingWaiverReauthMinor].(float64); ok {
			limit = int64(v)
		}
		if in.Amount > limit {
			if err := authz.PrincipalFromContext(ctx).RequireReauth(s.Now()); err != nil {
				return err
			}
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		d := Debtor{UnitID: &in.UnitID, Kind: in.DebtorKind}
		today := ledger.Today(site.Timezone, s.Now())
		if _, _, err := s.reconcile(ctx, tx, orgID, site, ch, d, today); err != nil {
			return err
		}
		charges, err := LoadCharges(ctx, tx, orgID, in.SiteID, d)
		if err != nil {
			return err
		}
		_, unpaidFee := OpenTotal(charges)
		if in.Amount > unpaidFee {
			return problem.Conflict("the waiver is larger than the unpaid late fee")
		}
		w = Waiver{ID: id.New(), UnitID: in.UnitID, DebtorKind: in.DebtorKind, Amount: in.Amount, Reason: in.Reason}
		// the waiver booking first: Expected then asks the reconciliation for the reversal
		period := today[:7]
		var prior uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT entry_id FROM late_fee_booking WHERE org_id=$1 AND site_id=$2 AND unit_id=$3 AND debtor_kind=$4 ORDER BY created_at DESC, id DESC LIMIT 1`,
			orgID, in.SiteID, in.UnitID, in.DebtorKind).Scan(&prior); err != nil {
			return problem.Conflict("there is no late fee to waive")
		}
		bookingID := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO late_fee_booking (id, org_id, site_id, unit_id, debtor_kind, period, kind, amount, entry_id) VALUES ($1,$2,$3,$4,$5,$6,'waiver',$7,$8)`,
			bookingID, orgID, in.SiteID, in.UnitID, in.DebtorKind, period, in.Amount, prior); err != nil {
			return err
		}
		e, _, err := s.reconcile(ctx, tx, orgID, site, ch, d, today)
		if err != nil {
			return err
		}
		if e == uuid.Nil {
			e = prior
		}
		w.EntryID = e
		if _, err := tx.Exec(ctx, `UPDATE late_fee_booking SET entry_id=$3 WHERE org_id=$1 AND id=$2`, orgID, bookingID, e); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO late_fee_waiver (id, org_id, site_id, unit_id, debtor_kind, amount, reason, by_user_id, entry_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			w.ID, orgID, in.SiteID, in.UnitID, in.DebtorKind, in.Amount, in.Reason, ledger.Actor(ctx), e); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "late_fee.waive", "late_fee_waiver", w.ID, nil, map[string]any{"unit_id": in.UnitID, "debtor_kind": in.DebtorKind, "amount": in.Amount, "reason": in.Reason})
	})
	return w, err
}

// WaiverThreshold returns the amount (minor units) above which a late fee
// waiver needs re-authentication (the site setting, or the default).
func (s *Service) WaiverThreshold(ctx context.Context, orgID, siteID uuid.UUID) (int64, error) {
	limit := int64(DefaultWaiverReauthMinor)
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		if v, ok := site.Settings[SettingWaiverReauthMinor].(float64); ok {
			limit = int64(v)
		}
		return nil
	})
	return limit, err
}

// SetWaiverThreshold sets the re-authentication threshold of late fee waivers (money.manage).
func (s *Service) SetWaiverThreshold(ctx context.Context, orgID, siteID uuid.UUID, minor int64) error {
	if minor < 0 {
		return bad("waiver_reauth_threshold", "invalid_value")
	}
	return s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyManage, orgID, siteID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE site SET settings = jsonb_set(settings, $3, to_jsonb($4::bigint)), updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, siteID, []string{SettingWaiverReauthMinor}, minor); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, siteID, "late_fee_policy.threshold", "late_fee_policy", siteID, nil, map[string]any{"waiver_reauth_minor": minor})
	})
}

// AccrueTx computes the late fee position of a unit side inside an existing
// transaction (the caller has checked permissions); statements use it to show
// the accrued but unbooked fee.
func AccrueTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, d Debtor, asOf string) (Accrual, error) {
	acc, _, _, err := accrue(ctx, tx, orgID, site, d, asOf)
	return acc, err
}

// FreezeInput stops late fee accrual for a unit side over a period (legal collection).
type FreezeInput struct {
	SiteID     uuid.UUID `json:"site_id"`
	UnitID     uuid.UUID `json:"unit_id"`
	DebtorKind string    `json:"debtor_kind"`
	From       string    `json:"from"`
	To         *string   `json:"to"`
	Reason     string    `json:"reason"`
}

// CreateLateFeeFreeze records a freeze (money.post).
func (s *Service) CreateLateFeeFreeze(ctx context.Context, orgID uuid.UUID, in FreezeInput) (uuid.UUID, error) {
	if _, err := ledger.ParseDate(in.From); err != nil || (in.DebtorKind != "owner" && in.DebtorKind != "tenant") || (in.To != nil && *in.To < in.From) {
		return uuid.Nil, bad("from", "invalid_value")
	}
	fid := id.New()
	err := s.run(ctx, orgID, "freeze", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO late_fee_freeze (id, org_id, site_id, unit_id, debtor_kind, from_on, to_on, reason) VALUES ($1,$2,$3,$4,$5,$6::date,$7::date,$8)`,
			fid, orgID, in.SiteID, in.UnitID, in.DebtorKind, in.From, in.To, in.Reason); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, in.SiteID, "late_fee_freeze.create", "late_fee_freeze", fid, nil, in)
	})
	return fid, err
}
