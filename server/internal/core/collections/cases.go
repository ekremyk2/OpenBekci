package collections

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Case is a collection case as the API shows it.
type Case struct {
	ID             uuid.UUID      `json:"id"`
	OrgID          uuid.UUID      `json:"org_id"`
	SiteID         uuid.UUID      `json:"site_id"`
	UnitID         *uuid.UUID     `json:"unit_id"`
	DebtorKind     *string        `json:"debtor_kind"`
	DebtorPersonID *uuid.UUID     `json:"debtor_person_id"`
	Debtor         billing.Debtor `json:"debtor"`
	OpenedOn       string         `json:"opened_on"`
	Lawyer         *string        `json:"lawyer"`
	FileNo         *string        `json:"file_no"`
	Status         string         `json:"status"`
	ClosedOn       *string        `json:"closed_on"`
	CostsCharged   money.Money    `json:"costs_charged"`
	Notes          *string        `json:"notes"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Version        int            `json:"version"`
	freezeID       *uuid.UUID
}

const caseSel = `SELECT c.id, c.org_id, c.site_id, c.unit_id, c.debtor_kind, c.debtor_person_id, c.opened_on::text, c.lawyer, c.file_no, c.status, c.closed_on::text, c.costs_charged, c.notes,
	c.created_at, c.updated_at, c.version, c.freeze_id, s.currency FROM collection_case c JOIN site s ON s.org_id = c.org_id AND s.id = c.site_id`

func scanCase(r pgx.Row) (c Case, err error) {
	var costs int64
	var cur string
	err = r.Scan(&c.ID, &c.OrgID, &c.SiteID, &c.UnitID, &c.DebtorKind, &c.DebtorPersonID, &c.OpenedOn, &c.Lawyer, &c.FileNo, &c.Status, &c.ClosedOn, &costs, &c.Notes, &c.CreatedAt, &c.UpdatedAt, &c.Version, &c.freezeID, &cur)
	c.CostsCharged = money.New(costs, money.Code(cur))
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	if c.UnitID != nil && c.DebtorKind != nil {
		c.Debtor = billing.Debtor{UnitID: c.UnitID, Kind: *c.DebtorKind}
	} else {
		c.Debtor = billing.Debtor{PersonID: c.DebtorPersonID}
	}
	return
}

// CaseInput opens a case. The person is resolved here: for a unit side it is whoever holds
// the side on the opening date (none when vacant); a person debtor is the person itself.
type CaseInput struct {
	SiteID   uuid.UUID      `json:"site_id"`
	Debtor   billing.Debtor `json:"debtor"`
	OpenedOn string         `json:"opened_on"`
	Lawyer   *string        `json:"lawyer"`
	FileNo   *string        `json:"file_no"`
	Notes    *string        `json:"notes"`
}

// CreateCase opens a collection case (collections.manage). For a unit side it also
// starts a late fee freeze from the opening date (through billing, in the same
// transaction); reminders skip the debtor while the case is open.
func (s *Service) CreateCase(ctx context.Context, orgID uuid.UUID, in CaseInput) (Case, error) {
	if err := in.Debtor.Validate(); err != nil {
		return Case{}, err
	}
	if _, err := ledger.ParseDate(in.OpenedOn); err != nil {
		return Case{}, bad("opened_on", "invalid_format")
	}
	var out Case
	err := s.run(ctx, orgID, "collection case", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermCollectionsManage, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if err := s.checkDebtor(ctx, tx, orgID, in.SiteID, in.Debtor); err != nil {
			return bad("debtor", "invalid_value")
		}
		cid := id.New()
		var unit *uuid.UUID
		var kind *string
		var person *uuid.UUID
		var freeze *uuid.UUID
		if in.Debtor.IsPerson() {
			person = in.Debtor.PersonID
		} else {
			unit, kind = in.Debtor.UnitID, &in.Debtor.Kind
			if person, _, err = occupantName(ctx, tx, orgID, *unit, *kind, in.OpenedOn); err != nil {
				return err
			}
		}
		if !in.Debtor.IsPerson() {
			fid, err := s.bill.WithTx(tx).CreateLateFeeFreeze(ctx, orgID, billing.FreezeInput{SiteID: in.SiteID, UnitID: *unit, DebtorKind: *kind, From: in.OpenedOn, Reason: "collection case " + cid.String()})
			if err != nil {
				return err
			}
			freeze = &fid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO collection_case (id, org_id, site_id, unit_id, debtor_kind, debtor_person_id, opened_on, lawyer, file_no, notes, freeze_id, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10,$11,$12)`,
			cid, orgID, in.SiteID, unit, kind, person, in.OpenedOn, in.Lawyer, in.FileNo, in.Notes, freeze, ledger.Actor(ctx)); err != nil {
			if tenancy.IsUnique(err, "collection_case_open") {
				return problem.Conflict("the debtor already has an open collection case")
			}
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "collection_case.open", "collection_case", cid, nil, map[string]any{"opened_on": in.OpenedOn, "site": site.Name}); err != nil {
			return err
		}
		out, err = scanCase(tx.QueryRow(ctx, caseSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, cid))
		return err
	})
	return out, err
}

// CaseFilter filters ListCases.
type CaseFilter struct {
	UnitID *uuid.UUID
	Status string
}

// ListCases lists a site's cases (money.read).
func (s *Service) ListCases(ctx context.Context, orgID, siteID uuid.UUID, f CaseFilter, p ledger.PageParams) ([]Case, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Case
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, caseSel+` WHERE c.org_id=$1 AND c.site_id=$2 AND c.id > $3 AND ($4::uuid IS NULL OR c.unit_id = $4) AND ($5 = '' OR c.status = $5) ORDER BY c.id LIMIT $6`,
			orgID, siteID, after, f.UnitID, f.Status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCase(rows)
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
	items, next := tenancy.Page(out, limit, func(c Case) uuid.UUID { return c.ID })
	return items, next, nil
}

// GetCase returns a case (money.read).
func (s *Service) GetCase(ctx context.Context, orgID, caseID uuid.UUID) (Case, error) {
	var c Case
	err := s.run(ctx, orgID, "collection case", func(tx pgx.Tx) (err error) {
		if c, err = scanCase(tx.QueryRow(ctx, caseSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, caseID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, c.SiteID)
	})
	return c, err
}

// CaseUpdate is a partial update: a nil pointer leaves the field, a pointer to a nil string clears it.
type CaseUpdate struct {
	Lawyer, FileNo, Notes **string
}

// UpdateCase edits the lawyer, file number and notes if version matches (collections.manage).
func (s *Service) UpdateCase(ctx context.Context, orgID, caseID uuid.UUID, version int, u CaseUpdate) (Case, error) {
	var out Case
	err := s.run(ctx, orgID, "collection case", func(tx pgx.Tx) error {
		c, err := scanCase(tx.QueryRow(ctx, caseSel+` WHERE c.org_id=$1 AND c.id=$2 FOR UPDATE OF c`, orgID, caseID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermCollectionsManage, c.SiteID); err != nil {
			return err
		}
		if c.Version != version {
			return problem.New(409, problem.CodeVersionConflict, "the case was changed by someone else")
		}
		if u.Lawyer != nil {
			c.Lawyer = *u.Lawyer
		}
		if u.FileNo != nil {
			c.FileNo = *u.FileNo
		}
		if u.Notes != nil {
			c.Notes = *u.Notes
		}
		if _, err := tx.Exec(ctx, `UPDATE collection_case SET lawyer=$3, file_no=$4, notes=$5, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`, orgID, caseID, c.Lawyer, c.FileNo, c.Notes); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, c.SiteID, "collection_case.update", "collection_case", caseID, nil, map[string]any{"file_no": c.FileNo}); err != nil {
			return err
		}
		out, err = scanCase(tx.QueryRow(ctx, caseSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, caseID))
		return err
	})
	return out, err
}

// CloseCase closes an open case (collections.manage): the late fee freeze ends today, and the
// legal costs, when given, are charged back to the debtor side as a legal_cost charge through
// billing, due today (only for unit sides). Reminders and late fees resume.
func (s *Service) CloseCase(ctx context.Context, orgID, caseID uuid.UUID, costs *money.Money, note string) (Case, error) {
	var out Case
	err := s.run(ctx, orgID, "collection case", func(tx pgx.Tx) error {
		c, err := scanCase(tx.QueryRow(ctx, caseSel+` WHERE c.org_id=$1 AND c.id=$2 FOR UPDATE OF c`, orgID, caseID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermCollectionsManage, orgID, c.SiteID)
		if err != nil {
			return err
		}
		if c.Status != "open" {
			return problem.Conflict("the case is already closed")
		}
		today := ledger.Today(site.Timezone, s.Now())
		var charged int64
		var chargeID *uuid.UUID
		if costs != nil && costs.Amount != 0 {
			if costs.Amount < 0 || costs.Currency != ledger.Currency(site) {
				return bad("costs", "invalid_value")
			}
			if c.UnitID == nil {
				return bad("costs", "unsupported")
			}
			desc := "Legal costs"
			if c.FileNo != nil && *c.FileNo != "" {
				desc += ": " + *c.FileNo
			}
			ch, err := s.bill.WithTx(tx).CreateCharge(ctx, orgID, billing.ChargeInput{SiteID: c.SiteID, UnitID: *c.UnitID, ChargeType: "legal_cost", DebtorKind: *c.DebtorKind, Amount: *costs, DueDate: today, Description: desc})
			if err != nil {
				return err
			}
			charged, chargeID = costs.Amount, &ch.ID
		}
		if c.freezeID != nil {
			if err := s.bill.WithTx(tx).EndLateFeeFreeze(ctx, orgID, *c.freezeID, today); err != nil {
				return err
			}
		}
		notes := c.Notes
		if note != "" {
			n := note
			if notes != nil && *notes != "" {
				n = *notes + "\n" + note
			}
			notes = &n
		}
		if _, err := tx.Exec(ctx, `UPDATE collection_case SET status='closed', closed_on=$3::date, costs_charged=$4, cost_charge_id=$5, notes=$6, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`,
			orgID, caseID, today, charged, chargeID, notes); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, c.SiteID, "collection_case.close", "collection_case", caseID, map[string]any{"status": "open"}, map[string]any{"status": "closed", "costs": charged}); err != nil {
			return err
		}
		out, err = scanCase(tx.QueryRow(ctx, caseSel+` WHERE c.org_id=$1 AND c.id=$2`, orgID, caseID))
		return err
	})
	return out, err
}
