package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// The access API end to end through the router with response validation on
// (the tests of this package always validate every response against the spec).
func TestIntegrationAccessAPI(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	site := f.Site.String()
	base := p.base()

	// create an agent: the enrolment token is shown once and starts with obk_enr_
	r := p.must(p.call("POST", base+"/agents", fmt.Sprintf(`{"site_id":%q,"name":"Gate Pi"}`, site), p.adm), 201)
	agent := r.Body["agent"].(map[string]any)
	token := str(r.Body, "enrolment_token")
	if !strings.HasPrefix(token, "obk_enr_") || str(agent, "status") != "pending" || str(agent, "id") == "" {
		t.Fatalf("created: %s", r.Raw)
	}
	agentID := str(agent, "id")
	p.must(p.call("POST", base+"/agents", fmt.Sprintf(`{"site_id":%q,"name":"Gate Pi"}`, site), p.adm), 409) // one name per site

	// enrol (no credentials: the one-time token is the credential)
	en := p.must(p.call("POST", "/v1/agent/enrol", fmt.Sprintf(`{"enrolment_token":%q,"hostname":"pi","platform":"linux/arm64","agent_version":"0.3.0"}`, token), ""), 200)
	agentToken := str(en.Body, "agent_token")
	if !strings.HasPrefix(agentToken, "obk_agt_"+agentID+".") || !strings.HasPrefix(str(en.Body, "ws_url"), "wss://") || !strings.HasPrefix(str(en.Body, "poll_url"), "https://") {
		t.Fatalf("enrolled: %s", en.Raw)
	}
	p.must(p.call("POST", "/v1/agent/enrol", fmt.Sprintf(`{"enrolment_token":%q,"hostname":"pi","platform":"linux/arm64","agent_version":"0.3.0"}`, token), ""), 404)

	got := p.must(p.call("GET", base+"/agents/"+agentID, "", p.adm), 200)
	if str(got.Body, "status") != "active" || got.Body["rotate_by"] == nil || got.Body["connected"] != false {
		t.Fatalf("agent: %s", got.Raw)
	}
	lst := p.must(p.call("GET", base+"/agents?site_id="+site+"&status=active", "", p.adm), 200)
	if len(items(lst)) != 1 {
		t.Fatalf("agents: %s", lst.Raw)
	}
	// a user session is not an agent token, and an agent token is not a user session
	p.must(p.call("POST", "/v1/agent/token:rotate", "", p.adm), 401)
	p.must(p.call("GET", base+"/agents?site_id="+site, "", agentToken), 401)

	// rename needs If-Match
	p.must(p.call("PATCH", base+"/agents/"+agentID, `{"name":"Gate Pi 2"}`, p.adm), 422)
	p.must(p.call("PATCH", base+"/agents/"+agentID, `{"name":"Gate Pi 2"}`, p.adm, "If-Match", `"99"`), 409)
	ren := p.must(p.call("PATCH", base+"/agents/"+agentID, `{"name":"Gate Pi 2"}`, p.adm, "If-Match", fmt.Sprintf(`"%d"`, num(got.Body, "version"))), 200)
	if str(ren.Body, "name") != "Gate Pi 2" {
		t.Fatalf("%s", ren.Raw)
	}

	// access points
	pt := p.must(p.call("POST", base+"/access-points", fmt.Sprintf(`{"site_id":%q,"name":"Main gate","kind":"vehicle_gate","direction":"in","agent_id":%q,"driver_ref":{"instance":"gate-relay","channel":"1"}}`, site, agentID), p.adm), 201)
	pointID := str(pt.Body, "id")
	if pt.Body["enabled"] != true || num(pt.Body, "version") != 1 {
		t.Fatalf("point: %s", pt.Raw)
	}
	p.must(p.call("POST", base+"/access-points", fmt.Sprintf(`{"site_id":%q,"name":"Bad","kind":"teleporter","direction":"in"}`, site), p.adm), 422)
	p.must(p.call("POST", base+"/access-points", fmt.Sprintf(`{"site_id":%q,"name":"Main gate","kind":"vehicle_gate","direction":"in"}`, site), p.adm), 409)
	p.must(p.call("POST", base+"/access-points", fmt.Sprintf(`{"site_id":%q,"name":"X","kind":"vehicle_gate","direction":"in","agent_id":%q}`, site, id.New()), p.adm), 422) // an agent that is not there
	up := p.must(p.call("PATCH", base+"/access-points/"+pointID, `{"driver_ref":null,"direction":"both"}`, p.adm, "If-Match", `"1"`), 200)
	if up.Body["driver_ref"] != nil || str(up.Body, "direction") != "both" || num(up.Body, "version") != 2 {
		t.Fatalf("patched: %s", up.Raw)
	}
	p.must(p.call("PATCH", base+"/access-points/"+pointID, `{"driver_ref":{"instance":"gate-relay","channel":"1"}}`, p.adm, "If-Match", `"2"`), 200)
	pl := p.must(p.call("GET", base+"/access-points?site_id="+site+"&agent_id="+agentID+"&enabled=true", "", p.adm), 200)
	if len(items(pl)) != 1 {
		t.Fatalf("points: %s", pl.Raw)
	}

	// credentials are derived from vehicles; the projection runs as the job does
	f2 := p.w.NewSite(t, p.org) // a second site stays out of the lists
	_ = f2
	p.w.NewOccupancy(t, f, f.Unit, p.w.NewPerson(t, p.org), "owner")
	v := p.w.NewVehicle(t, f)
	_ = v
	if _, err := p.e.acc.ProjectDirty(context.Background(), access.Site{OrgID: p.org.ID, SiteID: f.Site}); err != nil {
		t.Fatal(err)
	}
	cl := p.must(p.call("GET", base+"/access/credentials?site_id="+site+"&kind=plate&source_type=vehicle&status=active", "", p.adm), 200)
	if len(items(cl)) != 1 {
		t.Fatalf("credentials: %s", cl.Raw)
	}
	c := items(cl)[0]
	if str(c, "point_scope") != "all_vehicle" || str(c, "unit_door_number") != f.DoorNum || len(c["point_ids"].([]any)) != 0 {
		t.Fatalf("credential: %v", c)
	}
	p.must(p.call("GET", base+"/access/credentials/"+str(c, "id"), "", p.adm), 200)
	p.must(p.call("GET", base+"/access/credentials?site_id="+site+"&point_id="+pointID, "", p.adm), 200)
	rv := p.must(p.call("DELETE", base+"/access/credentials/"+str(c, "id"), "", p.adm), 409) // derived: ends with its vehicle
	if str(rv.Body, "code") != "conflict" {
		t.Fatalf("%s", rv.Raw)
	}

	// the manual open: refused while the agent is not connected, accepted once it is
	open := base + "/access-points/" + pointID + ":open"
	p.must(p.call("POST", open, "", p.adm), 422) // the Idempotency-Key is mandatory
	off := p.must(p.call("POST", open, "", p.adm, "Idempotency-Key", "open-key-0001"), 409)
	if str(off.Body, "code") != "agent_offline" {
		t.Fatalf("%s", off.Raw)
	}
	p.w.Exec(t, `UPDATE agent SET last_seen = now() WHERE id = $1`, agentID)
	acc := p.must(p.call("POST", open, `{}`, p.adm, "Idempotency-Key", "open-key-0002"), 202)
	cmdID := str(acc.Body, "id")
	if str(acc.Body, "status") != "pending" || str(acc.Body, "command") != "open_point" || cmdID == "" {
		t.Fatalf("open: %s", acc.Raw)
	}
	again := p.must(p.call("POST", open, `{}`, p.adm, "Idempotency-Key", "open-key-0002"), 202) // a replayed key returns the stored answer
	if str(again.Body, "id") != cmdID {
		t.Fatalf("replay: %s", again.Raw)
	}
	st := p.must(p.call("GET", base+"/access/commands/"+cmdID, "", p.adm), 200)
	if str(st.Body, "status") != "pending" || st.Body["completed_at"] != nil {
		t.Fatalf("command: %s", st.Raw)
	}
	p.must(p.call("GET", base+"/access/commands/"+id.New().String(), "", p.adm), 404)

	// the opening log
	p.w.Exec(t, `INSERT INTO access_event (id, org_id, site_id, point_id, agent_id, decision, reason, observed_plate, occurred_at)
		VALUES ($1, $2, $3, $4, $5, 'deny', 'unknown', '34XYZ999', now())`, id.New(), p.org.ID, f.Site, pointID, agentID)
	ev := p.must(p.call("GET", base+"/access/events?site_id="+site, "", p.adm), 200)
	if len(items(ev)) != 1 || str(items(ev)[0], "point_name") != "Main gate" || num(items(ev)[0], "sync_seq") < 1 {
		t.Fatalf("events: %s", ev.Raw)
	}
	seq := num(items(ev)[0], "sync_seq")
	feed := p.must(p.call("GET", fmt.Sprintf("%s/access/events?site_id=%s&after=%d", base, site, seq), "", p.adm), 200)
	if len(items(feed)) != 0 {
		t.Fatalf("feed after the last event: %s", feed.Raw)
	}
	p.must(p.call("GET", base+"/access/events?site_id="+site+"&after=nope", "", p.adm), 422)
	p.must(p.call("GET", base+"/access/events?site_id="+site+"&bogus=1", "", p.adm), 422)

	// force rotation, re-enrol, revoke (which needs a recent re-authentication)
	rot := p.must(p.call("POST", base+"/agents/"+agentID+":rotate-token", "", p.adm), 200)
	if str(rot.Body, "id") != agentID {
		t.Fatalf("%s", rot.Raw)
	}
	rv2 := p.must(p.call("POST", base+"/agents/"+agentID+":revoke", "", p.adm), 403)
	if str(rv2.Body, "code") != "reauth_required" {
		t.Fatalf("%s", rv2.Raw)
	}
	re := p.must(p.call("POST", base+"/agents/"+agentID+":reenrol", "", p.adm), 200)
	if !strings.HasPrefix(str(re.Body, "enrolment_token"), "obk_enr_") || str(re.Body["agent"].(map[string]any), "status") != "pending" {
		t.Fatalf("reenrol: %s", re.Raw)
	}

	// a point with history is disabled, not deleted
	pv := p.must(p.call("GET", base+"/access-points/"+pointID, "", p.adm), 200)
	p.must(p.call("DELETE", base+"/access-points/"+pointID, "", p.adm, "If-Match", fmt.Sprintf(`"%d"`, num(pv.Body, "version"))), 409)

	// a guard opens but does not manage
	guard := p.w.Token(t, p.w.Actor("guard"))
	p.must(p.call("GET", base+"/access/commands/"+cmdID, "", guard), 200)
	p.must(p.call("GET", base+"/agents?site_id="+site, "", guard), 403)
	p.must(p.call("GET", base+"/access/events?site_id="+site, "", guard), 403)
}
