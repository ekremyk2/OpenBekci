// Package accesstest is a fake agent for tests: an in-process WebSocket and
// long-poll client that speaks protocol v1, and a validator that holds every
// message the server sends to the served JSON schemas of api/agent-protocol/v1.
// Tests only.
package accesstest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

var (
	schemaOnce sync.Once
	schemas    map[string]*jsonschema.Schema
	schemaErr  error
)

func findSchemaDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, "api", "agent-protocol", "v1")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p, nil
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("api/agent-protocol/v1 not found above the working directory")
}

func loadSchemas() {
	dir, err := findSchemaDir()
	if err != nil {
		schemaErr = err
		return
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	paths, _ := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // test schemas found by walking up from the working directory
		if err != nil {
			schemaErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			schemaErr = err
			return
		}
		var head struct {
			ID string `json:"$id"`
		}
		_ = json.Unmarshal(data, &head)
		if err := c.AddResource(head.ID, doc); err != nil {
			schemaErr = err
			return
		}
	}
	schemas = map[string]*jsonschema.Schema{}
	for _, name := range []string{"envelope", "hello-agent", "hello-server", "snapshot", "delta", "event", "ack", "command", "heartbeat"} {
		s, err := c.Compile("https://openbekci.org/agent-protocol/v1/" + name + ".schema.json")
		if err != nil {
			schemaErr = err
			return
		}
		schemas[name] = s
	}
}

// Validate holds one frame to the envelope schema and its payload schema.
func Validate(data []byte) error {
	schemaOnce.Do(loadSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := schemas["envelope"].Validate(doc); err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	name := env.Type
	if env.Type == protocol.TypeHello {
		var probe struct {
			From string `json:"from"`
		}
		_ = json.Unmarshal(env.Payload, &probe)
		name = "hello-" + probe.From
	}
	s, ok := schemas[name]
	if !ok {
		return fmt.Errorf("no schema for %q", name)
	}
	payload, err := jsonschema.UnmarshalJSON(bytes.NewReader(env.Payload))
	if err != nil {
		return err
	}
	if err := s.Validate(payload); err != nil {
		return fmt.Errorf("%s payload: %w", name, err)
	}
	// and the typed decoder the agent uses must agree
	if _, err := env.Decode(); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// Conn is what a fake agent needs from a transport.
type Conn interface {
	Send(env protocol.Envelope) error
	Recv(timeout time.Duration) (protocol.Envelope, error)
	Close()
}

// Agent drives a transport like the real agent does.
type Agent struct {
	T       testing.TB
	ID      string
	Conn    Conn
	Offset  *int64
	nextTS  func() time.Time
	Backlog []protocol.Envelope
}

// NewAgent wraps a transport.
func NewAgent(t testing.TB, id string, c Conn) *Agent {
	return &Agent{T: t, ID: id, Conn: c, nextTS: time.Now}
}

// Send builds and sends a message of type typ.
func (a *Agent) Send(typ string, payload any) protocol.Envelope {
	a.T.Helper()
	env, err := protocol.New(typ, payload, a.nextTS())
	if err != nil {
		a.T.Fatal(err)
	}
	if err := a.Conn.Send(env); err != nil {
		a.T.Fatalf("send %s: %v", typ, err)
	}
	return env
}

// Hello sends the agent's hello.
func (a *Agent) Hello(snapshotVersion, configVersion int64) protocol.Envelope {
	a.T.Helper()
	return a.Send(protocol.TypeHello, protocol.AgentHello{
		From: "agent", AgentID: a.ID, AgentVersion: "0.3.0-test", Platform: "test/fake", ProtocolVersions: []int{1},
		SnapshotVersion: snapshotVersion, ConfigVersion: configVersion, Drivers: []protocol.DriverState{{Instance: "gate-relay", State: "ok"}},
		ClockOffsetMS: a.Offset,
	})
}

// Recv waits for the next server message (already validated against the schemas).
func (a *Agent) Recv(timeout time.Duration) protocol.Envelope {
	a.T.Helper()
	env, err := a.Conn.Recv(timeout)
	if err != nil {
		a.T.Fatalf("recv: %v", err)
	}
	return env
}

// TryRecv is Recv that reports false on timeout instead of failing.
func (a *Agent) TryRecv(timeout time.Duration) (protocol.Envelope, bool) {
	a.T.Helper()
	env, err := a.Conn.Recv(timeout)
	if err != nil {
		return protocol.Envelope{}, false
	}
	return env, true
}

// RecvType waits for the next message and requires its type (and, for hello, nothing more).
func (a *Agent) RecvType(typ string, timeout time.Duration) protocol.Envelope {
	a.T.Helper()
	env := a.Recv(timeout)
	if env.Type != typ {
		a.T.Fatalf("got a %s message, want %s: %s", env.Type, typ, env.Payload)
	}
	return env
}

// RecvCommand waits for a command message and returns its decoded payload.
func (a *Agent) RecvCommand(timeout time.Duration) (protocol.Envelope, protocol.Command) {
	a.T.Helper()
	env := a.RecvType(protocol.TypeCommand, timeout)
	p, err := env.Decode()
	if err != nil {
		a.T.Fatal(err)
	}
	return env, *p.(*protocol.Command)
}

// Ack answers a message.
func (a *Agent) Ack(ref, status string, detail any) {
	a.T.Helper()
	a.Send(protocol.TypeAck, protocol.NewAck(ref, status, detail))
}

// ---- WebSocket ----------------------------------------------------------------

type wsConn struct {
	t    testing.TB
	c    *websocket.Conn
	ctx  context.Context
	stop context.CancelFunc
	in   chan protocol.Envelope
	err  chan error
}

// DialWS connects like the agent: subprotocol openbekci.agent.v1 and the bearer token.
func DialWS(t testing.TB, url, token string) (*Agent, *websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Subprotocols: []string{protocol.Subprotocol},
		HTTPHeader:   http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		cancel()
		return nil, nil, resp, err
	}
	c.SetReadLimit(protocol.MaxFrameBytes)
	w := &wsConn{t: t, c: c, ctx: ctx, stop: cancel, in: make(chan protocol.Envelope, 256), err: make(chan error, 1)}
	go w.read()
	t.Cleanup(func() { w.Close() })
	return NewAgent(t, "", w), c, resp, nil
}

func (w *wsConn) read() {
	for {
		_, data, err := w.c.Read(w.ctx)
		if err != nil {
			w.err <- err
			return
		}
		if err := Validate(data); err != nil {
			w.err <- fmt.Errorf("server message does not validate: %w: %s", err, data)
			return
		}
		env, err := protocol.ParseEnvelope(data)
		if err != nil {
			w.err <- err
			return
		}
		w.in <- env
	}
}

func (w *wsConn) Send(env protocol.Envelope) error {
	data, err := env.MarshalJSON()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
	defer cancel()
	return w.c.Write(ctx, websocket.MessageText, data)
}

func (w *wsConn) Recv(timeout time.Duration) (protocol.Envelope, error) {
	select {
	case env := <-w.in:
		return env, nil
	default:
	}
	select {
	case env := <-w.in:
		return env, nil
	case err := <-w.err:
		return protocol.Envelope{}, err
	case <-time.After(timeout):
		return protocol.Envelope{}, fmt.Errorf("timed out after %s", timeout)
	}
}

func (w *wsConn) Close() {
	w.stop()
	_ = w.c.CloseNow()
}

// ---- long-poll ----------------------------------------------------------------

// Poll is a long-poll client. Every Send is a poll with wait_seconds 0; Recv
// polls in the background style of the real agent (a request per call).
type Poll struct {
	T       testing.TB
	URL     string
	Token   string
	HC      *http.Client
	Session string
	queue   []protocol.Envelope
	// Responses records every poll response (status, seq, message count) in order.
	Responses []PollResult
}

// PollResult is what one poll returned.
type PollResult struct {
	Status   int
	Seq      int64
	Messages int
	Body     []byte
}

// NewPoll returns a long-poll client.
func NewPoll(t testing.TB, url, token string) *Poll {
	return &Poll{T: t, URL: url, Token: token, HC: &http.Client{Timeout: 40 * time.Second}}
}

// Do posts one poll and returns the raw result; messages are validated and queued.
func (p *Poll) Do(msgs []protocol.Envelope, wait int) PollResult {
	p.T.Helper()
	if msgs == nil {
		msgs = []protocol.Envelope{}
	}
	body, err := json.Marshal(protocol.Poll{Messages: msgs, WaitSeconds: wait})
	if err != nil {
		p.T.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, p.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.Token)
	resp, err := p.HC.Do(req)
	if err != nil {
		p.T.Fatalf("poll: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	res := PollResult{Status: resp.StatusCode, Body: raw}
	if resp.StatusCode == http.StatusOK {
		var out struct {
			Session  string            `json:"session"`
			Seq      int64             `json:"seq"`
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			p.T.Fatalf("poll body: %v: %s", err, raw)
		}
		res.Seq, res.Messages = out.Seq, len(out.Messages)
		if out.Session != "" {
			p.Session = out.Session
		}
		for _, m := range out.Messages {
			if err := Validate(m); err != nil {
				p.T.Fatalf("poll message does not validate: %v: %s", err, m)
			}
			env, err := protocol.ParseEnvelope(m)
			if err != nil {
				p.T.Fatal(err)
			}
			p.queue = append(p.queue, env)
		}
	}
	p.Responses = append(p.Responses, res)
	return res
}

// Send posts one message (wait 0).
func (p *Poll) Send(env protocol.Envelope) error {
	res := p.Do([]protocol.Envelope{env}, 0)
	if res.Status != http.StatusOK {
		return fmt.Errorf("poll answered %d: %s", res.Status, res.Body)
	}
	return nil
}

// Recv returns the next queued message, polling (wait 1 s at a time) until timeout.
func (p *Poll) Recv(timeout time.Duration) (protocol.Envelope, error) {
	deadline := time.Now().Add(timeout)
	for {
		if len(p.queue) > 0 {
			env := p.queue[0]
			p.queue = p.queue[1:]
			return env, nil
		}
		if !time.Now().Before(deadline) {
			return protocol.Envelope{}, fmt.Errorf("timed out after %s", timeout)
		}
		res := p.Do(nil, 1)
		if res.Status != http.StatusOK {
			return protocol.Envelope{}, fmt.Errorf("poll answered %d: %s", res.Status, res.Body)
		}
	}
}

// Close is a no-op (a poll session ends by itself).
func (p *Poll) Close() {}
