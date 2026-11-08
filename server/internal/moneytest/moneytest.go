// Package moneytest builds the world the money module tests run in: a
// database, an organization with one site, units, people, occupancies and a
// signed-in manager holding every permission organization-wide, plus a fake
// clock. It is for tests only.
package moneytest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/permissions"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

// Set sets the time to midday UTC of a civil date.
func (c *Clock) Set(day string) {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	c.t = d.Add(12 * time.Hour)
	c.mu.Unlock()
}

// Env is the test world.
type Env struct {
	T       testing.TB
	DB      *db.DB
	OrgID   uuid.UUID
	SiteID  uuid.UUID
	BlockID uuid.UUID
	Clock   *Clock
	// Ctx carries a principal with every permission organization-wide.
	Ctx context.Context
}

// New creates the database, the organization, a site (Europe/Istanbul, TRY) and the manager.
func New(t testing.TB) *Env {
	t.Helper()
	d := dbtest.New(t)
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	e := &Env{T: t, DB: d, OrgID: id.New(), SiteID: id.New(), BlockID: id.New(), Clock: &Clock{}}
	e.Clock.Set("2026-01-01")
	e.exec(`INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, 'money-test', 'Money test')`, e.OrgID)
	e.exec(`INSERT INTO site (id, org_id, name) VALUES ($1, $2, 'Gul Sitesi')`, e.SiteID, e.OrgID)
	e.exec(`INSERT INTO block (id, org_id, site_id, name, hidden) VALUES ($1, $2, $3, 'Main', true)`, e.BlockID, e.OrgID, e.SiteID)
	reg, err := permissions.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var items []authz.Grant
	for _, p := range reg.Permissions() {
		items = append(items, authz.Grant{Permission: p, ScopeType: authz.ScopeOrg})
	}
	user := id.New()
	e.Ctx = authz.WithGrants(authz.WithPrincipal(context.Background(), &authz.Principal{ID: user, UserID: user, ReauthUntil: time.Now().Add(time.Hour)}), authz.Grants{OrgID: e.OrgID, Items: items})
	return e
}

func (e *Env) exec(sql string, args ...any) {
	e.T.Helper()
	err := e.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), sql, args...)
		return err
	})
	if err != nil {
		e.T.Fatalf("seed: %v\n%s", err, sql)
	}
}

// Exec runs SQL as the platform role (bypasses RLS) for seeding and checks.
func (e *Env) Exec(sql string, args ...any) { e.T.Helper(); e.exec(sql, args...) }

// Query runs a single-row query as the platform role.
func (e *Env) Query(dst []any, sql string, args ...any) {
	e.T.Helper()
	err := e.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql, args...).Scan(dst...)
	})
	if err != nil {
		e.T.Fatalf("query: %v\n%s", err, sql)
	}
}

// Int runs a query returning one int64.
func (e *Env) Int(sql string, args ...any) int64 {
	e.T.Helper()
	var n int64
	e.Query([]any{&n}, sql, args...)
	return n
}

// Unit adds a unit with the given arsa payi (numerator over 1000; 0 for none).
func (e *Env) Unit(door string, share int) uuid.UUID {
	u := id.New()
	e.exec(`INSERT INTO unit (id, org_id, site_id, block_id, door_number) VALUES ($1,$2,$3,$4,$5)`, u, e.OrgID, e.SiteID, e.BlockID, door)
	if share > 0 {
		var key uuid.UUID
		e.Query([]any{&key}, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind, is_default) VALUES ($1,$2,$3,'arsa_payi','Arsa payi','share',true)
			ON CONFLICT (org_id, site_id, code) DO UPDATE SET name = EXCLUDED.name RETURNING id`, id.New(), e.OrgID, e.SiteID)
		e.exec(`INSERT INTO allocation_key_value (id, org_id, site_id, key_id, unit_id, value, raw) VALUES ($1,$2,$3,$4,$5,$6::numeric / 1000,$7)`,
			id.New(), e.OrgID, e.SiteID, key, u, share, fmt.Sprintf("%d/1000", share))
	}
	return u
}

// Person adds a person.
func (e *Env) Person(name string) uuid.UUID {
	p := id.New()
	e.exec(`INSERT INTO person (id, org_id, display_name, contact) VALUES ($1,$2,$3,'{}')`, p, e.OrgID, name)
	return p
}

// WithoutReauth returns ctx with the same grants but a principal that has not re-authenticated.
func WithoutReauth(ctx context.Context) context.Context {
	p := *authz.PrincipalFromContext(ctx)
	p.ReauthUntil = time.Time{}
	return authz.WithPrincipal(ctx, &p)
}

// Occupy adds an occupancy of kind (owner or tenant) from a civil date; to may be empty.
func (e *Env) Occupy(unit, person uuid.UUID, kind, from, to string) uuid.UUID {
	o := id.New()
	var toDate any
	if to != "" {
		toDate = to
	}
	e.exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from, valid_to) VALUES ($1,$2,$3,$4,$5,$6,$6,$7::date,$8::date)`,
		o, e.OrgID, e.SiteID, unit, person, kind, from, toDate)
	return o
}
