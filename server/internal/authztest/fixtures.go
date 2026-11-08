package authztest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

var fixtureSeq atomic.Int64

// SiteFix is a freshly seeded site with one hidden block, one flat and the two default keys.
type SiteFix struct {
	Org      Org
	Site     uuid.UUID
	Block    uuid.UUID
	Unit     uuid.UUID
	Key      uuid.UUID // the share key arsa_payi
	DoorNum  string
	SiteName string
}

// Exec runs SQL through the platform pool (it bypasses row-level security).
func (w *World) Exec(t testing.TB, sql string, args ...any) {
	t.Helper()
	err := w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), sql, args...)
		return err
	})
	if err != nil {
		t.Fatalf("fixture sql: %v\n%s", err, sql)
	}
}

// NewSite seeds a site in org. Every call makes a new one (the matrix runs
// mutating operations repeatedly and needs fresh resources).
func (w *World) NewSite(t testing.TB, org Org) SiteFix {
	t.Helper()
	n := fixtureSeq.Add(1)
	f := SiteFix{Org: org, Site: id.New(), Block: id.New(), Unit: id.New(), Key: id.New(), DoorNum: fmt.Sprintf("A-%d", n), SiteName: fmt.Sprintf("Site %d", n)}
	w.Exec(t, `INSERT INTO site (id, org_id, name) VALUES ($1, $2, $3)`, f.Site, org.ID, f.SiteName)
	w.Exec(t, `INSERT INTO block (id, org_id, site_id, name, hidden) VALUES ($1, $2, $3, 'Main', true)`, f.Block, org.ID, f.Site)
	w.Exec(t, `INSERT INTO unit (id, org_id, site_id, block_id, door_number) VALUES ($1, $2, $3, $4, $5)`, f.Unit, org.ID, f.Site, f.Block, f.DoorNum)
	w.Exec(t, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind, is_default) VALUES ($1, $2, $3, 'arsa_payi', 'Arsa payi', 'share', true)`, f.Key, org.ID, f.Site)
	w.Exec(t, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind, is_default) VALUES ($1, $2, $3, 'equal', 'Equal', 'equal', true)`, id.New(), org.ID, f.Site)
	return f
}

// NewUnit adds another unit to the site.
func (w *World) NewUnit(t testing.TB, f SiteFix) uuid.UUID {
	t.Helper()
	u := id.New()
	w.Exec(t, `INSERT INTO unit (id, org_id, site_id, block_id, door_number) VALUES ($1, $2, $3, $4, $5)`, u, f.Org.ID, f.Site, f.Block, fmt.Sprintf("B-%d", fixtureSeq.Add(1)))
	return u
}

// NewPerson adds a person without an account.
func (w *World) NewPerson(t testing.TB, org Org) uuid.UUID {
	t.Helper()
	p := id.New()
	w.Exec(t, `INSERT INTO person (id, org_id, display_name, contact) VALUES ($1, $2, $3, '{"phone":"+905550000001"}')`, p, org.ID, fmt.Sprintf("Person %d", fixtureSeq.Add(1)))
	return p
}

// NewOccupancy adds an occupancy of kind (owner, tenant or household; household
// sits on the owner side) from 2020-01-01, open-ended, and returns its id.
func (w *World) NewOccupancy(t testing.TB, f SiteFix, unit, person uuid.UUID, kind string) uuid.UUID {
	t.Helper()
	o := id.New()
	side := "owner"
	if kind == "tenant" {
		side = "tenant"
	}
	w.Exec(t, `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from) VALUES ($1, $2, $3, $4, $5, $6, $7, '2020-01-01')`,
		o, f.Org.ID, f.Site, unit, person, kind, side)
	return o
}

// NewVehicle adds a vehicle to the fixture's unit.
func (w *World) NewVehicle(t testing.TB, f SiteFix) uuid.UUID {
	t.Helper()
	v := id.New()
	w.Exec(t, `INSERT INTO vehicle (id, org_id, site_id, unit_id, plate) VALUES ($1, $2, $3, $4, $5)`, v, f.Org.ID, f.Site, f.Unit, fmt.Sprintf("34TST%d", fixtureSeq.Add(1)))
	return v
}

// NewVendor adds a site vendor.
func (w *World) NewVendor(t testing.TB, f SiteFix) uuid.UUID {
	t.Helper()
	v := id.New()
	w.Exec(t, `INSERT INTO vendor (id, org_id, site_id, name) VALUES ($1, $2, $3, 'Vendor')`, v, f.Org.ID, f.Site)
	return v
}

// NewInvitation adds a pending invitation (kind manager or household) to a new
// person and returns its id and the token that opens it (a hash is stored).
func (w *World) NewInvitation(t testing.TB, f SiteFix, kind string, tokenHash []byte) uuid.UUID {
	t.Helper()
	person := w.NewPerson(t, f.Org)
	i := id.New()
	occ := "owner"
	side := "owner"
	if kind == "household" {
		occ = "household"
	}
	w.Exec(t, `INSERT INTO invitation (id, org_id, site_id, kind, unit_id, person_id, occupancy_kind, occupancy_side, channel, phone, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'link', '+905550000001', $9, now() + interval '7 days')`, i, f.Org.ID, f.Site, kind, f.Unit, person, occ, side, tokenHash)
	return i
}

// NewJoinCode makes the site's active join code (older ones are revoked).
func (w *World) NewJoinCode(t testing.TB, f SiteFix, code string) {
	t.Helper()
	w.Exec(t, `UPDATE invitation SET status = 'revoked' WHERE org_id = $1 AND site_id = $2 AND kind = 'site_code'`, f.Org.ID, f.Site)
	w.Exec(t, `INSERT INTO invitation (id, org_id, site_id, kind, channel, code) VALUES ($1, $2, $3, 'site_code', 'link', $4)`, id.New(), f.Org.ID, f.Site, code)
}

// NewClaim adds a pending claim of the user's person to the fixture's unit.
func (w *World) NewClaim(t testing.TB, f SiteFix, userID uuid.UUID) uuid.UUID {
	t.Helper()
	person := w.NewPerson(t, f.Org)
	c := id.New()
	w.Exec(t, `INSERT INTO unit_claim (id, org_id, site_id, unit_id, person_id, user_id, kind) VALUES ($1, $2, $3, $4, $5, $6, 'owner')`, c, f.Org.ID, f.Site, f.Unit, person, userID)
	return c
}

// NewMembership gives person an organization-scoped membership and returns its id.
func (w *World) NewMembership(t testing.TB, org Org, person uuid.UUID, role string) uuid.UUID {
	t.Helper()
	m := id.New()
	w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, valid_from) VALUES ($1, $2, $3, $4, 'org', now() - interval '1 minute')`, m, org.ID, person, role)
	return m
}

// NewMoveOut ends a fresh owner occupancy and records the move-out.
func (w *World) NewMoveOut(t testing.TB, f SiteFix) uuid.UUID {
	t.Helper()
	person := w.NewPerson(t, f.Org)
	unit := w.NewUnit(t, f)
	occ := w.NewOccupancy(t, f, unit, person, "owner")
	m := id.New()
	w.Exec(t, `UPDATE occupancy SET valid_to = '2026-01-01' WHERE id = $1`, occ)
	w.Exec(t, `INSERT INTO move_out (id, org_id, site_id, occupancy_id, unit_id, person_id, as_of) VALUES ($1, $2, $3, $4, $5, $6, '2026-01-01')`, m, f.Org.ID, f.Site, occ, unit, person)
	return m
}

// NewExport records an export with a small archive and returns its id.
func (w *World) NewExport(t testing.TB, f SiteFix) uuid.UUID {
	t.Helper()
	e := id.New()
	w.Exec(t, `INSERT INTO site_export (id, org_id, direction, site_id, site_name, source_org_id, archive) VALUES ($1, $2, 'export', $3, $4, $2, '{}'::bytea)`, e, f.Org.ID, f.Site, f.SiteName)
	return e
}
