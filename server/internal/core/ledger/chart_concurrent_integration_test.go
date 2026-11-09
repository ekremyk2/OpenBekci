package ledger_test

import (
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/moneytest"
)

// A pack creates the chart when it is enabled or a site is created, while a
// money entry point may create it on first use: concurrent callers must end with
// one chart and one default late fee row, and none of them may fail.
func TestIntegrationEnsureChartConcurrentCallersCreateOneChart(t *testing.T) {
	e := moneytest.New(t)
	ensure := func() error {
		return e.DB.InTenantTx(e.Ctx, e.OrgID, func(tx pgx.Tx) error {
			_, err := ledger.EnsureChart(e.Ctx, tx, e.OrgID, e.SiteID)
			return err
		})
	}
	count := func(table string) (n int) {
		if err := e.DB.InPlatformTx(e.Ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(e.Ctx, `SELECT count(*) FROM `+table+` WHERE org_id = $1 AND site_id = $2`, e.OrgID, e.SiteID).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	race := func() {
		t.Helper()
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- ensure() }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("a concurrent EnsureChart failed: %v", err)
			}
		}
	}

	race() // a site without a chart
	accounts, funds := count("account"), count("fund")
	if accounts == 0 || funds != 2 || count("late_fee_policy") != 1 {
		t.Fatalf("accounts %d funds %d late fee rows %d", accounts, funds, count("late_fee_policy"))
	}

	// a site that has its chart but lost the automatic default row (the row is created again, once)
	if err := e.DB.InPlatformTx(e.Ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.Ctx, `DELETE FROM late_fee_policy WHERE org_id = $1 AND site_id = $2 AND is_default`, e.OrgID, e.SiteID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	race()
	if count("account") != accounts || count("fund") != funds || count("late_fee_policy") != 1 {
		t.Errorf("after the second race: accounts %d funds %d late fee rows %d", count("account"), count("fund"), count("late_fee_policy"))
	}
}
