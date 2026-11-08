package access_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/access/accesstest"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

func pollAgent(e *env, a enrolled) (*accesstest.Agent, *accesstest.Poll) {
	p := accesstest.NewPoll(e.t, e.srv.URL+"/v1/agent/poll", a.Token)
	ag := accesstest.NewAgent(e.t, a.ID.String(), p)
	return ag, p
}

// The long-poll fallback: a session begins with hello, holds the same
// semantics as the socket, answers empty polls with 200, and ends with 410.
func TestIntegrationLongPollSession(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34ABC123")
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	e.project(e.site)
	ag, p := pollAgent(e, a)

	// a poll that does not begin with hello has no session to belong to
	res := p.Do(nil, 0)
	mustEq(t, "poll without a session", res.Status, http.StatusGone)
	var prob struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(res.Body, &prob)
	mustEq(t, "problem code", prob.Code, "agent_session_expired")

	ag.Hello(0, 0)
	got := []string{}
	for i := 0; i < 3; i++ {
		env := ag.Recv(5 * time.Second)
		got = append(got, env.Type)
	}
	if got[0] != "hello" || got[1] != "command" || got[2] != "delta" {
		t.Fatalf("after hello: %v", got)
	}
	// every response with messages is numbered from 1
	mustEq(t, "first numbered response", p.Responses[len(p.Responses)-1].Seq, 1)

	// an empty poll is a 200 with no messages and does not consume a number
	res = p.Do(nil, 0)
	mustEq(t, "empty poll status", res.Status, http.StatusOK)
	mustEq(t, "empty poll messages", res.Messages, 0)
	mustEq(t, "empty poll repeats the last seq", res.Seq, 1)

	// heartbeats are answered
	ag.Send(protocol.TypeHeartbeat, protocol.Heartbeat{Drivers: []protocol.DriverState{{Instance: "relay", State: "ok"}}})
	ack := decodeAs[protocol.Ack](t, ag.RecvType(protocol.TypeAck, 5*time.Second))
	mustEq(t, "heartbeat ack", ack.Status, protocol.AckOK)

	// a wrong handle, or silence beyond 60 s, ends the session
	body, _ := json.Marshal(map[string]any{"messages": []any{}, "wait_seconds": 0, "session": "not-my-session"})
	code, _ := rawPoll(e, a.Token, body)
	mustEq(t, "wrong session handle", code, http.StatusGone)
	e.clk.Advance(2 * time.Minute)
	e.svc.Hub().Sweep()
	res = p.Do(nil, 0)
	mustEq(t, "after 60 s of silence", res.Status, http.StatusGone)

	// the next hello starts a fresh session
	ag.Hello(1, 1)
	env := ag.RecvType(protocol.TypeHello, 5*time.Second)
	mustEq(t, "re-hello", env.Type, protocol.TypeHello)
}

func rawPoll(e *env, token string, body []byte) (int, []byte) {
	e.t.Helper()
	var payload any
	_ = json.Unmarshal(body, &payload)
	return e.post("/v1/agent/poll", token, payload)
}

// Two polls in flight at once (a long one and the send path) must not break
// the order of what the server delivers: the numbered responses, read in
// number order, carry the deltas in version order with no hole and no repeat.
func TestIntegrationConcurrentPollsDeliverInOrder(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	e.project(e.site)
	_, p1 := pollAgent(e, a)
	p2 := accesstest.NewPoll(t, e.srv.URL+"/v1/agent/poll", a.Token)

	hello, _ := protocol.New(protocol.TypeHello, protocol.AgentHello{From: "agent", AgentID: a.ID.String(), AgentVersion: "t", ProtocolVersions: []int{1},
		Drivers: []protocol.DriverState{}}, time.Now())
	res := p1.Do([]protocol.Envelope{hello}, 0)
	mustEq(t, "hello poll", res.Status, 200)

	var mu sync.Mutex
	var responses []accesstest.PollResult
	record := func(r accesstest.PollResult) {
		mu.Lock()
		responses = append(responses, r)
		mu.Unlock()
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, p := range []*accesstest.Poll{p1, p2} {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r := p.Do(nil, 2)
				if r.Status != 200 {
					return
				}
				record(r)
				time.Sleep(60 * time.Millisecond) // the agent paces itself under the 4 polls/s limit
			}
		}()
	}
	// versions arrive one by one while both polls wait
	const n = 8
	changed := 0
	for i := 0; i < n; i++ {
		e.vehicle(e.site, e.unit, "34XYZ"+string(rune('1'+i)))
		changed += e.project(e.site).Changed // each new plate takes one version
		time.Sleep(120 * time.Millisecond)
	}
	time.Sleep(2500 * time.Millisecond)
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	type numbered struct {
		seq  int64
		msgs []protocol.Envelope
	}
	bySeq := map[int64]numbered{}
	for _, r := range responses {
		if r.Messages == 0 {
			continue
		}
		if _, dup := bySeq[r.Seq]; dup {
			t.Fatalf("seq %d was issued twice", r.Seq)
		}
		var body protocol.Poll
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		bySeq[r.Seq] = numbered{r.Seq, body.Messages}
	}
	var seqs []int
	for s := range bySeq {
		seqs = append(seqs, int(s))
	}
	sort.Ints(seqs)
	// the first poll (hello) consumed the numbers before the loop started
	var last int64
	var versions []int64
	for _, s := range seqs {
		if last != 0 && int64(s) != last+1 {
			t.Fatalf("a hole in the sequence: %d after %d (%v)", s, last, seqs)
		}
		last = int64(s)
		for _, m := range bySeq[int64(s)].msgs {
			if m.Type != protocol.TypeDelta {
				continue
			}
			d := decodeAs[protocol.Delta](t, m)
			versions = append(versions, d.FromVersion, d.ToVersion)
		}
	}
	// A poll that reaches the server after a version has already landed (the
	// first request on a fresh connection is slow) is answered with one delta
	// spanning every version it missed, so the number of deltas is not fixed.
	// What is fixed: they chain from version 0 (the hello snapshot) with no hole,
	// no repeat and no reordering, up to the last version projected.
	if len(versions) == 0 || versions[0] != 0 || versions[len(versions)-1] != int64(changed) {
		t.Fatalf("the deltas do not run from version 0 to %d: %v", changed, versions)
	}
	for i := 0; i < len(versions); i += 2 {
		if versions[i+1] <= versions[i] || (i > 0 && versions[i] != versions[i-1]) {
			t.Fatalf("deltas out of order, repeated or with a hole: %v", versions)
		}
	}
}

func TestIntegrationLongPollHeldOpenAndReleasedByNews(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	_, p := pollAgent(e, a)
	hello, _ := protocol.New(protocol.TypeHello, protocol.AgentHello{From: "agent", AgentID: a.ID.String(), AgentVersion: "t", ProtocolVersions: []int{1},
		Drivers: []protocol.DriverState{}}, time.Now())
	_ = p.Do([]protocol.Envelope{hello}, 0)
	done := make(chan accesstest.PollResult, 1)
	go func() { done <- p.Do(nil, 20) }()
	select {
	case <-done:
		t.Fatal("a long poll with nothing to say must wait")
	case <-time.After(700 * time.Millisecond):
	}
	e.vehicle(e.site, e.unit, "34NEW111")
	e.project(e.site)
	select {
	case r := <-done:
		mustEq(t, "released status", r.Status, 200)
		if r.Messages == 0 {
			t.Fatal("released without the delta")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the long poll was not released by the new version")
	}
}

func TestIntegrationPollOfARevokedAgentIsToldSo(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	if _, err := e.svc.RevokeAgent(e.ctx, e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	p := accesstest.NewPoll(t, e.srv.URL+"/v1/agent/poll", a.Token)
	res := p.Do(nil, 0)
	mustEq(t, "status", res.Status, 200)
	var body protocol.Poll
	_ = json.Unmarshal(res.Body, &body)
	if len(body.Messages) != 1 {
		t.Fatalf("%s", res.Body)
	}
	_, cmd := accesstest.NewAgent(t, "", p), decodeAs[protocol.Command](t, body.Messages[0])
	mustEq(t, "command", cmd.Command, protocol.CmdRevoked)
	// an unknown token is a bare 401 (the agent quarantines, it does not wipe)
	bad := accesstest.NewPoll(t, e.srv.URL+"/v1/agent/poll", "obk_agt_00000000-0000-7000-8000-000000000000.xxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	mustEq(t, "unknown token", bad.Do(nil, 0).Status, 401)
}

func TestIntegrationPollRateLimit(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	p := accesstest.NewPoll(t, e.srv.URL+"/v1/agent/poll", a.Token)
	var limited bool
	for i := 0; i < 30; i++ {
		if p.Do(nil, 0).Status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("30 immediate polls must hit the 4/s limit")
	}
}
