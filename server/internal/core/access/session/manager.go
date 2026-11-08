package session

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

// Manager keeps the live sessions: one per agent. A new connection of an
// agent replaces its previous one, which is closed with 4409 (design 4.8).
type Manager struct {
	b   Backend
	opt Options

	mu      sync.Mutex
	byAgent map[uuid.UUID]*Session
}

// NewManager returns a Manager on b.
func NewManager(b Backend, opt Options) *Manager {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.IdlePoll == 0 {
		opt.IdlePoll = 60 * time.Second
	}
	if opt.SyncEvery == 0 {
		opt.SyncEvery = 2 * time.Second
	}
	if opt.TouchEvery == 0 {
		opt.TouchEvery = 10 * time.Second
	}
	if opt.StatusEvery == 0 {
		opt.StatusEvery = 30 * time.Second
	}
	if opt.SnapshotPartEntries <= 0 || opt.SnapshotPartEntries > protocol.MaxSnapshotEntries {
		opt.SnapshotPartEntries = protocol.MaxSnapshotEntries // the agent protocol's own limit per part
	}
	if opt.SnapshotPartBytes <= 0 {
		opt.SnapshotPartBytes = DefaultPartBytes
	}
	return &Manager{b: b, opt: opt, byAgent: map[uuid.UUID]*Session{}}
}

// SyncEvery is how often a session should look for new work by itself.
func (m *Manager) SyncEvery() time.Duration { return m.opt.SyncEvery }

// Open registers a new session of the agent and closes the one it replaces.
// The session is not usable until its hello has been handled.
func (m *Manager) Open(ref Ref, kind string) *Session {
	s := newSession(m, ref, kind)
	m.mu.Lock()
	old := m.byAgent[ref.AgentID]
	m.byAgent[ref.AgentID] = s
	m.mu.Unlock()
	if old != nil {
		old.CloseWith(protocol.CloseReplaced, "replaced by a newer session")
	}
	return s
}

// Current returns the live session of an agent, if any.
func (m *Manager) Current(agentID uuid.UUID) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byAgent[agentID]
}

// Remove forgets s (only if it is still the agent's current session).
func (m *Manager) Remove(s *Session) {
	m.mu.Lock()
	if m.byAgent[s.Ref.AgentID] == s {
		delete(m.byAgent, s.Ref.AgentID)
	}
	m.mu.Unlock()
}

// Connected reports whether the agent has a live session on this process.
func (m *Manager) Connected(agentID uuid.UUID) bool {
	s := m.Current(agentID)
	if s == nil {
		return false
	}
	if _, closing := s.Closing(); closing {
		return false
	}
	if s.Kind == KindPoll {
		return s.idleFor() < m.opt.IdlePoll
	}
	return true
}

// NotifySite wakes the sessions of a site: the allow-list, the configuration
// or a command changed.
func (m *Manager) NotifySite(siteID uuid.UUID) {
	for _, s := range m.snapshot() {
		if s.Ref.SiteID == siteID {
			s.Signal()
		}
	}
}

// NotifyAgent wakes the session of one agent (a command was queued for it).
func (m *Manager) NotifyAgent(agentID uuid.UUID) {
	if s := m.Current(agentID); s != nil {
		s.Signal()
	}
}

// Revoke tells a connected agent it is revoked (command revoked) and closes
// its session with 4401.
func (m *Manager) Revoke(agentID uuid.UUID) {
	s := m.Current(agentID)
	if s == nil {
		return
	}
	if env, err := protocol.New(protocol.TypeCommand, protocol.Command{Command: protocol.CmdRevoked}, m.opt.Now()); err == nil {
		s.enqueue(env)
	}
	s.CloseWith(protocol.CloseRevoked, "revoked")
}

func (m *Manager) snapshot() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.byAgent))
	for _, s := range m.byAgent {
		out = append(out, s)
	}
	return out
}

// Sessions is the number of live sessions (tests, metrics).
func (m *Manager) Sessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byAgent)
}

// Run drops long-poll sessions that have been silent for IdlePoll, until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Sweep()
		}
	}
}

// Sweep removes idle long-poll sessions.
func (m *Manager) Sweep() {
	for _, s := range m.snapshot() {
		if s.Kind == KindPoll && s.idleFor() > m.opt.IdlePoll {
			m.Remove(s)
		}
	}
}
