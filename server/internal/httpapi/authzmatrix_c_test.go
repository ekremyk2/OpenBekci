package httpapi

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of phase C (money). Every build seeds the site and the
// resources it needs afresh, so the repeated mutating calls of the matrix find their target.

// anyPerms is the matrix form of an operation that accepts any one of its permissions:
// one "a|b" alternatives name, sorted as the spec lists it.
func anyPerms(ps ...authz.Permission) []authz.Permission {
	sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
	return []authz.Permission{authz.AnyOf(ps...)}
}

func moneyIdem() []string { return []string{"Idempotency-Key", "matrix-" + id.New().String()} }

// seedChart creates the default chart of a fresh site and returns it.
func seedChart(w *authztest.World, t *testing.T, org authztest.Org, site uuid.UUID) ledger.Chart {
	t.Helper()
	var ch ledger.Chart
	err := w.DB.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) (err error) {
		ch, err = ledger.EnsureChart(context.Background(), tx, org.ID, site)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// seedEntry posts a bank/other-income entry on a fresh site.
func seedEntry(w *authztest.World, t *testing.T, org authztest.Org, site uuid.UUID, ch ledger.Chart) uuid.UUID {
	t.Helper()
	var eid uuid.UUID
	err := w.DB.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) (err error) {
		eid, err = ledger.PostEntry(context.Background(), tx, ledger.Entry{OrgID: org.ID, SiteID: site, EntryDate: "2026-01-05", SourceType: "matrix", Lines: []ledger.Line{
			{AccountID: ch.Account(ledger.SubBank), FundID: ch.OperatingFund, Debit: 100},
			{AccountID: ch.Account(ledger.SubOtherIncome), FundID: ch.OperatingFund, Credit: 100},
		}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return eid
}

func init() {
	siteQ := func(method, path string, ps []authz.Permission, status int, body string, headers ...string) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			b := body
			if b != "" {
				b = fmt.Sprintf(b, f.Site)
			}
			p := path
			if method == "GET" {
				p += "?site_id=" + f.Site.String()
			}
			return jsonReq(method, orgPath(org, p), b, headers...)
		}}
	}
	ifm := "If-Match"
	rows := map[string]mrow{
		"money.listFunds":  siteQ("GET", "/funds", perms(ledger.PermMoneyRead), 200, ""),
		"money.createFund": siteQ("POST", "/funds", perms(ledger.PermMoneyManage), 201, `{"site_id":"%s","name":"Roof","kind":"custom"}`),
		"money.getFund": {perms: perms(ledger.PermMoneyRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			ch := seedChart(w, t, org, f.Site)
			return mreq{method: "GET", path: orgPath(org, "/funds/"+ch.OperatingFund.String())}
		}},
		"money.updateFund": {perms: perms(ledger.PermMoneyManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			ch := seedChart(w, t, org, f.Site)
			return jsonReq("PATCH", orgPath(org, "/funds/"+ch.OperatingFund.String()), `{"name":"Renamed"}`, ifm, `"1"`)
		}},
		"money.listAccounts":  siteQ("GET", "/accounts", perms(ledger.PermMoneyRead), 200, ""),
		"money.createAccount": siteQ("POST", "/accounts", perms(ledger.PermMoneyManage), 201, `{"site_id":"%s","code":"5100","name":"Lift","type":"expense"}`),
		"money.getAccount": {perms: perms(ledger.PermMoneyRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			ch := seedChart(w, t, org, f.Site)
			return mreq{method: "GET", path: orgPath(org, "/accounts/"+ch.Account(ledger.SubBank).String())}
		}},
		"money.updateAccount": {perms: perms(ledger.PermMoneyManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			ch := seedChart(w, t, org, f.Site)
			return jsonReq("PATCH", orgPath(org, "/accounts/"+ch.Account(ledger.SubBank).String()), `{"name":"Main bank"}`, ifm, `"1"`)
		}},
		"money.listCashBoxes":      siteQ("GET", "/cash-boxes", perms(ledger.PermMoneyRead), 200, ""),
		"money.createCashBox":      siteQ("POST", "/cash-boxes", perms(ledger.PermMoneyManage), 201, `{"site_id":"%s","name":"Kapici"}`),
		"money.getCashBox":         cashBoxRow("GET", perms(ledger.PermMoneyRead), 200, ""),
		"money.updateCashBox":      cashBoxRow("PATCH", perms(ledger.PermMoneyManage), 200, `{"name":"Kasa"}`),
		"money.listFiscalYears":    siteQ("GET", "/fiscal-years", perms(ledger.PermMoneyRead), 200, ""),
		"money.createFiscalYear":   siteQ("POST", "/fiscal-years", perms(ledger.PermMoneyManage), 201, `{"site_id":"%s","start":"2026-01-01","end":"2026-12-31"}`),
		"money.getFiscalYear":      fyRow("GET", "", "open", perms(ledger.PermMoneyRead), 200),
		"money.closeFiscalYear":    fyRow("POST", ":close", "open", perms(ledger.PermMoneyLock), 200, moneyIdem()...),
		"money.approveFiscalYear":  fyRow("POST", ":approve", "closed", perms(ledger.PermMoneyLock), 200, moneyIdem()...),
		"money.listJournalEntries": siteQ("GET", "/journal-entries", perms(ledger.PermMoneyRead), 200, ""),
		"money.getJournalEntry": {perms: perms(ledger.PermMoneyRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := seedEntry(w, t, org, f.Site, seedChart(w, t, org, f.Site))
			return mreq{method: "GET", path: orgPath(org, "/journal-entries/"+e.String())}
		}},
		"money.reverseJournalEntry": {perms: perms(ledger.PermMoneyPost), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := seedEntry(w, t, org, f.Site, seedChart(w, t, org, f.Site))
			return jsonReq("POST", orgPath(org, "/journal-entries/"+e.String()+":reverse"), `{"reason":"matrix"}`, moneyIdem()...)
		}},
		"money.getLockDate": {perms: perms(ledger.PermMoneyRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return mreq{method: "GET", path: orgPath(org, "/sites/"+f.Site.String()+"/lock-date")}
		}},
		"money.setLockDate": {perms: perms(ledger.PermMoneyLock), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("PUT", orgPath(org, "/sites/"+f.Site.String()+"/lock-date"), `{"lock_date":"2026-01-31"}`, ifm, `"1"`)
		}},

		"money.listChargePlans": siteQ("GET", "/charge-plans", perms(ledger.PermMoneyRead), 200, ""),
		"money.createChargePlan": {perms: perms(ledger.PermMoneyManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/charge-plans"), fmt.Sprintf(`{"site_id":"%s","name":"Aidat","kind":"recurring","charge_type":"dues","amount_mode":"per_unit","allocation_key_id":"%s","amount":{"amount":1000,"currency":"TRY"},"scope":{"type":"site","id":null},"start":"2026-01-01"}`, f.Site, f.Key))
		}},
		"money.listCharges": siteQ("GET", "/charges", anyPerms(ledger.PermMoneyRead, ledger.PermStatementsReadOwn), 200, ""),
		"money.createCharge": {perms: perms(ledger.PermMoneyPost), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/charges"), fmt.Sprintf(`{"site_id":"%s","unit_id":"%s","charge_type":"special_assessment","amount":{"amount":500,"currency":"TRY"},"due_date":"2026-02-01"}`, f.Site, f.Unit), moneyIdem()...)
		}},
		"money.listPayments": siteQ("GET", "/payments", anyPerms(ledger.PermMoneyRead, ledger.PermStatementsReadOwn), 200, ""),
		"money.createPayment": {perms: perms(ledger.PermPaymentsRecord), status: 201, override: collectorOverride, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			ch := seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/payments"), fmt.Sprintf(`{"site_id":"%s","method":"bank_transfer","amount":{"amount":100,"currency":"TRY"},"received_on":"2026-01-10","asset_account_id":"%s","debtor":{"mode":"unit_side","unit_id":"%s","debtor_kind":"owner"}}`, f.Site, ch.Account(ledger.SubBank), f.Unit), moneyIdem()...)
		}},
		"money.getPayment": {perms: anyPerms(ledger.PermMoneyRead, ledger.PermStatementsReadOwn), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/payments/"+seedPayment(w, t, org).String())}
		}},
		"money.voidPayment": {perms: perms(ledger.PermPaymentsRecord), status: 200, override: collectorOverride, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/payments/"+seedPayment(w, t, org).String()+":void"), `{"reason":"matrix"}`, moneyIdem()...)
		}},
		"money.listOpeningBalances": siteQ("GET", "/opening-balances", perms(ledger.PermMoneyRead), 200, ""),
		"money.createOpeningBalance": {perms: perms(ledger.PermMoneyPost), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/opening-balances?dry_run=true"), fmt.Sprintf(`{"site_id":"%s","as_of":"2025-12-31","kind":"unit_receivable","unit_id":"%s","debtor_kind":"owner","principal":{"amount":1000,"currency":"TRY"}}`, f.Site, f.Unit), moneyIdem()...)
		}},
		"money.getLateFeePolicy": siteQ("GET", "/late-fee-policy", perms(ledger.PermMoneyRead), 200, ""),
		"money.setLateFeePolicy": {perms: perms(ledger.PermMoneyManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("PUT", orgPath(org, "/late-fee-policy?site_id="+f.Site.String()), `{"grace_days":0,"rate_pct_per_month":5}`, ifm, `"1"`)
		}},
		"money.getDebtorReport": siteQ("GET", "/reports/debtors", perms(ledger.PermReportsDebtors), 200, ""),
		"money.getUnitStatement": {perms: anyPerms(ledger.PermStatementsRead, ledger.PermStatementsReadOwn), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return mreq{method: "GET", path: orgPath(org, "/units/"+f.Unit.String()+"/statements/owner")}
		}},
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}

func cashBoxRow(method string, ps []authz.Permission, status int, body string) mrow {
	return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		f := w.NewSite(t, org)
		seedChart(w, t, org, f.Site)
		box, acc := id.New(), id.New()
		ch := seedChart(w, t, org, f.Site)
		w.Exec(t, `INSERT INTO account (id, org_id, site_id, code, name, type, subtype, fund_id, is_system) VALUES ($1,$2,$3,'1001','Cash','asset','cash_box',$4,true)`, acc, org.ID, f.Site, ch.OperatingFund)
		w.Exec(t, `INSERT INTO cash_box (id, org_id, site_id, name, account_id) VALUES ($1,$2,$3,'Kapici',$4)`, box, org.ID, f.Site, acc)
		if method == "GET" {
			return mreq{method: "GET", path: orgPath(org, "/cash-boxes/"+box.String())}
		}
		return jsonReq(method, orgPath(org, "/cash-boxes/"+box.String()), body, "If-Match", `"1"`)
	}}
}

func fyRow(method, suffix, status string, ps []authz.Permission, code int, headers ...string) mrow {
	return mrow{perms: ps, status: code, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		f := w.NewSite(t, org)
		fy := id.New()
		w.Exec(t, `INSERT INTO fiscal_year (id, org_id, site_id, start_on, end_on, status) VALUES ($1,$2,$3,'2026-01-01','2026-12-31',$4)`, fy, org.ID, f.Site, status)
		body := ""
		if method == "POST" {
			body = `{}`
		}
		return jsonReq(method, orgPath(org, "/fiscal-years/"+fy.String()+suffix), body, headers...)
	}}
}

// collectorOverride: staff hold payments.record but only for their own cash box (M5), and the matrix staff has none.
func collectorOverride(actor, target string) (int, bool) {
	if actor == "staff" && target == "own" {
		return 403, true
	}
	return 0, false
}

// seedPayment records a payment of 100 through the service on a fresh site and returns its id.
func seedPayment(w *authztest.World, t *testing.T, org authztest.Org) uuid.UUID {
	t.Helper()
	f := w.NewSite(t, org)
	ch := seedChart(w, t, org, f.Site)
	unit := f.Unit
	p, err := payments.NewService(w.DB, nil, nil).Record(ledger.SystemContext(context.Background(), org.ID), org.ID, payments.Input{SiteID: f.Site, Debtor: billing.Debtor{UnitID: &unit, Kind: "owner"},
		Method: "bank_transfer", Amount: money.New(100, "TRY"), ReceivedOn: "2026-01-10", AssetAccountID: ptrTo(ch.Account(ledger.SubBank))})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func ptrTo[T any](v T) *T { return &v }
