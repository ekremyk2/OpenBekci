// Package session is the server side of one agent connection, independent of
// the transport: the WebSocket handler and the long-poll handler both feed
// inbound envelopes to Session.Handle and drain what the session wants to
// send with Session.Drain, so the hello exchange, the allow-list
// synchronisation, event ingest and command delivery are one implementation.
// Manager keeps one live session per agent (a second connection replaces the
// first, closing it with 4409).
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

// Ref identifies the agent a session belongs to. It comes from the
// authenticated token, never from the agent's own messages.
type Ref struct {
	OrgID, SiteID, AgentID uuid.UUID
}

// HelloInfo is what the backend tells the server hello.
type HelloInfo struct {
	Site             protocol.Site
	SnapshotVersion  int64
	ConfigVersion    int64
	RotateBy         *time.Time
	RotateNow        bool
	HeartbeatSeconds int
}

// ConfigInfo is the current update_config of the site's agent.
type ConfigInfo struct {
	Version int64
	Command protocol.Command
}

// SnapshotData is the complete allow-list at Version.
type SnapshotData struct {
	Version  int64
	Timezone string
	Entries  []protocol.Entry
}

// OutCommand is a persisted command to deliver; ID is its envelope id.
type OutCommand struct {
	ID      uuid.UUID
	Command protocol.Command
}

// Backend is what the session needs from the module.
type Backend interface {
	// Hello records the agent's hello (versions, drivers, last_seen, clock) and
	// returns the facts of the server hello.
	Hello(ctx context.Context, ref Ref, h protocol.AgentHello, now time.Time) (HelloInfo, error)
	// Config returns the current update_config (bumping the config version when
	// the site's configuration changed since the last call).
	Config(ctx context.Context, ref Ref) (ConfigInfo, error)
	// SiteVersion is the current allow-list version of the site.
	SiteVersion(ctx context.Context, ref Ref) (int64, error)
	// Snapshot is the complete allow-list at one consistent version.
	Snapshot(ctx context.Context, ref Ref) (SnapshotData, error)
	// Deltas returns the changes after version from up to the current version as
	// gap-free chunks of at most 500 changes; ok is false when the change log no
	// longer reaches back to from (a snapshot is needed).
	Deltas(ctx context.Context, ref Ref, from int64) (deltas []protocol.Delta, ok bool, err error)
	// Heartbeat records a heartbeat (last_seen, flags, drivers, clock offset).
	Heartbeat(ctx context.Context, ref Ref, h protocol.Heartbeat, now time.Time) error
	// Touch records contact (last_seen) without anything else.
	Touch(ctx context.Context, ref Ref, now time.Time) error
	// Events ingests the valid events of a batch and reports each one.
	Events(ctx context.Context, ref Ref, clockOffsetMS *int64, events []protocol.Event, now time.Time) (protocol.EventAckDetail, error)
	// PendingCommands returns the commands to deliver now (and marks them
	// delivered); commands past their expiry are marked expired instead.
	PendingCommands(ctx context.Context, ref Ref, now time.Time) ([]OutCommand, error)
	// CommandAck records the agent's answer to a command (envelope id = command id).
	CommandAck(ctx context.Context, ref Ref, id uuid.UUID, a protocol.Ack, now time.Time) error
	// ConfigApplied records that the agent acknowledged update_config version v.
	ConfigApplied(ctx context.Context, ref Ref, v int64) error
}

// Options configures a Manager.
type Options struct {
	Now func() time.Time
	Log *slog.Logger
	// IdlePoll is how long a long-poll session may stay silent (default 60 s).
	IdlePoll time.Duration
	// SyncEvery is how often a session looks for new work on its own (default 2 s).
	SyncEvery time.Duration
	// TouchEvery bounds last_seen writes for ordinary frames (default 10 s).
	TouchEvery time.Duration
}

// Kinds of transport.
const (
	KindWS   = "ws"
	KindPoll = "poll"
)

// Close is why and with which WebSocket code a session ended.
type Close struct {
	Code   int
	Reason string
}

// Session is one agent connection.
type Session struct {
	m      *Manager
	Ref    Ref
	Kind   string
	handle string // the session handle (long-poll)

	// procMu serialises message handling and synchronisation (two poll
	// requests may be in flight) and guards the protocol state below.
	procMu     sync.Mutex
	helloed    bool
	haveSnap   int64 // version the agent said it has, then what we last sent
	sentSnap   int64
	sentCfg    int64
	forceSnap  bool
	allowLower bool
	lastTouch  time.Time
	sent       map[string]string // envelope id -> kind of message, to interpret acks
	cfgOf      map[string]int64  // update_config envelope id -> config version
	snapParts  map[int64]int     // version -> parts sent

	// qmu guards the outbound queue.
	qmu      sync.Mutex
	out      []protocol.Envelope
	wake     chan struct{}
	closing  *Close
	seq      int64
	lastSeen time.Time
}

// ErrNotHello is returned by Handle for a first message that is not a hello.
var ErrNotHello = errors.New("session: the first message must be hello")

func newSession(m *Manager, ref Ref, kind string) *Session {
	return &Session{
		m: m, Ref: ref, Kind: kind, handle: protocol.NewID(),
		wake: make(chan struct{}), lastSeen: m.opt.Now(),
		sent: map[string]string{}, cfgOf: map[string]int64{}, snapParts: map[int64]int{},
	}
}

// SessionHandle is the handle of a long-poll session.
func (s *Session) SessionHandle() string { return s.handle }

// enqueue adds envelopes to the outbound queue and wakes the writers.
func (s *Session) enqueue(envs ...protocol.Envelope) {
	if len(envs) == 0 {
		return
	}
	s.qmu.Lock()
	s.out = append(s.out, envs...)
	s.signalLocked()
	s.qmu.Unlock()
}

func (s *Session) signalLocked() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// Signal wakes whoever waits for output or closing (also used by the manager).
func (s *Session) Signal() {
	s.qmu.Lock()
	s.signalLocked()
	s.qmu.Unlock()
}

// Wake returns a channel that is closed at the next enqueue, close or signal.
func (s *Session) Wake() <-chan struct{} {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return s.wake
}

// Drain returns the queued envelopes (at most max, 0 = all) and, for the
// long-poll transport, the response sequence number: it counts the responses
// that carry messages from 1; an empty drain repeats the last number.
func (s *Session) Drain(max int) (msgs []protocol.Envelope, seq int64) {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	n := len(s.out)
	if max > 0 && n > max {
		n = max
	}
	msgs = s.out[:n:n]
	s.out = append([]protocol.Envelope(nil), s.out[n:]...)
	if len(msgs) > 0 {
		s.seq++
	}
	s.lastSeen = s.m.opt.Now()
	return msgs, max0(s.seq, 1)
}

func max0(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Pending reports whether output is queued.
func (s *Session) Pending() bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return len(s.out) > 0
}

// Closing returns the reason the session is ending, once there is one. The
// transport sends what is still queued, then closes with the code.
func (s *Session) Closing() (Close, bool) {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if s.closing == nil {
		return Close{}, false
	}
	return *s.closing, true
}

// CloseWith ends the session after its queue is flushed. The first reason wins.
func (s *Session) CloseWith(code int, reason string) {
	s.qmu.Lock()
	if s.closing == nil {
		s.closing = &Close{Code: code, Reason: reason}
	}
	s.signalLocked()
	s.qmu.Unlock()
}

// Touched records that the transport saw the agent (poll request or any frame).
func (s *Session) Touched() {
	s.qmu.Lock()
	s.lastSeen = s.m.opt.Now()
	s.qmu.Unlock()
}

func (s *Session) idleFor() time.Duration {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return s.m.opt.Now().Sub(s.lastSeen)
}

func (s *Session) ack(ref, status string, detail any) {
	env, err := protocol.New(protocol.TypeAck, protocol.NewAck(ref, status, detail), s.m.opt.Now())
	if err == nil {
		s.enqueue(env)
	}
}

func (s *Session) remember(id, kind string) {
	if len(s.sent) > 512 {
		s.sent, s.cfgOf = map[string]string{}, map[string]int64{}
	}
	s.sent[id] = kind
}

// Handle processes one inbound envelope. A returned error ends the session
// (the session is already marked closing); a protocol-level problem with one
// message is answered with an ack and is not an error.
func (s *Session) Handle(ctx context.Context, env protocol.Envelope) error {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	s.Touched()
	now := s.m.opt.Now()
	if err := env.Validate(); err != nil {
		s.ack(protocolRef(env), protocol.AckError, map[string]any{"reason": "invalid_envelope"})
		return nil
	}
	if !s.helloed && env.Type != protocol.TypeHello {
		s.CloseWith(1008, "hello first")
		return ErrNotHello
	}
	if s.helloed && env.Type != protocol.TypeHello {
		s.touch(ctx, now)
	}
	switch env.Type {
	case protocol.TypeHello:
		return s.onHello(ctx, env)
	case protocol.TypeHeartbeat:
		return s.onHeartbeat(ctx, env, now)
	case protocol.TypeEvent:
		return s.onEvent(ctx, env, now)
	case protocol.TypeAck:
		return s.onAck(ctx, env, now)
	case protocol.TypeSnapshot, protocol.TypeDelta, protocol.TypeCommand:
		// server-to-agent types from the agent: not valid here
		s.ack(env.ID, protocol.AckError, map[string]any{"reason": "wrong_direction"})
		return nil
	default:
		s.ack(env.ID, protocol.AckUnsupported, map[string]any{"type": env.Type})
		return nil
	}
}

func protocolRef(env protocol.Envelope) string {
	if protocol.IsUUID(env.ID) {
		return env.ID
	}
	return uuid.Nil.String()
}

// touch writes last_seen at most once per TouchEvery.
func (s *Session) touch(ctx context.Context, now time.Time) {
	if now.Sub(s.lastTouch) < s.m.opt.TouchEvery {
		return
	}
	s.lastTouch = now
	if err := s.m.b.Touch(ctx, s.Ref, now); err != nil {
		s.m.opt.Log.Warn("agent touch failed", "agent", s.Ref.AgentID, "error", err)
	}
}

func (s *Session) onHello(ctx context.Context, env protocol.Envelope) error {
	p, err := env.Decode()
	h, ok := p.(*protocol.AgentHello)
	if err != nil || !ok {
		s.ack(env.ID, protocol.AckError, map[string]any{"reason": "invalid_hello"})
		s.CloseWith(1002, "invalid hello")
		return fmt.Errorf("session: invalid hello: %v", err)
	}
	if h.AgentID != s.Ref.AgentID.String() {
		s.CloseWith(1008, "agent id does not match the token")
		return errors.New("session: hello names another agent")
	}
	common := false
	for _, v := range h.ProtocolVersions {
		if v == protocol.Version {
			common = true
		}
	}
	if !common {
		s.CloseWith(protocol.CloseNoCommonVersion, "no common protocol version")
		return errors.New("session: no common protocol version")
	}
	now := s.m.opt.Now()
	info, err := s.m.b.Hello(ctx, s.Ref, *h, now)
	if err != nil {
		s.CloseWith(1011, "hello failed")
		return fmt.Errorf("session: hello: %w", err)
	}
	hb := info.HeartbeatSeconds
	if hb == 0 {
		hb = 30
	}
	reply := protocol.ServerHello{
		From: "server", ProtocolVersion: protocol.Version, ServerTime: protocol.At(now), HeartbeatSeconds: hb,
		ConfigVersion: info.ConfigVersion, RotateNow: info.RotateNow, Site: info.Site, SnapshotVersion: info.SnapshotVersion,
		Session: s.handle,
	}
	if info.RotateBy != nil {
		t := protocol.At(*info.RotateBy)
		reply.RotateBy = &t
	}
	out, err := protocol.NewWithID(protocol.NewID(), protocol.TypeHello, reply, now)
	if err != nil {
		return err
	}
	s.helloed = true
	s.lastTouch = now
	s.haveSnap, s.sentSnap = h.SnapshotVersion, h.SnapshotVersion
	s.sentCfg = h.ConfigVersion
	s.forceSnap = false
	s.allowLower = true // a lower snapshot is legitimate right after hello
	s.enqueue(out)
	// update_config, then the allow-list, then pending commands (design 4.6)
	err = s.syncLocked(ctx, now)
	s.allowLower = false
	return err
}

func (s *Session) onHeartbeat(ctx context.Context, env protocol.Envelope, now time.Time) error {
	p, err := env.Decode()
	hb, ok := p.(*protocol.Heartbeat)
	if err != nil || !ok {
		s.ack(env.ID, protocol.AckError, map[string]any{"reason": "invalid_heartbeat"})
		return nil
	}
	if err := s.m.b.Heartbeat(ctx, s.Ref, *hb, now); err != nil {
		s.m.opt.Log.Warn("heartbeat not recorded", "agent", s.Ref.AgentID, "error", err)
	}
	s.lastTouch = now
	// A heartbeat is answered: it is the contact the agent's grace clock runs on.
	s.ack(env.ID, protocol.AckOK, nil)
	return nil
}

func (s *Session) onEvent(ctx context.Context, env protocol.Envelope, now time.Time) error {
	p, err := env.DecodeLoose()
	batch, ok := p.(*protocol.EventBatch)
	if err != nil || !ok || len(batch.Events) < 1 || len(batch.Events) > protocol.MaxEventBatch {
		s.ack(env.ID, protocol.AckError, map[string]any{"reason": "invalid_batch"})
		return nil
	}
	var valid []protocol.Event
	var rejected []protocol.Rejection
	for _, e := range batch.Events {
		if e.Validate() != nil {
			if protocol.IsUUID(e.ID) {
				rejected = append(rejected, protocol.Rejection{ID: e.ID, Code: protocol.RejectInvalid})
			}
			continue
		}
		valid = append(valid, e)
	}
	detail := protocol.EventAckDetail{Accepted: []string{}, Duplicates: []string{}, Rejected: []protocol.Rejection{}}
	if len(valid) > 0 {
		d, err := s.m.b.Events(ctx, s.Ref, batch.ClockOffsetMS, valid, now)
		if err != nil {
			// not final: the agent keeps the batch and retries
			s.m.opt.Log.Warn("event batch failed", "agent", s.Ref.AgentID, "error", err)
			s.ack(env.ID, protocol.AckError, map[string]any{"reason": "unavailable"})
			return nil
		}
		detail = d
	}
	detail.Rejected = append(detail.Rejected, rejected...)
	s.ack(env.ID, protocol.AckOK, detail)
	return nil
}

func (s *Session) onAck(ctx context.Context, env protocol.Envelope, now time.Time) error {
	p, err := env.Decode()
	a, ok := p.(*protocol.Ack)
	if err != nil || !ok {
		return nil // acks are never answered
	}
	kind := s.sent[a.Ref]
	switch kind {
	case "delta":
		if a.Status == protocol.AckGap {
			var d struct {
				Have int64 `json:"have_version"`
			}
			_ = json.Unmarshal(a.Detail, &d)
			s.sentSnap, s.forceSnap, s.allowLower = d.Have, true, true
			return s.syncLocked(ctx, now)
		}
	case "snapshot":
		switch a.Status {
		case protocol.AckStale:
			s.m.opt.Log.Warn("agent refused a snapshot as stale", "agent", s.Ref.AgentID)
		case protocol.AckOK:
			var d struct {
				Version int64 `json:"version"`
				Applied bool  `json:"applied"`
			}
			_ = json.Unmarshal(a.Detail, &d)
			if d.Applied {
				s.haveSnap = d.Version
				delete(s.snapParts, d.Version)
			}
		}
	case "config":
		if v, ok := s.cfgOf[a.Ref]; ok && a.Status == protocol.AckOK {
			_ = s.m.b.ConfigApplied(ctx, s.Ref, v)
		}
	default:
		if id, err := uuid.Parse(a.Ref); err == nil {
			if err := s.m.b.CommandAck(ctx, s.Ref, id, *a, now); err != nil {
				s.m.opt.Log.Warn("command ack not recorded", "agent", s.Ref.AgentID, "error", err)
			}
		}
	}
	return nil
}

// Tick looks for new work (configuration, allow-list versions, commands) and
// queues it. The transports call it on every wake-up and every SyncEvery.
func (s *Session) Tick(ctx context.Context) error {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	if !s.helloed {
		return nil
	}
	return s.syncLocked(ctx, s.m.opt.Now())
}

func (s *Session) syncLocked(ctx context.Context, now time.Time) error {
	b := s.m.b
	cfg, err := b.Config(ctx, s.Ref)
	if err != nil {
		return err
	}
	if cfg.Version != s.sentCfg {
		env, err := protocol.NewWithID(protocol.NewID(), protocol.TypeCommand, cfg.Command, now)
		if err != nil {
			return err
		}
		s.remember(env.ID, "config")
		s.cfgOf[env.ID] = cfg.Version
		s.sentCfg = cfg.Version
		s.enqueue(env)
	}
	if err := s.syncAllowlist(ctx, now); err != nil {
		return err
	}
	cmds, err := b.PendingCommands(ctx, s.Ref, now)
	if err != nil {
		return err
	}
	for _, c := range cmds {
		env, err := protocol.NewWithID(c.ID.String(), protocol.TypeCommand, c.Command, now)
		if err != nil {
			return err
		}
		s.enqueue(env)
		if c.Command.Command == protocol.CmdRequestSnapshot {
			s.forceSnap, s.allowLower = true, true
		}
	}
	if s.forceSnap { // a request_snapshot command was just delivered
		return s.syncAllowlist(ctx, now)
	}
	return nil
}

func (s *Session) syncAllowlist(ctx context.Context, now time.Time) error {
	b := s.m.b
	cur, err := b.SiteVersion(ctx, s.Ref)
	if err != nil {
		return err
	}
	if !s.forceSnap && cur == s.sentSnap {
		return nil
	}
	if !s.forceSnap && cur > s.sentSnap {
		deltas, ok, err := b.Deltas(ctx, s.Ref, s.sentSnap)
		if err != nil {
			return err
		}
		if ok {
			for _, d := range deltas {
				env, err := protocol.NewWithID(protocol.NewID(), protocol.TypeDelta, d, now)
				if err != nil {
					return err
				}
				s.remember(env.ID, "delta")
				s.enqueue(env)
				s.sentSnap = d.ToVersion
			}
			return nil
		}
	}
	// a snapshot: asked for, after a gap, behind the change log, or the agent is ahead of us
	snap, err := b.Snapshot(ctx, s.Ref)
	if err != nil {
		return err
	}
	if snap.Version < s.sentSnap && !s.allowLower {
		// The agent is ahead of this server without having said so in a hello:
		// a lower snapshot would be refused as stale. Wait for its next hello.
		s.m.opt.Log.Warn("allow-list version went backwards; not sending a lower snapshot", "agent", s.Ref.AgentID)
		return nil
	}
	s.allowLower = false
	parts := splitEntries(snap.Entries, protocol.MaxSnapshotEntries)
	for i, entries := range parts {
		env, err := protocol.NewWithID(protocol.NewID(), protocol.TypeSnapshot, protocol.Snapshot{
			Version: snap.Version, Part: i + 1, Parts: len(parts), Site: protocol.Site{Timezone: snap.Timezone}, Entries: entries,
		}, now)
		if err != nil {
			return err
		}
		s.remember(env.ID, "snapshot")
		s.enqueue(env)
	}
	s.snapParts[snap.Version] = len(parts)
	s.sentSnap, s.forceSnap = snap.Version, false
	return nil
}

// maxPartBytes keeps one snapshot frame well under the 1 MiB frame limit.
const maxPartBytes = 900_000

// splitEntries cuts entries into parts of at most n entries and about
// maxPartBytes of JSON (at least one part, even for an empty list).
func splitEntries(entries []protocol.Entry, n int) [][]protocol.Entry {
	if len(entries) == 0 {
		return [][]protocol.Entry{{}}
	}
	var parts [][]protocol.Entry
	start, size := 0, 0
	for i, e := range entries {
		b, _ := json.Marshal(e)
		if i > start && (i-start >= n || size+len(b) > maxPartBytes) {
			parts = append(parts, entries[start:i:i])
			start, size = i, 0
		}
		size += len(b) + 1
	}
	return append(parts, entries[start:len(entries):len(entries)])
}
