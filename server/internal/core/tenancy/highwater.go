package tenancy

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// siteHighWater is the time of the site's last change: the newest updated_at
// of any row the archive carries, and the newest audit event of the site that
// is not itself an export or import (which catches deletions). An export taken
// before it misses something.
func siteHighWater(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (*time.Time, error) {
	var high *time.Time
	take := func(t *time.Time) {
		if t != nil && (high == nil || t.After(*high)) {
			high = t
		}
	}
	for _, t := range exportTables {
		var m *time.Time
		if err := tx.QueryRow(ctx, `SELECT max((x->>'updated_at')::timestamptz) FROM (`+t.Query+`) q(x)`, orgID, siteID).Scan(&m); err != nil {
			return nil, err
		}
		take(m)
	}
	var m *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(at) FROM audit_event WHERE org_id = $1 AND site_id = $2 AND action NOT IN ('site.export', 'site.import', 'site.export_discard')`, orgID, siteID).Scan(&m); err != nil {
		return nil, err
	}
	take(m)
	return high, nil
}
