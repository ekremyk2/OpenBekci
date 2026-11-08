// Package authztest seeds the world the authorization tests run against: two
// organizations, one person with one org-scoped membership per built-in role
// in the first, a user with no membership anywhere and a platform
// administrator. It is for tests only (it writes through the platform pool).
//
// The authz matrix harness (internal/httpapi/authzmatrix_test.go) is built on
// it; module tests that need a signed-in caller of a given role reuse it too.
package authztest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Org is a seeded organization.
type Org struct {
	ID   uuid.UUID
	Slug string
}

// Actor is a seeded signed-in identity.
type Actor struct {
	// Name is "org_admin", ..., "guard" for the role actors, "no_membership"
	// for the user without memberships and "platform_admin" for the operator.
	Name string
	// Role is the membership role in OrgA; empty for the two non-members.
	Role     authz.Role
	UserID   uuid.UUID
	PersonID uuid.UUID
}

// World is the seeded data.
type World struct {
	DB       *db.DB
	Identity *identity.Service
	// OrgA has one member per built-in role; OrgB has only BAdmin.
	OrgA, OrgB Org
	// Actors are the seven role actors (in BuiltinRoles order), then
	// no_membership and platform_admin.
	Actors []Actor
	// BAdmin is an org_admin of OrgB and nothing in OrgA.
	BAdmin Actor
}

// Actor returns the actor called name.
func (w *World) Actor(name string) Actor {
	for _, a := range append(w.Actors, w.BAdmin) {
		if a.Name == name {
			return a
		}
	}
	panic("authztest: no actor " + name)
}

// NewWorld seeds the data. ident must run on d.
func NewWorld(t testing.TB, d *db.DB, ident *identity.Service) *World {
	t.Helper()
	ctx := context.Background()
	w := &World{DB: d, Identity: ident}
	w.OrgA = w.org(t, "org-a", "Organization A")
	w.OrgB = w.org(t, "org-b", "Organization B")
	for _, role := range authz.BuiltinRoles() {
		a := w.user(t, string(role))
		a.Role = role
		w.member(t, w.OrgA, &a, role)
		w.Actors = append(w.Actors, a)
	}
	w.Actors = append(w.Actors, w.user(t, "no_membership"))
	pa := w.user(t, "platform_admin")
	if err := ident.PromotePlatformAdmin(ctx, pa.UserID); err != nil {
		t.Fatal(err)
	}
	w.Actors = append(w.Actors, pa)
	w.BAdmin = w.user(t, "b_admin")
	w.member(t, w.OrgB, &w.BAdmin, authz.RoleOrgAdmin)
	return w
}

func (w *World) org(t testing.TB, slug, name string) Org {
	t.Helper()
	o := Org{ID: id.New(), Slug: slug}
	err := w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, $2, $3)`, o.ID, slug, name)
		return err
	})
	if err != nil {
		t.Fatalf("seed organization %s: %v", slug, err)
	}
	return o
}

func (w *World) user(t testing.TB, name string) Actor {
	t.Helper()
	u, err := w.Identity.CreateUser(context.Background(), identity.CreateUserParams{Email: name + "@authz.example.test"})
	if err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	return Actor{Name: name, UserID: u.ID}
}

// member gives the actor a person in org and an org-scoped membership that is valid now.
func (w *World) member(t testing.TB, org Org, a *Actor, role authz.Role) {
	t.Helper()
	a.PersonID = id.New()
	err := w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		ctx := context.Background()
		if _, err := tx.Exec(ctx, `INSERT INTO person (id, org_id, user_id, display_name) VALUES ($1, $2, $3, $4)`, a.PersonID, org.ID, a.UserID, a.Name); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO membership (id, org_id, person_id, role, scope_type, valid_from) VALUES ($1, $2, $3, $4, 'org', now() - interval '1 minute')`,
			id.New(), org.ID, a.PersonID, string(role))
		return err
	})
	if err != nil {
		t.Fatalf("seed membership %s: %v", a.Name, err)
	}
}

// AddMembership adds a membership for the actor's person in org with an
// explicit scope and validity window (validTo zero means open-ended).
func (w *World) AddMembership(t testing.TB, org Org, a Actor, role authz.Role, scope authz.ScopeType, scopeID *uuid.UUID, validFrom, validTo time.Time) {
	t.Helper()
	var to *time.Time
	if !validTo.IsZero() {
		to = &validTo
	}
	err := w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from, valid_to) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id.New(), org.ID, a.PersonID, string(role), string(scope), scopeID, validFrom, to)
		return err
	})
	if err != nil {
		t.Fatalf("add membership: %v", err)
	}
}

// Person links a new person (in org) to the actor's user, for actors that
// have none there; it returns the actor with PersonID set.
func (w *World) Person(t testing.TB, org Org, a Actor) Actor {
	t.Helper()
	a.PersonID = id.New()
	err := w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO person (id, org_id, user_id, display_name) VALUES ($1, $2, $3, $4)`, a.PersonID, org.ID, a.UserID, a.Name)
		return err
	})
	if err != nil {
		t.Fatalf("add person: %v", err)
	}
	return a
}

// Token issues a fresh bearer access token for the actor (its own session).
func (w *World) Token(t testing.TB, a Actor) string {
	t.Helper()
	tp, err := w.Identity.IssueTokens(context.Background(), a.UserID, identity.Meta{IP: "198.51.100.1"})
	if err != nil {
		t.Fatalf("issue token for %s: %v", a.Name, err)
	}
	return tp.AccessToken
}

// OrgVersion reads the organization's current version (for If-Match).
func (w *World) OrgVersion(t testing.TB, org Org) int {
	t.Helper()
	var v int
	err := w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT version FROM organization WHERE id = $1`, org.ID).Scan(&v)
	})
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	return v
}
