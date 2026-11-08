package access_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

// An event older than 30 days (after the clock correction) is rejected `invalid`.
func TestIntegrationAncientEventsAreRejected(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	ag := e.dial(a)
	handshake(t, ag, 0, 0)
	old, recent := protocol.NewID(), protocol.NewID()
	ev := func(id string, at time.Time) protocol.Event {
		return protocol.Event{ID: id, PointID: gate.String(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(at)}
	}
	ag.Send(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{
		ev(old, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)),
		ev(recent, time.Now().Add(-29*24*time.Hour)),
	}})
	ack := decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	var det protocol.EventAckDetail
	_ = json.Unmarshal(ack.Detail, &det)
	if len(det.Accepted) != 1 || det.Accepted[0] != recent || len(det.Rejected) != 1 || det.Rejected[0].ID != old || det.Rejected[0].Code != protocol.RejectInvalid {
		t.Fatalf("ack: %s", ack.Detail)
	}
}

// The sealed rotated token is erased once its 10-minute window has passed.
func TestIntegrationSealedRotationIsClearedAfterTheWindow(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	code, _ := e.post("/v1/agent/token:rotate", a.Token, struct{}{})
	mustEq(t, "rotate", code, 200)
	sealed := func() bool {
		var has bool
		_ = e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT rotation_sealed IS NOT NULL FROM agent WHERE id = $1`, a.ID).Scan(&has)
		})
		return has
	}
	if !sealed() {
		t.Fatal("nothing sealed inside the window")
	}
	if n, err := e.svc.ClearSealedRotations(context.Background()); err != nil || n != 0 || !sealed() {
		t.Fatalf("cleared %d inside the window: %v", n, err)
	}
	e.clk.Advance(access.RotationWindow + time.Minute)
	if n, err := e.svc.ClearSealedRotations(context.Background()); err != nil || n != 1 || sealed() {
		t.Fatalf("cleared %d after the window: %v", n, err)
	}
}

// A forced rotation reaches a connected agent at once, as update_config with rotate_now.
func TestIntegrationForcedRotationReachesAConnectedAgent(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	ag := e.dial(a)
	hello, _ := handshake(t, ag, 0, 0)
	if hello.RotateNow {
		t.Fatal("rotate_now before it was forced")
	}
	if _, err := e.svc.ForceRotation(e.ctx, e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		env := ag.Recv(time.Until(deadline))
		if env.Type != protocol.TypeCommand {
			continue
		}
		var c protocol.Command
		_ = json.Unmarshal(env.Payload, &c)
		if c.Command == protocol.CmdUpdateConfig && c.RotateNow {
			return
		}
	}
	t.Fatal("no update_config with rotate_now arrived")
}

// A revocation written by another replica (straight to the database) reaches a
// connected agent with its next heartbeat.
func TestIntegrationRevocationOnAnotherReplicaReachesTheSession(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	ag := e.dial(a)
	handshake(t, ag, 0, 0)
	ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{Drivers: []protocol.DriverState{}})
	t.Logf("pre ack %s", ag.RecvType(protocol.TypeAck, 3*time.Second).Type)
	e.exec(`UPDATE agent SET status = 'revoked', revoked_at = now() WHERE id = $1`, a.ID)
	ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{Drivers: []protocol.DriverState{}})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		env := ag.Recv(time.Until(deadline))
		t.Logf("got %s %s", env.Type, env.Payload)
		if env.Type == protocol.TypeCommand {
			var c protocol.Command
			_ = json.Unmarshal(env.Payload, &c)
			if c.Command == protocol.CmdRevoked {
				return
			}
		}
	}
	t.Fatal("the agent was not told it is revoked")
}

// The poll limiter admits the agent's worst case after an outage (hello, five event
// batches, their acks and a heartbeat, back to back) and still refuses a flood.
func TestIntegrationPollLimitAdmitsTheAgentsWorstCase(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	_, p := pollAgent(e, a)
	hello, _ := protocol.New(protocol.TypeHello, protocol.AgentHello{From: "agent", AgentID: a.ID.String(), AgentVersion: "t", ProtocolVersions: []int{1},
		Drivers: []protocol.DriverState{}}, time.Now())
	res := p.Do([]protocol.Envelope{hello}, 0)
	mustEq(t, "hello", res.Status, http.StatusOK)
	requests := 1
	for i := 0; i < 5; i++ { // five batches, each its own request
		env, err := protocol.New(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{{
			ID: protocol.NewID(), PointID: gate.String(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(time.Now()),
		}}}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		res = p.Do([]protocol.Envelope{env}, 0)
		requests++
		mustEq(t, "batch", res.Status, http.StatusOK)
	}
	for i := 0; i < 5; i++ { // acks of what the server sent, and a heartbeat
		res = p.Do(nil, 0)
		requests++
		mustEq(t, "follow-up", res.Status, http.StatusOK)
	}
	mustEq(t, "requests of the burst", requests, 11)
	if access.PollBurst < requests {
		t.Fatalf("burst %d is below the agent's worst case of %d requests", access.PollBurst, requests)
	}
	// a flood is still refused
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		limited = p.Do(nil, 0).Status == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("40 back-to-back polls were never limited")
	}
}
