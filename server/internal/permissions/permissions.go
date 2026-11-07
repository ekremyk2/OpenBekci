// Package permissions builds the permission registry from every module's Permissions().
// It is a package of its own so that cmd/genpermissions can run before the API code is generated.
package permissions

import (
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// NewRegistry builds the registry from every module's
// Permissions(). The list is explicit (no init() registration): a new module
// adds one line here. cmd/genpermissions renders the same registry into
// api/src/components/permissions.yaml.
func NewRegistry() (*authz.Registry, error) {
	reg := authz.NewRegistry()
	for _, m := range []struct {
		name string
		defs []authz.PermissionDef
	}{
		{"platformadmin", platformadmin.Permissions()},
		{"tenancy", tenancy.Permissions()},
		{"identity", identity.Permissions()},
		{"audit", audit.Permissions()},
		{"files", files.Permissions()},
	} {
		if err := reg.Register(m.name, m.defs...); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
