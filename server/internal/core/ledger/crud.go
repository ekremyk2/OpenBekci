package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Doc is the fields a client sent.
type Doc = tenancy.Doc

// PageParams are the pagination inputs of a list.
type PageParams = tenancy.PageParams

func bad(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// run runs fn in a tenant transaction and maps its error.
func (s *Service) run(ctx context.Context, orgID uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.db.InTenantTx(ctx, orgID, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return DBProblem(err, what)
}

// ---------------------------------------------------------------- funds

// Fund is a fund as the API shows it.
type Fund struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	SiteID    uuid.UUID `json:"site_id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

const fundCols = `id, org_id, site_id, code, name, kind, is_default, created_at, updated_at, version`

func scanFund(r pgx.Row) (f Fund, err error) {
	err = r.Scan(&f.ID, &f.OrgID, &f.SiteID, &f.Code, &f.Name, &f.Kind, &f.IsDefault, &f.CreatedAt, &f.UpdatedAt, &f.Version)
	f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
	return
}

var fundKinds = map[string]bool{"operating": true, "reserve": true, "custom": true}

// ListFunds lists a site's funds (money.read).
func (s *Service) ListFunds(ctx context.Context, orgID, siteID uuid.UUID, p PageParams) ([]Fund, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Fund
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := RequireSite(ctx, tx, PermMoneyRead, siteID); err != nil {
			return err
		}
		if _, err := EnsureChart(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+fundCols+` FROM fund WHERE org_id = $1 AND site_id = $2 AND id > $3 ORDER BY id LIMIT $4`, orgID, siteID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFund(rows)
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
	items, next := tenancy.Page(out, limit, func(f Fund) uuid.UUID { return f.ID })
	return items, next, nil
}

// GetFund returns a fund (money.read).
func (s *Service) GetFund(ctx context.Context, orgID, fundID uuid.UUID) (Fund, error) {
	var f Fund
	err := s.run(ctx, orgID, "fund", func(tx pgx.Tx) (err error) {
		if f, err = scanFund(tx.QueryRow(ctx, `SELECT `+fundCols+` FROM fund WHERE org_id = $1 AND id = $2`, orgID, fundID)); err != nil {
			return err
		}
		return RequireSite(ctx, tx, PermMoneyRead, f.SiteID)
	})
	return f, err
}

// FundInput creates a fund.
type FundInput struct {
	SiteID uuid.UUID `json:"site_id"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
}

// CreateFund adds a custom or reserve fund (money.manage).
func (s *Service) CreateFund(ctx context.Context, orgID uuid.UUID, in FundInput) (Fund, error) {
	if n := len([]rune(in.Name)); n < 1 || n > 200 {
		return Fund{}, bad("name", "invalid_value")
	}
	if !fundKinds[in.Kind] || in.Kind == "operating" {
		return Fund{}, bad("kind", "invalid_value")
	}
	var f Fund
	err := s.run(ctx, orgID, "fund", func(tx pgx.Tx) (err error) {
		if _, err = SiteFor(ctx, tx, PermMoneyManage, orgID, in.SiteID); err != nil {
			return err
		}
		if _, err = EnsureChart(ctx, tx, orgID, in.SiteID); err != nil {
			return err
		}
		fid := id.New()
		code := "fund_" + strings.ReplaceAll(fid.String()[24:], "-", "")
		if f, err = scanFund(tx.QueryRow(ctx, `INSERT INTO fund (id, org_id, site_id, code, name, kind) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+fundCols,
			fid, orgID, in.SiteID, code, in.Name, in.Kind)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, in.SiteID, "fund.create", "fund", f.ID, nil, f)
	})
	return f, err
}

// UpdateFund renames a fund (money.manage); the default fund keeps its kind.
func (s *Service) UpdateFund(ctx context.Context, orgID, fundID uuid.UUID, version int, d Doc) (Fund, error) {
	var f Fund
	err := s.run(ctx, orgID, "fund", func(tx pgx.Tx) error {
		before, err := scanFund(tx.QueryRow(ctx, `SELECT `+fundCols+` FROM fund WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, fundID))
		if err != nil {
			return err
		}
		if err := RequireSite(ctx, tx, PermMoneyManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != version {
			return tenancy.ErrVersion
		}
		next := before
		if unknown := d.Unknown("name", "kind"); len(unknown) > 0 {
			return problem.Validation(unknown...)
		}
		if d.Has("name") {
			if err := d.Get("name", &next.Name); err != nil {
				return err
			}
			if n := len([]rune(next.Name)); n < 1 || n > 200 {
				return bad("name", "invalid_value")
			}
		}
		if d.Has("kind") {
			if err := d.Get("kind", &next.Kind); err != nil {
				return err
			}
			if !fundKinds[next.Kind] || (before.IsDefault && next.Kind != before.Kind) {
				return bad("kind", "invalid_value")
			}
		}
		if f, err = scanFund(tx.QueryRow(ctx, `UPDATE fund SET name = $3, kind = $4, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2 RETURNING `+fundCols,
			orgID, fundID, next.Name, next.Kind)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, f.SiteID, "fund.update", "fund", f.ID, before, f)
	})
	return f, err
}

// ---------------------------------------------------------------- accounts

// Account is an account as the API shows it.
type Account struct {
	ID        uuid.UUID  `json:"id"`
	OrgID     uuid.UUID  `json:"org_id"`
	SiteID    uuid.UUID  `json:"site_id"`
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Subtype   *string    `json:"subtype"`
	FundID    *uuid.UUID `json:"fund_id"`
	IsSystem  bool       `json:"is_system"`
	Archived  bool       `json:"archived"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Version   int        `json:"version"`
}

const accountCols = `id, org_id, site_id, code, name, type, subtype, fund_id, is_system, archived, created_at, updated_at, version`

func scanAccount(r pgx.Row) (a Account, err error) {
	err = r.Scan(&a.ID, &a.OrgID, &a.SiteID, &a.Code, &a.Name, &a.Type, &a.Subtype, &a.FundID, &a.IsSystem, &a.Archived, &a.CreatedAt, &a.UpdatedAt, &a.Version)
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return
}

var accountTypes = map[string]bool{"asset": true, "liability": true, "equity": true, "income": true, "expense": true}

// AccountFilter filters ListAccounts.
type AccountFilter struct {
	Type, Subtype string
	Archived      *bool
}

// ListAccounts lists a site's accounts by code (money.read).
func (s *Service) ListAccounts(ctx context.Context, orgID, siteID uuid.UUID, f AccountFilter, p PageParams) ([]Account, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Account
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := RequireSite(ctx, tx, PermMoneyRead, siteID); err != nil {
			return err
		}
		if _, err := EnsureChart(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM account WHERE org_id = $1 AND site_id = $2 AND id > $3
			AND ($4 = '' OR type = $4) AND ($5 = '' OR subtype = $5) AND ($6::boolean IS NULL OR archived = $6) ORDER BY id LIMIT $7`,
			orgID, siteID, after, f.Type, f.Subtype, f.Archived, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAccount(rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(a Account) uuid.UUID { return a.ID })
	return items, next, nil
}

// GetAccount returns an account (money.read).
func (s *Service) GetAccount(ctx context.Context, orgID, accountID uuid.UUID) (Account, error) {
	var a Account
	err := s.run(ctx, orgID, "account", func(tx pgx.Tx) (err error) {
		if a, err = scanAccount(tx.QueryRow(ctx, `SELECT `+accountCols+` FROM account WHERE org_id = $1 AND id = $2`, orgID, accountID)); err != nil {
			return err
		}
		return RequireSite(ctx, tx, PermMoneyRead, a.SiteID)
	})
	return a, err
}

// AccountInput creates an account.
type AccountInput struct {
	SiteID   uuid.UUID  `json:"site_id"`
	Code     string     `json:"code"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Subtype  *string    `json:"subtype"`
	FundID   *uuid.UUID `json:"fund_id"`
	Archived bool       `json:"archived"`
}

func (in AccountInput) validate() error {
	var f []problem.Field
	if n := len(in.Code); n < 1 || n > 20 {
		f = append(f, problem.Field{Field: "code", Code: "invalid_value"})
	}
	if n := len([]rune(in.Name)); n < 1 || n > 200 {
		f = append(f, problem.Field{Field: "name", Code: "invalid_value"})
	}
	if !accountTypes[in.Type] {
		f = append(f, problem.Field{Field: "type", Code: "invalid_value"})
	}
	if len(f) > 0 {
		return problem.Validation(f...)
	}
	return nil
}

// CreateAccount adds an account (money.manage).
func (s *Service) CreateAccount(ctx context.Context, orgID uuid.UUID, in AccountInput) (Account, error) {
	if err := in.validate(); err != nil {
		return Account{}, err
	}
	var a Account
	err := s.run(ctx, orgID, "account", func(tx pgx.Tx) (err error) {
		if _, err = SiteFor(ctx, tx, PermMoneyManage, orgID, in.SiteID); err != nil {
			return err
		}
		if _, err = EnsureChart(ctx, tx, orgID, in.SiteID); err != nil {
			return err
		}
		if a, err = scanAccount(tx.QueryRow(ctx, `INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, archived) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+accountCols,
			id.New(), orgID, in.SiteID, in.Code, in.Name, in.Type, in.Subtype, in.FundID, in.Archived)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, in.SiteID, "account.create", "account", a.ID, nil, a)
	})
	return a, err
}

// UpdateAccount changes an account (money.manage). System accounts keep code, type and subtype.
func (s *Service) UpdateAccount(ctx context.Context, orgID, accountID uuid.UUID, version int, d Doc) (Account, error) {
	var a Account
	err := s.run(ctx, orgID, "account", func(tx pgx.Tx) error {
		before, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountCols+` FROM account WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, accountID))
		if err != nil {
			return err
		}
		if err := RequireSite(ctx, tx, PermMoneyManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != version {
			return tenancy.ErrVersion
		}
		if unknown := d.Unknown("code", "name", "type", "subtype", "fund_id", "archived"); len(unknown) > 0 {
			return problem.Validation(unknown...)
		}
		next := before
		for k, dst := range map[string]any{"code": &next.Code, "name": &next.Name, "type": &next.Type, "subtype": &next.Subtype, "fund_id": &next.FundID, "archived": &next.Archived} {
			if d.Has(k) {
				if err := d.Get(k, dst); err != nil {
					return err
				}
			}
		}
		if before.IsSystem && (next.Code != before.Code || next.Type != before.Type || fmt.Sprint(next.Subtype) != fmt.Sprint(before.Subtype) || next.Archived) {
			return problem.Conflict("a system account keeps its code, type and subtype and cannot be archived")
		}
		if err := (AccountInput{Code: next.Code, Name: next.Name, Type: next.Type}).validate(); err != nil {
			return err
		}
		if a, err = scanAccount(tx.QueryRow(ctx, `UPDATE account SET code=$3, name=$4, type=$5, subtype=$6, fund_id=$7, archived=$8, updated_at=now(), version=version+1
			WHERE org_id = $1 AND id = $2 RETURNING `+accountCols, orgID, accountID, next.Code, next.Name, next.Type, next.Subtype, next.FundID, next.Archived)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, a.SiteID, "account.update", "account", a.ID, before, a)
	})
	return a, err
}

// ---------------------------------------------------------------- cash boxes

// CashBox is a cash box with its balance.
type CashBox struct {
	ID                   uuid.UUID   `json:"id"`
	OrgID                uuid.UUID   `json:"org_id"`
	SiteID               uuid.UUID   `json:"site_id"`
	Name                 string      `json:"name"`
	AccountID            uuid.UUID   `json:"account_id"`
	CollectorPersonID    *uuid.UUID  `json:"collector_person_id"`
	CollectorDisplayName *string     `json:"collector_display_name"`
	Balance              money.Money `json:"balance"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	Version              int         `json:"version"`
}

const cashBoxSelect = `SELECT b.id, b.org_id, b.site_id, b.name, b.account_id, b.collector_person_id, p.display_name, s.currency,
	COALESCE((SELECT SUM(debit - credit) FROM journal_line l WHERE l.org_id = b.org_id AND l.site_id = b.site_id AND l.account_id = b.account_id), 0)::bigint,
	b.created_at, b.updated_at, b.version
	FROM cash_box b JOIN site s ON s.org_id = b.org_id AND s.id = b.site_id LEFT JOIN person p ON p.org_id = b.org_id AND p.id = b.collector_person_id`

func scanCashBox(r pgx.Row) (b CashBox, err error) {
	var cur string
	var bal int64
	err = r.Scan(&b.ID, &b.OrgID, &b.SiteID, &b.Name, &b.AccountID, &b.CollectorPersonID, &b.CollectorDisplayName, &cur, &bal, &b.CreatedAt, &b.UpdatedAt, &b.Version)
	b.Balance = money.New(bal, money.Code(cur))
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	return
}

// ListCashBoxes lists cash boxes (money.read; a collector sees only their own box).
func (s *Service) ListCashBoxes(ctx context.Context, orgID, siteID uuid.UUID, p PageParams) ([]CashBox, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []CashBox
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := RequireSite(ctx, tx, PermMoneyRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, cashBoxSelect+` WHERE b.org_id = $1 AND b.site_id = $2 AND b.id > $3 ORDER BY b.id LIMIT $4`, orgID, siteID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanCashBox(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(b CashBox) uuid.UUID { return b.ID })
	return items, next, nil
}

// GetCashBox returns a cash box with its balance (money.read).
func (s *Service) GetCashBox(ctx context.Context, orgID, boxID uuid.UUID) (CashBox, error) {
	var b CashBox
	err := s.run(ctx, orgID, "cash box", func(tx pgx.Tx) (err error) {
		if b, err = scanCashBox(tx.QueryRow(ctx, cashBoxSelect+` WHERE b.org_id = $1 AND b.id = $2`, orgID, boxID)); err != nil {
			return err
		}
		return RequireSite(ctx, tx, PermMoneyRead, b.SiteID)
	})
	return b, err
}

// CashBoxInput creates a cash box.
type CashBoxInput struct {
	SiteID            uuid.UUID  `json:"site_id"`
	Name              string     `json:"name"`
	CollectorPersonID *uuid.UUID `json:"collector_person_id"`
}

// CreateCashBox adds a cash box and its asset account (money.manage).
func (s *Service) CreateCashBox(ctx context.Context, orgID uuid.UUID, in CashBoxInput) (CashBox, error) {
	if n := len([]rune(in.Name)); n < 1 || n > 200 {
		return CashBox{}, bad("name", "invalid_value")
	}
	var out CashBox
	err := s.run(ctx, orgID, "cash box", func(tx pgx.Tx) error {
		if _, err := SiteFor(ctx, tx, PermMoneyManage, orgID, in.SiteID); err != nil {
			return err
		}
		ch, err := EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM cash_box WHERE org_id = $1 AND site_id = $2`, orgID, in.SiteID).Scan(&n); err != nil {
			return err
		}
		accID, boxID := id.New(), id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,$4,$5,'asset','cash_box',$6,true)`,
			accID, orgID, in.SiteID, fmt.Sprintf("10%02d", n+1), "Cash box: "+in.Name, ch.OperatingFund); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cash_box (id, org_id, site_id, name, account_id, collector_person_id) VALUES ($1,$2,$3,$4,$5,$6)`,
			boxID, orgID, in.SiteID, in.Name, accID, in.CollectorPersonID); err != nil {
			return err
		}
		if out, err = scanCashBox(tx.QueryRow(ctx, cashBoxSelect+` WHERE b.org_id = $1 AND b.id = $2`, orgID, boxID)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, in.SiteID, "cash_box.create", "cash_box", boxID, nil, map[string]any{"name": in.Name, "account_id": accID, "collector_person_id": in.CollectorPersonID})
	})
	return out, err
}

// UpdateCashBox renames a cash box or rebinds its collector (money.manage).
func (s *Service) UpdateCashBox(ctx context.Context, orgID, boxID uuid.UUID, version int, d Doc) (CashBox, error) {
	var out CashBox
	err := s.run(ctx, orgID, "cash box", func(tx pgx.Tx) error {
		before, err := scanCashBox(tx.QueryRow(ctx, cashBoxSelect+` WHERE b.org_id = $1 AND b.id = $2 FOR UPDATE OF b`, orgID, boxID))
		if err != nil {
			return err
		}
		if err := RequireSite(ctx, tx, PermMoneyManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != version {
			return tenancy.ErrVersion
		}
		if unknown := d.Unknown("name", "collector_person_id"); len(unknown) > 0 {
			return problem.Validation(unknown...)
		}
		name, collector := before.Name, before.CollectorPersonID
		if d.Has("name") {
			if err := d.Get("name", &name); err != nil {
				return err
			}
		}
		if d.Has("collector_person_id") {
			collector = nil
			if err := d.Get("collector_person_id", &collector); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE cash_box SET name=$3, collector_person_id=$4, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`, orgID, boxID, name, collector); err != nil {
			return err
		}
		if out, err = scanCashBox(tx.QueryRow(ctx, cashBoxSelect+` WHERE b.org_id = $1 AND b.id = $2`, orgID, boxID)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, out.SiteID, "cash_box.update", "cash_box", boxID,
			map[string]any{"name": before.Name, "collector_person_id": before.CollectorPersonID}, map[string]any{"name": out.Name, "collector_person_id": out.CollectorPersonID})
	})
	return out, err
}

// CollectorBox returns the cash box account the caller collects into: the box
// bound to the person linked to the caller's user, if any.
func CollectorBox(ctx context.Context, tx pgx.Tx, orgID, siteID, userID uuid.UUID) (accountID uuid.UUID, ok bool, err error) {
	err = tx.QueryRow(ctx, `SELECT b.account_id FROM cash_box b JOIN person p ON p.org_id = b.org_id AND p.id = b.collector_person_id
		WHERE b.org_id = $1 AND b.site_id = $2 AND p.user_id = $3 LIMIT 1`, orgID, siteID, userID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return accountID, err == nil, err
}

// ---------------------------------------------------------------- fiscal years

// FiscalYear is a reporting grouping with an approval status.
type FiscalYear struct {
	ID                   uuid.UUID  `json:"id"`
	OrgID                uuid.UUID  `json:"org_id"`
	SiteID               uuid.UUID  `json:"site_id"`
	Start                string     `json:"start"`
	End                  string     `json:"end"`
	Status               string     `json:"status"`
	ApprovedByDecisionID *uuid.UUID `json:"approved_by_decision_id"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	Version              int        `json:"version"`
}

const fyCols = `id, org_id, site_id, start_on::text, end_on::text, status, approved_by_decision_id, created_at, updated_at, version`

func scanFY(r pgx.Row) (f FiscalYear, err error) {
	err = r.Scan(&f.ID, &f.OrgID, &f.SiteID, &f.Start, &f.End, &f.Status, &f.ApprovedByDecisionID, &f.CreatedAt, &f.UpdatedAt, &f.Version)
	f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
	return
}

// ListFiscalYears lists a site's fiscal years, newest first by start (money.read).
func (s *Service) ListFiscalYears(ctx context.Context, orgID, siteID uuid.UUID) ([]FiscalYear, error) {
	out := []FiscalYear{}
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := RequireSite(ctx, tx, PermMoneyRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+fyCols+` FROM fiscal_year WHERE org_id = $1 AND site_id = $2 ORDER BY start_on DESC`, orgID, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFY(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}

// GetFiscalYear returns a fiscal year (money.read).
func (s *Service) GetFiscalYear(ctx context.Context, orgID, fyID uuid.UUID) (FiscalYear, error) {
	var f FiscalYear
	err := s.run(ctx, orgID, "fiscal year", func(tx pgx.Tx) (err error) {
		if f, err = scanFY(tx.QueryRow(ctx, `SELECT `+fyCols+` FROM fiscal_year WHERE org_id = $1 AND id = $2`, orgID, fyID)); err != nil {
			return err
		}
		return RequireSite(ctx, tx, PermMoneyRead, f.SiteID)
	})
	return f, err
}

// FiscalYearInput creates a fiscal year.
type FiscalYearInput struct {
	SiteID uuid.UUID `json:"site_id"`
	Start  string    `json:"start"`
	End    string    `json:"end"`
}

// CreateFiscalYear adds a fiscal year (money.manage); years of a site may not overlap.
func (s *Service) CreateFiscalYear(ctx context.Context, orgID uuid.UUID, in FiscalYearInput) (FiscalYear, error) {
	st, e1 := ParseDate(in.Start)
	en, e2 := ParseDate(in.End)
	if e1 != nil || e2 != nil || !en.After(st) {
		return FiscalYear{}, bad("start", "invalid_range")
	}
	var f FiscalYear
	err := s.run(ctx, orgID, "fiscal year", func(tx pgx.Tx) (err error) {
		if _, err = SiteFor(ctx, tx, PermMoneyManage, orgID, in.SiteID); err != nil {
			return err
		}
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM fiscal_year WHERE org_id=$1 AND site_id=$2 AND start_on <= $4::date AND end_on >= $3::date`, orgID, in.SiteID, in.Start, in.End).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problem.Conflict("the fiscal year overlaps another one")
		}
		if f, err = scanFY(tx.QueryRow(ctx, `INSERT INTO fiscal_year (id, org_id, site_id, start_on, end_on) VALUES ($1,$2,$3,$4::date,$5::date) RETURNING `+fyCols,
			id.New(), orgID, in.SiteID, in.Start, in.End)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, in.SiteID, "fiscal_year.create", "fiscal_year", f.ID, nil, f)
	})
	return f, err
}

// SetFiscalYearStatus closes or approves a fiscal year (money.lock); closing
// needs an open year, approving a closed one, optionally citing a decision.
func (s *Service) SetFiscalYearStatus(ctx context.Context, orgID, fyID uuid.UUID, to string, decisionID *uuid.UUID) (FiscalYear, error) {
	var f FiscalYear
	err := s.run(ctx, orgID, "fiscal year", func(tx pgx.Tx) error {
		before, err := scanFY(tx.QueryRow(ctx, `SELECT `+fyCols+` FROM fiscal_year WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, fyID))
		if err != nil {
			return err
		}
		if err := RequireSite(ctx, tx, PermMoneyLock, before.SiteID); err != nil {
			return err
		}
		if (to == "closed" && before.Status != "open") || (to == "approved" && before.Status != "closed") {
			return problem.Conflict(fmt.Sprintf("a %s fiscal year cannot become %s", before.Status, to))
		}
		if f, err = scanFY(tx.QueryRow(ctx, `UPDATE fiscal_year SET status=$3, approved_by_decision_id=COALESCE($4, approved_by_decision_id), updated_at=now(), version=version+1
			WHERE org_id=$1 AND id=$2 RETURNING `+fyCols, orgID, fyID, to, decisionID)); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, f.SiteID, "fiscal_year."+map[string]string{"closed": "close", "approved": "approve"}[to], "fiscal_year", fyID, before, f)
	})
	return f, err
}

// ---------------------------------------------------------------- lock date

// LockDate is a site's lock date with who changed it last.
type LockDate struct {
	SiteID    uuid.UUID  `json:"site_id"`
	LockDate  *string    `json:"lock_date"`
	ChangedBy *uuid.UUID `json:"changed_by"`
	ChangedAt *time.Time `json:"changed_at"`
	Version   int        `json:"version"`
}

func loadLockDate(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (LockDate, error) {
	l := LockDate{SiteID: siteID}
	if err := tx.QueryRow(ctx, `SELECT lock_date::text, version FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&l.LockDate, &l.Version); err != nil {
		return l, err
	}
	err := tx.QueryRow(ctx, `SELECT actor_user_id, at FROM audit_event WHERE org_id = $1 AND action = 'site.lock_date.set' AND target_id = $2 ORDER BY at DESC, id DESC LIMIT 1`,
		orgID, siteID).Scan(&l.ChangedBy, &l.ChangedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return l, err
	}
	if l.ChangedAt != nil {
		t := l.ChangedAt.UTC()
		l.ChangedAt = &t
	}
	return l, nil
}

// GetLockDate returns the lock date (money.read).
func (s *Service) GetLockDate(ctx context.Context, orgID, siteID uuid.UUID) (LockDate, error) {
	var l LockDate
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) (err error) {
		if err = RequireSite(ctx, tx, PermMoneyRead, siteID); err != nil {
			return err
		}
		l, err = loadLockDate(ctx, tx, orgID, siteID)
		return err
	})
	return l, err
}

// SetLockDate moves the lock date (money.lock). Moving it back or clearing it
// needs re-authentication (rule 4, section 16). version is the If-Match value.
func (s *Service) SetLockDate(ctx context.Context, orgID, siteID uuid.UUID, version int, to *string) (LockDate, error) {
	if to != nil {
		if _, err := ParseDate(*to); err != nil {
			return LockDate{}, bad("lock_date", "invalid_format")
		}
	}
	var out LockDate
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := RequireSite(ctx, tx, PermMoneyLock, siteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, true, false)
		if err != nil {
			return err
		}
		if site.Version != version {
			return tenancy.ErrVersion
		}
		if site.LockDate != nil && (to == nil || *to < *site.LockDate) {
			if err := authz.PrincipalFromContext(ctx).RequireReauth(s.Now()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE site SET lock_date = $3::date, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, siteID, to); err != nil {
			return err
		}
		if err := Audit(ctx, tx, orgID, siteID, "site.lock_date.set", "site_lock", siteID, map[string]any{"lock_date": site.LockDate}, map[string]any{"lock_date": to}); err != nil {
			return err
		}
		out, err = loadLockDate(ctx, tx, orgID, siteID)
		return err
	})
	return out, err
}

// ---------------------------------------------------------------- journal

// JournalLine is a line as the API shows it.
type JournalLine struct {
	ID          uuid.UUID   `json:"id"`
	AccountID   uuid.UUID   `json:"account_id"`
	AccountCode string      `json:"account_code"`
	FundID      uuid.UUID   `json:"fund_id"`
	Debit       money.Money `json:"debit"`
	Credit      money.Money `json:"credit"`
	UnitID      *uuid.UUID  `json:"unit_id"`
	DebtorKind  *string     `json:"debtor_kind"`
	PersonID    *uuid.UUID  `json:"person_id"`
	BlockID     *uuid.UUID  `json:"block_id"`
	VendorID    *uuid.UUID  `json:"vendor_id"`
}

// JournalEntry is an entry with its lines.
type JournalEntry struct {
	ID                uuid.UUID     `json:"id"`
	OrgID             uuid.UUID     `json:"org_id"`
	SiteID            uuid.UUID     `json:"site_id"`
	EntryDate         string        `json:"entry_date"`
	DocumentDate      string        `json:"document_date"`
	FiscalYearID      *uuid.UUID    `json:"fiscal_year_id"`
	Memo              *string       `json:"memo"`
	SourceType        string        `json:"source_type"`
	SourceID          *uuid.UUID    `json:"source_id"`
	PostedBy          *uuid.UUID    `json:"posted_by"`
	PostedAt          time.Time     `json:"posted_at"`
	ReversesEntryID   *uuid.UUID    `json:"reverses_entry_id"`
	ReversedByEntryID *uuid.UUID    `json:"reversed_by_entry_id"`
	Lines             []JournalLine `json:"lines"`
}

const entryCols = `e.id, e.org_id, e.site_id, e.entry_date::text, e.document_date::text, e.fiscal_year_id, NULLIF(e.memo, ''), e.source_type, e.source_id, e.posted_by, e.posted_at, e.reverses_entry_id,
	(SELECT r.id FROM journal_entry r WHERE r.org_id = e.org_id AND r.reverses_entry_id = e.id), s.currency`

func scanEntry(r pgx.Row) (e JournalEntry, cur string, err error) {
	err = r.Scan(&e.ID, &e.OrgID, &e.SiteID, &e.EntryDate, &e.DocumentDate, &e.FiscalYearID, &e.Memo, &e.SourceType, &e.SourceID, &e.PostedBy, &e.PostedAt, &e.ReversesEntryID, &e.ReversedByEntryID, &cur)
	e.PostedAt = e.PostedAt.UTC()
	return
}

func loadLines(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, e *JournalEntry, cur string) error {
	rows, err := tx.Query(ctx, `SELECT l.id, l.account_id, a.code, l.fund_id, l.debit, l.credit, l.unit_id, l.debtor_kind, l.person_id, l.block_id, l.vendor_id
		FROM journal_line l JOIN account a ON a.org_id = l.org_id AND a.id = l.account_id WHERE l.org_id = $1 AND l.entry_id = $2 ORDER BY l.id`, orgID, e.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	e.Lines = []JournalLine{}
	for rows.Next() {
		var l JournalLine
		var d, c int64
		if err := rows.Scan(&l.ID, &l.AccountID, &l.AccountCode, &l.FundID, &d, &c, &l.UnitID, &l.DebtorKind, &l.PersonID, &l.BlockID, &l.VendorID); err != nil {
			return err
		}
		l.Debit, l.Credit = money.New(d, money.Code(cur)), money.New(c, money.Code(cur))
		e.Lines = append(e.Lines, l)
	}
	return rows.Err()
}

// JournalFilter filters ListJournalEntries.
type JournalFilter struct {
	From, To   string
	AccountID  *uuid.UUID
	SourceType string
	SourceID   *uuid.UUID
}

// ListJournalEntries lists a site's entries newest first (money.read).
func (s *Service) ListJournalEntries(ctx context.Context, orgID, siteID uuid.UUID, f JournalFilter, p PageParams) ([]JournalEntry, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []JournalEntry
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if err := RequireSite(ctx, tx, PermMoneyRead, siteID); err != nil {
			return err
		}
		// keyset on id (UUIDv7: creation order), descending
		afterClause := ""
		if after != uuid.Nil {
			afterClause = ` AND e.id < '` + after.String() + `'`
		}
		rows, err := tx.Query(ctx, `SELECT `+entryCols+` FROM journal_entry e JOIN site s ON s.org_id = e.org_id AND s.id = e.site_id
			WHERE e.org_id = $1 AND e.site_id = $2`+afterClause+`
			  AND ($3 = '' OR e.entry_date >= $3::date) AND ($4 = '' OR e.entry_date <= $4::date)
			  AND ($5 = '' OR e.source_type = $5) AND ($6::uuid IS NULL OR e.source_id = $6)
			  AND ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM journal_line l WHERE l.org_id = e.org_id AND l.entry_id = e.id AND l.account_id = $7))
			ORDER BY e.id DESC LIMIT $8`, orgID, siteID, f.From, f.To, f.SourceType, f.SourceID, f.AccountID, limit+1)
		if err != nil {
			return err
		}
		type pair struct {
			e   JournalEntry
			cur string
		}
		var ps []pair
		for rows.Next() {
			e, cur, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return err
			}
			ps = append(ps, pair{e, cur})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range ps {
			if err := loadLines(ctx, tx, orgID, &ps[i].e, ps[i].cur); err != nil {
				return err
			}
			out = append(out, ps[i].e)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(e JournalEntry) uuid.UUID { return e.ID })
	return items, next, nil
}

// GetJournalEntry returns an entry with its lines (money.read).
func (s *Service) GetJournalEntry(ctx context.Context, orgID, entryID uuid.UUID) (JournalEntry, error) {
	var e JournalEntry
	err := s.run(ctx, orgID, "journal entry", func(tx pgx.Tx) error {
		var cur string
		var err error
		if e, cur, err = scanEntry(tx.QueryRow(ctx, `SELECT `+entryCols+` FROM journal_entry e JOIN site s ON s.org_id = e.org_id AND s.id = e.site_id WHERE e.org_id = $1 AND e.id = $2`, orgID, entryID)); err != nil {
			return err
		}
		if err = RequireSite(ctx, tx, PermMoneyRead, e.SiteID); err != nil {
			return err
		}
		return loadLines(ctx, tx, orgID, &e, cur)
	})
	return e, err
}

// ReverseJournalEntry posts the reversal of an entry dated today (money.post).
func (s *Service) ReverseJournalEntry(ctx context.Context, orgID, entryID uuid.UUID, memo string) (JournalEntry, error) {
	var out JournalEntry
	err := s.run(ctx, orgID, "journal entry", func(tx pgx.Tx) error {
		var siteID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT site_id FROM journal_entry WHERE org_id = $1 AND id = $2`, orgID, entryID).Scan(&siteID); err != nil {
			return err
		}
		site, err := SiteFor(ctx, tx, PermMoneyPost, orgID, siteID)
		if err != nil {
			return err
		}
		rid, err := Reverse(ctx, tx, orgID, siteID, entryID, Today(site.Timezone, s.Now()), memo)
		if err != nil {
			return err
		}
		if err := Audit(ctx, tx, orgID, siteID, "journal_entry.reverse", "journal_entry", entryID, nil, map[string]any{"reversal_entry_id": rid}); err != nil {
			return err
		}
		var cur string
		if out, cur, err = scanEntry(tx.QueryRow(ctx, `SELECT `+entryCols+` FROM journal_entry e JOIN site s ON s.org_id = e.org_id AND s.id = e.site_id WHERE e.org_id = $1 AND e.id = $2`, orgID, rid)); err != nil {
			return err
		}
		return loadLines(ctx, tx, orgID, &out, cur)
	})
	return out, err
}

// ManualEntryInput is a manual journal entry (accountant use, admin post-entry).
type ManualEntryInput struct {
	SiteID    uuid.UUID
	EntryDate string
	Memo      string
	Lines     []Line
}

// PostManual posts a manual journal entry (money.post).
func (s *Service) PostManual(ctx context.Context, orgID uuid.UUID, in ManualEntryInput) (uuid.UUID, error) {
	var eid uuid.UUID
	err := s.run(ctx, orgID, "journal entry", func(tx pgx.Tx) (err error) {
		if _, err = SiteFor(ctx, tx, PermMoneyPost, orgID, in.SiteID); err != nil {
			return err
		}
		if eid, err = PostEntry(ctx, tx, Entry{OrgID: orgID, SiteID: in.SiteID, EntryDate: in.EntryDate, Memo: in.Memo, SourceType: "manual", Lines: in.Lines}); err != nil {
			return err
		}
		return Audit(ctx, tx, orgID, in.SiteID, "journal_entry.post", "journal_entry", eid, nil, map[string]any{"memo": in.Memo, "entry_date": in.EntryDate})
	})
	return eid, err
}
