package authz

import (
	"context"
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
// hierarchy is not looked up here). Never build a Scope with SiteID or BlockID
// by hand: use ScopeOfSite / ScopeOfBlock, which load the parents from storage
// (a lint test enforces it). The zero Scope is the instance (platform.manage).
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

// HasInOrg reports whether a grant of perm reaches anything inside the
// organization orgID: org-wide, or limited to a site or block of it. It is the
// coarse check "may this caller attempt perm in this organization at all"; the
// service then narrows with Has for the concrete scope. Instance grants do not
// count.
func (g Grants) HasInOrg(perm Permission, orgID uuid.UUID) bool {
	if orgID == uuid.Nil || orgID != g.OrgID {
		return false
	}
	for _, it := range g.Items {
		if it.Permission != perm {
			continue
		}
		switch it.ScopeType {
		case ScopeOrg:
			return true
		case ScopeSite, ScopeBlock:
			if it.ScopeID != nil {
				return true
			}
		}
	}
	return false
}

// Reach is where a permission applies inside one organization.
type Reach struct {
	// OrgWide is true when an org-scoped grant holds the permission; the
	// narrower lists are then irrelevant.
	OrgWide  bool
	SiteIDs  []uuid.UUID
	BlockIDs []uuid.UUID
}

// Any reports whether the permission applies anywhere in the organization.
func (r Reach) Any() bool { return r.OrgWide || len(r.SiteIDs) > 0 || len(r.BlockIDs) > 0 }

// ReachOf returns where perm applies in organization orgID: the services of
// operations that site- and block-scoped members may use filter their result
// by it (audit.list shows only the events of the caller's sites and blocks).
func (g Grants) ReachOf(perm Permission, orgID uuid.UUID) Reach {
	var r Reach
	if orgID == uuid.Nil || orgID != g.OrgID {
		return r
	}
	for _, it := range g.Items {
		if it.Permission != perm {
			continue
		}
		switch {
		case it.ScopeType == ScopeOrg:
			r.OrgWide = true
		case it.ScopeType == ScopeSite && it.ScopeID != nil:
			r.SiteIDs = append(r.SiteIDs, *it.ScopeID)
		case it.ScopeType == ScopeBlock && it.ScopeID != nil:
			r.BlockIDs = append(r.BlockIDs, *it.ScopeID)
		}
	}
	return r
}

// merge returns g plus the grants of o (the org id of o wins when set).
func (g Grants) merge(o Grants) Grants {
	if o.OrgID != uuid.Nil {
		g.OrgID = o.OrgID
	}
	g.Items = append(slices.Clone(g.Items), o.Items...)
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

// RequireInOrg checks that the caller holds perm somewhere in the
// organization (org-wide, or at a site or block of it); see Grants.HasInOrg.
// Services of operations that site- and block-scoped members may use call it
// and then filter by the scopes the caller holds (Grants.ReachOf).
func RequireInOrg(ctx context.Context, perm Permission, orgID uuid.UUID) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	if !GrantsFromContext(ctx).HasInOrg(perm, orgID) {
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

// CanAccessUnit answers whether the caller may act on a unit side through an
// occupancy (owner, tenant, household).
//
// TODO(S3, phase B): occupancies do not exist yet, so nobody has portal
// rights and this always answers 403. Phase B resolves occupancies next to
// memberships in the Resolver and implements the owner/tenant/household rules.
func CanAccessUnit(ctx context.Context, _ uuid.UUID, _ UnitSide) error {
	if PrincipalFromContext(ctx) == nil {
		return problem.Unauthenticated("authentication is required")
	}
	return problem.Forbidden("no access to this unit")
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
// and derives the grants. member is false when there is none, or the
// organization does not exist or is not active: callers answer 404 so the
// existence of the organization does not leak.
func (r *Resolver) ResolveOrg(ctx context.Context, p *Principal, orgID uuid.UUID) (g Grants, member bool, err error) {
	if p == nil {
		return Grants{}, false, nil
	}
	now := r.now()
	var ms []Membership
	err = r.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.role, m.scope_type, m.scope_id
			FROM membership m
			JOIN person p ON p.org_id = m.org_id AND p.id = m.person_id
			JOIN organization o ON o.id = m.org_id AND o.status = 'active'
			WHERE m.org_id = $1 AND p.user_id = $2
			  AND m.valid_from <= $3 AND (m.valid_to IS NULL OR m.valid_to > $3)`, orgID, p.UserID, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Membership
			if err := rows.Scan(&m.Role, &m.ScopeType, &m.ScopeID); err != nil {
				return err
			}
			ms = append(ms, m)
		}
		return rows.Err()
	})
	if err != nil {
		return Grants{}, false, problem.Internal(fmt.Errorf("resolve grants: %w", err))
	}
	if len(ms) == 0 {
		return Grants{}, false, nil
	}
	return Grants{OrgID: orgID, Items: GrantsFromMemberships(r.reg, ms)}, true, nil
}
