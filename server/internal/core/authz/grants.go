package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// Scope is the part of an organization a permission is requested for. With
// only OrgID set the request is organization-wide. A request for a block must
// also name the block's site (a site grant is matched against SiteID; the
// hierarchy is not looked up). The zero Scope is the instance (platform.manage).
type Scope struct {
	OrgID   uuid.UUID
	SiteID  *uuid.UUID
	BlockID *uuid.UUID
}

// Grant is one effective permission with the scope it reaches. ScopeID is nil
// for org and instance scope.
type Grant struct {
	Permission Permission
	ScopeType  ScopeType
	ScopeID    *uuid.UUID
}

// contains reports whether the grant covers the requested scope:
// org contains site contains block; an instance grant covers only the
// instance-level request (zero OrgID), so a platform administrator needs a
// membership to act inside an organization.
func (g Grant) contains(grantOrg uuid.UUID, want Scope) bool {
	switch g.ScopeType {
	case ScopeInstance:
		return want.OrgID == uuid.Nil
	case ScopeOrg:
		return want.OrgID != uuid.Nil && want.OrgID == grantOrg
	case ScopeSite:
		return want.OrgID != uuid.Nil && want.OrgID == grantOrg && g.ScopeID != nil && want.SiteID != nil && *want.SiteID == *g.ScopeID
	case ScopeBlock:
		return want.OrgID != uuid.Nil && want.OrgID == grantOrg && g.ScopeID != nil && want.BlockID != nil && *want.BlockID == *g.ScopeID
	}
	return false
}

// Grants are a principal's effective permissions in one organization (plus
// the instance-level ones). They are resolved once per request and carried in
// the context.
type Grants struct {
	// OrgID is the organization the grants were resolved for; uuid.Nil for
	// instance-level grants only.
	OrgID uuid.UUID
	Items []Grant
	// Occupancies are the caller's occupancies in the organization (current
	// and former) at active sites; portal rights derive from them (S3, S7).
	// Today is the date they were classified at, in the organization's time zone.
	Occupancies []OccupancyAccess
	Today       time.Time
}

// OccupancyAccess is one occupancy of the caller, as the Resolver loads it.
// ValidFrom and ValidTo are dates (UTC midnight); ValidTo is exclusive and
// nil while open.
type OccupancyAccess struct {
	UnitID        uuid.UUID
	SiteID        uuid.UUID
	Kind          OccupancyKind
	Side          UnitSide
	MayMoney      bool
	ValidFrom     time.Time
	ValidTo       *time.Time
	IsCorrespondt bool
}

// OccupancyKind is the kind of an occupancy.
type OccupancyKind string

// Occupancy kinds.
const (
	OccOwner     OccupancyKind = "owner"
	OccTenant    OccupancyKind = "tenant"
	OccHousehold OccupancyKind = "household"
)

func (k OccupancyKind) known() bool { return k == OccOwner || k == OccTenant || k == OccHousehold }

// coversDate reports whether the occupancy is in force on day (a date).
func (o OccupancyAccess) coversDate(day time.Time) bool {
	return !day.Before(o.ValidFrom) && (o.ValidTo == nil || day.Before(*o.ValidTo))
}

// sideMoney reports whether the occupancy gives money access to side:
// owners both sides, tenants the tenant side, household members their own
// side when the owner or tenant granted it.
func (o OccupancyAccess) sideMoney(side UnitSide) bool {
	switch o.Kind {
	case OccOwner:
		return true
	case OccTenant:
		return side == SideTenant
	case OccHousehold:
		return o.MayMoney && o.Side == side
	}
	return false
}

// Current returns the occupancies in force today.
func (g Grants) Current() []OccupancyAccess {
	var out []OccupancyAccess
	for _, o := range g.Occupancies {
		if o.coversDate(g.Today) {
			out = append(out, o)
		}
	}
	return out
}

// ScopeIDs returns the site and block ids perm is granted for, and whether
// it is held organization-wide.
func (g Grants) ScopeIDs(perm Permission) (orgWide bool, siteIDs, blockIDs []uuid.UUID) {
	for _, it := range g.Items {
		if it.Permission != perm {
			continue
		}
		switch {
		case it.ScopeType == ScopeOrg:
			orgWide = true
		case it.ScopeType == ScopeSite && it.ScopeID != nil:
			siteIDs = append(siteIDs, *it.ScopeID)
		case it.ScopeType == ScopeBlock && it.ScopeID != nil:
			blockIDs = append(blockIDs, *it.ScopeID)
		}
	}
	return orgWide, siteIDs, blockIDs
}

// HoldsAnywhere reports whether perm is granted at any scope of the organization.
func (g Grants) HoldsAnywhere(perm Permission) bool {
	for _, it := range g.Items {
		if it.Permission == perm && it.ScopeType != ScopeInstance {
			return true
		}
	}
	return false
}

// Has reports whether a grant of perm covers want.
func (g Grants) Has(perm Permission, want Scope) bool {
	for _, it := range g.Items {
		if it.Permission == perm && it.contains(g.OrgID, want) {
			return true
		}
	}
	return false
}

// merge returns g plus the grants of o (the org id of o wins when set).
func (g Grants) merge(o Grants) Grants {
	if o.OrgID != uuid.Nil {
		g.OrgID = o.OrgID
	}
	g.Items = append(slices.Clone(g.Items), o.Items...)
	g.Occupancies = append(slices.Clone(g.Occupancies), o.Occupancies...)
	if !o.Today.IsZero() {
		g.Today = o.Today
	}
	return g
}

type grantsKey struct{}

// WithGrants returns ctx carrying g (the per-request cache). Grants already in
// ctx (instance-level ones) are kept.
func WithGrants(ctx context.Context, g Grants) context.Context {
	if prev, ok := ctx.Value(grantsKey{}).(Grants); ok {
		g = prev.merge(g)
	}
	return context.WithValue(ctx, grantsKey{}, g)
}

// GrantsFromContext returns the grants resolved for the request.
func GrantsFromContext(ctx context.Context) Grants {
	g, _ := ctx.Value(grantsKey{}).(Grants)
	return g
}

// Require checks that the caller holds perm for scope. It answers 401
// unauthenticated without a principal and 403 insufficient_permission without
// a covering grant. Services call it before touching data.
func Require(ctx context.Context, perm Permission, scope Scope) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	if !GrantsFromContext(ctx).Has(perm, scope) {
		return problem.Forbidden("you do not have the " + string(perm) + " permission here")
	}
	return nil
}

// Has is Require as a boolean, for filtering what a caller sees.
func Has(ctx context.Context, perm Permission, scope Scope) bool {
	return PrincipalFromContext(ctx) != nil && GrantsFromContext(ctx).Has(perm, scope)
}

// UnitSide is the side of a unit a portal right covers.
type UnitSide string

// Unit sides.
const (
	SideOwner  UnitSide = "owner"
	SideTenant UnitSide = "tenant"
)

// CanAccessUnit answers whether the caller may act on the money of a unit
// side through a current occupancy (S3): an owner reaches both sides, a tenant
// the tenant side, a household member their own side only when the owner or
// tenant granted may_see_money. It answers nil, 401 or 403.
//
// Role holders (managers, board, accountant) are not covered here: services
// check their permission first (authz.Has) and fall back to this for portal users.
func CanAccessUnit(ctx context.Context, unitID uuid.UUID, side UnitSide) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	g := GrantsFromContext(ctx)
	for _, o := range g.Current() {
		if o.UnitID == unitID && o.sideMoney(side) {
			return nil
		}
	}
	return problem.Forbidden("no access to this unit")
}

// CanReadUnitAt is the read right of former occupants (S7): it answers
// whether some occupancy of the caller on the unit, in force on day, reaches
// the side's money. A former resident keeps read access to what is dated
// inside their own period and nothing after it.
func CanReadUnitAt(ctx context.Context, unitID uuid.UUID, side UnitSide, day time.Time) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	for _, o := range GrantsFromContext(ctx).Occupancies {
		if o.UnitID == unitID && o.coversDate(day) && o.sideMoney(side) {
			return nil
		}
	}
	return problem.Forbidden("no access to this unit at that date")
}

// CanActOnUnit answers whether the caller currently lives at or owns the
// unit in any capacity (tickets, announcements, bookings, visitors): any
// current occupancy qualifies, household members included, money or not.
func CanActOnUnit(ctx context.Context, unitID uuid.UUID) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	for _, o := range GrantsFromContext(ctx).Current() {
		if o.UnitID == unitID {
			return nil
		}
	}
	return problem.Forbidden("no access to this unit")
}

// CurrentOccupancy returns the caller's current occupancy on the unit that
// reaches side's money with the widest right (owner first), if any.
func CurrentOccupancy(ctx context.Context, unitID uuid.UUID) (OccupancyAccess, bool) {
	var best OccupancyAccess
	found := false
	for _, o := range GrantsFromContext(ctx).Current() {
		if o.UnitID != unitID {
			continue
		}
		if !found || rank(o.Kind) < rank(best.Kind) {
			best, found = o, true
		}
	}
	return best, found
}

func rank(k OccupancyKind) int {
	switch k {
	case OccOwner:
		return 0
	case OccTenant:
		return 1
	}
	return 2
}

// RequireAnywhere is Require for operations addressed by a resource id: it
// answers 403 unless perm is granted at some scope of the organization, before
// any data is read. The service then looks the resource up and calls Require
// with its real scope.
func RequireAnywhere(ctx context.Context, perm Permission) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	if !GrantsFromContext(ctx).HoldsAnywhere(perm) {
		return problem.Forbidden("you do not have the " + string(perm) + " permission here")
	}
	return nil
}

// Membership is an active role grant, the input of GrantsFromMemberships.
type Membership struct {
	Role      Role
	ScopeType ScopeType
	ScopeID   *uuid.UUID
}

// GrantsFromMemberships derives the effective grants of a set of active
// memberships from the registry: each role carries the permissions that list
// it, at the membership's scope, provided the permission can be granted at
// that scope type. Duplicates are removed.
func GrantsFromMemberships(reg *Registry, ms []Membership) []Grant {
	type key struct {
		p  Permission
		st ScopeType
		id uuid.UUID
	}
	seen := map[key]bool{}
	var out []Grant
	for _, m := range ms {
		for _, p := range reg.RolePermissions(m.Role) {
			if !slices.Contains(reg.defs[p].Scopes, m.ScopeType) {
				continue
			}
			k := key{p: p, st: m.ScopeType}
			if m.ScopeID != nil {
				k.id = *m.ScopeID
			}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, Grant{Permission: p, ScopeType: m.ScopeType, ScopeID: m.ScopeID})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Permission != b.Permission {
			return a.Permission < b.Permission
		}
		if a.ScopeType != b.ScopeType {
			return a.ScopeType < b.ScopeType
		}
		return idString(a.ScopeID) < idString(b.ScopeID)
	})
	return out
}

func idString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// PermissionSummary is a permission as GET /v1/me shows it: org-wide when both
// lists are empty, else limited to the listed sites and blocks.
type PermissionSummary struct {
	Permission Permission
	SiteIDs    []uuid.UUID
	BlockIDs   []uuid.UUID
}

// Summarize folds grants into one entry per permission. An org-scoped grant
// makes the permission org-wide (the narrower grants are dropped).
func Summarize(grants []Grant) []PermissionSummary {
	idx := map[Permission]*PermissionSummary{}
	orgWide := map[Permission]bool{}
	var order []Permission
	for _, g := range grants {
		if g.ScopeType == ScopeInstance {
			continue
		}
		s := idx[g.Permission]
		if s == nil {
			s = &PermissionSummary{Permission: g.Permission}
			idx[g.Permission] = s
			order = append(order, g.Permission)
		}
		switch {
		case g.ScopeType == ScopeOrg:
			orgWide[g.Permission] = true
		case g.ScopeType == ScopeSite && g.ScopeID != nil:
			s.SiteIDs = append(s.SiteIDs, *g.ScopeID)
		case g.ScopeType == ScopeBlock && g.ScopeID != nil:
			s.BlockIDs = append(s.BlockIDs, *g.ScopeID)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]PermissionSummary, 0, len(order))
	for _, p := range order {
		s := *idx[p]
		if orgWide[p] {
			s.SiteIDs, s.BlockIDs = nil, nil
		}
		out = append(out, s)
	}
	return out
}

// TxRunner is the database access the Resolver needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Resolver turns the principal's memberships into effective grants.
type Resolver struct {
	db  TxRunner
	reg *Registry
	now func() time.Time
}

// NewResolver returns a Resolver; now is the clock (nil means time.Now).
func NewResolver(db TxRunner, reg *Registry, now func() time.Time) *Resolver {
	if now == nil {
		now = time.Now
	}
	return &Resolver{db: db, reg: reg, now: now}
}

// Registry returns the permission registry.
func (r *Resolver) Registry() *Registry { return r.reg }

// InstanceGrants are the grants that need no organization: platform
// administrators hold the instance-level permissions of the platform_admin
// role (platform.manage), and nothing inside organizations.
func (r *Resolver) InstanceGrants(p *Principal) Grants {
	var g Grants
	if p == nil || !p.IsPlatformAdmin {
		return g
	}
	for _, perm := range r.reg.RolePermissions(RolePlatformAdmin) {
		if slices.Contains(r.reg.defs[perm].Scopes, ScopeInstance) {
			g.Items = append(g.Items, Grant{Permission: perm, ScopeType: ScopeInstance})
		}
	}
	return g
}

// ResolveOrg loads the principal's memberships in the organization that are
// valid now (valid_from <= now < valid_to, open-ended when valid_to is null)
// and the occupancies of their person at active sites, and derives the
// grants: role grants from the memberships, portal grants (the permissions
// that list the occupancy kind in PermissionDef.Occupants, at the site of the
// unit) from the occupancies in force today. member is false when the person
// has neither (former occupants are members, so they keep their read access),
// or the organization does not exist or is not active: callers answer 404 so
// the existence of the organization does not leak.
func (r *Resolver) ResolveOrg(ctx context.Context, p *Principal, orgID uuid.UUID) (g Grants, member bool, err error) {
	if p == nil {
		return Grants{}, false, nil
	}
	now := r.now()
	var ms []Membership
	var occs []OccupancyAccess
	var today time.Time
	err = r.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var tz string
		if err := tx.QueryRow(ctx, `SELECT timezone FROM organization WHERE id = $1 AND status = 'active'`, orgID).Scan(&tz); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		loc, lerr := time.LoadLocation(tz)
		if lerr != nil {
			loc = time.UTC
		}
		ny, nm, nd := now.In(loc).Date()
		today = time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC)
		rows, err := tx.Query(ctx, `
			SELECT m.role, m.scope_type, m.scope_id
			FROM membership m
			JOIN person p ON p.org_id = m.org_id AND p.id = m.person_id
			WHERE m.org_id = $1 AND p.user_id = $2
			  AND m.valid_from <= $3 AND (m.valid_to IS NULL OR m.valid_to > $3)`, orgID, p.UserID, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			var m Membership
			if err := rows.Scan(&m.Role, &m.ScopeType, &m.ScopeID); err != nil {
				rows.Close()
				return err
			}
			ms = append(ms, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		orows, err := tx.Query(ctx, `
			SELECT oc.unit_id, oc.site_id, oc.kind, oc.side, oc.may_see_money, oc.valid_from, oc.valid_to, oc.is_correspondent
			FROM occupancy oc
			JOIN person p ON p.org_id = oc.org_id AND p.id = oc.person_id
			JOIN site s ON s.org_id = oc.org_id AND s.id = oc.site_id AND s.status = 'active'
			WHERE oc.org_id = $1 AND p.user_id = $2`, orgID, p.UserID)
		if err != nil {
			return err
		}
		defer orows.Close()
		for orows.Next() {
			var o OccupancyAccess
			var kind, side string
			var from time.Time
			var to *time.Time
			if err := orows.Scan(&o.UnitID, &o.SiteID, &kind, &side, &o.MayMoney, &from, &to, &o.IsCorrespondt); err != nil {
				return err
			}
			o.Kind, o.Side = OccupancyKind(kind), UnitSide(side)
			o.ValidFrom = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
			if to != nil {
				t := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
				o.ValidTo = &t
			}
			occs = append(occs, o)
		}
		return orows.Err()
	})
	if err != nil {
		return Grants{}, false, problem.Internal(fmt.Errorf("resolve grants: %w", err))
	}
	if len(ms) == 0 && len(occs) == 0 {
		return Grants{}, false, nil
	}
	g = Grants{OrgID: orgID, Items: GrantsFromMemberships(r.reg, ms), Occupancies: occs, Today: today}
	g.Items = append(g.Items, r.occupantGrants(g.Current())...)
	return g, true, nil
}

// occupantGrants derives the portal permissions of the current occupancies:
// each carries the permissions that list its kind, at the unit's site.
func (r *Resolver) occupantGrants(current []OccupancyAccess) []Grant {
	type key struct {
		p    Permission
		site uuid.UUID
	}
	seen := map[key]bool{}
	var out []Grant
	for _, o := range current {
		for _, perm := range r.reg.OccupantPermissions(o.Kind) {
			k := key{perm, o.SiteID}
			if seen[k] {
				continue
			}
			seen[k] = true
			site := o.SiteID
			out = append(out, Grant{Permission: perm, ScopeType: ScopeSite, ScopeID: &site})
		}
	}
	return out
}
