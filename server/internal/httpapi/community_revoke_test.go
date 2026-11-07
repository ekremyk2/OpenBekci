package httpapi

import (
	"fmt"
	"testing"
)

func TestIntegrationCommunityRevokeClosesOpenVisit(t *testing.T) {
	c := newCommEnv(t)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	rv := c.want(c.req(owner, "POST", c.path("/recurring-visitors"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Cleaner","days":[1,2,3,4,5,6,7],"start":"00:00","end":"23:59","valid_from":"2026-01-01"}`, c.f.Site, c.f.Unit)), 201)
	qr := rv["qr_token"].(string)
	// the scan opens a visit; revoking the recurring visitor must close it
	scan := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr)), 200)
	if scan["valid"] != true {
		t.Fatalf("scan = %v", scan)
	}
	visit := scan["entry"].(map[string]any)["id"].(string)
	c.want(c.req(owner, "POST", c.path("/recurring-visitors/"+rv["id"].(string)+":revoke"), ""), 200)
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors/"+visit+":arrive"), ""), 409, "conflict")
	if ok, why := c.scanReason(qr); ok || why != "revoked" {
		t.Fatalf("scan after revoke: valid=%v reason=%s", ok, why)
	}
	// even if the visit were still open (cancelled flag cleared), arrival checks the recurring visitor's status
	c.w.Exec(t, `UPDATE visitor_entry SET cancelled = false WHERE id = $1`, visit)
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors/"+visit+":arrive"), ""), 409, "conflict")
}
