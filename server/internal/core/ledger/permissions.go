package ledger

import "github.com/openbekci/openbekci/server/internal/core/authz"

// Money permissions. They are declared here, with the ledger, and used by the
// billing, payments, expenses, collections and reports modules.
const (
	PermMoneyRead         authz.Permission = "money.read"
	PermMoneyManage       authz.Permission = "money.manage"
	PermMoneyPost         authz.Permission = "money.post"
	PermMoneyLock         authz.Permission = "money.lock"
	PermPaymentsRecord    authz.Permission = "payments.record"
	PermPaymentsDeclare   authz.Permission = "payments.declare"
	PermStatementsRead    authz.Permission = "statements.read"
	PermStatementsReadOwn authz.Permission = "statements.read_own"
	PermReportsRead       authz.Permission = "reports.read"
	PermReportsDebtors    authz.Permission = "reports.debtors"
	PermReportsAggregate  authz.Permission = "reports.aggregate"
	PermCollectionsManage authz.Permission = "collections.manage"
)

// Permissions declares the money permissions; the permission registry collects them.
func Permissions() []authz.PermissionDef {
	overview := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant}
	posting := []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAccountant}
	orgSite := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}
	owners := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant}
	all := []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}
	return []authz.PermissionDef{
		{Name: PermMoneyRead, Description: "Read ledger, charges, payments, plans, imports and the money overview", Roles: overview, Scopes: orgSite},
		{Name: PermMoneyManage, Description: "Edit accounts, funds, cash boxes, plans, budgets, policies and importers", Roles: posting, Scopes: orgSite},
		{Name: PermMoneyPost, Description: "Post money movements: charges, expenses, bills, transfers, refunds, waivers, opening balances, reversals", Roles: posting, Scopes: orgSite},
		{Name: PermMoneyLock, Description: "Move the lock date and close fiscal years (moving it back needs re-authentication)",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleAccountant}, Scopes: orgSite},
		{Name: PermPaymentsRecord, Description: "Record and confirm payments, allocate them and issue receipts (cash collectors only into their own cash box)",
			Roles: append(append([]authz.Role{}, posting...), authz.RoleStaff), Scopes: orgSite},
		{Name: PermPaymentsDeclare, Description: "Declare a bank transfer already made", Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager}, Scopes: orgSite, Occupants: owners},
		{Name: PermStatementsRead, Description: "Read unit-side and person statements", Roles: overview, Scopes: orgSite},
		{Name: PermStatementsReadOwn, Description: "Read statements, balances, charges, payments and receipts of the caller's own side", Roles: overview, Scopes: orgSite, Occupants: all},
		{Name: PermReportsRead, Description: "Read the financial reports", Roles: overview, Scopes: orgSite},
		{Name: PermReportsDebtors, Description: "Read the debtor list (never public)", Roles: overview, Scopes: orgSite},
		{Name: PermReportsAggregate, Description: "Read gelir-gider and budget vs actual when the site enables financial transparency to owners",
			Roles: overview, Scopes: orgSite, Occupants: []authz.OccupancyKind{authz.OccOwner}},
		{Name: PermCollectionsManage, Description: "Run reminders, manage dunning stages and collection cases",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember}, Scopes: orgSite},
	}
}
