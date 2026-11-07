package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/authz"
)

// TestRolePermissionsGolden compares the registry with the hand-written S5
// table in testdata/roles.golden.json, which is independent of the registry
// (and of the generated spec, which is derived from it): a wrong role
// assignment in a module cannot pass by being generated consistently.
func TestRolePermissionsGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "roles.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]json.RawMessage
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, role := range authz.EveryRole() {
		rawPerms, ok := golden[string(role)]
		if !ok {
			t.Errorf("golden table has no entry for role %s", role)
			continue
		}
		seen++
		var want []string
		if err := json.Unmarshal(rawPerms, &want); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, p := range reg.RolePermissions(role) {
			got = append(got, string(p))
		}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("role %s: registry has %v, S5 golden table says %v", role, got, want)
		}
	}
	if extra := len(golden) - 1 - seen; extra != 0 { // minus _comment
		t.Errorf("golden table lists %d roles the code does not know", extra)
	}
	// every registered permission is covered by some role in the golden table (no orphan permission)
	for _, p := range reg.Permissions() {
		held := false
		for _, rawPerms := range golden {
			var ps []string
			if json.Unmarshal(rawPerms, &ps) == nil && slices.Contains(ps, string(p)) {
				held = true
			}
		}
		if !held {
			t.Errorf("permission %s is registered but no role in the golden table holds it", p)
		}
	}
}
