package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/collections"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of phase C3 (bank statement import and collections).

const matrixStatement = "booked_on,amount,counterparty,description,reference\n2026-01-10,\"1.000,00\",Ayse,Aidat,A-1-2026-01\n2026-01-11,\"-25,00\",Banka,Masraf,\n"

// seedImport imports a two-line statement through the service on a fresh site and returns the site, unit,
// bank account, import id and the two line ids (the incoming line first).
func seedImport(w *authztest.World, t *testing.T, org authztest.Org) (site, unit, account, imp, in, out uuid.UUID) {
	t.Helper()
	f := w.NewSite(t, org)
	ch := seedChart(w, t, org, f.Site)
	ctx := ledger.SystemContext(context.Background(), org.ID)
	svc := bankimport.NewService(w.DB, payments.NewService(w.DB, nil, nil), nil)
	im, err := svc.Create(ctx, org.ID, bankimport.CreateInput{SiteID: f.Site, Importer: bankimport.CoreCSV, AssetAccountID: ch.Account(ledger.SubBank), Filename: "s.csv", Data: []byte(matrixStatement)})
	if err != nil {
		t.Fatal(err)
	}
	lines, _, err := svc.ListTransactions(ctx, org.ID, f.Site, bankimport.TxFilter{ImportID: &im.ID}, ledger.PageParams{})
	if err != nil || len(lines) != 2 {
		t.Fatalf("lines %v %v", lines, err)
	}
	return f.Site, f.Unit, ch.Account(ledger.SubBank), im.ID, lines[0].ID, lines[1].ID
}

func seedStage(w *authztest.World, t *testing.T, org authztest.Org, site uuid.UUID) uuid.UUID {
	t.Helper()
	sid := id.New()
	w.Exec(t, `INSERT INTO dunning_stage (id, org_id, site_id, name, kind, days_after_due, channels, position) VALUES ($1,$2,$3,'Reminder','reminder',3,'{in_app}',1)`, sid, org.ID, site)
	return sid
}

func seedCase(w *authztest.World, t *testing.T, org authztest.Org) uuid.UUID {
	t.Helper()
	f := w.NewSite(t, org)
	seedChart(w, t, org, f.Site)
	cid := id.New()
	w.Exec(t, `INSERT INTO collection_case (id, org_id, site_id, unit_id, debtor_kind, opened_on) VALUES ($1,$2,$3,$4,'owner','2026-01-10')`, cid, org.ID, f.Site, f.Unit)
	return cid
}

func init() {
	siteGet := func(path string, ps ...string) func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		return func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			q := "?site_id=" + f.Site.String()
			for _, p := range ps {
				q += "&" + p
			}
			return mreq{method: "GET", path: orgPath(org, path+q)}
		}
	}
	read := perms(ledger.PermMoneyRead)
	manage := perms(ledger.PermCollectionsManage)
	rec := perms(ledger.PermPaymentsRecord)
	ifm := "If-Match"
	rows := map[string]mrow{
		"money.listBankImporters": {perms: read, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/bank-importers")}
		}},
		"money.listBankStatementImports": {perms: read, status: 200, build: siteGet("/bank-statement-imports")},
		"money.createBankStatementImport": {perms: perms(ledger.PermMoneyPost), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			ch := seedChart(w, t, org, f.Site)
			return multipartReq(orgPath(org, "/bank-statement-imports"), map[string]string{"site_id": f.Site.String(), "importer": "core_csv", "asset_account_id": ch.Account(ledger.SubBank).String()}, "s.csv", matrixStatement)
		}},
		"money.getBankStatementImport": {perms: read, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, _, _, imp, _, _ := seedImport(w, t, org)
			return mreq{method: "GET", path: orgPath(org, "/bank-statement-imports/"+imp.String())}
		}},
		"money.deleteBankStatementImport": {perms: perms(ledger.PermMoneyPost), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, _, _, imp, _, _ := seedImport(w, t, org)
			return mreq{method: "DELETE", path: orgPath(org, "/bank-statement-imports/"+imp.String())}
		}},
		"money.getImportReconciliation": {perms: read, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, _, _, imp, _, _ := seedImport(w, t, org)
			return mreq{method: "GET", path: orgPath(org, "/bank-statement-imports/"+imp.String()+"/reconciliation")}
		}},
		"money.listBankTransactions": {perms: read, status: 200, build: siteGet("/bank-transactions")},
		"money.getBankTransaction": {perms: read, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, _, _, _, in, _ := seedImport(w, t, org)
			return mreq{method: "GET", path: orgPath(org, "/bank-transactions/"+in.String())}
		}},
		"money.matchBankTransaction": {perms: rec, status: 200, override: collectorOverride, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, unit, _, _, in, _ := seedImport(w, t, org)
			return jsonReq("POST", orgPath(org, "/bank-transactions/"+in.String()+":match"), fmt.Sprintf(`{"debtor":{"unit_id":"%s","debtor_kind":"owner"}}`, unit), moneyIdem()...)
		}},
		"money.unmatchBankTransaction": {perms: rec, status: 200, override: collectorOverride, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			site, unit, _, _, in, _ := seedImport(w, t, org)
			svc := bankimport.NewService(w.DB, payments.NewService(w.DB, nil, nil), nil)
			if _, err := svc.Match(ledger.SystemContext(context.Background(), org.ID), org.ID, in, bankimport.MatchInput{Debtor: &billing.Debtor{UnitID: &unit, Kind: "owner"}}); err != nil {
				t.Fatal(err)
			}
			_ = site
			return jsonReq("POST", orgPath(org, "/bank-transactions/"+in.String()+":unmatch"), `{"reason":"matrix"}`, moneyIdem()...)
		}},
		"money.ignoreBankTransaction": {perms: rec, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, _, _, _, _, out := seedImport(w, t, org)
			return jsonReq("POST", orgPath(org, "/bank-transactions/"+out.String()+":ignore"), `{"reason":"bank fee"}`)
		}},

		"money.listDunningStages": {perms: read, status: 200, build: siteGet("/dunning-stages")},
		"money.createDunningStage": {perms: manage, status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/dunning-stages"), fmt.Sprintf(`{"site_id":"%s","name":"Reminder 1","kind":"reminder","days_after_due":3,"channels":["in_app","letter"]}`, f.Site))
		}},
		"money.getDunningStage": {perms: read, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return mreq{method: "GET", path: orgPath(org, "/dunning-stages/"+seedStage(w, t, org, f.Site).String())}
		}},
		"money.updateDunningStage": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("PATCH", orgPath(org, "/dunning-stages/"+seedStage(w, t, org, f.Site).String()), `{"enabled":false,"template_key":null}`, ifm, `"1"`)
		}},
		"money.deleteDunningStage": {perms: manage, status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("DELETE", orgPath(org, "/dunning-stages/"+seedStage(w, t, org, f.Site).String()), "", ifm, `"1"`)
		}},
		"money.previewDunningRun": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/dunning-runs:preview"), fmt.Sprintf(`{"site_id":"%s","stage_id":"%s","channels":["in_app"]}`, f.Site, seedStage(w, t, org, f.Site)))
		}},
		"money.createDunningRun": {perms: manage, status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/dunning-runs"), fmt.Sprintf(`{"site_id":"%s","stage_id":"%s","channels":["in_app"]}`, f.Site, seedStage(w, t, org, f.Site)), moneyIdem()...)
		}},
		"money.listDunningEvents": {perms: read, status: 200, build: siteGet("/dunning-events")},
		"money.getDunningLetter": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return mreq{method: "GET", path: orgPath(org, fmt.Sprintf("/dunning-letter?site_id=%s&unit_id=%s&debtor_kind=owner", f.Site, f.Unit))}
		}},

		"money.listCollectionCases": {perms: read, status: 200, build: siteGet("/collection-cases")},
		"money.createCollectionCase": {perms: manage, status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return jsonReq("POST", orgPath(org, "/collection-cases"), fmt.Sprintf(`{"site_id":"%s","debtor":{"unit_id":"%s","debtor_kind":"owner"},"opened_on":"2026-02-01","lawyer":"Av. Demir"}`, f.Site, f.Unit))
		}},
		"money.getCollectionCase": {perms: read, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/collection-cases/"+seedCase(w, t, org).String())}
		}},
		"money.updateCollectionCase": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/collection-cases/"+seedCase(w, t, org).String()), `{"lawyer":"Av. Demir","file_no":null}`, ifm, `"1"`)
		}},
		"money.closeCollectionCase": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/collection-cases/"+seedCase(w, t, org).String()+":close"), `{"note":"settled"}`)
		}},
		"money.getDebtBreakdown": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			seedChart(w, t, org, f.Site)
			return mreq{method: "GET", path: orgPath(org, fmt.Sprintf("/debt-breakdown?site_id=%s&unit_id=%s&debtor_kind=owner", f.Site, f.Unit))}
		}},
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
	_ = collections.LetterDays
}

// newTestMoney wires the money services of the test router.
