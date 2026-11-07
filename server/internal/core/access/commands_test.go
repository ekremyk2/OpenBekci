package access_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func wantProblem(t *testing.T, err error, status int, code problem.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %d %s, got no error", status, code)
	}
	pe := problem.From(err)
	if pe.Status != status || (code != "" && pe.Code != code) {
		t.Fatalf("got %d %s (%v), want %d %s", pe.Status, pe.Code, err, status, code)
	}
}

// The manual open: refused at once when the agent is offline or its clock is
// suspect, delivered with its id, answered by ack, audited with the actor,
// and never retried.
func TestIntegrationManualOpen(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")

	// no agent assigned, a disabled point, an agent that never connected: visible failures
	free := e.point("Free", "vehicle_gate", nil, "")
	_, err := e.svc.OpenPoint(e.ctx, e.org, free, access.OpenRequest{}, "")
	wantProblem(t, err, 409, problem.CodeAgentOffline)
	_, err = e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	wantProblem(t, err, 409, problem.CodeAgentOffline)
	e.exec(`UPDATE access_point SET enabled = false WHERE id = $1`, free)
	_, err = e.svc.OpenPoint(e.ctx, e.org, free, access.OpenRequest{}, "")
	wantProblem(t, err, 409, problem.CodeConflict)
	_, err = e.svc.OpenPoint(e.ctx, e.org, uuid.New(), access.OpenRequest{}, "")
	wantProblem(t, err, 404, problem.CodeNotFound)

	ag := e.dial(a)
	handshake(t, ag, 0, 0)

	// a suspect clock refuses (the agent says so in a heartbeat)
	ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{ClockSuspect: true, Drivers: []protocol.DriverState{}})
	ag.RecvType(protocol.TypeAck, 5*time.Second)
	_, err = e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	wantProblem(t, err, 409, problem.CodeAgentOffline)
	// a measured offset beyond 24 h refuses as well, however it got there
	off := (25 * time.Hour).Milliseconds()
	ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{ClockOffsetMS: &off, Drivers: []protocol.DriverState{}})
	ag.RecvType(protocol.TypeAck, 5*time.Second)
	_, err = e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	wantProblem(t, err, 409, problem.CodeAgentOffline)
	ok := int64(0)
	ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{ClockOffsetMS: &ok, Drivers: []protocol.DriverState{}})
	ag.RecvType(protocol.TypeAck, 5*time.Second)

	cred := uuid.New()
	e.exec(`INSERT INTO credential (id, org_id, site_id, kind, value, source_type, point_scope, valid_from) VALUES ($1, $2, $3, 'plate', '34OPN111', 'manual', 'all', now())`, cred, e.org, e.site.SiteID)
	visitor := uuid.New()
	cmd, err := e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{CredentialID: &cred, VisitorEntryID: &visitor}, "")
	if err != nil {
		t.Fatal(err)
	}
	env, open := ag.RecvCommand(5 * time.Second)
	mustEq(t, "command id", env.ID, cmd.ID.String())
	mustEq(t, "point", open.PointID, gate.String())
	mustEq(t, "credential", open.CredentialID, cred.String())
	mustEq(t, "visitor entry", open.VisitorEntryID, visitor.String())
	if open.Actor == nil || open.Actor.UserID != authz.PrincipalFromContext(e.ctx).UserID.String() {
		t.Fatalf("actor: %+v", open.Actor)
	}
	if got := open.ExpiresAt.Sub(open.IssuedAt.Time); got != access.OpenTTL {
		t.Fatalf("ttl %s", got)
	}
	// the agent could not pulse the relay: failed / driver_error, shown to the guard with the reason
	ag.Ack(env.ID, protocol.AckFailed, map[string]any{"reason": "driver_error"})
	waitFor(t, "the failure to be recorded", func() bool {
		c, err := e.svc.GetCommand(e.ctx, e.org, cmd.ID)
		return err == nil && c.Status == "failed" && c.Reason != nil && *c.Reason == "driver_error" && c.CompletedAt != nil
	})
	// replayed acks do not change the answer (the agent re-acks a replayed command with its stored result)
	ag.Ack(env.ID, protocol.AckDone, nil)
	time.Sleep(300 * time.Millisecond)
	c, _ := e.svc.GetCommand(e.ctx, e.org, cmd.ID)
	mustEq(t, "the first answer wins", c.Status, "failed")

	// never retried: a failed or interrupted open is not sent again to a reconnecting agent
	ag.Conn.Close()
	ag2 := e.dial(a)
	_, rest := handshake(t, ag2, 0, 0)
	if len(ofType(rest, protocol.TypeCommand)) > 1 { // only update_config may follow
		t.Fatalf("a finished command was redelivered: %v", types(rest))
	}
	for _, m := range ofType(rest, protocol.TypeCommand) {
		if decodeAs[protocol.Command](t, m).Command == protocol.CmdOpenPoint {
			t.Fatal("open_point redelivered")
		}
	}

	// both the request and the result are in the audit trail, the request with the actor
	var actions []string
	var actors []uuid.UUID
	_ = e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT action, actor_user_id FROM audit_event WHERE org_id = $1 AND action LIKE 'access.open%' ORDER BY at`, e.org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var action string
			var actor *uuid.UUID
			if err := rows.Scan(&action, &actor); err != nil {
				return err
			}
			actions = append(actions, action)
			if actor != nil {
				actors = append(actors, *actor)
			}
		}
		return rows.Err()
	})
	if len(actions) != 2 || actions[0] != "access.open_requested" || actions[1] != "access.open_completed" {
		t.Fatalf("audit: %v", actions)
	}
	if len(actors) != 1 || actors[0] != authz.PrincipalFromContext(e.ctx).UserID {
		t.Fatalf("audit actors: %v", actors)
	}
}

func TestIntegrationOpenCommandsExpireAndAreRateLimited(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	ag := e.dial(a)
	handshake(t, ag, 0, 0)
	cmd, err := e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	ag.RecvCommand(5 * time.Second) // delivered, then never answered
	e.clk.Advance(access.OpenTTL + 31*time.Second)
	if _, err := e.svc.Reconcile(context.Background(), e.site); err != nil { // any sync of the agent expires it; poke the session instead
		t.Fatal(err)
	}
	e.svc.WakeSite(e.site.SiteID)
	waitFor(t, "the unanswered command to expire", func() bool {
		c, err := e.svc.GetCommand(e.ctx, e.org, cmd.ID)
		return err == nil && c.Status == "expired"
	})

	// per point: 6 a minute; the seventh is 429 (the first one above counted)
	var limited error
	for i := 0; i < 8 && limited == nil; i++ {
		_, limited = e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	}
	wantProblem(t, limited, 429, problem.CodeRateLimited)
}

func TestIntegrationPendingCommandsWaitForAnAgentAndExpireIfNobodyComes(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	// last seen a moment ago (the 90 s rule) but not connected: accepted
	e.exec(`UPDATE agent SET last_seen = $2 WHERE id = $1`, a.ID, e.clk.Now())
	cmd, err := e.svc.OpenPoint(e.ctx, e.org, gate, access.OpenRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	// it reaches the agent when it connects within the 15 s
	ag := e.dial(a)
	ag.Hello(0, 0)
	ag.RecvType(protocol.TypeHello, 5*time.Second)
	var got bool
	for i := 0; i < 4 && !got; i++ {
		if env, ok := ag.TryRecv(time.Second); ok && env.Type == protocol.TypeCommand && env.ID == cmd.ID.String() {
			got = true
		}
	}
	if !got {
		t.Fatal("the pending command was not delivered at hello")
	}

	// a command nobody picks up is expired the moment somebody asks after its time
	b := e.newAgent("pi-2") // never connects
	gate2 := e.point("Gate 2", "vehicle_gate", &b.ID, "relay")
	e.exec(`UPDATE agent SET last_seen = $2 WHERE id = $1`, b.ID, e.clk.Now())
	cmd2, err := e.svc.OpenPoint(e.ctx, e.org, gate2, access.OpenRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(20 * time.Second)
	c, err := e.svc.GetCommand(e.ctx, e.org, cmd2.ID)
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, "undelivered after its ttl", c.Status, "expired")
}

func TestIntegrationOpenLifetimeIsCappedByTheDatabase(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	for name, interval := range map[string]string{"61 seconds": "61 seconds", "an hour": "1 hour", "zero": "0 seconds", "negative": "-5 seconds"} {
		err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `INSERT INTO agent_command (id, org_id, site_id, agent_id, point_id, command, issued_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, 'open_point', now(), now() + $6::interval)`, uuid.New(), e.org, e.site.SiteID, a.ID, gate, interval)
			return err
		})
		if err == nil {
			t.Errorf("an open_point living %s must be refused", name)
		}
	}
	err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO agent_command (id, org_id, site_id, agent_id, point_id, command, issued_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'open_point', now(), now() + interval '60 seconds')`, uuid.New(), e.org, e.site.SiteID, a.ID, gate)
		return err
	})
	if err != nil {
		t.Errorf("60 s is the limit and allowed: %v", err)
	}
}

// Events: invalid ones are rejected alone, offsets correct the time, the feed
// resumes after a cursor.
func TestIntegrationEventIngestAndFeed(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	ag := e.dial(a)
	handshake(t, ag, 0, 0)

	// the batch carries the agent's measured offset: the server is 30 s ahead
	offset := int64(30_000)
	reported := time.Now().Add(-5 * time.Minute)
	ids := []string{protocol.NewID(), protocol.NewID(), protocol.NewID()}
	good := func(i int, at time.Time) protocol.Event {
		return protocol.Event{ID: ids[i], PointID: gate.String(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(at), ObservedPlate: "34 zzz 00" + string(rune('1'+i))}
	}
	badDecision := good(2, reported)
	badDecision.Decision = "teleport"
	ag.Send(protocol.TypeEvent, protocol.EventBatch{ClockOffsetMS: &offset, Events: []protocol.Event{good(0, reported), good(1, time.Now().Add(time.Hour)), badDecision}})
	ack := decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	var det protocol.EventAckDetail
	_ = json.Unmarshal(ack.Detail, &det)
	if len(det.Accepted) != 2 || len(det.Rejected) != 1 || det.Rejected[0].ID != ids[2] || det.Rejected[0].Code != protocol.RejectInvalid {
		t.Fatalf("ack: %s", ack.Detail)
	}

	evs, _, err := e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, "stored", len(evs), 2)
	byID := map[string]access.Event{}
	for _, ev := range evs {
		byID[ev.ID.String()] = ev
	}
	corrected := byID[ids[0]].OccurredAt
	if d := corrected.Sub(reported.Add(30 * time.Second)); d < -time.Second || d > time.Second {
		t.Fatalf("occurred_at %s, want the reported time plus the 30 s offset", corrected)
	}
	future := byID[ids[1]]
	if future.OccurredAt.After(future.SyncedAt) {
		t.Fatalf("occurred_at %s is later than synced_at %s", future.OccurredAt, future.SyncedAt)
	}
	// the unadjusted time is kept in the payload
	var raw []byte
	_ = e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT raw_payload FROM access_event WHERE id = $1`, ids[0]).Scan(&raw)
	})
	if !jsonHas(raw, "reported_at") {
		t.Fatalf("raw_payload = %s", raw)
	}

	// the feed: oldest first after a sync_seq, then the cursor continues
	for i := 0; i < 5; i++ {
		ag.Send(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{{ID: protocol.NewID(), PointID: gate.String(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(time.Now())}}})
		ag.RecvType(protocol.TypeAck, 5*time.Second)
	}
	all, _, _ := e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{}, 50, "")
	mustEq(t, "events", len(all), 7)
	if all[0].SyncSeq < all[1].SyncSeq {
		t.Fatal("the log lists newest first")
	}
	after := all[len(all)-1].SyncSeq // the oldest
	feed, next, err := e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{After: &after}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 2 || feed[0].SyncSeq <= after || feed[0].SyncSeq >= feed[1].SyncSeq || next == "" {
		t.Fatalf("feed page: %+v next=%q", feed, next)
	}
	rest, next2, err := e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{After: &after}, 50, next)
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, "the rest of the feed", len(rest), 4)
	mustEq(t, "the feed ends", next2, "")
	if rest[0].SyncSeq <= feed[1].SyncSeq {
		t.Fatal("the cursor must continue after the last event of the page")
	}
	// filters
	byPlate, _, _ := e.svc.ListEvents(e.ctx, e.org, e.site.SiteID, access.EventFilter{Plate: "34 zzz 001"}, 50, "")
	mustEq(t, "plate filter", len(byPlate), 1)
}

func jsonHas(raw []byte, key string) bool {
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && m[key] != nil
}

// The snapshot rules: a lower version only right after hello or on request, a
// gap is answered with a snapshot, and request_snapshot is followed by one.
func TestIntegrationSnapshotRules(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34SNP111")
	e.vehicle(e.site, e.unit, "34SNP222")
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	e.project(e.site)
	mustEq(t, "version", e.siteVersion(), 2)

	// an agent that is ahead of the server (restored backup) gets the lower snapshot right after hello
	ag := e.dial(a)
	_, rest := handshake(t, ag, 99, 0)
	snaps := ofType(rest, protocol.TypeSnapshot)
	if len(snaps) != 1 {
		t.Fatalf("after hello: %v", types(rest))
	}
	sn := decodeAs[protocol.Snapshot](t, snaps[0])
	if sn.Version != 2 || len(sn.Entries) != 2 || sn.Site.Timezone != "Europe/Istanbul" {
		t.Fatalf("%+v", sn)
	}
	ag.Ack(snaps[0].ID, protocol.AckOK, map[string]any{"version": 2, "part": 1, "applied": true})

	// the counter going backwards later is not announced by a hello: nothing is sent
	e.exec(`UPDATE access_site_state SET version = 1 WHERE site_id = $1`, e.site.SiteID)
	if env, ok := ag.TryRecv(3 * time.Second); ok {
		t.Fatalf("a lower snapshot was sent mid-session: %s", env.Type)
	}
	e.exec(`UPDATE access_site_state SET version = 2 WHERE site_id = $1`, e.site.SiteID)

	// a gap ack is answered with a snapshot at the current version
	e.vehicle(e.site, e.unit, "34SNP333")
	e.project(e.site)
	d := ag.RecvType(protocol.TypeDelta, 5*time.Second)
	ag.Ack(d.ID, protocol.AckGap, map[string]any{"have_version": 0})
	sn = decodeAs[protocol.Snapshot](t, ag.RecvType(protocol.TypeSnapshot, 5*time.Second))
	mustEq(t, "snapshot after a gap", sn.Version, 3)
	mustEq(t, "entries", len(sn.Entries), 3)

	// request_snapshot: the command, then the snapshot
	if _, err := e.svc.RequestSnapshot(e.ctx, e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	_, cmd := ag.RecvCommand(5 * time.Second)
	mustEq(t, "command", cmd.Command, protocol.CmdRequestSnapshot)
	sn = decodeAs[protocol.Snapshot](t, ag.RecvType(protocol.TypeSnapshot, 5*time.Second))
	mustEq(t, "snapshot after the request", sn.Version, 3)
}

// An agent behind but inside the change log gets deltas; one older than the
// log gets a snapshot.
func TestIntegrationChangeLogRetention(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	for _, p := range []string{"34RET111", "34RET222", "34RET333"} {
		e.vehicle(e.site, e.unit, p)
	}
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	e.project(e.site)
	mustEq(t, "version", e.siteVersion(), 3)

	ag := e.dial(a)
	_, rest := handshake(t, ag, 1, 0)
	deltas := ofType(rest, protocol.TypeDelta)
	if len(deltas) != 1 {
		t.Fatalf("a behind agent within the log: %v", types(rest))
	}
	d := decodeAs[protocol.Delta](t, deltas[0])
	if d.FromVersion != 1 || d.ToVersion != 3 || len(d.Changes) != 2 {
		t.Fatalf("%+v", d)
	}
	ag.Conn.Close()

	// the log is pruned past version 1: an agent at 0 is sent a snapshot
	e.exec(`UPDATE allowlist_change SET at = now() - interval '8 days' WHERE version <= 2`)
	if _, err := e.svc.Prune(context.Background(), e.site); err != nil {
		t.Fatal(err)
	}
	ag2 := e.dial(a)
	_, rest = handshake(t, ag2, 0, 0)
	if len(ofType(rest, protocol.TypeSnapshot)) != 1 || len(ofType(rest, protocol.TypeDelta)) != 0 {
		t.Fatalf("an agent older than the log: %v", types(rest))
	}
	ag2.Conn.Close()
	// ... while one at version 2 can still be served from what is left
	ag3 := e.dial(a)
	_, rest = handshake(t, ag3, 2, 0)
	if len(ofType(rest, protocol.TypeDelta)) != 1 {
		t.Fatalf("agent at 2: %v", types(rest))
	}
}
