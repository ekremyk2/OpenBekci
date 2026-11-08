package httpapi

import (
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/authztest"
)

// Every money operation whose spec requires If-Match compares it with the row
// version: a stale value is a 409 version_conflict (the same problem the
// tenancy and people handlers answer), a missing one a 422, and nothing is
// changed. The matrix rows send "1", which is the real version of the fresh
// fixture each row builds; here the header is replaced by a version that never
// existed.
func TestIntegrationMoneyUpdatesCompareIfMatch(t *testing.T) {
	e := newAuthzEnv(t)
	spec := loadSpec(t)
	var matrix map[string][]string
	if !extJSON(t, spec.Extensions, "x-openbekci-roles", &matrix) {
		t.Fatal("no root x-openbekci-roles")
	}
	var ids []string
	need := map[string][]string{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if !strings.HasPrefix(op.OperationID, "money.") {
				continue
			}
			for _, p := range op.Parameters {
				if p.Value != nil && p.Value.In == "header" && p.Value.Name == "If-Match" && p.Value.Required {
					req, _ := opRequired(t, op.Extensions)
					ids, need[op.OperationID] = append(ids, op.OperationID), req
				}
			}
		}
	}
	sort.Strings(ids)
	if len(ids) < 13 {
		t.Fatalf("only %d money operations require If-Match: %v", len(ids), ids)
	}
	send := func(r mreq, a authztest.Actor, ifMatch *string) *httptest.ResponseRecorder {
		hdr := map[string]string{}
		for k, v := range r.headers {
			hdr[k] = v
		}
		delete(hdr, "If-Match")
		if ifMatch != nil {
			hdr["If-Match"] = *ifMatch
		}
		hs := []string{"Authorization", "Bearer " + e.w.Token(t, a)}
		for k, v := range hdr {
			hs = append(hs, k, v)
		}
		return e.do(r.method, r.path, r.body, false, hs...)
	}
	for _, id := range ids {
		row := authzMatrixRows[id]
		var actor *authztest.Actor
		for i, a := range e.w.Actors {
			if a.Role != "" && holdsAll(matrix[string(a.Role)], need[id]) {
				actor = &e.w.Actors[i]
				break
			}
		}
		if actor == nil {
			t.Errorf("%s: no role holds %v", id, need[id])
			continue
		}
		stale, none := `"9999"`, (*string)(nil)
		if rec := send(row.build(e.w, e.w.OrgA, t), *actor, &stale); rec.Code != 409 || !strings.Contains(rec.Body.String(), `"version_conflict"`) {
			t.Errorf("%s with a stale If-Match: %d %s, want 409 version_conflict", id, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if rec := send(row.build(e.w, e.w.OrgA, t), *actor, none); rec.Code != 422 {
			t.Errorf("%s without If-Match: %d %s, want 422", id, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}
