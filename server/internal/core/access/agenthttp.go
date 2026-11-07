package access

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/access/session"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// MaxPollWait is the longest a poll is held open (the spec allows up to 30 s;
// the server keeps below the 30 s request timeout of the router).
const MaxPollWait = 25 * time.Second

// Mount registers the agent transport on r: POST /v1/agent/enrol (no
// credentials, the one-time token is the credential), GET /v1/agent/ws, POST
// /v1/agent/poll and POST /v1/agent/token:rotate (agent token). They are
// documented in the spec under the agent tag but served here, outside the
// user-session pipeline: an agent token never satisfies user authorization and
// a user session never authenticates an agent.
func (s *Service) Mount(r chi.Router) {
	r.Post("/v1/agent/enrol", s.handleEnrol)
	r.Get("/v1/agent/ws", s.handleWS)
	r.Post("/v1/agent/poll", s.handlePoll)
	r.Post("/v1/agent/token:rotate", s.handleRotate)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// authenticate resolves the agent token; the one failure is a bare 401.
func (s *Service) authenticate(w http.ResponseWriter, r *http.Request) (AgentPrincipal, bool) {
	p, err := s.AuthenticateAgent(r.Context(), bearer(r))
	if err != nil {
		if errors.Is(err, ErrUnknownToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem.Write(w, r, problem.Unauthenticated("the agent token is not valid"))
		} else {
			problem.Write(w, r, problem.Internal(err))
		}
		return AgentPrincipal{}, false
	}
	return p, true
}

func (s *Service) handleEnrol(w http.ResponseWriter, r *http.Request) {
	var req protocol.EnrolRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil || json.Unmarshal(body, &req) != nil {
		problem.Write(w, r, problem.BadRequest("the request body is not valid JSON"))
		return
	}
	resp, err := s.Enrol(r.Context(), req, clientIP(r))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleRotate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if p.Status != "active" {
		// a revoked agent learns it from the socket or the poll, not from here
		w.Header().Set("WWW-Authenticate", "Bearer")
		problem.Write(w, r, problem.Unauthenticated("the agent token is not valid"))
		return
	}
	resp, err := s.RotateToken(r.Context(), p)
	if errors.Is(err, ErrUnknownToken) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		problem.Write(w, r, problem.Unauthenticated("the agent token is not valid"))
		return
	}
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) refOf(p AgentPrincipal) session.Ref {
	return session.Ref{OrgID: p.OrgID, SiteID: p.SiteID, AgentID: p.AgentID}
}

func revokedEnvelope(now time.Time) (protocol.Envelope, error) {
	return protocol.New(protocol.TypeCommand, protocol.Command{Command: protocol.CmdRevoked}, now)
}

// ---- long-poll --------------------------------------------------------------

const maxPollMessages = 200

func (s *Service) handlePoll(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if ok, wait := s.pollLimit.Allow(p.AgentID.String()); !ok {
		problem.Write(w, r, problem.RateLimited(wait))
		return
	}
	if p.Status == "revoked" {
		env, err := revokedEnvelope(s.now())
		if err != nil {
			problem.Write(w, r, problem.Internal(err))
			return
		}
		writeJSON(w, http.StatusOK, protocol.Poll{Messages: []protocol.Envelope{env}, Seq: 1})
		return
	}
	var req protocol.Poll
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil || json.Unmarshal(body, &req) != nil || len(req.Messages) > maxPollMessages || req.WaitSeconds < 0 {
		problem.Write(w, r, problem.BadRequest("the request body is not a valid poll"))
		return
	}
	ctx := r.Context()
	var sess *session.Session
	if len(req.Messages) > 0 && req.Messages[0].Type == protocol.TypeHello {
		sess = s.hub.Open(s.refOf(p), session.KindPoll) // a hello always starts over
	} else {
		sess = s.hub.Current(p.AgentID)
		if sess == nil || sess.Kind != session.KindPoll || (req.Session != "" && req.Session != sess.SessionHandle()) {
			problem.Write(w, r, problem.New(http.StatusGone, problem.CodeAgentSessionExpired, "the poll session has expired; start a new one with hello"))
			return
		}
		if _, closing := sess.Closing(); closing {
			problem.Write(w, r, problem.New(http.StatusGone, problem.CodeAgentSessionExpired, "the poll session has ended; start a new one with hello"))
			return
		}
	}
	sess.Touched()
	var handleErr error
	for _, env := range req.Messages {
		if err := sess.Handle(ctx, env); err != nil {
			handleErr = err
			break
		}
	}
	wait := time.Duration(min(req.WaitSeconds, int(MaxPollWait/time.Second))) * time.Second
	deadline := time.Now().Add(wait)
	for {
		if err := sess.Tick(ctx); err != nil {
			s.cfg.Log.WarnContext(ctx, "poll sync failed", "agent", p.AgentID, "error", err)
		}
		msgs, seq := sess.Drain(0)
		_, closing := sess.Closing()
		if len(msgs) > 0 || closing || handleErr != nil || !time.Now().Before(deadline) {
			if closing || handleErr != nil {
				s.hub.Remove(sess)
			}
			if msgs == nil {
				msgs = []protocol.Envelope{}
			}
			sess.Touched()
			writeJSON(w, http.StatusOK, protocol.Poll{Messages: msgs, Session: sess.SessionHandle(), Seq: seq})
			return
		}
		select {
		case <-sess.Wake():
		case <-time.After(min(s.hub.SyncEvery(), time.Until(deadline))):
		case <-ctx.Done():
			return
		}
	}
}

// ---- WebSocket --------------------------------------------------------------

const (
	wsWriteTimeout = 10 * time.Second
	wsPingEvery    = 30 * time.Second
	wsPongWithin   = 60 * time.Second
	wsReadIdle     = 3 * time.Minute
)

func (s *Service) handleWS(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if ok, wait := s.wsLimit.Allow(p.AgentID.String()); !ok {
		problem.Write(w, r, problem.RateLimited(wait))
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{protocol.Subprotocol},
		CompressionMode: websocket.CompressionContextTakeover, // permessage-deflate
	})
	if err != nil {
		return // Accept has answered
	}
	c.SetReadLimit(protocol.MaxFrameBytes)
	// The router's request timeout must not cut a connection that lives for days.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	if c.Subprotocol() != protocol.Subprotocol {
		_ = c.Close(websocket.StatusProtocolError, "subprotocol "+protocol.Subprotocol+" required")
		return
	}
	if p.Status == "revoked" {
		s.wsSendAndClose(ctx, c, nil, session.Close{Code: protocol.CloseRevoked, Reason: "revoked"}, true)
		return
	}
	sess := s.hub.Open(s.refOf(p), session.KindWS)
	defer s.hub.Remove(sess)

	// reader: one JSON envelope per text frame
	readErr := make(chan error, 1)
	go func() {
		for {
			rctx, rcancel := context.WithTimeout(ctx, wsReadIdle)
			typ, data, err := c.Read(rctx)
			rcancel()
			if err != nil {
				readErr <- err
				return
			}
			if typ != websocket.MessageText {
				sess.CloseWith(int(websocket.StatusUnsupportedData), "text frames only")
				readErr <- errors.New("binary frame")
				return
			}
			env, err := protocol.ParseEnvelope(data)
			if err != nil {
				sess.CloseWith(int(websocket.StatusProtocolError), "invalid envelope")
				readErr <- err
				return
			}
			if err := sess.Handle(ctx, env); err != nil {
				readErr <- err
				return
			}
		}
	}()

	tick := time.NewTicker(s.hub.SyncEvery())
	defer tick.Stop()
	ping := time.NewTicker(wsPingEvery)
	defer ping.Stop()
	for {
		if err := sess.Tick(ctx); err != nil {
			s.cfg.Log.WarnContext(ctx, "websocket sync failed", "agent", p.AgentID, "error", err)
		}
		msgs, _ := sess.Drain(0)
		cl, closing := sess.Closing()
		if err := s.wsWrite(ctx, c, msgs); err != nil {
			_ = c.CloseNow()
			return
		}
		if closing {
			s.wsSendAndClose(ctx, c, nil, cl, false)
			return
		}
		select {
		case <-sess.Wake():
		case <-tick.C:
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, wsPongWithin)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				_ = c.CloseNow()
				return
			}
		case <-readErr:
			// the reader ended: flush a final close if the session asked for one
			if cl, closing := sess.Closing(); closing {
				msgs, _ := sess.Drain(0)
				_ = s.wsWrite(ctx, c, msgs)
				s.wsSendAndClose(ctx, c, nil, cl, false)
				return
			}
			_ = c.CloseNow()
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) wsWrite(ctx context.Context, c *websocket.Conn, msgs []protocol.Envelope) error {
	for _, env := range msgs {
		data, err := env.MarshalJSON()
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
		err = c.Write(wctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// wsSendAndClose writes the given messages (and the revoked command when
// asked), then closes with the code of cl.
func (s *Service) wsSendAndClose(ctx context.Context, c *websocket.Conn, msgs []protocol.Envelope, cl session.Close, revoked bool) {
	if revoked {
		if env, err := revokedEnvelope(s.now()); err == nil {
			msgs = append(msgs, env)
		}
	}
	_ = s.wsWrite(ctx, c, msgs)
	reason := cl.Reason
	if len(reason) > 120 {
		reason = reason[:120]
	}
	_ = c.Close(websocket.StatusCode(cl.Code), reason)
}
