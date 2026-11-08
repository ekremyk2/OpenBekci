package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Site names the organization and site a source belongs to.
type Site struct {
	OrgID, SiteID uuid.UUID
}

// VisitorCredential is what a visitor entry projects to: one plate or one QR
// credential (design 2.2). The community module produces them.
type VisitorCredential struct {
	// EntryID is the visitor entry (source_id of the credential).
	EntryID uuid.UUID
	// Kind is KindPlate or KindVisitorQR.
	Kind string
	// Value is the plate (normalized here again) or hex(sha256(qr token)).
	Value string
	// UnitID and PersonID are for display and reporting; they never reach the agent.
	UnitID, PersonID *uuid.UUID
	// PointScope defaults to all_vehicle for a plate and all for a QR.
	PointScope string
	PointIDs   []uuid.UUID
	ValidFrom  time.Time
	ValidTo    *time.Time
	Recurring  *protocol.Recurring
	// MaxUses is 1 for a single-entry visitor, nil when recurring.
	MaxUses *int
	// Active is false for a denied, expired or cancelled entry: the credential is revoked with Reason.
	Active bool
	Reason string
}

// CredentialSource supplies the credentials of visitor entries. The community
// module implements it (community.VisitorCredentials); until then the default
// VehiclesOnly projects vehicles alone.
type CredentialSource interface {
	// VisitorCredentials returns the visitor credentials of a site that changed
	// after since (the zero time means all of them). A cancelled, denied or
	// expired entry is returned with Active false so its credential is revoked.
	// With since == zero the result is the complete set: visitor credentials
	// missing from it are revoked as well (the reconcile).
	VisitorCredentials(ctx context.Context, site Site, since time.Time) ([]VisitorCredential, error)
}

// VehiclesOnly is the default CredentialSource: no visitor credentials.
type VehiclesOnly struct{}

// VisitorCredentials implements CredentialSource.
func (VehiclesOnly) VisitorCredentials(context.Context, Site, time.Time) ([]VisitorCredential, error) {
	return nil, nil
}

// derived is the credential row a source projects to.
type derived struct {
	kind, value, sourceType string
	sourceID                uuid.UUID
	unitID, personID        *uuid.UUID
	scope                   string
	pointIDs                []uuid.UUID
	validFrom               time.Time
	validTo                 *time.Time
	recurring               *protocol.Recurring
	maxUses                 *int
	active                  bool
	reason                  string
}

// upsertDerived creates or updates the credential of a source, then syncs the
// allow-list entry. The unique key (site, kind, source type, source id) makes
// it idempotent; the entry is only republished when it changed.
func upsertDerived(ctx context.Context, tx pgx.Tx, site Site, d derived, points []pointRow) (bool, error) {
	status, reason := StatusActive, (*string)(nil)
	if !d.active {
		status = StatusRevoked
		r := d.reason
		if r == "" {
			r = "source_ended"
		}
		reason = &r
	}
	var rec []byte
	if d.recurring != nil {
		rec, _ = json.Marshal(d.recurring)
	}
	pointIDs := d.pointIDs
	if pointIDs == nil {
		pointIDs = []uuid.UUID{}
	}
	var credID uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO credential (id, org_id, site_id, kind, value, source_type, source_id, unit_id, person_id, point_scope, point_ids,
			valid_from, valid_to, recurring, max_uses, status, status_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (org_id, site_id, kind, source_type, source_id) DO UPDATE SET
			value = EXCLUDED.value, unit_id = EXCLUDED.unit_id, person_id = EXCLUDED.person_id, point_scope = EXCLUDED.point_scope,
			point_ids = EXCLUDED.point_ids, valid_from = EXCLUDED.valid_from, valid_to = EXCLUDED.valid_to, recurring = EXCLUDED.recurring,
			max_uses = EXCLUDED.max_uses,
			-- a credential that was used up stays used up while its source still says "single entry"
			status = CASE WHEN credential.status_reason = 'used_up' AND EXCLUDED.status = 'active' AND credential.max_uses IS NOT NULL
			              AND credential.uses >= credential.max_uses THEN credential.status ELSE EXCLUDED.status END,
			status_reason = CASE WHEN credential.status_reason = 'used_up' AND EXCLUDED.status = 'active' AND credential.max_uses IS NOT NULL
			              AND credential.uses >= credential.max_uses THEN credential.status_reason ELSE EXCLUDED.status_reason END,
			updated_at = CASE WHEN (credential.value, credential.valid_from, credential.valid_to, credential.recurring, credential.max_uses,
			                        credential.status, credential.point_scope, credential.point_ids, credential.unit_id)
			                     IS DISTINCT FROM (EXCLUDED.value, EXCLUDED.valid_from, EXCLUDED.valid_to, EXCLUDED.recurring, EXCLUDED.max_uses,
			                        EXCLUDED.status, EXCLUDED.point_scope, EXCLUDED.point_ids, EXCLUDED.unit_id)
			                  THEN now() ELSE credential.updated_at END
		RETURNING id`,
		id.New(), site.OrgID, site.SiteID, d.kind, d.value, d.sourceType, d.sourceID, d.unitID, d.personID, d.scope, pointIDs,
		d.validFrom, d.validTo, rec, d.maxUses, status, reason).Scan(&credID)
	if err != nil {
		return false, err
	}
	return syncCredentialByID(ctx, tx, site.OrgID, site.SiteID, credID, points)
}

// revokeSource revokes every credential of a source that is gone.
func revokeSource(ctx context.Context, tx pgx.Tx, site Site, sourceType string, sourceID uuid.UUID, reason string, points []pointRow) (bool, error) {
	rows, err := tx.Query(ctx, `UPDATE credential SET status = 'revoked', status_reason = $5, updated_at = now()
		WHERE org_id = $1 AND site_id = $2 AND source_type = $3 AND source_id = $4 AND status <> 'revoked' RETURNING id`,
		site.OrgID, site.SiteID, sourceType, sourceID, reason)
	if err != nil {
		return false, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var c uuid.UUID
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	changed := false
	for _, c := range ids {
		ok, err := syncCredentialByID(ctx, tx, site.OrgID, site.SiteID, c, points)
		if err != nil {
			return changed, err
		}
		changed = changed || ok
	}
	// a revoked credential may also have an entry still published from before
	rows2, err := tx.Query(ctx, `SELECT id FROM credential WHERE org_id = $1 AND site_id = $2 AND source_type = $3 AND source_id = $4 AND status = 'revoked' AND published IS NOT NULL`,
		site.OrgID, site.SiteID, sourceType, sourceID)
	if err != nil {
		return changed, err
	}
	var left []uuid.UUID
	for rows2.Next() {
		var c uuid.UUID
		if err := rows2.Scan(&c); err != nil {
			rows2.Close()
			return changed, err
		}
		left = append(left, c)
	}
	rows2.Close()
	for _, c := range left {
		ok, err := syncCredentialByID(ctx, tx, site.OrgID, site.SiteID, c, points)
		if err != nil {
			return changed, err
		}
		changed = changed || ok
	}
	return changed, nil
}

type occupancyWindow struct {
	from  time.Time
	to    *time.Time
	count int
}

// activeOccupancy reads the unit's current occupancies (owner, tenant or
// household) in the site's time zone: when at least one is active today the
// vehicle may drive in, until the end of the occupancy if it has one.
func activeOccupancy(ctx context.Context, tx pgx.Tx, orgID, unitID uuid.UUID, loc *time.Location, now time.Time) (occupancyWindow, error) {
	today := now.In(loc).Format("2006-01-02")
	rows, err := tx.Query(ctx, `SELECT valid_from, valid_to FROM occupancy WHERE org_id = $1 AND unit_id = $2
		AND valid_from <= $3::date AND (valid_to IS NULL OR valid_to > $3::date)`, orgID, unitID, today)
	if err != nil {
		return occupancyWindow{}, err
	}
	defer rows.Close()
	var w occupancyWindow
	open := false
	for rows.Next() {
		var from time.Time
		var to *time.Time
		if err := rows.Scan(&from, &to); err != nil {
			return w, err
		}
		start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
		if w.count == 0 || start.Before(w.from) {
			w.from = start
		}
		w.count++
		if to == nil {
			open = true
		} else if end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc); !open && (w.to == nil || end.After(*w.to)) {
			w.to = &end
		}
	}
	if open {
		w.to = nil
	}
	return w, rows.Err()
}

// projectVehicle derives the plate credential of one vehicle (design 2.2):
// active while its unit has a current occupancy and the plate normalizes to
// something; gone with the vehicle.
func projectVehicle(ctx context.Context, tx pgx.Tx, site Site, vehicleID uuid.UUID, loc *time.Location, points []pointRow, now time.Time) (bool, error) {
	var unitID uuid.UUID
	var plate string
	err := tx.QueryRow(ctx, `SELECT unit_id, plate FROM vehicle WHERE org_id = $1 AND site_id = $2 AND id = $3`, site.OrgID, site.SiteID, vehicleID).Scan(&unitID, &plate)
	if errors.Is(err, pgx.ErrNoRows) {
		return revokeSource(ctx, tx, site, SourceVehicle, vehicleID, "vehicle_removed", points)
	}
	if err != nil {
		return false, err
	}
	value := NormalizePlate(plate)
	if value == "" {
		return revokeSource(ctx, tx, site, SourceVehicle, vehicleID, "invalid_plate", points)
	}
	w, err := activeOccupancy(ctx, tx, site.OrgID, unitID, loc, now)
	if err != nil {
		return false, err
	}
	if w.count == 0 {
		return revokeSource(ctx, tx, site, SourceVehicle, vehicleID, "no_occupancy", points)
	}
	return upsertDerived(ctx, tx, site, derived{
		kind: KindPlate, value: value, sourceType: SourceVehicle, sourceID: vehicleID, unitID: &unitID, scope: ScopeAllVehicle,
		validFrom: w.from.UTC(), validTo: utcPtr(w.to), active: true,
	}, points)
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// projectVisitor applies one visitor credential from the CredentialSource.
func projectVisitor(ctx context.Context, tx pgx.Tx, site Site, v VisitorCredential, points []pointRow) (bool, error) {
	value := v.Value
	scope := v.PointScope
	switch v.Kind {
	case KindPlate:
		value = NormalizePlate(v.Value)
		if scope == "" {
			scope = ScopeAllVehicle
		}
	case KindVisitorQR:
		if scope == "" {
			scope = ScopeAll
		}
	default:
		return false, fmt.Errorf("visitor credential %s: unsupported kind %q", v.EntryID, v.Kind)
	}
	if value == "" {
		return revokeSource(ctx, tx, site, SourceVisitorEntry, v.EntryID, "invalid_value", points)
	}
	return upsertDerived(ctx, tx, site, derived{
		kind: v.Kind, value: value, sourceType: SourceVisitorEntry, sourceID: v.EntryID, unitID: v.UnitID, personID: v.PersonID,
		scope: scope, pointIDs: v.PointIDs, validFrom: v.ValidFrom.UTC(), validTo: utcPtr(v.ValidTo), recurring: v.Recurring,
		maxUses: v.MaxUses, active: v.Active, reason: v.Reason,
	}, points)
}

func siteLock(ctx context.Context, tx pgx.Tx, site Site) error {
	h := fnv.New64a()
	_, _ = h.Write(site.SiteID[:])
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(h.Sum64()>>1))
	return err
}

func (s *Service) siteLocation(ctx context.Context, tx pgx.Tx, site Site) (*time.Location, error) {
	var tz string
	if err := tx.QueryRow(ctx, `SELECT timezone FROM site WHERE org_id = $1 AND id = $2`, site.OrgID, site.SiteID).Scan(&tz); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// MarkDirty queues a source (vehicle, visitor_entry) of a site for
// re-projection, in the caller's transaction: the transactional outbox. The
// vehicle and occupancy tables do it by trigger; the community module calls
// this when a visitor entry is created, decided, cancelled or changed, and
// then Kick after the commit.
func MarkDirty(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, sourceType string, sourceID uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO access_dirty (org_id, site_id, source_type, source_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, source_type, source_id) DO UPDATE SET queued_at = now()`, orgID, siteID, sourceType, sourceID)
	return err
}

// ProjectResult says what a projection run did.
type ProjectResult struct {
	Sources int
	Changed int
}

// ProjectDirty drains the queued sources of one site: each is re-projected,
// the visitor source is asked for what changed since the last run, and only a
// changed allow-list entry takes a version. It is the work of the
// access.project job; running it twice is harmless.
func (s *Service) ProjectDirty(ctx context.Context, site Site) (ProjectResult, error) {
	var res ProjectResult
	for {
		var n, changed int
		err := s.db.InTenantTx(ctx, site.OrgID, func(tx pgx.Tx) error {
			if err := siteLock(ctx, tx, site); err != nil {
				return err
			}
			loc, err := s.siteLocation(ctx, tx, site)
			if err != nil {
				return err
			}
			points, err := loadPoints(ctx, tx, site.OrgID, site.SiteID)
			if err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `DELETE FROM access_dirty WHERE (org_id, source_type, source_id) IN (
					SELECT org_id, source_type, source_id FROM access_dirty WHERE org_id = $1 AND site_id = $2 ORDER BY queued_at LIMIT 500 FOR UPDATE SKIP LOCKED)
				RETURNING source_type, source_id`, site.OrgID, site.SiteID)
			if err != nil {
				return err
			}
			type src struct {
				typ string
				id  uuid.UUID
			}
			var srcs []src
			for rows.Next() {
				var x src
				if err := rows.Scan(&x.typ, &x.id); err != nil {
					rows.Close()
					return err
				}
				srcs = append(srcs, x)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			now := s.now()
			for _, x := range srcs {
				var ok bool
				switch x.typ {
				case SourceVehicle:
					ok, err = projectVehicle(ctx, tx, site, x.id, loc, points, now)
				case SourceVisitorEntry:
					// the entry's current state comes from the source; MarkDirty alone does not carry it
				default:
					continue
				}
				if err != nil {
					return fmt.Errorf("project %s %s: %w", x.typ, x.id, err)
				}
				if ok {
					changed++
				}
			}
			n = len(srcs)
			ch, err := s.pullVisitors(ctx, tx, site, points, false)
			changed += ch
			return err
		})
		if err != nil {
			return res, err
		}
		res.Sources += n
		res.Changed += changed
		if changed > 0 {
			s.WakeSite(site.SiteID)
		}
		if n < 500 {
			return res, nil
		}
	}
}

// pullVisitors asks the CredentialSource for the visitor credentials that
// changed since the watermark (all of them when full) and applies them.
func (s *Service) pullVisitors(ctx context.Context, tx pgx.Tx, site Site, points []pointRow, full bool) (int, error) {
	if err := ensureState(ctx, tx, site.OrgID, site.SiteID); err != nil {
		return 0, err
	}
	var since *time.Time
	if err := tx.QueryRow(ctx, `SELECT visitor_since FROM access_site_state WHERE org_id = $1 AND site_id = $2`, site.OrgID, site.SiteID).Scan(&since); err != nil {
		return 0, err
	}
	started := s.now()
	var from time.Time
	if since != nil && !full {
		from = since.Add(-2 * time.Second) // overlap: a change committed just before the last run
	}
	list, err := s.cfg.Source.VisitorCredentials(ctx, site, from)
	if err != nil {
		return 0, fmt.Errorf("visitor credentials: %w", err)
	}
	changed := 0
	seen := map[uuid.UUID]bool{}
	for _, v := range list {
		seen[v.EntryID] = true
		ok, err := projectVisitor(ctx, tx, site, v, points)
		if err != nil {
			return changed, err
		}
		if ok {
			changed++
		}
	}
	if full {
		// the source's complete answer: a visitor credential it no longer lists is gone
		rows, err := tx.Query(ctx, `SELECT DISTINCT source_id FROM credential WHERE org_id = $1 AND site_id = $2 AND source_type = 'visitor_entry' AND status = 'active'`, site.OrgID, site.SiteID)
		if err != nil {
			return changed, err
		}
		var gone []uuid.UUID
		for rows.Next() {
			var sid uuid.UUID
			if err := rows.Scan(&sid); err != nil {
				rows.Close()
				return changed, err
			}
			if !seen[sid] {
				gone = append(gone, sid)
			}
		}
		rows.Close()
		for _, sid := range gone {
			ok, err := revokeSource(ctx, tx, site, SourceVisitorEntry, sid, "source_gone", points)
			if err != nil {
				return changed, err
			}
			if ok {
				changed++
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE access_site_state SET visitor_since = $3 WHERE org_id = $1 AND site_id = $2`, site.OrgID, site.SiteID, started)
	return changed, err
}

// ReconcileResult says what a reconcile repaired.
type ReconcileResult struct {
	Vehicles, Expired, Changed int
}

// Reconcile is the safety net (access.reconcile, nightly and on demand): it
// re-projects every vehicle and visitor entry of the site, revokes what lost
// its source, expires credentials past their end, re-evaluates the entries
// against the current points and repairs drift. Idempotent, and safe next to
// live jobs (per-site advisory lock).
func (s *Service) Reconcile(ctx context.Context, site Site) (ReconcileResult, error) {
	var res ReconcileResult
	err := s.db.InTenantTx(ctx, site.OrgID, func(tx pgx.Tx) error {
		res = ReconcileResult{}
		if err := siteLock(ctx, tx, site); err != nil {
			return err
		}
		loc, err := s.siteLocation(ctx, tx, site)
		if err != nil {
			return err
		}
		points, err := loadPoints(ctx, tx, site.OrgID, site.SiteID)
		if err != nil {
			return err
		}
		now := s.now()
		// every vehicle, and every vehicle credential whose vehicle is gone
		ids := map[uuid.UUID]bool{}
		for _, q := range []string{
			`SELECT id FROM vehicle WHERE org_id = $1 AND site_id = $2`,
			`SELECT source_id FROM credential WHERE org_id = $1 AND site_id = $2 AND source_type = 'vehicle' AND status <> 'revoked'`,
		} {
			if err := scanIDs(ctx, tx, ids, q, site.OrgID, site.SiteID); err != nil {
				return err
			}
		}
		for vid := range ids {
			ok, err := projectVehicle(ctx, tx, site, vid, loc, points, now)
			if err != nil {
				return fmt.Errorf("reconcile vehicle %s: %w", vid, err)
			}
			res.Vehicles++
			if ok {
				res.Changed++
			}
		}
		ch, err := s.pullVisitors(ctx, tx, site, points, true)
		if err != nil {
			return err
		}
		res.Changed += ch
		// credentials past their end
		rows, err := tx.Query(ctx, `UPDATE credential SET status = 'expired', status_reason = 'valid_to_passed', updated_at = now()
			WHERE org_id = $1 AND site_id = $2 AND status = 'active' AND valid_to IS NOT NULL AND valid_to < $3 RETURNING id`, site.OrgID, site.SiteID, now)
		if err != nil {
			return err
		}
		var expired []uuid.UUID
		for rows.Next() {
			var c uuid.UUID
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, c)
		}
		rows.Close()
		res.Expired = len(expired)
		// entries against the current points (and the expired ones leaving the list)
		n, err := resyncSite(ctx, tx, site.OrgID, site.SiteID)
		res.Changed += n
		return err
	})
	if err != nil {
		return res, err
	}
	if res.Changed > 0 {
		s.WakeSite(site.SiteID)
	}
	return res, nil
}

// Prune applies the retention of the change log (7 days, and beyond the newest
// 50 000 per site: an agent older than the oldest retained version is sent a
// snapshot) and clears long-dirty rows. The opening log's retention is the
// org-level SECURITY DEFINER function access_prune_events (see PruneEvents).
func (s *Service) Prune(ctx context.Context, site Site) (int64, error) {
	var n int64
	err := s.db.InTenantTx(ctx, site.OrgID, func(tx pgx.Tx) error {
		cutoff := s.now().Add(-ChangeLogRetention)
		ct, err := tx.Exec(ctx, `DELETE FROM allowlist_change WHERE org_id = $1 AND site_id = $2
			AND (at < $3 OR version <= (SELECT version - $4 FROM access_site_state WHERE org_id = $1 AND site_id = $2))`,
			site.OrgID, site.SiteID, cutoff, int64(ChangeLogKeep))
		if err != nil {
			return err
		}
		n = ct.RowsAffected()
		return nil
	})
	return n, err
}

// EventRetention is how long the opening log is kept (the site visitor-log
// setting of the community module defaults to 90 days).
const EventRetention = 90 * 24 * time.Hour

// PruneEvents deletes opening-log rows older than the retention for an
// organization, through the SECURITY DEFINER function, which only the platform
// role may execute (the application roles cannot delete from access_event).
func (s *Service) PruneEvents(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT access_prune_events($1, $2)`, orgID, s.now().Add(-EventRetention)).Scan(&n)
	})
	return n, err
}

// Sites lists the sites that have queued sources (dirty) or access points
// (active), through the platform pool; the background jobs run without an
// organization.
func (s *Service) sites(ctx context.Context, query string) ([]Site, error) {
	var out []Site
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st Site
			if err := rows.Scan(&st.OrgID, &st.SiteID); err != nil {
				return err
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	return out, err
}

// DirtySites are the sites with queued sources.
func (s *Service) DirtySites(ctx context.Context) ([]Site, error) {
	return s.sites(ctx, `SELECT DISTINCT org_id, site_id FROM access_dirty ORDER BY 1, 2 LIMIT 200`)
}

// ActiveSites are the sites with at least one access point.
func (s *Service) ActiveSites(ctx context.Context) ([]Site, error) {
	return s.sites(ctx, `SELECT DISTINCT org_id, site_id FROM access_point ORDER BY 1, 2`)
}

// Kick projects a site's queued sources now, in the background of this
// process: the approval of a visitor reaches a connected agent within seconds
// without waiting for the next job run. Errors are logged; the job retries.
func (s *Service) Kick(site Site) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.ProjectDirty(ctx, site); err != nil {
			s.cfg.Log.Warn("access projection failed", "site", site.SiteID, "error", err)
		}
	}()
}
