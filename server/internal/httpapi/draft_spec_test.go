package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The draft contract (api/openapi-draft.yaml) is the single contract backend
// module builders and web screen builders work against for releases 0.1 to
// 0.3. Builders move path references from the draft root into the served root
// as they implement them. These tests keep every draft operation to the same
// conventions the served spec is held to (TestSpecConformance), plus the
// draft-only rules: authorization metadata, pagination and permissions list.

func loadDraftSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi-draft.yaml"))
	if err != nil {
		t.Fatalf("load api/openapi-draft.yaml: %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("draft spec invalid: %v", err)
	}
	return spec
}

// rolesOf decodes the x-openbekci-roles extension (role to permissions).
func rolesOf(op *openapi3.Operation) map[string][]string {
	raw, ok := op.Extensions["x-openbekci-roles"]
	if !ok {
		return nil
	}
	var b []byte
	switch v := raw.(type) {
	case json.RawMessage:
		b = v
	default:
		b, _ = json.Marshal(v)
	}
	var out map[string][]string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// listedPermsOf reads the other form of x-openbekci-roles: a served operation lists the
// permissions it needs (the draft maps roles to them).
func listedPermsOf(op *openapi3.Operation) []string {
	raw, ok := op.Extensions["x-openbekci-roles"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var list []string
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	return list
}

func paramNames(item *openapi3.PathItem, op *openapi3.Operation, in string) map[string]*openapi3.Parameter {
	out := map[string]*openapi3.Parameter{}
	for _, params := range []openapi3.Parameters{item.Parameters, op.Parameters} {
		for _, ref := range params {
			if ref.Value != nil && ref.Value.In == in {
				out[ref.Value.Name] = ref.Value
			}
		}
	}
	return out
}

func TestDraftSpecConformance(t *testing.T) {
	spec := loadDraftSpec(t)
	if spec.OpenAPI != "3.0.3" {
		t.Errorf("openapi = %q, want 3.0.3", spec.OpenAPI)
	}
	problemRef := "#/components/responses/Problem"
	served := loadSpec(t)
	servedIDs := map[string]bool{}
	for _, item := range served.Paths.Map() {
		for _, op := range item.Operations() {
			servedIDs[op.OperationID] = true
		}
	}
	seen := map[string]bool{}
	nOps := 0
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			nOps++
			where := method + " " + path
			if !operationID.MatchString(op.OperationID) {
				t.Errorf("%s: operationId %q does not match %s", where, op.OperationID, operationID)
			}
			if seen[op.OperationID] {
				t.Errorf("%s: duplicate operationId %q", where, op.OperationID)
			}
			seen[op.OperationID] = true
			if len(op.Tags) != 1 {
				t.Errorf("%s: want exactly one tag, got %v", where, op.Tags)
			}
			// Operations already served are held to TestSpecConformance only.
			isServed := servedIDs[op.OperationID]
			if op.Security == nil {
				t.Errorf("%s: no explicit security key (use security: [] for a public operation)", where)
			}
			if v := extString(op.Extensions, "x-stability"); !isServed && v != "stable" && v != "preview" {
				t.Errorf("%s: x-stability %q, want stable or preview", where, v)
			}
			if op.Responses.Default() == nil {
				t.Errorf("%s: no default (Problem) response", where)
			}
			for code, ref := range op.Responses.Map() {
				if code != "default" && !strings.HasPrefix(code, "4") && !strings.HasPrefix(code, "5") {
					continue
				}
				if ref.Ref != problemRef {
					t.Errorf("%s: response %s is %q, must reference %s", where, code, ref.Ref, problemRef)
				}
			}
			// Every org-scoped operation names the permissions it needs.
			if strings.HasPrefix(path, "/v1/orgs/{org_id}") && !isServed {
				if len(rolesOf(op)) == 0 {
					t.Errorf("%s: org-scoped operation without x-openbekci-roles", where)
				}
			}
			// Every mutating operation declares its idempotency mode.
			if method != http.MethodGet && method != http.MethodHead {
				v := extString(op.Extensions, "x-idempotent")
				if v != "optional" && v != "required" {
					t.Errorf("%s: mutation without x-idempotent optional|required (got %q)", where, v)
				}
				if v == "required" {
					if authModeOf(spec, op) != authRequired {
						t.Errorf("%s: x-idempotent: required needs mandatory authentication", where)
					}
					h := paramNames(item, op, openapi3.ParameterInHeader)["Idempotency-Key"]
					if h == nil || !h.Required {
						t.Errorf("%s: x-idempotent: required without a required Idempotency-Key header", where)
					}
				}
			}
			// Updatable resources carry If-Match.
			if method == http.MethodPatch {
				h := paramNames(item, op, openapi3.ParameterInHeader)["If-Match"]
				if h == nil && !isServed && path != "/v1/me/profile" {
					t.Errorf("%s: PATCH without If-Match", where)
				}
			}
			// Every list returns {items, next_cursor} and takes limit and cursor.
			if method == http.MethodGet {
				ok200 := op.Responses.Value("200")
				if ok200 == nil || ok200.Value == nil {
					continue
				}
				mt := ok200.Value.Content.Get("application/json")
				if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
					continue
				}
				props := mt.Schema.Value.Properties
				if _, isList := props["items"]; !isList || isServed {
					continue
				}
				q := paramNames(item, op, openapi3.ParameterInQuery)
				if q["limit"] == nil || q["cursor"] == nil {
					t.Errorf("%s: list without limit and cursor query parameters", where)
				}
				nc := props["next_cursor"]
				if nc == nil || nc.Value == nil || !nc.Value.Nullable {
					t.Errorf("%s: list response without nullable next_cursor", where)
				}
				if !containsString(mt.Schema.Value.Required, "next_cursor") || !containsString(mt.Schema.Value.Required, "items") {
					t.Errorf("%s: list response must require items and next_cursor", where)
				}
			}
		}
	}
	if nOps < 400 {
		t.Errorf("draft has only %d operations; the contract for 0.1 to 0.3 is expected to have more than 400", nOps)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The draft must contain everything the served spec serves, unchanged in name.
func TestDraftIncludesServedOperations(t *testing.T) {
	served := loadSpec(t)
	draft := loadDraftSpec(t)
	have := map[string]bool{}
	for _, item := range draft.Paths.Map() {
		for _, op := range item.Operations() {
			have[op.OperationID] = true
		}
	}
	for path, item := range served.Paths.Map() {
		for method, op := range item.Operations() {
			if !have[op.OperationID] {
				t.Errorf("served operation %s %s (%s) is missing from the draft", method, path, op.OperationID)
			}
		}
	}
}

// api/PERMISSIONS.md lists every permission the draft uses, and every listed
// permission is used by some operation.
func TestDraftPermissionsDocumented(t *testing.T) {
	spec := loadDraftSpec(t)
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "PERMISSIONS.md"))
	if err != nil {
		t.Fatalf("read api/PERMISSIONS.md: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+\\.[a-z_]+)` \\|").FindAllStringSubmatch(string(doc), -1) {
		listed[m[1]] = true
	}
	used := map[string]bool{}
	permName := regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			for _, p := range listedPermsOf(op) {
				used[p] = true
			}
			for role, perms := range rolesOf(op) {
				if role == "" {
					t.Errorf("%s %s: empty role key", method, path)
				}
				for _, p := range perms {
					if !permName.MatchString(p) {
						t.Errorf("%s %s: permission %q is not <module>.<verb>", method, path, p)
					}
					used[p] = true
					if !listed[p] {
						t.Errorf("%s %s: permission %q is not in api/PERMISSIONS.md", method, path, p)
					}
				}
			}
		}
	}
	var unused []string
	for p := range listed {
		if !used[p] {
			unused = append(unused, p)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("permissions listed but used by no operation: %v", unused)
	}
}

// The JSON Schemas of the agent protocol exist, parse, and are the ones the
// transport operations reference.
func TestAgentProtocolSchemas(t *testing.T) {
	spec := loadDraftSpec(t)
	root := filepath.Join("..", "..", "..")
	files, err := filepath.Glob(filepath.Join(root, "api", "agent-protocol", "v1", "*.schema.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no agent protocol schemas found (%v)", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: $schema = %v, want draft 2020-12", f, doc["$schema"])
		}
	}
	found := false
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			raw, ok := op.Extensions["x-agent-message-schemas"]
			if !ok {
				continue
			}
			found = true
			b, _ := json.Marshal(raw)
			if rm, ok := raw.(json.RawMessage); ok {
				b = rm
			}
			var m map[string]string
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("%s: x-agent-message-schemas: %v", op.OperationID, err)
			}
			for typ, rel := range m {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
					t.Errorf("%s: schema for %q: %v", op.OperationID, typ, err)
				}
			}
		}
	}
	if !found {
		t.Error("no operation references the agent message schemas")
	}
}
