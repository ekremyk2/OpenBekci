package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// A site-scoped auditor reads the organization and only the audit events of
// its site; org.manage stays org-only.
func TestIntegrationSiteScopedAuditorSeesOnlyItsSite(t *testing.T) {
	e := newAuthzEnv(t)
	org := e.w.OrgA
	siteA, siteB := id.New(), id.New()
	u := e.w.Person(t, org, e.w.Actor("no_membership"))
	e.w.AddMembership(t, org, u, authz.RoleAuditor, authz.ScopeSite, &siteA, time.Now().Add(-time.Hour), time.Time{})

	// events: one org-level (no site), one of each site
	err := e.db.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) error {
		ctx := context.Background()
		for _, ev := range []audit.Event{
			{OrgID: org.ID, Action: "org.update", TargetType: "organization", TargetID: org.ID},
			{OrgID: org.ID, SiteID: &siteA, Action: "thing.create", TargetType: "thing", TargetID: id.New()},
			{OrgID: org.ID, SiteID: &siteB, Action: "thing.create", TargetType: "thing", TargetID: id.New()},
		} {
			if err := audit.Record(ctx, tx, ev); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.mustJSON(e.as(u, "GET", e.orgPath(org, ""), ""), 200, nil)
	e.problem(e.as(u, "PATCH", e.orgPath(org, ""), `{"name":"x"}`, "If-Match", `"1"`), 403, "insufficient_permission")
	var list struct {
		Items []struct {
			SiteID *string `json:"site_id"`
		} `json:"items"`
	}
	e.mustJSON(e.as(u, "GET", e.orgPath(org, "/audit"), ""), 200, &list)
	if len(list.Items) != 1 || list.Items[0].SiteID == nil || *list.Items[0].SiteID != siteA.String() {
		b, _ := json.Marshal(list)
		t.Fatalf("site auditor sees %s, want only site A's event", b)
	}
	// an org-wide auditor sees all three
	e.mustJSON(e.as(e.w.Actor("auditor"), "GET", e.orgPath(org, "/audit"), ""), 200, &list)
	if len(list.Items) != 3 {
		t.Errorf("org auditor sees %d events, want 3", len(list.Items))
	}
}
