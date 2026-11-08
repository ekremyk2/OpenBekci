package community

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module (names of the draft contract, release 0.3).
const (
	PermFacilitiesManage authz.Permission = "facilities.manage"
	PermBookingsRead     authz.Permission = "bookings.read"
	PermBookingsCreate   authz.Permission = "bookings.create"
	PermBookingsManage   authz.Permission = "bookings.manage"
	PermVisitorsRead     authz.Permission = "visitors.read"
	PermVisitorsManage   authz.Permission = "visitors.manage"
	PermVisitorsDecide   authz.Permission = "visitors.decide"
	PermDeliveriesRead   authz.Permission = "deliveries.read"
	PermGateOperate      authz.Permission = "gate.operate"
	PermGateManage       authz.Permission = "gate.manage"
)

// gateRoles are the roles that hold gate.operate: what a guard session's
// person must hold (a session never grants more than the console login).
var gateRoles = []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleGuard}

// Permissions declares the module's permissions; the wiring registers them.
//
// Residents hold the portal permissions through a current occupancy
// (PermissionDef.Occupants); the service narrows them to the caller's own
// unit with authz.CanActOnUnit.
func Permissions() []authz.PermissionDef {
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}
	board := append(append([]authz.Role{}, managers...), authz.RoleBoardMember)
	overview := append(append([]authz.Role{}, board...), authz.RoleAuditor)
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	residents := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}
	return []authz.PermissionDef{
		{Name: PermFacilitiesManage, Description: "Create and change facilities, their booking rules and blocked dates", Roles: board, Scopes: orgSite},
		{Name: PermBookingsRead, Description: "Read facilities and bookings (residents: their own unit's, other slots only as taken)",
			Roles: board, Scopes: orgSite, Occupants: residents},
		{Name: PermBookingsCreate, Description: "Book a facility and cancel one's own booking", Roles: board, Scopes: orgSite, Occupants: residents},
		{Name: PermBookingsManage, Description: "Cancel any booking", Roles: board, Scopes: orgSite},
		{Name: PermVisitorsRead, Description: "Read visitor entries, recurring visitors and parcels of the whole site", Roles: overview, Scopes: orgSite},
		{Name: PermVisitorsManage, Description: "Pre-register, change and cancel visitors and recurring visitors (residents: their own unit)",
			Roles: board, Scopes: orgSite, Occupants: residents},
		{Name: PermVisitorsDecide, Description: "Approve or deny a visitor at the gate for one's own unit", Scopes: orgSite, Occupants: residents},
		{Name: PermDeliveriesRead, Description: "Read the parcels of one's own unit", Scopes: orgSite, Occupants: residents},
		{Name: PermGateOperate, Description: "Operate the gate console: admit visitors, ask residents, log parcels", Roles: gateRoles, Scopes: orgSite},
		{Name: PermGateManage, Description: "Set guard PINs", Roles: managers, Scopes: orgSite},
	}
}
