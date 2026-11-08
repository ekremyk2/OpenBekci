package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/rates"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// chargeIn is a charge to create with its journal entry.
type chargeIn struct {
	OrgID, SiteID, UnitID, FundID uuid.UUID
	PlanID                        *uuid.UUID
	Period, Kind, ChargeType      string
	DebtorKind                    string // owner, tenant or person
	PersonID                      *uuid.UUID
	Amount                        int64
	DueDate, EntryDate            string
	Description                   string
	Basis                         json.RawMessage
	CreditAccount                 uuid.UUID // income, deposit, late fee income or opening equity
	SourceType                    string
}

// postCharge posts debit receivable / credit CreditAccount and inserts the charge.
func postCharge(ctx context.Context, tx pgx.Tx, ch ledger.Chart, c chargeIn) (uuid.UUID, error) {
	chargeID := id.New()
	d := Debtor{UnitID: &c.UnitID, Kind: c.DebtorKind}
	if c.DebtorKind == "person" {
		d = Debtor{PersonID: c.PersonID}
	}
	debit := d.Line(ledger.Line{AccountID: ch.Account(d.ReceivableSubtype()), FundID: c.FundID, Debit: c.Amount}, c.PersonID)
	if d.IsPerson() {
		debit.UnitID = &c.UnitID
	}
	credit := ledger.Line{AccountID: c.CreditAccount, FundID: c.FundID, Credit: c.Amount, UnitID: &c.UnitID}
	if c.DebtorKind != "person" {
		credit.DebtorKind = c.DebtorKind
	}
	eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: c.OrgID, SiteID: c.SiteID, EntryDate: c.EntryDate, DocumentDate: c.DueDate, Memo: c.Description,
		SourceType: c.SourceType, SourceID: &chargeID, Lines: []ledger.Line{debit, credit}})
	if err != nil {
		return uuid.Nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO charge (id, org_id, site_id, plan_id, unit_id, period, kind, charge_type, debtor_kind, debtor_person_id, amount, due_date, description, allocation_basis, fund_id, journal_entry_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13,$14,$15,$16)`,
		chargeID, c.OrgID, c.SiteID, c.PlanID, c.UnitID, c.Period, c.Kind, c.ChargeType, c.DebtorKind, c.PersonID, c.Amount, c.DueDate, c.Description, nullJSON(c.Basis), c.FundID, eid)
	return chargeID, err
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// GenerateCharges creates every missing (plan, unit, period) charge of the
// site up to today (the hourly catch-up job, rule 7). It is idempotent: a
// period already billed is skipped, so running it twice creates nothing new.
// It returns how many charges were created. It needs money.post (jobs run
// with ledger.SystemContext).
func (s *Service) GenerateCharges(ctx context.Context, orgID, siteID uuid.UUID) (int, error) {
	var planIDs []uuid.UUID
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		var err error
		if _, err = ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM charge_plan WHERE org_id=$1 AND site_id=$2 AND status='active' ORDER BY id`, orgID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var pid uuid.UUID
			if err := rows.Scan(&pid); err != nil {
				return err
			}
			planIDs = append(planIDs, pid)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, pid := range planIDs {
		n, err := s.generatePlan(ctx, orgID, siteID, pid)
		if err != nil {
			return total, fmt.Errorf("plan %s: %w", pid, err)
		}
		total += n
	}
	return total, nil
}

func (s *Service) generatePlan(ctx context.Context, orgID, siteID, planID uuid.UUID) (int, error) {
	created := 0
	err := s.run(ctx, orgID, "plan", func(tx pgx.Tx) error {
		p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, planID))
		if err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false)
		if err != nil {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		tz := ledger.Location(site.Timezone)
		today := ledger.Today(site.Timezone, s.Now())
		pers, err := periodsOf(p, today, tz)
		if err != nil {
			return err
		}
		sc, err := loadSchedule(ctx, tx, orgID, planID, ledger.Currency(site), tz)
		if err != nil {
			return err
		}
		credit := ch.Account(ledger.IncomeSubtype(p.ChargeType))
		if p.IncomeAccountID != nil {
			credit = *p.IncomeAccountID
		}
		touched := map[string]Debtor{}
		for _, per := range pers {
			var have int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM charge WHERE org_id=$1 AND plan_id=$2 AND period=$3 AND kind='principal'`, orgID, planID, per.Key).Scan(&have); err != nil {
				return err
			}
			if have > 0 {
				continue
			}
			rate, ok := rates.ForPeriod(sc, per, tz)
			if !ok || rate.Amount.Amount == 0 {
				continue
			}
			due := per.DueDate.In(tz).Format(ledger.DateLayout)
			shares, err := s.planShares(ctx, tx, site, p, per, rate.Amount, due)
			if err != nil {
				return err
			}
			for _, sh := range shares {
				_, err := postCharge(ctx, tx, ch, chargeIn{OrgID: orgID, SiteID: siteID, UnitID: sh.UnitID, FundID: p.FundID, PlanID: &planID, Period: per.Key,
					Kind: "principal", ChargeType: p.ChargeType, DebtorKind: sh.Side, PersonID: sh.PersonID, Amount: sh.Amount, DueDate: due,
					EntryDate: due, Description: p.Name + " " + per.Key, Basis: sh.Basis, CreditAccount: credit, SourceType: "charge"})
				if err != nil {
					return err
				}
				created++
				uid := sh.UnitID
				touched[uid.String()+sh.Side] = Debtor{UnitID: &uid, Kind: sh.Side}
			}
		}
		for _, d := range touched {
			if _, err := ApplyCredit(ctx, tx, orgID, site, ch, d, today); err != nil {
				return err
			}
		}
		if created > 0 {
			return ledger.Audit(ctx, tx, orgID, siteID, "charge.generate", "charge_plan", planID, nil, map[string]any{"created": created})
		}
		return nil
	})
	return created, err
}

// GenerationStatus lists, per plan, the periods due up to today that have no charge yet.
type GenerationStatus struct {
	PlanID  uuid.UUID `json:"plan_id"`
	Name    string    `json:"name"`
	Missing []string  `json:"missing_periods"`
	// Units is how many units each missing period would bill (the active units of the site).
	Units int `json:"units"`
}

// ChargeGenerationStatus reports the periods the catch-up job has still to bill (money.read).
func (s *Service) ChargeGenerationStatus(ctx context.Context, orgID, siteID uuid.UUID) ([]GenerationStatus, error) {
	out := []GenerationStatus{}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		tz := ledger.Location(site.Timezone)
		today := ledger.Today(site.Timezone, s.Now())
		rows, err := tx.Query(ctx, `SELECT `+planCols+` FROM charge_plan WHERE org_id=$1 AND site_id=$2 AND status='active' ORDER BY id`, orgID, siteID)
		if err != nil {
			return err
		}
		var plans []Plan
		for rows.Next() {
			p, err := scanPlan(rows)
			if err != nil {
				rows.Close()
				return err
			}
			plans = append(plans, p)
		}
		rows.Close()
		for _, p := range plans {
			pers, err := periodsOf(p, today, tz)
			if err != nil {
				return err
			}
			st := GenerationStatus{PlanID: p.ID, Name: p.Name, Missing: []string{}}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM unit WHERE org_id=$1 AND site_id=$2 AND status='active' AND type <> 'common'`, orgID, siteID).Scan(&st.Units); err != nil {
				return err
			}
			for _, per := range pers {
				var have int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM charge WHERE org_id=$1 AND plan_id=$2 AND period=$3`, orgID, p.ID, per.Key).Scan(&have); err != nil {
					return err
				}
				if have == 0 {
					st.Missing = append(st.Missing, per.Key)
				}
			}
			out = append(out, st)
		}
		return nil
	})
	return out, err
}

// ChargeInput is a one-off charge outside any plan.
type ChargeInput struct {
	SiteID          uuid.UUID   `json:"site_id"`
	UnitID          uuid.UUID   `json:"unit_id"`
	ChargeType      string      `json:"charge_type"`
	DebtorKind      string      `json:"debtor_kind"`
	Amount          money.Money `json:"amount"`
	DueDate         string      `json:"due_date"`
	Description     string      `json:"description"`
	FundID          *uuid.UUID  `json:"fund_id"`
	IncomeAccountID *uuid.UUID  `json:"income_account_id"`
}

// CreateCharge posts a one-off charge to a unit side (money.post). Without a
// debtor_kind the effective payer rule decides (owner by default); the debtor
// person is snapshotted from the occupancy on the due date.
func (s *Service) CreateCharge(ctx context.Context, orgID uuid.UUID, in ChargeInput) (Charge, error) {
	if in.Amount.Amount <= 0 {
		return Charge{}, bad("amount", "invalid_value")
	}
	if _, err := ledger.ParseDate(in.DueDate); err != nil {
		return Charge{}, bad("due_date", "invalid_format")
	}
	var out Charge
	err := s.run(ctx, orgID, "charge", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if in.Amount.Currency != ledger.Currency(site) {
			return bad("amount", "invalid_value")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT payer_overrides FROM unit WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, in.UnitID).Scan(&raw); err != nil {
			return bad("unit_id", "invalid_value")
		}
		side := in.DebtorKind
		if side == "" {
			var po map[string]string
			_ = json.Unmarshal(raw, &po)
			side = tenancy.EffectivePayer(tenancy.Unit{PayerOverrides: po}, site, in.ChargeType, "owner")
		}
		if side != "owner" && side != "tenant" {
			return bad("debtor_kind", "invalid_value")
		}
		person, err := occupantOn(ctx, tx, orgID, in.SiteID, in.UnitID, side, in.DueDate)
		if err != nil {
			return err
		}
		if side == "tenant" && person == nil && in.DebtorKind == "" {
			side = "owner"
			if person, err = occupantOn(ctx, tx, orgID, in.SiteID, in.UnitID, side, in.DueDate); err != nil {
				return err
			}
		}
		fund := ch.OperatingFund
		if in.FundID != nil {
			fund = *in.FundID
		}
		credit := ch.Account(ledger.IncomeSubtype(in.ChargeType))
		if in.IncomeAccountID != nil {
			credit = *in.IncomeAccountID
		}
		cid, err := postCharge(ctx, tx, ch, chargeIn{OrgID: orgID, SiteID: in.SiteID, UnitID: in.UnitID, FundID: fund, Period: in.DueDate[:7], Kind: "principal", ChargeType: in.ChargeType,
			DebtorKind: side, PersonID: person, Amount: in.Amount.Amount, DueDate: in.DueDate, EntryDate: in.DueDate,
			Description: in.Description, CreditAccount: credit, SourceType: "charge"})
		if err != nil {
			return err
		}
		if _, err := ApplyCredit(ctx, tx, orgID, site, ch, Debtor{UnitID: &in.UnitID, Kind: side}, ledger.Today(site.Timezone, s.Now())); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "charge.create", "charge", cid, nil, map[string]any{"unit_id": in.UnitID, "amount": in.Amount.Amount, "due_date": in.DueDate}); err != nil {
			return err
		}
		out, err = scanCharge(tx.QueryRow(ctx, chargeSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, cid))
		return err
	})
	return out, err
}

// Charge is a charge as the API shows it.
type Charge struct {
	ID                uuid.UUID       `json:"id"`
	OrgID             uuid.UUID       `json:"org_id"`
	SiteID            uuid.UUID       `json:"site_id"`
	PlanID            *uuid.UUID      `json:"plan_id"`
	UnitID            uuid.UUID       `json:"unit_id"`
	DoorNumber        string          `json:"unit_door_number"`
	Period            string          `json:"period"`
	Kind              string          `json:"kind"`
	ChargeType        string          `json:"charge_type"`
	DebtorKind        string          `json:"debtor_kind"`
	DebtorPersonID    *uuid.UUID      `json:"debtor_person_id"`
	DebtorDisplayName *string         `json:"debtor_display_name"`
	Amount            money.Money     `json:"amount"`
	Paid              money.Money     `json:"paid"`
	Balance           money.Money     `json:"balance"`
	EffectiveDebtor   EffectiveDebtor `json:"effective_debtor"`
	DueDate           string          `json:"due_date"`
	Description       string          `json:"description"`
	AllocationBasis   any             `json:"allocation_basis"`
	JournalEntryID    uuid.UUID       `json:"journal_entry_id"`
	Status            string          `json:"status"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// EffectiveDebtor is who is (or was) charged and why (M1).
type EffectiveDebtor struct {
	DebtorKind  string     `json:"debtor_kind"`
	PersonID    *uuid.UUID `json:"person_id"`
	DisplayName *string    `json:"display_name"`
	Source      string     `json:"source"`
}

const chargeSel = `SELECT (u.payer_overrides ? c.charge_type), (SELECT pp.display_name FROM person pp WHERE pp.org_id = c.org_id AND pp.id = c.debtor_person_id), c.id, c.org_id, c.site_id, c.plan_id, c.unit_id, u.door_number, c.period, c.kind, c.charge_type, c.debtor_kind, c.debtor_person_id, c.amount, c.due_date::text, c.description, c.allocation_basis, c.journal_entry_id, c.status, c.created_at, s.currency,
	COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0)::bigint
	FROM charge c JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id JOIN site s ON s.org_id = c.org_id AND s.id = c.site_id`

func scanCharge(r pgx.Row) (c Charge, err error) {
	var basis []byte
	var amt, paid int64
	var cur string
	var override bool
	var name *string
	err = r.Scan(&override, &name, &c.ID, &c.OrgID, &c.SiteID, &c.PlanID, &c.UnitID, &c.DoorNumber, &c.Period, &c.Kind, &c.ChargeType, &c.DebtorKind, &c.DebtorPersonID, &amt, &c.DueDate, &c.Description, &basis, &c.JournalEntryID, &c.Status, &c.CreatedAt, &cur, &paid)
	cc := money.Code(cur)
	c.Amount, c.Paid, c.Balance = money.New(amt, cc), money.New(paid, cc), money.New(amt-paid, cc)
	src := "charge"
	switch {
	case c.DebtorKind == "person":
		src = "carried"
	case override:
		src = "unit_override"
	case c.PlanID != nil:
		src = "plan_default"
	}
	c.DebtorDisplayName = name
	c.EffectiveDebtor = EffectiveDebtor{DebtorKind: c.DebtorKind, PersonID: c.DebtorPersonID, DisplayName: name, Source: src}
	c.UpdatedAt = c.CreatedAt
	if len(basis) > 0 {
		_ = json.Unmarshal(basis, &c.AllocationBasis)
	}
	c.CreatedAt = c.CreatedAt.UTC()
	return
}

// ChargeFilter filters ListCharges.
type ChargeFilter struct {
	UnitID     *uuid.UUID
	DebtorKind string
	PersonID   *uuid.UUID
	PlanID     *uuid.UUID
	Status     string
	ChargeType string
	Overdue    bool
	From, To   string
}

// ListCharges lists charges (money.read, or statements.read_own narrowed to the caller's unit sides).
func (s *Service) ListCharges(ctx context.Context, orgID, siteID uuid.UUID, f ChargeFilter, p ledger.PageParams) ([]Charge, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Charge
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true)
		if err != nil {
			return err
		}
		if !ledger.HasSite(ctx, tx, ledger.PermMoneyRead, siteID) {
			if err := OwnSide(ctx, f.UnitID, f.DebtorKind); err != nil {
				return err
			}
		}
		today := ledger.Today(site.Timezone, s.Now())
		rows, err := tx.Query(ctx, chargeSel+` WHERE c.org_id=$1 AND c.site_id=$2 AND c.id > $3
			AND ($4::uuid IS NULL OR c.unit_id = $4) AND ($5 = '' OR c.debtor_kind = $5) AND ($6::uuid IS NULL OR c.debtor_person_id = $6)
			AND ($7::uuid IS NULL OR c.plan_id = $7) AND ($8 = '' OR c.status = $8) AND ($9 = '' OR c.charge_type = $9)
			AND (NOT $10 OR (c.due_date < $11::date AND c.status IN ('open','partially_paid') AND c.amount > 0))
			AND ($12 = '' OR c.due_date >= $12::date) AND ($13 = '' OR c.due_date <= $13::date)
			ORDER BY c.id LIMIT $14`, orgID, siteID, after, f.UnitID, f.DebtorKind, f.PersonID, f.PlanID, f.Status, f.ChargeType, f.Overdue, today, f.From, f.To, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCharge(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(c Charge) uuid.UUID { return c.ID })
	return items, next, nil
}

// OwnSide lets a resident through to the money of their own unit side: the
// request must name a unit and side the caller occupies (S3, authz.CanAccessUnit);
// managers pass with the manager permission instead.
func OwnSide(ctx context.Context, unit *uuid.UUID, side string) error {
	if unit == nil || (side != "owner" && side != "tenant") {
		return problem.Forbidden("name the unit and side (unit_id and debtor_kind) of your own account")
	}
	return authz.CanAccessUnit(ctx, *unit, authz.UnitSide(side))
}

// GetCharge returns a charge (money.read).
func (s *Service) GetCharge(ctx context.Context, orgID, chargeID uuid.UUID) (Charge, error) {
	var c Charge
	err := s.run(ctx, orgID, "charge", func(tx pgx.Tx) (err error) {
		if c, err = scanCharge(tx.QueryRow(ctx, chargeSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, chargeID)); err != nil {
			return err
		}
		if ledger.HasSite(ctx, tx, ledger.PermMoneyRead, c.SiteID) {
			return nil
		}
		// a resident reads the charges of their own unit side
		if c.EffectiveDebtor.DebtorKind == "person" || OwnSide(ctx, &c.UnitID, c.DebtorKind) != nil {
			return problem.NotFound("no such charge")
		}
		return nil
	})
	return c, err
}

// CancelCharge cancels a charge by reversing its entry dated today (money.post).
// Payments applied to it stay valid: their amount becomes prepayment credit of the debtor.
func (s *Service) CancelCharge(ctx context.Context, orgID, chargeID uuid.UUID, reason string) (Charge, error) {
	var out Charge
	err := s.run(ctx, orgID, "charge", func(tx pgx.Tx) error {
		c, err := scanCharge(tx.QueryRow(ctx, chargeSel+` WHERE c.org_id=$1 AND c.id=$2 FOR UPDATE OF c`, orgID, chargeID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, c.SiteID)
		if err != nil {
			return err
		}
		if c.Status == "cancelled" {
			return problem.Conflict("the charge is already cancelled")
		}
		var paid int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM payment_allocation WHERE org_id=$1 AND charge_id=$2 AND NOT voided`, orgID, chargeID).Scan(&paid); err != nil {
			return err
		}
		today := ledger.Today(site.Timezone, s.Now())
		if paid > 0 {
			// what was applied to the charge goes back to the debtor as credit; the payments stay valid
			var fund uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT fund_id FROM charge WHERE org_id=$1 AND id=$2`, orgID, chargeID).Scan(&fund); err != nil {
				return err
			}
			ch, err := ledger.EnsureChart(ctx, tx, orgID, c.SiteID)
			if err != nil {
				return err
			}
			d := Debtor{UnitID: &c.UnitID, Kind: c.DebtorKind}
			if c.DebtorKind == "person" {
				d = Debtor{PersonID: c.DebtorPersonID}
			}
			if err := MoveReceivableToCredit(ctx, tx, orgID, c.SiteID, ch, d, fund, paid, today, "Charge cancelled: payments kept as credit"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_allocation SET voided = true WHERE org_id=$1 AND charge_id=$2 AND NOT voided`, orgID, chargeID); err != nil {
				return err
			}
		}
		rid, err := ledger.Reverse(ctx, tx, orgID, c.SiteID, c.JournalEntryID, today, "Cancelled: "+reason)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE charge SET cancel_entry_id=$3, status='cancelled' WHERE org_id=$1 AND id=$2`, orgID, chargeID, rid); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, c.SiteID, "charge.cancel", "charge", chargeID, map[string]any{"status": c.Status}, map[string]any{"status": "cancelled", "reason": reason}); err != nil {
			return err
		}
		out, err = scanCharge(tx.QueryRow(ctx, chargeSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, chargeID))
		return err
	})
	return out, err
}
