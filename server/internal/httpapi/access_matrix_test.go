package httpapi

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// newTestAccess builds the access service of the test environments.
func newTestAccess(t testing.TB, d *db.DB, log *slog.Logger) *access.Service {
	t.Helper()
	sealer, err := crypto.NewFieldCipher([]byte("0123456789abcdef0123456789abcdef-enc"), "access.agent_token")
	if err != nil {
		t.Fatal(err)
	}
	return access.NewService(d, access.Config{BaseURL: testBase, Sealer: sealer, Log: log})
}

var accSeq atomic.Int64

// accFix is what a matrix row seeds afresh: a site with an online active agent,
// a point driven by it, a manual credential and a pending command.
type accFix struct {
	f                       authztest.SiteFix
	agent, point, cred, cmd uuid.UUID
}

func seedAccess(w *authztest.World, t *testing.T, org authztest.Org) accFix {
	t.Helper()
	n := accSeq.Add(1)
	f := w.NewSite(t, org)
	x := accFix{f: f, agent: id.New(), point: id.New(), cred: id.New(), cmd: id.New()}
	w.Exec(t, `INSERT INTO agent (id, org_id, site_id, name, status, token_hash, last_seen, rotate_by)
		VALUES ($1, $2, $3, $4, 'active', decode('00', 'hex'), now(), now() + interval '90 days')`, x.agent, org.ID, f.Site, fmt.Sprintf("agent-%d", n))
	w.Exec(t, `INSERT INTO access_point (id, org_id, site_id, name, kind, direction, agent_id, driver_ref)
		VALUES ($1, $2, $3, $4, 'vehicle_gate', 'in', $5, '{"instance":"gate-relay","channel":"1"}')`, x.point, org.ID, f.Site, fmt.Sprintf("gate-%d", n), x.agent)
	w.Exec(t, `INSERT INTO credential (id, org_id, site_id, kind, value, source_type, point_scope, valid_from)
		VALUES ($1, $2, $3, 'tag', $4, 'manual', 'all', now())`, x.cred, org.ID, f.Site, fmt.Sprintf("tag%d", n))
	w.Exec(t, `INSERT INTO agent_command (id, org_id, site_id, agent_id, point_id, command, payload, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'open_point', '{}', now(), now() + interval '15 seconds')`, x.cmd, org.ID, f.Site, x.agent, x.point)
	return x
}

func init() {
	acc := func(method, _ string, ps []authz.Permission, status int, body string, build func(x accFix) string, h ...string) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			return jsonReq(method, orgPath(org, build(x)), body, h...)
		}}
	}
	read, manage, open, view := perms(access.PermRead), perms(access.PermManage), perms(access.PermOpen), perms(access.PermViewEvents)
	site := func(x accFix) string { return "?site_id=" + x.f.Site.String() }
	ifm := "If-Match"
	idem := func() string { return fmt.Sprintf("matrix-key-%d-xxxxxxxx", accSeq.Add(1)) }

	rows := map[string]mrow{
		"access.listAccessPoints": acc("GET", "", read, 200, "", func(x accFix) string { return "/access-points" + site(x) }),
		"access.createAccessPoint": {perms: manage, status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			return jsonReq("POST", orgPath(org, "/access-points"),
				fmt.Sprintf(`{"site_id":%q,"name":"Matrix gate %d","kind":"vehicle_gate","direction":"in"}`, x.f.Site, accSeq.Add(1)))
		}},
		"access.getAccessPoint": acc("GET", "", read, 200, "", func(x accFix) string { return "/access-points/" + x.point.String() }),
		"access.updateAccessPoint": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			return jsonReq("PATCH", orgPath(org, "/access-points/"+x.point.String()), `{"enabled":false}`, ifm, `"1"`)
		}},
		"access.deleteAccessPoint": {perms: manage, status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			// the point has a pending command (a foreign key), so drop that first: the matrix tests rights, not history
			w.Exec(t, `DELETE FROM agent_command WHERE org_id = $1 AND id = $2`, org.ID, x.cmd)
			return jsonReq("DELETE", orgPath(org, "/access-points/"+x.point.String()), "", ifm, `"1"`)
		}},
		"access.openAccessPoint": {perms: open, status: 202, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			return jsonReq("POST", orgPath(org, "/access-points/"+x.point.String()+":open"), "", "Idempotency-Key", idem())
		}},
		"access.getAgentCommand": acc("GET", "", open, 200, "", func(x accFix) string { return "/access/commands/" + x.cmd.String() }),

		"access.listAgents": acc("GET", "", read, 200, "", func(x accFix) string { return "/agents" + site(x) }),
		"access.createAgent": {perms: manage, status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/agents"), fmt.Sprintf(`{"site_id":%q,"name":"Matrix agent %d"}`, f.Site, accSeq.Add(1)))
		}},
		"access.getAgent": acc("GET", "", read, 200, "", func(x accFix) string { return "/agents/" + x.agent.String() }),
		"access.updateAgent": {perms: manage, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			return jsonReq("PATCH", orgPath(org, "/agents/"+x.agent.String()), `{"name":"Renamed by the matrix"}`, ifm, `"1"`)
		}},
		"access.reenrolAgent": acc("POST", "", manage, 200, "", func(x accFix) string { return "/agents/" + x.agent.String() + ":reenrol" }),
		// revoking needs a recent re-authentication: the permitted roles are stopped by reauth_required (403)
		"access.revokeAgent": {perms: manage, status: 403, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedAccess(w, t, org)
			return jsonReq("POST", orgPath(org, "/agents/"+x.agent.String()+":revoke"), "")
		}},
		"access.rotateAgentToken": acc("POST", "", manage, 200, "", func(x accFix) string { return "/agents/" + x.agent.String() + ":rotate-token" }),

		"access.listCredentials":  acc("GET", "", read, 200, "", func(x accFix) string { return "/access/credentials" + site(x) }),
		"access.getCredential":    acc("GET", "", read, 200, "", func(x accFix) string { return "/access/credentials/" + x.cred.String() }),
		"access.revokeCredential": acc("DELETE", "", manage, 204, "", func(x accFix) string { return "/access/credentials/" + x.cred.String() }),
		"access.listAccessEvents": acc("GET", "", view, 200, "", func(x accFix) string { return "/access/events" + site(x) }),
	}
	// the agent transport: an agent token is the only credential; a user session (every matrix actor) is not one
	agentRow := func(method, path, body string) mrow {
		return mrow{status: 401, build: func(*authztest.World, authztest.Org, *testing.T) mreq { return jsonReq(method, path, body) }}
	}
	rows["agent.enrol"] = mrow{status: 422, build: fixed("POST", "/v1/agent/enrol", `{}`)}
	rows["agent.connect"] = agentRow("GET", "/v1/agent/ws", "")
	rows["agent.poll"] = agentRow("POST", "/v1/agent/poll", `{"messages":[],"wait_seconds":0}`)
	rows["agent.rotateToken"] = agentRow("POST", "/v1/agent/token:rotate", "")

	// the revoke row: permitted roles answer reauth_required, everybody else as derived
	r := rows["access.revokeAgent"]
	r.override = func(actor, target string) (int, bool) {
		if actor == "anonymous" {
			return 0, false
		}
		if target == "other" || actor == "no_membership" || actor == "platform_admin" {
			return 404, true
		}
		return 403, true
	}
	rows["access.revokeAgent"] = r

	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
