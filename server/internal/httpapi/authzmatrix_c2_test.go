package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of the money operations served in C2 (plans, late fees, declared payments, reports,
// transfers, refunds, expenses, bills and budgets).

// sysCtx is a job-like context that has re-authenticated, for seeding through the services.
func sysCtx(org authztest.Org) context.Context {
	ctx := ledger.SystemContext(context.Background(), org.ID)
	p := *authz.PrincipalFromContext(ctx)
	p.ReauthUntil = time.Now().Add(time.Hour)
	return authz.WithPrincipal(ctx, &p)
}

type c2Site struct {
	f  authztest.SiteFix
	ch ledger.Chart
}

func c2NewSite(w *authztest.World, t *testing.T, org authztest.Org) c2Site {
	f := w.NewSite(t, org)
	return c2Site{f: f, ch: seedChart(w, t, org, f.Site)}
}

func c2Billing(w *authztest.World) *billing.Service {
	return billing.NewService(w.DB, tenancy.NewService(w.DB), nil)
}

func c2Treasury(w *authztest.World) *treasury.Service { return treasury.NewService(w.DB, nil, nil) }

func (s c2Site) cashBox(w *authztest.World, t *testing.T) uuid.UUID {
	acc := id.New()
	w.Exec(t, `INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,'1001','Cash','asset','cash_box',$4,true)`, acc, s.f.Org.ID, s.f.Site, s.ch.OperatingFund)
	return acc
}

func (s c2Site) plan(w *authztest.World, t *testing.T) uuid.UUID {
	amount := int64(1000)
	p, err := c2Billing(w).CreatePlan(sysCtx(s.f.Org), s.f.Org.ID, billing.PlanInput{SiteID: s.f.Site, Name: "Aidat", Start: "2026-01-01", AmountMode: "per_unit", Amount: &amount})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func (s c2Site) charge(w *authztest.World, t *testing.T) uuid.UUID {
	c, err := c2Billing(w).CreateCharge(sysCtx(s.f.Org), s.f.Org.ID, billing.ChargeInput{SiteID: s.f.Site, UnitID: s.f.Unit, ChargeType: "special_assessment", DebtorKind: "owner",
		Amount: money.New(500, "TRY"), DueDate: "2026-02-01"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func (s c2Site) fiscalYear(w *authztest.World, t *testing.T) uuid.UUID {
	fy := id.New()
	w.Exec(t, `INSERT INTO fiscal_year (id, org_id, site_id, start_on, end_on) VALUES ($1,$2,$3,'2026-01-01','2026-12-31')`, fy, s.f.Org.ID, s.f.Site)
	return fy
}

func (s c2Site) opening(w *authztest.World, t *testing.T) uuid.UUID {
	ob, err := c2Billing(w).CreateOpeningBalance(sysCtx(s.f.Org), s.f.Org.ID, billing.OpeningInput{SiteID: s.f.Site, AsOf: "2025-12-31", Kind: "unit_receivable", UnitID: &s.f.Unit, DebtorKind: "owner", Principal: 1000}, false)
	if err != nil {
		t.Fatal(err)
	}
	return ob.ID
}

func (s c2Site) declared(w *authztest.World, t *testing.T) uuid.UUID {
	unit := s.f.Unit
	p, err := payments.NewService(w.DB, nil, nil).Declare(sysCtx(s.f.Org), s.f.Org.ID, payments.DeclareInput{SiteID: s.f.Site, Debtor: billing.Debtor{UnitID: &unit, Kind: "owner"}, Amount: money.New(100, "TRY"), PaidOn: "2026-01-10"})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func (s c2Site) expenseInput() treasury.ExpenseInput {
	return treasury.ExpenseInput{SiteID: s.f.Site, CategoryAccountID: s.ch.AccountCode("5000"), FundID: s.ch.OperatingFund, Amount: money.New(300, "TRY"), PaidOn: "2026-01-10",
		PaidFromAccountID: s.ch.Account(ledger.SubBank), Description: "Lamps"}
}

func (s c2Site) billsOn(w *authztest.World, t *testing.T) {
	w.Exec(t, `UPDATE site SET settings = settings || '{"bills_enabled": true}'::jsonb WHERE org_id = $1 AND id = $2`, s.f.Org.ID, s.f.Site)
}

func (s c2Site) bill(w *authztest.World, t *testing.T) uuid.UUID {
	s.billsOn(w, t)
	v := w.NewVendor(t, s.f)
	b, err := c2Treasury(w).CreateBill(sysCtx(s.f.Org), s.f.Org.ID, treasury.BillInput{SiteID: s.f.Site, VendorID: v, IssueDate: "2026-01-05", DueDate: "2026-02-05", Amount: money.New(900, "TRY"),
		CategoryAccountID: s.ch.AccountCode("5000"), FundID: s.ch.OperatingFund})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func init() {
	siteQ := func(path string, ps []authz.Permission, extra string) mrow {
		return mrow{perms: ps, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("GET", orgPath(org, path+"?site_id="+s.f.Site.String()+extra), "")
		}}
	}
	on := func(ps []authz.Permission, status int, method, path string, build func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string)) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			suffix, body, h := build(w, t, s)
			return jsonReq(method, orgPath(org, path+suffix), body, h...)
		}}
	}
	ifm := "If-Match"
	rows := map[string]mrow{
		"money.getChargePlan": on(perms(ledger.PermMoneyRead), 200, "GET", "/charge-plans/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.plan(w, t).String(), "", nil
		}),
		"money.updateChargePlan": on(perms(ledger.PermMoneyManage), 200, "PATCH", "/charge-plans/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.plan(w, t).String(), `{"name":"Aidat 2"}`, []string{ifm, `"1"`}
		}),
		"money.listChargePlanRates": on(perms(ledger.PermMoneyRead), 200, "GET", "/charge-plans/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.plan(w, t).String() + "/rates", "", nil
		}),
		"money.createChargePlanRate": on(perms(ledger.PermMoneyManage), 201, "POST", "/charge-plans/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.plan(w, t).String() + "/rates", `{"from":"2027-01-01","amount":{"amount":1200,"currency":"TRY"}}`, moneyIdem()
		}),
		"money.deleteChargePlanRate": on(perms(ledger.PermMoneyManage), 204, "DELETE", "/charge-plans/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			p := s.plan(w, t)
			r, err := c2Billing(w).AddRate(sysCtx(s.f.Org), s.f.Org.ID, p, billing.RateInput{From: "2027-01-01", Amount: 1200})
			if err != nil {
				t.Fatal(err)
			}
			return p.String() + "/rates/" + r.ID.String(), "", moneyIdem()
		}),
		"money.previewChargePlan": on(perms(ledger.PermMoneyManage), 200, "POST", "/charge-plans:preview", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"plan_id":"%s","period":"2026-03"}`, s.plan(w, t)), nil
		}),
		"money.getChargeGenerationStatus": siteQ("/charge-generation", perms(ledger.PermMoneyRead), ""),
		"money.getCharge": on(anyPerms(ledger.PermMoneyRead, ledger.PermStatementsReadOwn), 200, "GET", "/charges/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.charge(w, t).String(), "", nil
		}),
		"money.cancelCharge": on(perms(ledger.PermMoneyPost), 200, "POST", "/charges/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.charge(w, t).String() + ":cancel", `{"reason":"matrix"}`, moneyIdem()
		}),
		"money.getOpeningBalance": on(perms(ledger.PermMoneyRead), 200, "GET", "/opening-balances/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.opening(w, t).String(), "", nil
		}),
		"money.voidOpeningBalance": {perms: perms(ledger.PermMoneyPost), status: 200, override: needsReauth, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("POST", orgPath(org, "/opening-balances/"+s.opening(w, t).String()+":void"), `{"reason":"matrix"}`, moneyIdem()...)
		}},
		"money.importOpeningBalances": {perms: perms(ledger.PermMoneyPost), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			csv := fmt.Sprintf("kind,unit,side,principal,late_fee,account_id\nunit_receivable,%s,owner,100.50,0,\n", s.f.DoorNum)
			r := multipartReq(orgPath(org, "/opening-balances:import?dry_run=true"), map[string]string{"site_id": s.f.Site.String(), "as_of": "2025-12-31"}, "opening.csv", csv)
			r.headers["Idempotency-Key"] = "matrix-" + id.New().String()
			return r
		}},
		"money.listLateFeeAccruals": siteQ("/late-fee-accruals", perms(ledger.PermMoneyRead), ""),
		"money.listLateFeeBookings": siteQ("/late-fee-bookings", perms(ledger.PermMoneyRead), ""),
		"money.listLateFeeWaivers":  siteQ("/late-fee-waivers", perms(ledger.PermMoneyRead), ""),
		// authorized callers get as far as "there is no late fee to waive" (409)
		"money.createLateFeeWaiver": on(perms(ledger.PermMoneyPost), 409, "POST", "/late-fee-waivers", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","amount":{"amount":100,"currency":"TRY"},"reason":"goodwill","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"owner"}}`, s.f.Site, s.f.Unit), moneyIdem()
		}),
		"money.listLateFeeFreezes": siteQ("/late-fee-freezes", perms(ledger.PermMoneyRead), ""),
		"money.createLateFeeFreeze": on(perms(ledger.PermMoneyManage), 201, "POST", "/late-fee-freezes", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","from":"2026-01-01","reason":"legal","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"tenant"}}`, s.f.Site, s.f.Unit), nil
		}),
		"money.getLateFeeFreeze": on(perms(ledger.PermMoneyRead), 200, "GET", "/late-fee-freezes/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.freeze(w, t).String(), "", nil
		}),
		"money.updateLateFeeFreeze": on(perms(ledger.PermMoneyManage), 200, "PATCH", "/late-fee-freezes/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.freeze(w, t).String(), `{"reason":"settled"}`, []string{ifm, `"1"`}
		}),
		"money.declarePayment": on(perms(ledger.PermPaymentsDeclare), 201, "POST", "/payments:declare", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","amount":{"amount":100,"currency":"TRY"},"paid_on":"2026-01-10","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"owner"}}`, s.f.Site, s.f.Unit), moneyIdem()
		}),
		"money.confirmPayment": {perms: perms(ledger.PermPaymentsRecord), status: 200, override: collectorOverride, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("POST", orgPath(org, "/payments/"+s.declared(w, t).String()+":confirm"), fmt.Sprintf(`{"asset_account_id":"%s"}`, s.ch.Account(ledger.SubBank)), moneyIdem()...)
		}},
		"money.rejectPayment": {perms: perms(ledger.PermPaymentsRecord), status: 200, override: collectorOverride, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return jsonReq("POST", orgPath(org, "/payments/"+s.declared(w, t).String()+":reject"), `{"reason":"not received"}`)
		}},
		"money.getPersonStatement": {perms: anyPerms(ledger.PermStatementsRead, ledger.PermStatementsReadOwn), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			p := w.NewPerson(t, org)
			cid := s.charge(w, t)
			w.Exec(t, `UPDATE charge SET debtor_kind = 'person', debtor_person_id = $3 WHERE org_id = $1 AND id = $2`, org.ID, cid, p)
			return mreq{method: "GET", path: orgPath(org, "/persons/"+p.String()+"/statement")}
		}},
		"money.getIncomeExpenseReport":    siteQ("/reports/income-expense", anyPerms(ledger.PermReportsRead, ledger.PermReportsAggregate), "&from=2026-01-01&to=2026-12-31"),
		"money.getTrialBalanceReport":     siteQ("/reports/trial-balance", perms(ledger.PermReportsRead), ""),
		"money.getReceivablesAgingReport": siteQ("/reports/receivables-aging", perms(ledger.PermReportsRead), ""),
		"money.getFundBalancesReport":     siteQ("/reports/fund-balances", perms(ledger.PermReportsRead), ""),
		"money.getBudgetVsActualReport": {perms: anyPerms(ledger.PermReportsRead, ledger.PermReportsAggregate), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			s := c2NewSite(w, t, org)
			return mreq{method: "GET", path: orgPath(org, "/reports/budget-vs-actual?site_id="+s.f.Site.String()+"&fiscal_year_id="+s.fiscalYear(w, t).String())}
		}},
		"money.getMoneyOverview": siteQ("/money/overview", perms(ledger.PermMoneyRead), ""),

		"money.getPaymentReceipt": {perms: anyPerms(ledger.PermPaymentsRecord, ledger.PermStatementsReadOwn), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/payments/"+seedPayment(w, t, org).String()+"/receipt")}
		}},
		"money.sendPaymentReceipt": {perms: perms(ledger.PermPaymentsRecord), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/payments/"+seedPayment(w, t, org).String()+":send-receipt"), `{"channels":["email"]}`)
		}},
		"money.sendStatement": on(perms(ledger.PermStatementsRead), 200, "POST", "/statements:send", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"owner"},"from":"2026-01-01","to":"2026-01-31","channels":["in_app"]}`, s.f.Unit), moneyIdem()
		}),
		"money.listTransfers": siteQ("/transfers", perms(ledger.PermMoneyRead), ""),
		"money.createTransfer": on(perms(ledger.PermMoneyPost), 201, "POST", "/transfers", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","from_account_id":"%s","to_account_id":"%s","amount":{"amount":500,"currency":"TRY"},"transferred_on":"2026-01-12"}`, s.f.Site, s.cashBox(w, t), s.ch.Account(ledger.SubBank)), moneyIdem()
		}),
		"money.getTransfer": on(perms(ledger.PermMoneyRead), 200, "GET", "/transfers/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			tr, err := c2Treasury(w).CreateTransfer(sysCtx(s.f.Org), s.f.Org.ID, treasury.TransferInput{SiteID: s.f.Site, FromAccountID: s.cashBox(w, t), ToAccountID: s.ch.Account(ledger.SubBank),
				Amount: money.New(500, "TRY"), TransferredOn: "2026-01-12"})
			if err != nil {
				t.Fatal(err)
			}
			return tr.ID.String(), "", nil
		}),
		"money.listRefunds": siteQ("/refunds", perms(ledger.PermMoneyRead), ""),
		"money.createRefund": on(perms(ledger.PermMoneyPost), 201, "POST", "/refunds", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","amount":{"amount":200,"currency":"TRY"},"reason":"returned","kind":"other","paid_from_account_id":"%s","refunded_on":"2026-01-12"}`, s.f.Site, s.ch.Account(ledger.SubBank)), moneyIdem()
		}),
		"money.getRefund": on(perms(ledger.PermMoneyRead), 200, "GET", "/refunds/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			r, err := c2Treasury(w).CreateRefund(sysCtx(s.f.Org), s.f.Org.ID, treasury.RefundInput{SiteID: s.f.Site, Amount: money.New(200, "TRY"), Reason: "returned", Kind: "other",
				PaidFromAccount: s.ch.Account(ledger.SubBank), RefundedOn: "2026-01-12"})
			if err != nil {
				t.Fatal(err)
			}
			return r.ID.String(), "", nil
		}),
		"money.listExpenses": siteQ("/expenses", perms(ledger.PermMoneyRead), ""),
		"money.createExpense": on(perms(ledger.PermMoneyPost), 201, "POST", "/expenses", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","category_account_id":"%s","fund_id":"%s","amount":{"amount":300,"currency":"TRY"},"paid_on":"2026-01-10","paid_from_account_id":"%s","description":"Lamps","recurring":{"frequency":"monthly","day":10}}`,
				s.f.Site, s.ch.AccountCode("5000"), s.ch.OperatingFund, s.ch.Account(ledger.SubBank)), moneyIdem()
		}),
		"money.getExpense": on(perms(ledger.PermMoneyRead), 200, "GET", "/expenses/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			e, err := c2Treasury(w).CreateExpense(sysCtx(s.f.Org), s.f.Org.ID, s.expenseInput())
			if err != nil {
				t.Fatal(err)
			}
			return e.ID.String(), "", nil
		}),
		"money.voidExpense": on(perms(ledger.PermMoneyPost), 200, "POST", "/expenses/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			e, err := c2Treasury(w).CreateExpense(sysCtx(s.f.Org), s.f.Org.ID, s.expenseInput())
			if err != nil {
				t.Fatal(err)
			}
			return e.ID.String() + ":void", `{"reason":"duplicate"}`, moneyIdem()
		}),
		"money.listBills": siteQ("/bills", perms(ledger.PermMoneyRead), ""),
		"money.createBill": on(perms(ledger.PermMoneyPost), 201, "POST", "/bills", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			s.billsOn(w, t)
			return "", fmt.Sprintf(`{"site_id":"%s","vendor_id":"%s","issue_date":"2026-01-05","due_date":"2026-02-05","amount":{"amount":900,"currency":"TRY"},"category_account_id":"%s","fund_id":"%s"}`,
				s.f.Site, w.NewVendor(t, s.f), s.ch.AccountCode("5000"), s.ch.OperatingFund), moneyIdem()
		}),
		"money.getBill": on(perms(ledger.PermMoneyRead), 200, "GET", "/bills/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.bill(w, t).String(), "", nil
		}),
		"money.createBillPayment": on(perms(ledger.PermMoneyPost), 201, "POST", "/bills/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.bill(w, t).String() + "/payments", fmt.Sprintf(`{"amount":{"amount":400,"currency":"TRY"},"paid_on":"2026-01-20","paid_from_account_id":"%s"}`, s.ch.Account(ledger.SubBank)), moneyIdem()
		}),
		"money.listBudgets": siteQ("/budgets", perms(ledger.PermMoneyRead), ""),
		"money.createBudget": on(perms(ledger.PermMoneyManage), 201, "POST", "/budgets", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return "", fmt.Sprintf(`{"site_id":"%s","fiscal_year_id":"%s"}`, s.f.Site, s.fiscalYear(w, t)), nil
		}),
		"money.getBudget": on(perms(ledger.PermMoneyRead), 200, "GET", "/budgets/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.budget(w, t).String(), "", nil
		}),
		"money.updateBudget": on(perms(ledger.PermMoneyManage), 200, "PATCH", "/budgets/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.budget(w, t).String(), `{"decision_id":null}`, []string{ifm, `"1"`}
		}),
		"money.deleteBudget": on(perms(ledger.PermMoneyManage), 204, "DELETE", "/budgets/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.budget(w, t).String(), "", []string{ifm, `"1"`}
		}),
		"money.getBudgetLines": on(perms(ledger.PermMoneyRead), 200, "GET", "/budgets/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.budget(w, t).String() + "/lines", "", nil
		}),
		"money.putBudgetLines": on(perms(ledger.PermMoneyManage), 200, "PUT", "/budgets/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			return s.budget(w, t).String() + "/lines", fmt.Sprintf(`{"lines":[{"expense_category_account_id":"%s","fund_id":"%s","amount":{"amount":5000,"currency":"TRY"}}]}`, s.ch.AccountCode("5000"), s.ch.OperatingFund), []string{ifm, `"1"`}
		}),
		"money.finalizeBudget": on(perms(ledger.PermMoneyManage), 200, "POST", "/budgets/", func(w *authztest.World, t *testing.T, s c2Site) (string, string, []string) {
			b := s.budget(w, t)
			if _, err := c2Treasury(w).PutBudgetLines(sysCtx(s.f.Org), s.f.Org.ID, b, 1, []treasury.BudgetLine{{ExpenseCategoryAccountID: s.ch.AccountCode("5000"), FundID: s.ch.OperatingFund, Amount: money.New(5000, "TRY")}}); err != nil {
				t.Fatal(err)
			}
			return b.String() + ":finalize", `{}`, nil
		}),
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}

func (s c2Site) freeze(w *authztest.World, t *testing.T) uuid.UUID {
	fid, err := c2Billing(w).CreateLateFeeFreeze(sysCtx(s.f.Org), s.f.Org.ID, billing.FreezeInput{SiteID: s.f.Site, UnitID: s.f.Unit, DebtorKind: "owner", From: "2026-01-01", Reason: "legal"})
	if err != nil {
		t.Fatal(err)
	}
	return fid
}

func (s c2Site) budget(w *authztest.World, t *testing.T) uuid.UUID {
	b, err := c2Treasury(w).CreateBudget(sysCtx(s.f.Org), s.f.Org.ID, s.f.Site, s.fiscalYear(w, t), nil)
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

// needsReauth: the matrix sessions have not re-authenticated recently, so a sensitive operation stops at 403 reauth_required
// for everyone who holds its permission.
func needsReauth(actor, target string) (int, bool) {
	switch actor {
	case "org_admin", "site_manager", "board_member", "accountant":
		if target == "own" {
			return 403, true
		}
	}
	return 0, false
}
