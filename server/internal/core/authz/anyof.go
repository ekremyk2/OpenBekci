package authz

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// formerGrants derives the permissions that list Former for the sites where all
// of the caller's occupancies have ended (a current occupancy there already
// carries the portal permissions).
func (r *Resolver) formerGrants(all, current []OccupancyAccess, today time.Time) []Grant {
	cur := map[uuid.UUID]bool{}
	for _, o := range current {
		cur[o.SiteID] = true
	}
	var perms []Permission
	for _, p := range r.reg.Permissions() {
		if r.reg.defs[p].Former {
			perms = append(perms, p)
		}
	}
	seen := map[uuid.UUID]bool{}
	var out []Grant
	for _, o := range all {
		if cur[o.SiteID] || seen[o.SiteID] || o.ValidFrom.After(today) { // not started yet: not a former occupant
			continue
		}
		seen[o.SiteID] = true
		for _, p := range perms {
			site := o.SiteID
			out = append(out, Grant{Permission: p, ScopeType: ScopeSite, ScopeID: &site, FromOccupancy: true})
		}
	}
	return out
}

// AnyOf names a set of permissions of which holding one suffices. It is the
// form used in the x-openbekci-roles list of an operation ("a|b|c") and in the
// rows of the authorization matrix; a single permission is its own name.
func AnyOf(perms ...Permission) Permission {
	names := make([]string, len(perms))
	for i, p := range perms {
		names[i] = string(p)
	}
	return Permission(strings.Join(names, "|"))
}

// Alternatives splits an AnyOf name back into its permissions.
func (p Permission) Alternatives() []Permission {
	parts := strings.Split(string(p), "|")
	out := make([]Permission, len(parts))
	for i, s := range parts {
		out[i] = Permission(s)
	}
	return out
}

// HasAnyInOrg is HasInOrg for an AnyOf name: true when one alternative is held.
func (g Grants) HasAnyInOrg(perm Permission, orgID uuid.UUID) bool {
	for _, p := range perm.Alternatives() {
		if g.HasInOrg(p, orgID) {
			return true
		}
	}
	return false
}
