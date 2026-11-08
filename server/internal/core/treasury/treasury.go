// Package treasury is the money that goes out and moves inside the building (backend plan 4.7):
// transfers between cash boxes and bank accounts, refunds of credit, expenses (light, with
// optional recurrence), bills and their payments (behind the site setting bills_enabled) and
// budgets with the budget-versus-actual report.
package treasury

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SettingBillsEnabled is the site setting that turns the optional bill workflow on.
const SettingBillsEnabled = "bills_enabled"

// Service is the treasury module.
type Service struct {
	db    ledger.TxRunner
	Now   func() time.Time
	Files *files.Service
}

// NewService returns a Service; now nil means time.Now. fs may be nil (receipt files are then refused).
func NewService(db ledger.TxRunner, fs *files.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, Files: fs, Now: now}
}

func (s *Service) run(ctx context.Context, orgID uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.db.InTenantTx(ctx, orgID, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return ledger.DBProblem(err, what)
}

func bad(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// accountInfo is what the treasury operations need to know about an account.
type accountInfo struct {
	id      uuid.UUID
	typ     string
	subtype string
	fund    *uuid.UUID
}

func loadAccount(ctx context.Context, tx pgx.Tx, orgID, siteID, accountID uuid.UUID) (accountInfo, error) {
	a := accountInfo{id: accountID}
	err := tx.QueryRow(ctx, `SELECT type, COALESCE(subtype, ''), fund_id FROM account WHERE org_id=$1 AND site_id=$2 AND id=$3 AND NOT archived`, orgID, siteID, accountID).Scan(&a.typ, &a.subtype, &a.fund)
	return a, err
}

// cashAccount loads an account that holds money: a bank account or a cash box.
func cashAccount(ctx context.Context, tx pgx.Tx, orgID, siteID, accountID uuid.UUID, field string) (accountInfo, error) {
	a, err := loadAccount(ctx, tx, orgID, siteID, accountID)
	if err != nil || a.typ != "asset" || (a.subtype != ledger.SubBank && a.subtype != ledger.SubCashBox) {
		return a, bad(field, "invalid_value")
	}
	return a, nil
}

// fundOfAccount is the fund an account's postings belong to: its own, or the operating fund.
func fundOfAccount(a accountInfo, ch ledger.Chart) uuid.UUID {
	if a.fund != nil {
		return *a.fund
	}
	return ch.OperatingFund
}

func today(site string, now time.Time) string { return ledger.Today(site, now) }

func checkAmount(amount money.Money, cur money.Code) error {
	if amount.Amount <= 0 || amount.Currency != cur {
		return bad("amount", "invalid_value")
	}
	return nil
}

func checkDay(field, day, today string) error {
	if _, err := ledger.ParseDate(day); err != nil {
		return bad(field, "invalid_format")
	}
	if day > today {
		return bad(field, "in_the_future")
	}
	return nil
}

func actorOrNil(ctx context.Context) uuid.UUID {
	if u := ledger.Actor(ctx); u != nil {
		return *u
	}
	return uuid.Nil
}
