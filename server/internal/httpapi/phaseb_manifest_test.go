package httpapi

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// A new tenant table must be listed in the site export manifest, either as
// exported or as excluded with a reason, so a site hand-over cannot silently
// lose data. Tables outside RLS (instance-wide) have no org_id and are not covered.
func TestIntegrationExportManifestCoversEveryTenantTable(t *testing.T) {
	e := newIDEnv(t, false, false)
	var tables []string
	err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT c.table_name FROM information_schema.columns c
			JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
			WHERE c.table_schema = 'public' AND c.column_name = 'org_id' ORDER BY 1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return err
			}
			tables = append(tables, n)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	// tables of the identity side that carry org_id for isolation but are not site data
	notSiteData := []string{"idempotency_key", "membership_org_stub"}
	manifest := tenancy.ManifestTables()
	for _, tb := range tables {
		if slices.Contains(notSiteData, tb) {
			continue
		}
		if !slices.Contains(manifest, tb) && !slices.Contains([]string{"person", "membership"}, tb) {
			t.Errorf("tenant table %s is not in the site export manifest (tenancy.exportTables or excludedTables): add it", tb)
		}
	}
	for _, m := range manifest {
		if !slices.Contains(tables, m) {
			t.Errorf("the export manifest lists %s, which is not a tenant table", m)
		}
	}
}
