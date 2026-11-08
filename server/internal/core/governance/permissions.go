package governance

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Permissions of the module (names follow the API draft).
const (
	PermMeetingsRead     authz.Permission = "meetings.read"
	PermMeetingsManage   authz.Permission = "meetings.manage"
	PermDecisionsManage  authz.Permission = "decisions.manage"
	PermPollsAnswer      authz.Permission = "polls.answer"
	PermAttendanceManage authz.Permission = "attendance.manage"
)

// Permissions declares the module's permissions; the wiring registers them.
//
// Owners and tenants hold meetings.read through their occupancy (portal
// rights, S3); the service narrows what a tenant sees to decisions the
// manager marked as concerning residents. polls.answer is the owners' right:
// one answer per unit, through the unit's representative.
func Permissions() []authz.PermissionDef {
	managers := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember}
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	return []authz.PermissionDef{
		{Name: PermMeetingsRead, Description: "Read meetings, agendas, notices to oneself, decisions and minutes (tenants only what concerns residents)",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor}, Scopes: orgSite,
			Occupants: []authz.OccupancyKind{authz.OccOwner, authz.OccTenant}},
		{Name: PermMeetingsManage, Description: "Run assemblies: meetings, agenda, call and notices, polls, minutes, governance settings", Roles: managers, Scopes: orgSite},
		{Name: PermDecisionsManage, Description: "Record vote counts and decisions and apply their effects", Roles: managers, Scopes: orgSite},
		{Name: PermAttendanceManage, Description: "Run the attendance desk: check owners in, record proxies, read the quorum", Roles: managers, Scopes: orgSite},
		{Name: PermPollsAnswer, Description: "Answer a non-binding pre-meeting poll for one's own unit (owners; a manager who owns a unit answers for it too)", Roles: managers, Scopes: orgSite,
			Occupants: []authz.OccupancyKind{authz.OccOwner}},
	}
}
