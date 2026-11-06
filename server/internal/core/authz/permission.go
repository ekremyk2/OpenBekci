package authz

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Permission is a string constant such as "org.read", declared by the module
// that owns it (in its permissions.go) and registered in a Registry.
type Permission string

// Role is a built-in membership role, or the platform_admin pseudo-role (the
// instance operator flag on the user; it is not a membership role).
type Role string

// Built-in roles (decisions S3 and S5).
const (
	RoleOrgAdmin      Role = "org_admin"
	RoleSiteManager   Role = "site_manager"
	RoleBoardMember   Role = "board_member"
	RoleAuditor       Role = "auditor"
	RoleAccountant    Role = "accountant"
	RoleStaff         Role = "staff"
	RoleGuard         Role = "guard"
	RolePlatformAdmin Role = "platform_admin"
)

// BuiltinRoles are the seven membership roles, in a stable order.
func BuiltinRoles() []Role {
	return []Role{RoleOrgAdmin, RoleSiteManager, RoleBoardMember, RoleAuditor, RoleAccountant, RoleStaff, RoleGuard}
}

// EveryRole is the seven membership roles plus platform_admin: what a
// permission that every authenticated caller holds lists.
func EveryRole() []Role { return append(BuiltinRoles(), RolePlatformAdmin) }

func (r Role) known() bool { return r == RolePlatformAdmin || slices.Contains(BuiltinRoles(), r) }

// ScopeType is how far a grant reaches.
type ScopeType string

// Scope types. ScopeInstance is for instance-wide permissions
// (platform.manage); the others are the membership scopes.
const (
	ScopeInstance ScopeType = "instance"
	ScopeOrg      ScopeType = "org"
	ScopeSite     ScopeType = "site"
	ScopeBlock    ScopeType = "block"
)

func (s ScopeType) known() bool {
	return s == ScopeInstance || s == ScopeOrg || s == ScopeSite || s == ScopeBlock
}

// PermissionDef declares one permission of a module.
type PermissionDef struct {
	Name        Permission
	Description string
	// Roles that hold the permission (the role to permission matrix).
	Roles []Role
	// Scopes the permission can be granted at. A membership whose scope type
	// is not listed does not carry the permission (an org_admin membership
	// limited to one site does not get an org-wide permission).
	Scopes []ScopeType
}

var permissionName = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$`)

// Registry holds every registered permission. It is built once at start-up in
// the wiring from each module's Permissions() and is read-only afterwards.
type Registry struct {
	defs    map[Permission]PermissionDef
	modules map[Permission]string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{defs: map[Permission]PermissionDef{}, modules: map[Permission]string{}}
}

// Register adds the permissions of a module. It fails on a malformed or
// duplicate name, an unknown role or scope, or a definition without roles or
// scopes. Nothing is registered when an error is returned.
func (r *Registry) Register(module string, defs ...PermissionDef) error {
	if module == "" {
		return fmt.Errorf("authz: register: empty module name")
	}
	seen := map[Permission]bool{}
	for _, d := range defs {
		switch {
		case !permissionName.MatchString(string(d.Name)):
			return fmt.Errorf("authz: %s: permission name %q must look like area.action", module, d.Name)
		case seen[d.Name]:
			return fmt.Errorf("authz: %s: permission %q declared twice", module, d.Name)
		case r.has(d.Name):
			return fmt.Errorf("authz: %s: permission %q is already registered by %s", module, d.Name, r.modules[d.Name])
		case d.Description == "":
			return fmt.Errorf("authz: %s: permission %q has no description", module, d.Name)
		case len(d.Roles) == 0:
			return fmt.Errorf("authz: %s: permission %q has no roles", module, d.Name)
		case len(d.Scopes) == 0:
			return fmt.Errorf("authz: %s: permission %q has no scopes", module, d.Name)
		}
		for _, role := range d.Roles {
			if !role.known() {
				return fmt.Errorf("authz: %s: permission %q lists unknown role %q", module, d.Name, role)
			}
		}
		for _, sc := range d.Scopes {
			if !sc.known() {
				return fmt.Errorf("authz: %s: permission %q lists unknown scope %q", module, d.Name, sc)
			}
		}
		seen[d.Name] = true
	}
	for _, d := range defs {
		d.Roles = slices.Clone(d.Roles)
		d.Scopes = slices.Clone(d.Scopes)
		r.defs[d.Name] = d
		r.modules[d.Name] = module
	}
	return nil
}

func (r *Registry) has(p Permission) bool { _, ok := r.defs[p]; return ok }

// Lookup returns the definition of p.
func (r *Registry) Lookup(p Permission) (PermissionDef, bool) {
	d, ok := r.defs[p]
	return d, ok
}

// Module returns the module that registered p.
func (r *Registry) Module(p Permission) string { return r.modules[p] }

// Permissions returns every registered permission, sorted by name.
func (r *Registry) Permissions() []Permission {
	out := make([]Permission, 0, len(r.defs))
	for p := range r.defs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RolePermissions returns the permissions role holds, sorted by name.
func (r *Registry) RolePermissions(role Role) []Permission {
	var out []Permission
	for _, p := range r.Permissions() {
		if slices.Contains(r.defs[p].Roles, role) {
			out = append(out, p)
		}
	}
	return out
}

// RoleHas reports whether role holds p.
func (r *Registry) RoleHas(role Role, p Permission) bool {
	d, ok := r.defs[p]
	return ok && slices.Contains(d.Roles, role)
}

// SpecYAML renders api/src/components/permissions.yaml: the Permission enum
// schema and the role to permission matrix (x-openbekci-roles). The output is
// deterministic; `task gen` writes it and a test fails when the committed file
// differs.
func (r *Registry) SpecYAML() []byte {
	var b strings.Builder
	b.WriteString("# GENERATED by `go run ./cmd/genpermissions` (task gen:permissions) from the permission\n")
	b.WriteString("# registry (server/internal/permissions). Do not edit; change the module's Permissions() instead.\n")
	b.WriteString("schemas:\n  Permission:\n    type: string\n    x-open-enum: true\n")
	b.WriteString("    description: 'Open enum: every permission a role can hold (backend plan section 7).'\n    enum:\n")
	for _, p := range r.Permissions() {
		fmt.Fprintf(&b, "      - %s\n", p)
	}
	b.WriteString("x-openbekci-roles:\n")
	roles := EveryRole() // fixed order: built-in roles, then platform_admin
	for _, role := range roles {
		ps := r.RolePermissions(role)
		if len(ps) == 0 {
			fmt.Fprintf(&b, "  %s: []\n", role)
			continue
		}
		fmt.Fprintf(&b, "  %s:\n", role)
		for _, p := range ps {
			fmt.Fprintf(&b, "    - %s\n", p)
		}
	}
	return []byte(b.String())
}
