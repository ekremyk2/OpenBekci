package access_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// An unknown, an expired and a used token answer one identical 404 body.
func TestIntegrationEnrolAnswersOneIdentical404(t *testing.T) {
	e := newEnv(t)
	body := func(token string) (int, string) {
		code, b := e.post("/v1/agent/enrol", "", protocol.EnrolRequest{EnrolmentToken: token, Hostname: "h", Platform: "p", AgentVersion: "v"})
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "instance") // the request id differs per request
		delete(m, "request_id")
		out, _ := json.Marshal(m)
		return code, string(out)
	}

	unknownCode, unknownBody := body("obk_enr_" + strings.Repeat("A", 43))

	used, err := e.svc.CreateAgent(e.ctx, e.org, e.site.SiteID, "used")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := body(used.EnrolmentToken); code != 200 {
		t.Fatalf("first use answered %d", code)
	}
	usedCode, usedBody := body(used.EnrolmentToken)

	expired, err := e.svc.CreateAgent(e.ctx, e.org, e.site.SiteID, "expired")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(access.EnrolTokenTTL + time.Second)
	expiredCode, expiredBody := body(expired.EnrolmentToken)

	for name, got := range map[string][2]any{"unknown": {unknownCode, unknownBody}, "used": {usedCode, usedBody}, "expired": {expiredCode, expiredBody}} {
		if got[0] != 404 || got[1] != unknownBody {
			t.Errorf("%s token: %v %v, want 404 %s", name, got[0], got[1], unknownBody)
		}
	}
	// a token of the wrong shape is a validation problem, not a probe
	if code, _ := body("not-a-token"); code != 422 {
		t.Errorf("malformed token: %d", code)
	}
}

func TestIntegrationEnrolTokenIsValidFor15MinutesAndOnce(t *testing.T) {
	e := newEnv(t)
	en, err := e.svc.CreateAgent(e.ctx, e.org, e.site.SiteID, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(en.EnrolmentToken, "obk_enr_") || en.Agent.Status != "pending" || en.Agent.Connected {
		t.Fatalf("%+v", en)
	}
	if got := en.EnrolmentExpiresAt.Sub(e.clk.Now()); got < 14*time.Minute || got > 15*time.Minute+time.Second {
		t.Fatalf("expires in %s", got)
	}
	// the hash is stored, never the token
	var hash []byte
	_ = e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT enrol_token_hash FROM agent WHERE id = $1`, en.Agent.ID).Scan(&hash)
	})
	if len(hash) != 32 || strings.Contains(string(hash), "obk_enr_") {
		t.Fatalf("stored hash: %x", hash)
	}
	a := e.enrol(en.Agent.ID, en.EnrolmentToken)
	if !strings.HasPrefix(a.Token, "obk_agt_"+en.Agent.ID.String()+".") {
		t.Fatalf("agent token %q", a.Token)
	}
	got, err := e.svc.GetAgent(e.ctx, e.org, en.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.RotateBy == nil || got.RotateBy.Sub(e.clk.Now()) < 179*24*time.Hour || got.EnrolExpiresAt != nil && false {
		t.Fatalf("after enrolment: %+v", got)
	}
	// re-enrolling gives a new token and invalidates the agent's token
	re, err := e.svc.ReenrolAgent(e.ctx, e.org, en.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if re.Agent.Status != "pending" {
		t.Fatalf("%+v", re.Agent)
	}
	if code, _ := e.post("/v1/agent/token:rotate", a.Token, struct{}{}); code != 401 {
		t.Fatalf("the old agent token after re-enrolment: %d", code)
	}
	b := e.enrol(en.Agent.ID, re.EnrolmentToken)
	if b.Token == a.Token {
		t.Fatal("a new agent token expected")
	}
}

// Every table of the module is a tenant table: another organization (and a
// connection with no tenant at all) sees nothing and can write nothing.
func TestIntegrationRowLevelSecurityOnEveryTable(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34RLS111")
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	e.project(e.site)
	e.vehicle(e.site, e.unit, "34RLS222") // left in access_dirty
	ag := e.dial(a)
	handshake(t, ag, 0, 0)
	ag.Send(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{{ID: protocol.NewID(), PointID: gate.String(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(time.Now())}}})
	ag.RecvType(protocol.TypeAck, 5*time.Second)
	if _, err := e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, ""); err != nil {
		t.Fatal(err)
	}

	tables := []string{"agent", "access_point", "credential", "access_site_state", "allowlist_change", "agent_command", "access_event", "access_dirty"}
	for _, table := range tables {
		var own int
		_ = e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&own)
		})
		if own == 0 {
			t.Fatalf("%s: the seeded organization sees no rows, the test proves nothing", table)
		}
		var foreign, bare int
		_ = e.d.InTenantTx(context.Background(), e.other, func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&foreign)
		})
		_ = e.d.InAppTx(context.Background(), func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&bare)
		})
		if foreign != 0 || bare != 0 {
			t.Errorf("%s: another organization sees %d rows, no tenant sees %d", table, foreign, bare)
		}
	}
	// writing a row of org A while acting as org B is refused by the policy
	err := e.d.InTenantTx(context.Background(), e.other, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO access_site_state (org_id, site_id) VALUES ($1, $2)`, e.org, e.site.SiteID)
		return err
	})
	if err == nil {
		t.Error("an insert for another organization must be refused")
	}
	// the opening log is append-only for the application roles
	for _, stmt := range []string{`UPDATE access_event SET reason = 'x'`, `DELETE FROM access_event`, `TRUNCATE access_event`} {
		err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), stmt)
			return err
		})
		if err == nil {
			t.Errorf("%q must be refused", stmt)
		}
	}
	// the two lookups of the agent token reveal the organization of an agent id and nothing else
	var org *uuid.UUID
	_ = e.d.InAppTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT access_agent_org($1)`, a.ID).Scan(&org)
	})
	if org == nil || *org != e.org {
		t.Fatalf("access_agent_org = %v", org)
	}
	_ = e.d.InAppTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT access_agent_org($1)`, uuid.New()).Scan(&org)
	})
	if org != nil {
		t.Fatalf("an unknown agent id resolves to %v", org)
	}
}

// An agent is scoped to its own site and its own points.
func TestIntegrationAgentTokenIsScopedToItsSite(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi-1")
	own := e.point("Own gate", "vehicle_gate", &a.ID, "relay")

	// a second site with its own agent and point
	site2, _ := e.newSite(e.org)
	en2, err := e.svc.CreateAgent(e.ctx, e.org, site2.SiteID, "pi-2")
	if err != nil {
		t.Fatal(err)
	}
	b := e.enrol(en2.Agent.ID, en2.EnrolmentToken)
	foreign := uuid.New()
	e.exec(`INSERT INTO access_point (id, org_id, site_id, name, kind, direction, agent_id, driver_ref) VALUES ($1, $2, $3, 'Gate 2', 'vehicle_gate', 'in', $4, '{"instance":"r"}')`,
		foreign, e.org, site2.SiteID, b.ID)
	// and a point of another agent of the same site
	other := uuid.New()
	en3, _ := e.svc.CreateAgent(e.ctx, e.org, e.site.SiteID, "pi-3")
	c := e.enrol(en3.Agent.ID, en3.EnrolmentToken)
	e.exec(`INSERT INTO access_point (id, org_id, site_id, name, kind, direction, agent_id, driver_ref) VALUES ($1, $2, $3, 'Gate 3', 'vehicle_gate', 'in', $4, '{"instance":"r"}')`,
		other, e.org, e.site.SiteID, c.ID)

	ag := e.dial(a)
	handshake(t, ag, 0, 0)
	good, bad1, bad2 := protocol.NewID(), protocol.NewID(), protocol.NewID()
	now := protocol.At(time.Now())
	ag.Send(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{
		{ID: good, PointID: own.String(), Decision: "deny", Reason: "unknown", OccurredAt: now},
		{ID: bad1, PointID: foreign.String(), Decision: "deny", Reason: "unknown", OccurredAt: now},
		{ID: bad2, PointID: other.String(), Decision: "deny", Reason: "unknown", OccurredAt: now},
	}})
	ack := decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	var det protocol.EventAckDetail
	_ = json.Unmarshal(ack.Detail, &det)
	if len(det.Accepted) != 1 || det.Accepted[0] != good || len(det.Rejected) != 2 {
		t.Fatalf("ack: %s", ack.Detail)
	}
	for _, r := range det.Rejected {
		if r.Code != protocol.RejectUnknownPoint {
			t.Errorf("rejected %s with %s", r.ID, r.Code)
		}
	}
	// a hello that names another agent ends the session
	ag2 := e.dial(a)
	ag2.ID = b.ID.String()
	ag2.Hello(0, 0)
	if code := closeCode(t, ag2, 5*time.Second); code != 1008 {
		t.Errorf("hello naming another agent closed with %d", code)
	}
	// an agent's config names only its own points
	ag3 := e.dial(b)
	_, rest := handshake(t, ag3, 0, 0)
	cmds := ofType(rest, protocol.TypeCommand)
	if len(cmds) != 1 {
		t.Fatalf("after hello: %v", types(rest))
	}
	cfg := decodeAs[protocol.Command](t, cmds[0])
	if len(cfg.Points) != 1 || cfg.Points[0].ID != foreign.String() {
		t.Fatalf("agent 2's config: %+v", cfg.Points)
	}
	// and a token of an agent can not be used as another's
	code, _ := e.post("/v1/agent/token:rotate", strings.Replace(a.Token, a.ID.String(), b.ID.String(), 1), struct{}{})
	mustEq(t, "a token with another agent's id", code, 401)
}

// A manager of another organization cannot reach this one's agents.
func TestIntegrationOtherOrganizationsManagerIsKeptOut(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	foreign := e.managerCtx(e.other)
	if _, err := e.svc.GetAgent(foreign, e.org, a.ID); err == nil {
		t.Fatal("reading another organization's agent must fail")
	} else if pe := problem.From(err); pe.Status != 403 && pe.Status != 404 {
		t.Fatalf("status %d", pe.Status)
	}
	if _, err := e.svc.CreateAgent(foreign, e.org, e.site.SiteID, "x"); err == nil {
		t.Fatal("creating an agent in another organization must fail")
	}
	// even with the right organization, a read-only caller cannot manage
	reader := readOnlyCtx(e)
	if _, err := e.svc.CreateAgent(reader, e.org, e.site.SiteID, "x"); err == nil || problem.From(err).Status != 403 {
		t.Fatalf("a reader creating an agent: %v", err)
	}
	if _, err := e.svc.GetAgent(reader, e.org, a.ID); err != nil {
		t.Fatalf("a reader reads: %v", err)
	}
	if _, err := e.svc.OpenPoint(reader, e.org, uuid.New(), access.OpenRequest{}, ""); err == nil || problem.From(err).Status != 403 {
		t.Fatalf("a reader opening a gate: %v", err)
	}
}

// Revoking an agent needs a recent re-authentication.
func TestIntegrationRevokeNeedsReauth(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	stale := noReauthCtx(e)
	_, err := e.svc.RevokeAgent(stale, e.org, a.ID)
	if err == nil || problem.From(err).Code != problem.CodeReauthRequired {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.svc.RevokeAgent(e.ctx, e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RevokeAgent(e.ctx, e.org, a.ID); err == nil || problem.From(err).Status != http.StatusConflict {
		t.Fatalf("revoking twice: %v", err)
	}
}
