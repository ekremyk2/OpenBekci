package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Account subtypes the money modules look up.
const (
	SubBank                = "bank"
	SubCashBox             = "cash_box"
	SubReceivableOwner     = "receivable_owner"
	SubReceivableTenant    = "receivable_tenant"
	SubPersonReceivable    = "person_receivable"
	SubPrepayment          = "prepayment_liability"
	SubDeposit             = "deposit_liability"
	SubAccountsPayable     = "accounts_payable"
	SubOpeningEquity       = "opening_equity"
	SubPriorYearSurplus    = "prior_year_surplus"
	SubDues                = "dues_contribution"
	SubReserveContribution = "reserve_contribution"
	SubSpecialAssessment   = "special_assessment_income"
	SubUtilityRecharge     = "utility_recharge_income"
	SubBookingIncome       = "booking_income"
	SubLegalCostRecovery   = "legal_cost_recovery"
	SubLateFeeIncome       = "late_fee_income"
	SubOtherIncome         = "other_income"
	SubExpenseCategory     = "expense_category"
	SubProvisionSeverance  = "provision_severance"
	SubWithholding         = "withholding_receivable"
)

// Fund codes.
const (
	FundOperating = "operating"
	FundReserve   = "reserve"
)

type chartAccount struct{ Code, Name, Type, Subtype, Fund string }

// defaultChart is the chart created for every site (packs localize the names).
var defaultChart = []chartAccount{
	{"1100", "Bank", "asset", SubBank, FundOperating},
	{"1200", "Receivable: owners", "asset", SubReceivableOwner, ""},
	{"1210", "Receivable: tenants", "asset", SubReceivableTenant, ""},
	{"1220", "Receivable: persons (carried debt)", "asset", SubPersonReceivable, ""},
	{"1300", "Withholding receivable", "asset", SubWithholding, FundOperating},
	{"2000", "Prepayments (credit)", "liability", SubPrepayment, ""},
	{"2100", "Deposits held", "liability", SubDeposit, ""},
	{"2200", "Vendor payables", "liability", SubAccountsPayable, FundOperating},
	{"2300", "Severance provision", "liability", SubProvisionSeverance, FundOperating},
	{"3000", "Opening balance equity", "equity", SubOpeningEquity, ""},
	{"3100", "Prior-year surplus", "equity", SubPriorYearSurplus, ""},
	{"4000", "Dues (aidat)", "income", SubDues, FundOperating},
	{"4010", "Reserve contributions", "income", SubReserveContribution, FundReserve},
	{"4020", "Special assessments", "income", SubSpecialAssessment, FundOperating},
	{"4030", "Utility recharges", "income", SubUtilityRecharge, FundOperating},
	{"4040", "Booking fees", "income", SubBookingIncome, FundOperating},
	{"4050", "Legal cost recovery", "income", SubLegalCostRecovery, FundOperating},
	{"4100", "Late fee income", "income", SubLateFeeIncome, FundOperating},
	{"4900", "Other income", "income", SubOtherIncome, FundOperating},
	{"5000", "General expenses", "expense", SubExpenseCategory, FundOperating},
	{"5010", "Utilities", "expense", SubExpenseCategory, FundOperating},
	{"5020", "Maintenance and repairs", "expense", SubExpenseCategory, FundOperating},
	{"5030", "Staff", "expense", SubExpenseCategory, FundOperating},
	{"5040", "Insurance", "expense", SubExpenseCategory, FundOperating},
	{"5900", "Other expenses", "expense", SubExpenseCategory, FundOperating},
}

// Chart gives the system accounts and funds of a site by subtype and code.
type Chart struct {
	bySub  map[string]uuid.UUID
	byCode map[string]uuid.UUID
	Funds  map[string]uuid.UUID
	// OperatingFund is the default fund.
	OperatingFund uuid.UUID
}

// Account returns the (first by code) system account of subtype.
func (c Chart) Account(subtype string) uuid.UUID { return c.bySub[subtype] }

// AccountCode returns the account with a code.
func (c Chart) AccountCode(code string) uuid.UUID { return c.byCode[code] }

// EnsureChart creates the default funds and chart of the site when missing
// (idempotent) and returns it. Money entry points call it first; there is no
// hook into site creation.
func EnsureChart(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (Chart, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM fund WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&n); err != nil {
		return Chart{}, err
	}
	if n == 0 {
		// serialize concurrent first uses of the same site
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 7))`, siteID.String()); err != nil {
			return Chart{}, err
		}
		for _, f := range []struct{ code, name, kind string }{{FundOperating, "Operating fund", "operating"}, {FundReserve, "Reserve fund", "reserve"}} {
			if _, err := tx.Exec(ctx, `INSERT INTO fund (id, org_id, site_id, code, name, kind, is_default) VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (org_id, site_id, code) DO NOTHING`, id.New(), orgID, siteID, f.code, f.name, f.kind, f.code == FundOperating); err != nil {
				return Chart{}, err
			}
		}
	}
	c := Chart{bySub: map[string]uuid.UUID{}, byCode: map[string]uuid.UUID{}, Funds: map[string]uuid.UUID{}}
	rows, err := tx.Query(ctx, `SELECT code, id FROM fund WHERE org_id = $1 AND site_id = $2`, orgID, siteID)
	if err != nil {
		return Chart{}, err
	}
	for rows.Next() {
		var code string
		var fid uuid.UUID
		if err := rows.Scan(&code, &fid); err != nil {
			rows.Close()
			return Chart{}, err
		}
		c.Funds[code] = fid
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Chart{}, err
	}
	c.OperatingFund = c.Funds[FundOperating]
	if n == 0 {
		for _, a := range defaultChart {
			var fund *uuid.UUID
			if a.Fund != "" {
				f := c.Funds[a.Fund]
				fund = &f
			}
			if _, err := tx.Exec(ctx, `INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
				ON CONFLICT (org_id, site_id, code) DO NOTHING`, id.New(), orgID, siteID, a.Code, a.Name, a.Type, a.Subtype, fund); err != nil {
				return Chart{}, err
			}
		}
	}
	arows, err := tx.Query(ctx, `SELECT code, id, COALESCE(subtype, ''), is_system FROM account WHERE org_id = $1 AND site_id = $2 ORDER BY code`, orgID, siteID)
	if err != nil {
		return Chart{}, err
	}
	defer arows.Close()
	for arows.Next() {
		var code, sub string
		var aid uuid.UUID
		var sys bool
		if err := arows.Scan(&code, &aid, &sub, &sys); err != nil {
			return Chart{}, err
		}
		c.byCode[code] = aid
		if _, ok := c.bySub[sub]; sys && !ok && sub != "" {
			c.bySub[sub] = aid
		}
	}
	if err := arows.Err(); err != nil {
		return Chart{}, err
	}
	if c.bySub[SubBank] == uuid.Nil {
		return Chart{}, fmt.Errorf("ledger: site %s has no default chart", siteID)
	}
	return c, nil
}

// ReceivableSub returns the receivable account subtype of a debtor side.
func ReceivableSub(debtorKind string) string {
	if debtorKind == "tenant" {
		return SubReceivableTenant
	}
	return SubReceivableOwner
}

// IncomeSubtype is the income account subtype a charge type books to ("" for
// deposits, which are liabilities).
func IncomeSubtype(chargeType string) string {
	switch chargeType {
	case "dues":
		return SubDues
	case "reserve_contribution":
		return SubReserveContribution
	case "special_assessment":
		return SubSpecialAssessment
	case "utility":
		return SubUtilityRecharge
	case "booking_fee":
		return SubBookingIncome
	case "legal_cost":
		return SubLegalCostRecovery
	case "deposit":
		return SubDeposit
	}
	return SubOtherIncome
}
