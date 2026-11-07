package access_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/accesstest"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// clock is a controllable clock shared by the service and its sessions.
type clock struct{ ns atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.ns.Store(time.Now().UnixNano())
	return c
}
func (c *clock) Now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *clock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

// source is a CredentialSource the tests fill.
type source struct {
	mu   sync.Mutex
	list []access.VisitorCredential
}

func (s *source) set(l ...access.VisitorCredential) {
	s.mu.Lock()
	s.list = l
	s.mu.Unlock()
}

func (s *source) VisitorCredentials(_ context.Context, _ access.Site, _ time.Time) ([]access.VisitorCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]access.VisitorCredential(nil), s.list...), nil
}

type env struct {
	t     *testing.T
	d     *db.DB
	svc   *access.Service
	srv   *httptest.Server
	clk   *clock
	src   *source
	org   uuid.UUID
	site  access.Site
	unit  uuid.UUID
	ctx   context.Context // a manager with every access permission, re-authenticated
	other uuid.UUID       // a second organization
}

var testSecret = []byte("0123456789abcdef0123456789abcdef-enc")

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.New(t)
	authz.SetScopeLoader(tenancy.ScopeLoader{})
	t.Cleanup(func() { authz.SetScopeLoader(nil) })
	e := &env{t: t, d: d, clk: newClock(), src: &source{}}
	router := chi.NewRouter()
	e.srv = httptest.NewServer(router)
	t.Cleanup(e.srv.Close)
	sealer, err := crypto.NewFieldCipher(testSecret, "access.agent_token")
	if err != nil {
		t.Fatal(err)
	}
	e.svc = access.NewService(d, access.Config{
		BaseURL: e.srv.URL, InsecureDev: true, Sealer: sealer, Source: e.src, Now: e.clk.Now,
		Log: slog.New(slog.NewTextHandler(&tlog{t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	e.svc.Mount(router)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.svc.Run(ctx)

	e.org, e.other = id.New(), id.New()
	for _, o := range []uuid.UUID{e.org, e.other} {
		e.exec(`INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, $2, 'Org')`, o, "org-"+o.String())
	}
	e.site, e.unit = e.newSite(e.org)
	e.ctx = e.managerCtx(e.org)
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	err := e.d.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), sql, args...)
		return err
	})
	if err != nil {
		e.t.Fatalf("sql: %v\n%s", err, sql)
	}
}

func (e *env) newSite(org uuid.UUID) (access.Site, uuid.UUID) {
	site, block, unit := id.New(), id.New(), id.New()
	e.exec(`INSERT INTO site (id, org_id, name, timezone) VALUES ($1, $2, 'Site', 'Europe/Istanbul')`, site, org)
	e.exec(`INSERT INTO block (id, org_id, site_id, name, hidden) VALUES ($1, $2, $3, 'Main', true)`, block, org, site)
	e.exec(`INSERT INTO unit (id, org_id, site_id, block_id, door_number) VALUES ($1, $2, $3, $4, 'A-1')`, unit, org, site, block)
	return access.Site{OrgID: org, SiteID: site}, unit
}

func (e *env) managerCtx(org uuid.UUID) context.Context {
	uid := id.New()
	var items []authz.Grant
	for _, p := range []authz.Permission{access.PermRead, access.PermManage, access.PermOpen, access.PermViewEvents} {
		items = append(items, authz.Grant{Permission: p, ScopeType: authz.ScopeOrg})
	}
	pr := &authz.Principal{ID: uid, UserID: uid, ReauthUntil: time.Now().Add(time.Hour)}
	return authz.WithGrants(authz.WithPrincipal(context.Background(), pr), authz.Grants{OrgID: org, Items: items})
}

func ctxWith(org uuid.UUID, reauth bool, perms ...authz.Permission) context.Context {
	uid := id.New()
	var items []authz.Grant
	for _, p := range perms {
		items = append(items, authz.Grant{Permission: p, ScopeType: authz.ScopeOrg})
	}
	pr := &authz.Principal{ID: uid, UserID: uid}
	if reauth {
		pr.ReauthUntil = time.Now().Add(time.Hour)
	}
	return authz.WithGrants(authz.WithPrincipal(context.Background(), pr), authz.Grants{OrgID: org, Items: items})
}

// readOnlyCtx holds access.read and nothing else.
func readOnlyCtx(e *env) context.Context { return ctxWith(e.org, true, access.PermRead) }

// noReauthCtx holds every access permission but has not re-authenticated.
func noReauthCtx(e *env) context.Context {
	return ctxWith(e.org, false, access.PermRead, access.PermManage, access.PermOpen, access.PermViewEvents)
}

// occupy gives the unit an open-ended owner occupancy.
func (e *env) occupy(unit uuid.UUID, site access.Site) uuid.UUID {
	person, occ := id.New(), id.New()
	e.exec(`INSERT INTO person (id, org_id, display_name, contact) VALUES ($1, $2, 'Owner', '{}')`, person, site.OrgID)
	e.exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from) VALUES ($1, $2, $3, $4, $5, 'owner', 'owner', '2020-01-01')`,
		occ, site.OrgID, site.SiteID, unit, person)
	return occ
}

func (e *env) vehicle(site access.Site, unit uuid.UUID, plate string) uuid.UUID {
	v := id.New()
	e.exec(`INSERT INTO vehicle (id, org_id, site_id, unit_id, plate) VALUES ($1, $2, $3, $4, $5)`, v, site.OrgID, site.SiteID, unit, plate)
	return v
}

// project drains the queue like the access.project job.
func (e *env) project(site access.Site) access.ProjectResult {
	e.t.Helper()
	res, err := e.svc.ProjectDirty(context.Background(), site)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) point(name, kind string, agent *uuid.UUID, instance string) uuid.UUID {
	e.t.Helper()
	body := map[string]any{"site_id": e.site.SiteID, "name": name, "kind": kind, "direction": "in"}
	if agent != nil {
		body["agent_id"] = agent.String()
	}
	if instance != "" {
		body["driver_ref"] = map[string]any{"instance": instance, "channel": "1"}
	}
	doc, _ := tenancy.ToDoc(body)
	p, err := e.svc.CreatePoint(e.ctx, e.org, doc)
	if err != nil {
		e.t.Fatalf("create point: %v", err)
	}
	return p.ID
}

func (e *env) siteVersion() int64 {
	e.t.Helper()
	var v int64
	err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		err := tx.QueryRow(context.Background(), `SELECT version FROM access_site_state WHERE org_id = $1 AND site_id = $2`, e.org, e.site.SiteID).Scan(&v)
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) count(table string) int {
	e.t.Helper()
	var n int
	err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE org_id = $1`, e.org).Scan(&n)
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// agent creates an agent, enrols it over HTTP and returns the answer.
type enrolled struct {
	ID    uuid.UUID
	Token string
	Resp  protocol.EnrolResponse
}

func (e *env) newAgent(name string) enrolled {
	e.t.Helper()
	en, err := e.svc.CreateAgent(e.ctx, e.org, e.site.SiteID, name)
	if err != nil {
		e.t.Fatalf("create agent: %v", err)
	}
	return e.enrol(en.Agent.ID, en.EnrolmentToken)
}

func (e *env) enrol(agentID uuid.UUID, token string) enrolled {
	e.t.Helper()
	code, body := e.post("/v1/agent/enrol", "", protocol.EnrolRequest{EnrolmentToken: token, Hostname: "pi-gate", Platform: "linux/arm64", AgentVersion: "0.3.0"})
	if code != 200 {
		e.t.Fatalf("enrol answered %d: %s", code, body)
	}
	var resp protocol.EnrolResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		e.t.Fatal(err)
	}
	return enrolled{ID: agentID, Token: resp.AgentToken, Resp: resp}
}

func (e *env) post(path, token string, body any) (int, []byte) {
	e.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) wsURL() string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/v1/agent/ws" }

// dial connects a fake agent over WebSocket.
func (e *env) dial(a enrolled) *accesstest.Agent {
	e.t.Helper()
	ag, _, resp, err := accesstest.DialWS(e.t, e.wsURL(), a.Token)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		e.t.Fatalf("dial: %v (status %d)", err, status)
	}
	ag.ID = a.ID.String()
	return ag
}

// handshake does hello and returns the server hello, with the messages that follow until quiet.
func handshake(t *testing.T, ag *accesstest.Agent, snap, cfg int64) (protocol.ServerHello, []protocol.Envelope) {
	t.Helper()
	ag.Hello(snap, cfg)
	env := ag.RecvType(protocol.TypeHello, 5*time.Second)
	p, err := env.Decode()
	if err != nil {
		t.Fatal(err)
	}
	var rest []protocol.Envelope
	for {
		m, ok := ag.TryRecv(400 * time.Millisecond)
		if !ok {
			break
		}
		rest = append(rest, m)
	}
	return *p.(*protocol.ServerHello), rest
}

func ofType(msgs []protocol.Envelope, typ string) []protocol.Envelope {
	var out []protocol.Envelope
	for _, m := range msgs {
		if m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

func decodeAs[T any](t *testing.T, env protocol.Envelope) T {
	t.Helper()
	p, err := env.Decode()
	if err != nil {
		t.Fatal(err)
	}
	v, ok := any(p).(*T)
	if !ok {
		t.Fatalf("payload is %T", p)
	}
	return *v
}

func mustEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

var _ = fmt.Sprintf

// tlog sends the service's log to the test output.
type tlog struct{ t *testing.T }

func (l *tlog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
