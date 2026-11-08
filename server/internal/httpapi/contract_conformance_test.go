package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// contractTags are the modules whose draft definitions the served spec adopts verbatim.
var contractTags = map[string]bool{"tenancy": true, "people": true, "codes": true, "operations": true, "dashboards": true}

func inContract(path string, op *openapi3.Operation) bool {
	if !strings.HasPrefix(path, "/v1/orgs/") && !strings.HasPrefix(path, "/v1/invitations/") && !strings.HasPrefix(path, "/v1/join/") {
		return false
	}
	for _, tag := range op.Tags {
		if contractTags[tag] {
			return true
		}
	}
	// the household self-service operations are tagged `me`
	return strings.Contains(path, "/me/")
}

// The draft is the contract: every served tenancy, people, codes and household
// operation is the draft's definition, byte for byte (operationId, method, path,
// parameters, security, extensions, request and response schema references).
func TestServedContractOperationsAreTheDraftsDefinition(t *testing.T) {
	served, draft := loadSpec(t), loadDraftSpec(t)
	n := 0
	for path, item := range served.Paths.Map() {
		for method, op := range item.Operations() {
			if !inContract(path, op) {
				continue
			}
			n++
			dItem := draft.Paths.Find(path)
			if dItem == nil || dItem.GetOperation(method) == nil {
				t.Errorf("%s %s (%s) is served but the draft has no such operation", method, path, op.OperationID)
				continue
			}
			dOp := dItem.GetOperation(method)
			if dOp.OperationID != op.OperationID {
				t.Errorf("%s %s: operationId %q, the draft says %q", method, path, op.OperationID, dOp.OperationID)
			}
			sj, _ := json.Marshal(op)
			dj, _ := json.Marshal(dOp)
			if string(sj) != string(dj) {
				t.Errorf("%s %s (%s) differs from the draft definition:\nserved: %s\ndraft:  %s", method, path, op.OperationID, sj, dj)
			}
			spj, _ := json.Marshal(item.Parameters)
			dpj, _ := json.Marshal(dItem.Parameters)
			if string(spj) != string(dpj) {
				t.Errorf("%s: path parameters differ from the draft: %s vs %s", path, spj, dpj)
			}
		}
	}
	if n < 90 {
		t.Errorf("only %d contract operations are served; expected the tenancy and people contract", n)
	}
}

// Every component schema the served contract operations use is the draft's.
func TestServedContractSchemasAreTheDraftsDefinition(t *testing.T) {
	served, draft := loadSpec(t), loadDraftSpec(t)
	for name, ref := range served.Components.Schemas {
		dRef, ok := draft.Components.Schemas[name]
		if !ok {
			continue // schemas of other modules (auth, audit) may carry another name in the draft
		}
		sj, _ := json.Marshal(ref.Value)
		dj, _ := json.Marshal(dRef.Value)
		if string(sj) != string(dj) {
			if isContractSchema(name) {
				t.Errorf("schema %s differs from the draft definition", name)
			}
		}
	}
}

func isContractSchema(name string) bool {
	switch name {
	case "Problem", "ProblemFieldError", "Money", "PageInfo", "Me", "MeUser", "OrgAccess", "AuthResult", "AuthUser", "Permission", "PermissionGrant", "RoleGrant", "MeSite", "AuditEvent", "AuditEventList", "AuditChange":
		return false
	}
	return true
}
