package session_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/access/session"
)

// fake is a Backend whose answers the tests set.
type fake struct {
	mu        sync.Mutex
	site      uuid.UUID
	version   int64
	cfg       int64
	entries   []protocol.Entry
	deltas    []protocol.Delta
	deltaOK   bool
	commands  []session.OutCommand
	acks      []protocol.Ack
	events    [][]protocol.Event
	heartbeat int
	calls     []string
}

func (f *fake) log(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }

func (f *fake) Hello(_ context.Context, _ session.Ref, _ protocol.AgentHello, _ time.Time) (session.HelloInfo, error) {
	f.log("hello")
	return session.HelloInfo{Site: protocol.Site{ID: f.site.String(), Timezone: "Europe/Istanbul"}, SnapshotVersion: f.version, ConfigVersion: f.cfg}, nil
}

func (f *fake) Config(context.Context, session.Ref) (session.ConfigInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.cfg
	site := protocol.Site{ID: f.site.String(), Timezone: "Europe/Istanbul"}
	return session.ConfigInfo{Version: v, Command: protocol.Command{Command: protocol.CmdUpdateConfig, ConfigVersion: &v, Site: &site,
		Points: []protocol.PointConfig{}, Settings: &protocol.Settings{OfflineGraceHours: 72, PlateMatch: "exact", HeartbeatSeconds: 30}}}, nil
}

func (f *fake) SiteVersion(context.Context, session.Ref) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, nil
}

func (f *fake) Snapshot(context.Context, session.Ref) (session.SnapshotData, error) {
	f.log("snapshot")
	f.mu.Lock()
	defer f.mu.Unlock()
	return session.SnapshotData{Version: f.version, Timezone: "Europe/Istanbul", Entries: f.entries}, nil
}

func (f *fake) Deltas(_ context.Context, _ session.Ref, from int64) ([]protocol.Delta, bool, error) {
	f.log("deltas")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deltas, f.deltaOK, nil
}

func (f *fake) Heartbeat(context.Context, session.Ref, protocol.Heartbeat, time.Time) error {
	f.mu.Lock()
	f.heartbeat++
	f.mu.Unlock()
	return nil
}
func (f *fake) Touch(context.Context, session.Ref, time.Time) error { return nil }

func (f *fake) Events(_ context.Context, _ session.Ref, _ *int64, ev []protocol.Event, _ time.Time) (protocol.EventAckDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	d := protocol.EventAckDetail{Accepted: []string{}, Duplicates: []string{}, Rejected: []protocol.Rejection{}}
	for _, e := range ev {
		d.Accepted = append(d.Accepted, e.ID)
	}
	return d, nil
}

func (f *fake) PendingCommands(context.Context, session.Ref, time.Time) ([]session.OutCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.commands
	f.commands = nil
	return out, nil
}

func (f *fake) CommandAck(_ context.Context, _ session.Ref, _ uuid.UUID, a protocol.Ack, _ time.Time) error {
	f.mu.Lock()
	f.acks = append(f.acks, a)
	f.mu.Unlock()
	return nil
}

func (f *fake) ConfigApplied(context.Context, session.Ref, int64) error { return nil }

type rig struct {
	t   *testing.T
	f   *fake
	m   *session.Manager
	ref session.Ref
	now time.Time
}

func newRig(t *testing.T) *rig {
	f := &fake{site: uuid.New(), version: 3, cfg: 5, deltaOK: true}
	r := &rig{t: t, f: f, ref: session.Ref{OrgID: uuid.New(), SiteID: f.site, AgentID: uuid.New()}, now: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}
	r.m = session.NewManager(f, session.Options{Now: func() time.Time { return r.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return r
}

func (r *rig) hello(s *session.Session, snap, cfg int64) error {
	r.t.Helper()
	env, err := protocol.New(protocol.TypeHello, protocol.AgentHello{From: "agent", AgentID: r.ref.AgentID.String(), AgentVersion: "t", ProtocolVersions: []int{1},
		SnapshotVersion: snap, ConfigVersion: cfg, Drivers: []protocol.DriverState{}}, r.now)
	if err != nil {
		r.t.Fatal(err)
	}
	return s.Handle(context.Background(), env)
}

func kinds(msgs []protocol.Envelope) []string {
	var out []string
	for _, m := range msgs {
		k := m.Type
		if m.Type == protocol.TypeCommand {
			var c protocol.Command
			_ = json.Unmarshal(m.Payload, &c)
			k += ":" + c.Command
		}
		out = append(out, k)
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func entry(i int) protocol.Entry {
	return protocol.Entry{ID: uuid.NewString(), Kind: "plate", Value: "34ABC" + string(rune('0'+i%10)), Points: []string{uuid.NewString()}, ValidFrom: protocol.At(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
}

func TestHelloIsAnsweredThenConfigThenAllowList(t *testing.T) {
	r := newRig(t)
	r.f.deltas = []protocol.Delta{{FromVersion: 1, ToVersion: 3, Changes: []protocol.Change{{Op: "remove", CredentialID: uuid.NewString()}}}}
	s := r.m.Open(r.ref, session.KindWS)
	if err := r.hello(s, 1, 4); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"hello", "command:update_config", "delta"})
	hello := msgs[0]
	p, err := hello.Decode()
	if err != nil {
		t.Fatal(err)
	}
	sh := p.(*protocol.ServerHello)
	if sh.SnapshotVersion != 3 || sh.ConfigVersion != 5 || sh.HeartbeatSeconds != 30 || sh.Session == "" {
		t.Fatalf("%+v", sh)
	}
}

func TestEqualVersionsSendNothingAfterHello(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"hello"})
}

func TestBehindTheChangeLogGetsASnapshotInParts(t *testing.T) {
	r := newRig(t)
	r.f.deltaOK = false
	for i := 0; i < 4500; i++ {
		r.f.entries = append(r.f.entries, entry(i))
	}
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 0, 5)
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"hello", "snapshot", "snapshot", "snapshot"})
	total := 0
	for i, m := range msgs[1:] {
		p, err := m.Decode() // the strict decoder enforces <= 2000 entries per part
		if err != nil {
			t.Fatalf("part %d: %v", i+1, err)
		}
		sn := p.(*protocol.Snapshot)
		if sn.Part != i+1 || sn.Parts != 3 || sn.Version != 3 {
			t.Fatalf("part %d: %+v", i+1, sn)
		}
		total += len(sn.Entries)
	}
	if total != 4500 {
		t.Fatalf("entries across parts = %d", total)
	}
}

func TestAnEmptyAllowListIsStillOneSnapshotPart(t *testing.T) {
	r := newRig(t)
	r.f.deltaOK = false
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 99, 5) // the agent is ahead: a lower snapshot right after hello is legitimate
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"hello", "snapshot"})
}

func TestNoLowerSnapshotMidSessionWithoutCause(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	r.f.mu.Lock()
	r.f.version = 2 // the server's counter went backwards (a restored backup)
	r.f.mu.Unlock()
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := s.Drain(0); len(msgs) != 0 {
		t.Fatalf("sent %v", kinds(msgs))
	}
}

func TestNewVersionsGoOutAsDeltasOnTick(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	r.f.mu.Lock()
	r.f.version = 4
	r.f.deltas = []protocol.Delta{{FromVersion: 3, ToVersion: 4, Changes: []protocol.Change{{Op: "remove", CredentialID: uuid.NewString()}}}}
	r.f.mu.Unlock()
	_ = s.Tick(context.Background())
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"delta"})
	_ = s.Tick(context.Background()) // nothing new: nothing more
	if msgs, _ := s.Drain(0); len(msgs) != 0 {
		t.Fatalf("repeated %v", kinds(msgs))
	}
}

func TestGapAckAnswersWithASnapshot(t *testing.T) {
	r := newRig(t)
	r.f.deltas = []protocol.Delta{{FromVersion: 3, ToVersion: 4, Changes: []protocol.Change{{Op: "remove", CredentialID: uuid.NewString()}}}}
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	r.f.mu.Lock()
	r.f.version = 4
	r.f.entries = []protocol.Entry{entry(1)}
	r.f.mu.Unlock()
	_ = s.Tick(context.Background())
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"delta"})
	gap, _ := protocol.New(protocol.TypeAck, protocol.NewAck(msgs[0].ID, protocol.AckGap, map[string]any{"have_version": 1}), r.now)
	if err := s.Handle(context.Background(), gap); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.Drain(0)
	eq(t, kinds(msgs), []string{"snapshot"})
}

func TestCommandsAreDeliveredWithTheirIDAndAcksReachTheBackend(t *testing.T) {
	r := newRig(t)
	cid := uuid.New()
	issued, expires := protocol.At(r.now), protocol.At(r.now.Add(15*time.Second))
	r.f.commands = []session.OutCommand{{ID: cid, Command: protocol.Command{Command: protocol.CmdOpenPoint, PointID: uuid.NewString(), IssuedAt: &issued, ExpiresAt: &expires}}}
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"hello", "command:open_point"})
	if msgs[1].ID != cid.String() {
		t.Fatalf("the command's envelope id %s is not its id %s", msgs[1].ID, cid)
	}
	ack, _ := protocol.New(protocol.TypeAck, protocol.NewAck(cid.String(), protocol.AckFailed, map[string]any{"reason": "interrupted"}), r.now)
	if err := s.Handle(context.Background(), ack); err != nil {
		t.Fatal(err)
	}
	if len(r.f.acks) != 1 || r.f.acks[0].Status != protocol.AckFailed {
		t.Fatalf("acks: %+v", r.f.acks)
	}
}

func TestRequestSnapshotCommandIsFollowedByASnapshot(t *testing.T) {
	r := newRig(t)
	r.f.entries = []protocol.Entry{entry(1)}
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	r.f.commands = []session.OutCommand{{ID: uuid.New(), Command: protocol.Command{Command: protocol.CmdRequestSnapshot}}}
	_ = s.Tick(context.Background())
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"command:request_snapshot", "snapshot"})
}

func TestFirstMessageMustBeHello(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindPoll)
	hb, _ := protocol.New(protocol.TypeHeartbeat, protocol.Heartbeat{Drivers: []protocol.DriverState{}}, r.now)
	if err := s.Handle(context.Background(), hb); err != session.ErrNotHello {
		t.Fatalf("err = %v", err)
	}
	if cl, ok := s.Closing(); !ok || cl.Code != 1008 {
		t.Fatalf("closing = %+v %v", cl, ok)
	}
}

func TestHelloForAnotherAgentOrWithoutAVersionInCommonIsRefused(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	env, _ := protocol.New(protocol.TypeHello, protocol.AgentHello{From: "agent", AgentID: uuid.NewString(), AgentVersion: "t", ProtocolVersions: []int{1}, Drivers: []protocol.DriverState{}}, r.now)
	if err := s.Handle(context.Background(), env); err == nil {
		t.Fatal("a hello that names another agent must be refused")
	}
	if cl, _ := s.Closing(); cl.Code != 1008 {
		t.Fatalf("close code %d", cl.Code)
	}
	s2 := r.m.Open(r.ref, session.KindWS)
	env, _ = protocol.New(protocol.TypeHello, protocol.AgentHello{From: "agent", AgentID: r.ref.AgentID.String(), AgentVersion: "t", ProtocolVersions: []int{7, 8}, Drivers: []protocol.DriverState{}}, r.now)
	if err := s2.Handle(context.Background(), env); err == nil {
		t.Fatal("no common version must end the session")
	}
	if cl, _ := s2.Closing(); cl.Code != protocol.CloseNoCommonVersion {
		t.Fatalf("close code %d, want 4426", cl.Code)
	}
}

func TestUnknownTypeIsAnsweredUnsupported(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	env := protocol.Envelope{V: 1, Type: "teleport", ID: protocol.NewID(), TS: protocol.At(r.now), Payload: json.RawMessage(`{}`)}
	if err := s.Handle(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"ack"})
	p, _ := msgs[0].Decode()
	if a := p.(*protocol.Ack); a.Status != protocol.AckUnsupported || a.Ref != env.ID {
		t.Fatalf("%+v", a)
	}
}

func TestEventBatchWithABadEventRejectsOnlyThatOne(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	good := protocol.Event{ID: protocol.NewID(), PointID: uuid.NewString(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(r.now)}
	bad := good
	bad.ID = protocol.NewID()
	bad.Decision = "teleport"
	env, _ := protocol.New(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{good, bad}}, r.now)
	if err := s.Handle(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Drain(0)
	p, err := msgs[0].Decode()
	if err != nil {
		t.Fatal(err)
	}
	var d protocol.EventAckDetail
	_ = json.Unmarshal(p.(*protocol.Ack).Detail, &d)
	if len(d.Accepted) != 1 || d.Accepted[0] != good.ID || len(d.Rejected) != 1 || d.Rejected[0].ID != bad.ID || d.Rejected[0].Code != protocol.RejectInvalid {
		t.Fatalf("%+v", d)
	}
	if len(r.f.events) != 1 || len(r.f.events[0]) != 1 {
		t.Fatalf("the backend saw %v", r.f.events)
	}
}

func TestHeartbeatIsAcknowledged(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	hb, _ := protocol.New(protocol.TypeHeartbeat, protocol.Heartbeat{Drivers: []protocol.DriverState{}}, r.now)
	_ = s.Handle(context.Background(), hb)
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"ack"})
	p, _ := msgs[0].Decode()
	if a := p.(*protocol.Ack); a.Ref != hb.ID || a.Status != protocol.AckOK {
		t.Fatalf("%+v", a)
	}
	if r.f.heartbeat != 1 {
		t.Fatalf("heartbeats recorded: %d", r.f.heartbeat)
	}
}

func TestOneLiveSessionPerAgent(t *testing.T) {
	r := newRig(t)
	first := r.m.Open(r.ref, session.KindWS)
	second := r.m.Open(r.ref, session.KindWS)
	if cl, ok := first.Closing(); !ok || cl.Code != protocol.CloseReplaced {
		t.Fatalf("the replaced session is closed with 4409, got %+v %v", cl, ok)
	}
	if _, ok := second.Closing(); ok || r.m.Current(r.ref.AgentID) != second {
		t.Fatal("the newer session is the current one")
	}
	r.m.Remove(first) // removing a replaced session must not drop the live one
	if r.m.Current(r.ref.AgentID) != second {
		t.Fatal("Remove(first) dropped the live session")
	}
}

func TestRevokeSendsTheCommandThenClosesWith4401(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	_ = r.hello(s, 3, 5)
	s.Drain(0)
	r.m.Revoke(r.ref.AgentID)
	msgs, _ := s.Drain(0)
	eq(t, kinds(msgs), []string{"command:revoked"})
	if cl, ok := s.Closing(); !ok || cl.Code != protocol.CloseRevoked {
		t.Fatalf("%+v %v", cl, ok)
	}
}

func TestDrainSequenceCountsOnlyResponsesWithMessages(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindPoll)
	if _, seq := s.Drain(0); seq != 1 {
		t.Fatalf("an empty first response repeats 1, got %d", seq)
	}
	_ = r.hello(s, 3, 5)
	_, seq := s.Drain(0)
	if seq != 1 {
		t.Fatalf("first response with messages is 1, got %d", seq)
	}
	if _, seq := s.Drain(0); seq != 1 {
		t.Fatalf("an empty response must not consume a number, got %d", seq)
	}
	hb, _ := protocol.New(protocol.TypeHeartbeat, protocol.Heartbeat{Drivers: []protocol.DriverState{}}, r.now)
	_ = s.Handle(context.Background(), hb)
	if _, seq := s.Drain(0); seq != 2 {
		t.Fatalf("second response with messages is 2, got %d", seq)
	}
}

func TestIdlePollSessionsAreSwept(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindPoll)
	if !r.m.Connected(r.ref.AgentID) {
		t.Fatal("a fresh poll session is connected")
	}
	r.now = r.now.Add(61 * time.Second)
	if r.m.Connected(r.ref.AgentID) {
		t.Fatal("a poll session silent for 60 s is not connected")
	}
	r.m.Sweep()
	if r.m.Current(r.ref.AgentID) != nil || r.m.Sessions() != 0 {
		t.Fatal("the idle session must be removed")
	}
	_ = s
}

func TestWakeChannelFiresOnEnqueue(t *testing.T) {
	r := newRig(t)
	s := r.m.Open(r.ref, session.KindWS)
	ch := s.Wake()
	select {
	case <-ch:
		t.Fatal("woke without cause")
	default:
	}
	r.m.NotifySite(r.ref.SiteID)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("a site notification must wake the session")
	}
}
