package access_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/accesstest"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

func closeCode(t *testing.T, ag *accesstest.Agent, within time.Duration) int {
	t.Helper()
	_, err := ag.Conn.Recv(within)
	if err == nil {
		t.Fatal("expected the connection to close")
	}
	return int(websocket.CloseStatus(err))
}

// The whole protocol against Postgres, through a real WebSocket: enrol, hello,
// config, delta, events (accepted, duplicate, unknown point), the open command
// and its ack, heartbeat, token rotation and revocation.
func TestIntegrationFullAgentFlow(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34ABC123")
	a := e.newAgent("gate-pi")
	gate := e.point("Main gate in", "vehicle_gate", &a.ID, "gate-relay")
	e.project(e.site)
	mustEq(t, "site version after the first projection", e.siteVersion(), 1)

	// the enrolment answer
	mustEq(t, "ws_url scheme", a.Resp.WSURL[:5], "ws://")
	mustEq(t, "protocol versions", len(a.Resp.ProtocolVersions), 1)
	if a.Resp.OrgID != e.org.String() || a.Resp.SiteID != e.site.SiteID.String() || a.Resp.AgentID != a.ID.String() {
		t.Fatalf("enrolment answer names the wrong ids: %+v", a.Resp)
	}

	ag := e.dial(a)
	hello, rest := handshake(t, ag, 0, 0)
	mustEq(t, "server hello snapshot_version", hello.SnapshotVersion, 1)
	mustEq(t, "heartbeat_seconds", hello.HeartbeatSeconds, 30)
	mustEq(t, "site timezone", hello.Site.Timezone, "Europe/Istanbul")
	if hello.RotateBy == nil || hello.RotateNow {
		t.Fatalf("rotate_by/rotate_now: %+v", hello)
	}
	// update_config first, then the delta
	if len(rest) != 2 || rest[0].Type != protocol.TypeCommand || rest[1].Type != protocol.TypeDelta {
		t.Fatalf("after hello: %v", types(rest))
	}
	cfg := decodeAs[protocol.Command](t, rest[0])
	mustEq(t, "command", cfg.Command, protocol.CmdUpdateConfig)
	mustEq(t, "config points", len(cfg.Points), 1)
	mustEq(t, "config point id", cfg.Points[0].ID, gate.String())
	mustEq(t, "grace hours", cfg.Settings.OfflineGraceHours, 72)
	mustEq(t, "config version equals the hello's", *cfg.ConfigVersion, hello.ConfigVersion)
	delta := decodeAs[protocol.Delta](t, rest[1])
	mustEq(t, "delta from", delta.FromVersion, 0)
	mustEq(t, "delta to", delta.ToVersion, 1)
	mustEq(t, "change op", delta.Changes[0].Op, protocol.OpUpsert)
	entry := delta.Changes[0].Entry
	mustEq(t, "entry value is the normalized plate", entry.Value, "34ABC123")
	mustEq(t, "entry points", entry.Points[0], gate.String())
	ag.Ack(rest[0].ID, protocol.AckOK, nil)
	ag.Ack(rest[1].ID, protocol.AckOK, nil)

	// a new credential reaches the connected agent as the next delta, within seconds
	e.vehicle(e.site, e.unit, "06S4444")
	e.project(e.site)
	d2 := decodeAs[protocol.Delta](t, ag.RecvType(protocol.TypeDelta, 5*time.Second))
	mustEq(t, "second delta from", d2.FromVersion, 1)
	mustEq(t, "second delta to", d2.ToVersion, 2)
	mustEq(t, "second entry", d2.Changes[0].Entry.Value, "06S4444")

	// events: accepted, unknown point rejected, a repeat is a duplicate
	cred := entry.ID
	ev1 := protocol.NewID()
	evBad := protocol.NewID()
	other := uuid.NewString()
	batch := protocol.EventBatch{Events: []protocol.Event{
		{ID: ev1, PointID: gate.String(), Decision: "allow", Reason: "match", CredentialID: cred, PresentedKind: "plate", ObservedPlate: "34 ABC 123",
			OccurredAt: protocol.At(time.Now().Add(-time.Second)), RawRef: "snap/1.jpg", Detail: json.RawMessage(`{"confidence":0.93}`)},
		{ID: evBad, PointID: other, Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(time.Now())},
	}}
	sent := ag.Send(protocol.TypeEvent, batch)
	ack := decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	mustEq(t, "ack ref", ack.Ref, sent.ID)
	var det protocol.EventAckDetail
	_ = json.Unmarshal(ack.Detail, &det)
	if len(det.Accepted) != 1 || det.Accepted[0] != ev1 || len(det.Rejected) != 1 || det.Rejected[0].ID != evBad || det.Rejected[0].Code != protocol.RejectUnknownPoint {
		t.Fatalf("event ack: %s", ack.Detail)
	}
	ag.Send(protocol.TypeEvent, batch) // the retry after a lost ack
	ack = decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	det = protocol.EventAckDetail{}
	_ = json.Unmarshal(ack.Detail, &det)
	if len(det.Accepted) != 0 || len(det.Duplicates) != 1 || det.Duplicates[0] != ev1 {
		t.Fatalf("repeat ack: %s", ack.Detail)
	}
	mustEq(t, "stored events", e.count("access_event"), 1)

	// the opening log
	evs, _, err := e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, "listed events", len(evs), 1)
	mustEq(t, "observed plate", *evs[0].ObservedPlate, "34ABC123")
	mustEq(t, "point name", *evs[0].PointName, "Main gate in")
	mustEq(t, "unit door number", *evs[0].UnitDoorNumber, "A-1")

	// heartbeat is answered and recorded
	hb := ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{SnapshotVersion: 2, QueueDepth: 3, UptimeS: 10, LostEvents: 2, StoreError: "disk full", DeadLetters: 1,
		Drivers: []protocol.DriverState{{Instance: "gate-relay", State: "ok", Mock: true}}})
	hack := decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	mustEq(t, "heartbeat ack ref", hack.Ref, hb.ID)
	mustEq(t, "heartbeat ack status", hack.Status, protocol.AckOK)
	got, err := e.svc.GetAgent(e.ctx, e.org, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Connected || got.Status != "active" || *got.QueueDepth != 3 || *got.LostEvents != 2 || *got.StoreError != "disk full" || *got.DeadLetters != 1 ||
		len(got.Drivers) != 1 || !got.Drivers[0].Mock || got.LastSeen == nil {
		t.Fatalf("agent after a heartbeat: %+v", got)
	}

	// the open command: id = envelope id, ttl <= 60 s, actor stamped by the server
	cmd, err := e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, "pending command", cmd.Status, "pending")
	env, open := ag.RecvCommand(5 * time.Second)
	mustEq(t, "command id is the envelope id", env.ID, cmd.ID.String())
	mustEq(t, "command", open.Command, protocol.CmdOpenPoint)
	if ttl := open.ExpiresAt.Sub(open.IssuedAt.Time); ttl <= 0 || ttl > 60*time.Second || ttl != 15*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
	ag.Ack(env.ID, protocol.AckDone, nil)
	waitFor(t, "the command to be done", func() bool {
		c, err := e.svc.GetCommand(e.ctx, e.org, cmd.ID)
		return err == nil && c.Status == "done"
	})
	manual := protocol.NewID()
	ag.Send(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{{ID: manual, PointID: gate.String(), Decision: "manual_open", Reason: "guard",
		CommandID: cmd.ID.String(), OccurredAt: protocol.At(time.Now())}}})
	ag.RecvType(protocol.TypeAck, 5*time.Second)
	evs, _, _ = e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{}, 10, "")
	mustEq(t, "events after the manual open", len(evs), 2)
	if evs[0].Decision != "manual_open" || evs[0].ActorUserID == nil || *evs[0].ActorUserID != authz.PrincipalFromContext(e.ctx).UserID || evs[0].CommandID == nil {
		t.Fatalf("manual_open event: %+v", evs[0])
	}

	// token rotation: idempotent for the retired token inside the window
	code, body := e.post("/v1/agent/token:rotate", a.Token, struct{}{})
	mustEq(t, "rotate status", code, 200)
	var rot1 protocol.RotateResponse
	_ = json.Unmarshal(body, &rot1)
	if rot1.AgentToken == "" || rot1.AgentToken == a.Token {
		t.Fatalf("rotate answer: %s", body)
	}
	code, body = e.post("/v1/agent/token:rotate", a.Token, struct{}{}) // the agent crashed before saving: retry with the old one
	mustEq(t, "repeat rotate status", code, 200)
	var rot2 protocol.RotateResponse
	_ = json.Unmarshal(body, &rot2)
	mustEq(t, "the same new token", rot2.AgentToken, rot1.AgentToken)
	code, _ = e.post("/v1/agent/token:rotate", "obk_agt_"+uuid.NewString()+".nope-nope-nope-nope-nope", struct{}{})
	mustEq(t, "unknown token", code, 401)
	e.clk.Advance(11 * time.Minute)
	code, _ = e.post("/v1/agent/token:rotate", a.Token, struct{}{})
	mustEq(t, "the retired token after the window", code, 401)
	code, _ = e.post("/v1/agent/token:rotate", rot1.AgentToken, struct{}{})
	mustEq(t, "the new token still works", code, 200)

	// revocation: command revoked, then close 4401
	if _, err := e.svc.RevokeAgent(e.ctx, e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	_, rc := ag.RecvCommand(5 * time.Second)
	mustEq(t, "revoked command", rc.Command, protocol.CmdRevoked)
	mustEq(t, "close code", closeCode(t, ag, 5*time.Second), protocol.CloseRevoked)
	// an agent that was offline when it was revoked is told the same on its next connection
	ag2, _, _, err := accesstest.DialWS(t, e.wsURL(), rot1.AgentToken)
	if err != nil {
		t.Fatal(err)
	}
	_, rc = ag2.RecvCommand(5 * time.Second)
	mustEq(t, "revoked command on reconnect", rc.Command, protocol.CmdRevoked)
	mustEq(t, "close code on reconnect", closeCode(t, ag2, 5*time.Second), protocol.CloseRevoked)
	// bare 401 only for tokens nobody knows
	_, _, resp, err := accesstest.DialWS(t, e.wsURL(), "obk_agt_"+uuid.NewString()+".nope-nope-nope-nope-nope")
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown token on the socket: %v %v", err, resp)
	}
}

func types(msgs []protocol.Envelope) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Type)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

var _ = context.Background
var _ = id.New
