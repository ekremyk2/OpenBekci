package access

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

// Credential kinds, sources, scopes and statuses.
const (
	KindPlate     = "plate"
	KindVisitorQR = "visitor_qr"
	KindTag       = "tag"
	KindPin       = "pin"

	SourceVehicle      = "vehicle"
	SourceVisitorEntry = "visitor_entry"
	SourceManual       = "manual"

	ScopeAllVehicle    = "all_vehicle"
	ScopeAllPedestrian = "all_pedestrian"
	ScopeAll           = "all"
	ScopeIDs           = "ids"

	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusRevoked   = "revoked"
	StatusExpired   = "expired"
)

// DefaultMaxSnapshotEntries is the size of an allow-list the server refuses to go
// beyond (design 6.5); Config.MaxSnapshotEntries changes it. A site above it can
// never sync (an error on every session tick), so raise it, or cut the list, before
// a site grows past it. One snapshot part carries at most 2000 entries (the
// protocol's limit), so 20 000 entries are 10 parts.
const DefaultMaxSnapshotEntries = 20000

// pointRow is an access point as the allow-list and the configuration need it.
type pointRow struct {
	ID        uuid.UUID
	Name      string
	Kind      string
	Direction string
	AgentID   *uuid.UUID
	DriverRef *protocol.DriverRef
	Enabled   bool
}

func loadPoints(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) ([]pointRow, error) {
	rows, err := tx.Query(ctx, `SELECT id, name, kind, direction, agent_id, driver_ref, enabled
		FROM access_point WHERE org_id = $1 AND site_id = $2 ORDER BY name, id`, orgID, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pointRow
	for rows.Next() {
		var p pointRow
		var ref []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Direction, &p.AgentID, &ref, &p.Enabled); err != nil {
			return nil, err
		}
		if len(ref) > 0 && string(ref) != "null" {
			var d protocol.DriverRef
			if json.Unmarshal(ref, &d) == nil && d.Instance != "" {
				p.DriverRef = &d
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// resolvePoints turns a credential's point scope into the ids of the enabled
// points it covers, sorted so the entry is deterministic.
func resolvePoints(scope string, ids []uuid.UUID, points []pointRow) []string {
	var out []string
	for _, p := range points {
		if !p.Enabled {
			continue
		}
		ok := false
		switch scope {
		case ScopeAll:
			ok = true
		case ScopeAllVehicle:
			ok = p.Kind == "vehicle_gate"
		case ScopeAllPedestrian:
			ok = p.Kind == "pedestrian_door"
		case ScopeIDs:
			ok = slices.Contains(ids, p.ID)
		}
		if ok {
			out = append(out, p.ID.String())
		}
	}
	sort.Strings(out)
	return out
}

// credRow is the part of a credential the allow-list is built from.
type credRow struct {
	ID        uuid.UUID
	Kind      string
	Value     string
	Status    string
	Scope     string
	PointIDs  []uuid.UUID
	ValidFrom time.Time
	ValidTo   *time.Time
	Recurring []byte
	MaxUses   *int
	Published []byte
}

const rowSelect = `SELECT id, kind, value, status, point_scope, point_ids, valid_from, valid_to, recurring, max_uses, published FROM credential`

func scanCred(row pgx.Row) (credRow, error) {
	var c credRow
	err := row.Scan(&c.ID, &c.Kind, &c.Value, &c.Status, &c.Scope, &c.PointIDs, &c.ValidFrom, &c.ValidTo, &c.Recurring, &c.MaxUses, &c.Published)
	return c, err
}

// desiredEntry is the allow-list entry of a credential, nil when it has none
// (not active, or no enabled point to open).
func desiredEntry(c credRow, points []pointRow) (*protocol.Entry, error) {
	if c.Status != StatusActive {
		return nil, nil
	}
	pts := resolvePoints(c.Scope, c.PointIDs, points)
	if len(pts) == 0 {
		return nil, nil
	}
	e := &protocol.Entry{
		ID: c.ID.String(), Kind: c.Kind, Value: c.Value, Points: pts,
		ValidFrom: protocol.At(c.ValidFrom), MaxUses: c.MaxUses,
	}
	if c.ValidTo != nil {
		t := protocol.At(*c.ValidTo)
		e.ValidTo = &t
	}
	if len(c.Recurring) > 0 && string(c.Recurring) != "null" {
		var r protocol.Recurring
		if err := json.Unmarshal(c.Recurring, &r); err != nil {
			return nil, fmt.Errorf("credential %s: recurring: %w", c.ID, err)
		}
		e.Recurring = &r
	}
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("credential %s: %w", c.ID, err)
	}
	return e, nil
}

// syncCredential brings the published entry of one credential in line with
// the credential row. Only when the entry actually changes does it take a new
// allow-list version and append to the change log (no change, no bump); both
// happen in the caller's transaction. It reports whether a version was taken.
func syncCredential(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, c credRow, points []pointRow) (bool, error) {
	want, err := desiredEntry(c, points)
	if err != nil {
		return false, err
	}
	var have *protocol.Entry
	if len(c.Published) > 0 && string(c.Published) != "null" {
		have = &protocol.Entry{}
		if err := json.Unmarshal(c.Published, have); err != nil {
			return false, fmt.Errorf("credential %s: stored entry: %w", c.ID, err)
		}
	}
	switch {
	case want == nil && have == nil:
		return false, nil
	case want != nil && have != nil && reflect.DeepEqual(canon(*want), canon(*have)):
		return false, nil
	}
	version, err := bumpVersion(ctx, tx, orgID, siteID)
	if err != nil {
		return false, err
	}
	if want == nil {
		if _, err := tx.Exec(ctx, `INSERT INTO allowlist_change (org_id, site_id, version, op, credential_id) VALUES ($1, $2, $3, 'remove', $4)`,
			orgID, siteID, version, c.ID); err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE credential SET published = NULL, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, c.ID)
		return true, err
	}
	raw, err := json.Marshal(want)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO allowlist_change (org_id, site_id, version, op, credential_id, entry) VALUES ($1, $2, $3, 'upsert', $4, $5)`,
		orgID, siteID, version, c.ID, raw); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE credential SET published = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, c.ID, raw)
	return true, err
}

// canon normalizes an entry for comparison (time instants, not representations).
func canon(e protocol.Entry) protocol.Entry {
	e.ValidFrom = protocol.At(e.ValidFrom.Time)
	if e.ValidTo != nil {
		t := protocol.At(e.ValidTo.Time)
		e.ValidTo = &t
	}
	e.Points = slices.Clone(e.Points)
	sort.Strings(e.Points)
	return e
}

// syncCredentialByID loads a credential (locked) and syncs it.
func syncCredentialByID(ctx context.Context, tx pgx.Tx, orgID, siteID, credID uuid.UUID, points []pointRow) (bool, error) {
	c, err := scanCred(tx.QueryRow(ctx, rowSelect+` WHERE org_id = $1 AND site_id = $2 AND id = $3 FOR UPDATE`, orgID, siteID, credID))
	if err != nil {
		return false, err
	}
	return syncCredential(ctx, tx, orgID, siteID, c, points)
}

// resyncSite re-evaluates every credential of the site against the current
// points: adding, enabling, disabling or deleting a gate changes the entries
// of the credentials whose scope names it (design 2.2, rule 2). It reports
// how many entries changed.
func resyncSite(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (int, error) {
	points, err := loadPoints(ctx, tx, orgID, siteID)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, rowSelect+` WHERE org_id = $1 AND site_id = $2 AND (status = 'active' OR published IS NOT NULL) ORDER BY id FOR UPDATE`, orgID, siteID)
	if err != nil {
		return 0, err
	}
	var creds []credRow
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		creds = append(creds, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	changed := 0
	for _, c := range creds {
		ok, err := syncCredential(ctx, tx, orgID, siteID, c, points)
		if err != nil {
			return changed, err
		}
		if ok {
			changed++
		}
	}
	return changed, nil
}

// ---- snapshots and deltas -------------------------------------------------

// siteVersion reads the allow-list version of a site (0 before the first change).
func siteVersion(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (int64, error) {
	var v int64
	err := tx.QueryRow(ctx, `SELECT version FROM access_site_state WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// loadSnapshot reads the complete allow-list at one consistent version: the
// share lock on the version row holds writers (who take it FOR UPDATE through
// the version bump) off until the entries are read.
func loadSnapshot(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, limit int) (int64, []protocol.Entry, error) {
	if err := ensureState(ctx, tx, orgID, siteID); err != nil {
		return 0, nil, err
	}
	var v int64
	if err := tx.QueryRow(ctx, `SELECT version FROM access_site_state WHERE org_id = $1 AND site_id = $2 FOR SHARE`, orgID, siteID).Scan(&v); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT published FROM credential WHERE org_id = $1 AND site_id = $2 AND published IS NOT NULL ORDER BY id LIMIT $3`,
		orgID, siteID, limit+1)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	entries := []protocol.Entry{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return 0, nil, err
		}
		var e protocol.Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return 0, nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	if len(entries) > limit {
		return 0, nil, fmt.Errorf("the allow-list of site %s exceeds %d entries", siteID, limit)
	}
	return v, entries, nil
}

// loadDeltas returns the changes after version from up to the current version,
// in chunks of at most protocol.MaxDeltaChanges. ok is false when the log no
// longer reaches back to from or from is ahead of the server.
func loadDeltas(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, from int64) ([]protocol.Delta, bool, error) {
	cur, err := siteVersion(ctx, tx, orgID, siteID)
	if err != nil {
		return nil, false, err
	}
	if from > cur {
		return nil, false, nil
	}
	if from == cur {
		return nil, true, nil
	}
	rows, err := tx.Query(ctx, `SELECT version, op, credential_id, entry FROM allowlist_change
		WHERE org_id = $1 AND site_id = $2 AND version > $3 AND version <= $4 ORDER BY version`, orgID, siteID, from, cur)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []protocol.Delta
	next := from + 1
	var chunk protocol.Delta
	flush := func() {
		if len(chunk.Changes) > 0 {
			out = append(out, chunk)
			chunk = protocol.Delta{}
		}
	}
	for rows.Next() {
		var v int64
		var op string
		var credID uuid.UUID
		var raw []byte
		if err := rows.Scan(&v, &op, &credID, &raw); err != nil {
			return nil, false, err
		}
		if v != next { // a hole: the log was pruned under us
			return nil, false, nil
		}
		next++
		if len(chunk.Changes) == 0 {
			chunk = protocol.Delta{FromVersion: v - 1, Changes: []protocol.Change{}}
		}
		ch := protocol.Change{Op: op}
		if op == protocol.OpUpsert {
			var e protocol.Entry
			if err := json.Unmarshal(raw, &e); err != nil {
				return nil, false, err
			}
			ch.Entry = &e
		} else {
			ch.CredentialID = credID.String()
		}
		chunk.Changes = append(chunk.Changes, ch)
		chunk.ToVersion = v
		if len(chunk.Changes) == protocol.MaxDeltaChanges {
			flush()
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	flush()
	if next != cur+1 {
		return nil, false, nil
	}
	return out, true, nil
}

// ---- configuration --------------------------------------------------------

// siteAccessSettings are the access settings of a site (site.settings.access).
type siteAccessSettings struct {
	OfflineGraceHours  int    `json:"offline_grace_hours"`
	AgentSilentMinutes int    `json:"agent_silent_minutes"`
	PlateMatch         string `json:"plate_match"`
	MaintenanceHour    int    `json:"maintenance_hour"`
}

func readSettings(raw []byte) siteAccessSettings {
	s := siteAccessSettings{OfflineGraceHours: 72, AgentSilentMinutes: 5, PlateMatch: "exact", MaintenanceHour: 3}
	var doc struct {
		Access struct {
			OfflineGraceHours  *int   `json:"offline_grace_hours"`
			AgentSilentMinutes *int   `json:"agent_silent_minutes"`
			PlateMatch         string `json:"plate_match"`
			MaintenanceHour    *int   `json:"maintenance_hour"`
		} `json:"access"`
	}
	if json.Unmarshal(raw, &doc) == nil {
		a := doc.Access
		if a.OfflineGraceHours != nil && *a.OfflineGraceHours >= protocol.MinGraceHours && *a.OfflineGraceHours <= protocol.MaxGraceHours {
			s.OfflineGraceHours = *a.OfflineGraceHours
		}
		if a.AgentSilentMinutes != nil && *a.AgentSilentMinutes > 0 {
			s.AgentSilentMinutes = *a.AgentSilentMinutes
		}
		if a.PlateMatch == "tolerant" {
			s.PlateMatch = "tolerant"
		}
		if a.MaintenanceHour != nil && *a.MaintenanceHour >= 0 && *a.MaintenanceHour <= 23 {
			s.MaintenanceHour = *a.MaintenanceHour
		}
	}
	return s
}

// siteConfig is everything the agents' configuration is made of.
type siteConfig struct {
	Timezone string
	Settings siteAccessSettings
	Points   []pointRow
}

func loadSiteConfig(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (siteConfig, error) {
	var tz string
	var settings []byte
	if err := tx.QueryRow(ctx, `SELECT timezone, settings FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&tz, &settings); err != nil {
		return siteConfig{}, err
	}
	pts, err := loadPoints(ctx, tx, orgID, siteID)
	if err != nil {
		return siteConfig{}, err
	}
	return siteConfig{Timezone: tz, Settings: readSettings(settings), Points: pts}, nil
}

func (c siteConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// updateConfigFor builds the update_config command of one agent: the site
// timezone, the agent's own points that have a driver reference, the settings.
func (c siteConfig) updateConfigFor(version int64, siteID, agentID uuid.UUID) protocol.Command {
	pts := []protocol.PointConfig{}
	for _, p := range c.Points {
		if p.AgentID == nil || *p.AgentID != agentID || p.DriverRef == nil {
			continue
		}
		pts = append(pts, protocol.PointConfig{ID: p.ID.String(), Name: p.Name, Kind: p.Kind, Direction: p.Direction, DriverRef: *p.DriverRef, Enabled: p.Enabled})
	}
	site := protocol.Site{ID: siteID.String(), Timezone: c.Timezone}
	settings := protocol.Settings{
		OfflineGraceHours: c.Settings.OfflineGraceHours, PlateMatch: c.Settings.PlateMatch,
		HeartbeatSeconds: HeartbeatSeconds, MaintenanceHour: c.Settings.MaintenanceHour,
	}
	return protocol.Command{Command: protocol.CmdUpdateConfig, ConfigVersion: &version, Site: &site, Points: pts, Settings: &settings}
}

// currentConfigVersion returns the site's config version, bumping it first
// when the configuration changed since it was last recorded (a point edit, a
// timezone or settings change). The version is a function of the content, so
// every replica agrees on it.
func currentConfigVersion(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, c siteConfig) (int64, error) {
	if err := ensureState(ctx, tx, orgID, siteID); err != nil {
		return 0, err
	}
	h := c.hash()
	var v int64
	var have *string
	if err := tx.QueryRow(ctx, `SELECT config_version, config_hash FROM access_site_state WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&v, &have); err != nil {
		return 0, err
	}
	if have != nil && *have == h {
		return v, nil
	}
	err := tx.QueryRow(ctx, `UPDATE access_site_state SET config_version = config_version + 1, config_hash = $3, updated_at = now()
		WHERE org_id = $1 AND site_id = $2 AND config_hash IS DISTINCT FROM $3 RETURNING config_version`, orgID, siteID, h).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) { // another transaction recorded it first
		err = tx.QueryRow(ctx, `SELECT config_version FROM access_site_state WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&v)
	}
	return v, err
}
