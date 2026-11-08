// Package access is the access-control module (design 07-access-control,
// release 0.3): access points, the on-site agents that drive them, credentials
// projected from vehicles and visitor entries, the allow-list versions and
// change log, the agent protocol server (enrolment, tokens, snapshots, deltas,
// events, commands), the opening log and the manual open command.
//
// The wire format is package protocol; the live connections are package
// session; the reference evaluator that pins the decision rules for the
// fixtures is package evaltest.
package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/session"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// DB is the database access the module needs, satisfied by *db.DB.
// InPlatformTx (the BYPASSRLS pool) serves the lookups that run before a tenant
// is known (agent token to organization, the jobs' site lists) and the
// retention function of the opening log; request handlers never use it.
type DB interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Sealer encrypts the small secrets the module must read back (the rotated
// agent token, kept for the 10-minute idempotency window). *crypto.FieldCipher
// satisfies it.
type Sealer interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(sealed, aad []byte) ([]byte, error)
}

// GuardResolver turns the X-Guard-Session header of the gate console into the
// guard the action is attributed to. The community module implements it; the
// default (nil) attributes every action to the login principal.
type GuardResolver interface {
	ResolveGuard(ctx context.Context, orgID, siteID uuid.UUID, token string) (userID uuid.UUID, display string, err error)
}

// Config configures the Service.
type Config struct {
	// BaseURL is the public base URL of the instance; ws_url and poll_url in the
	// enrolment answer are derived from it and must be https (wss) unless
	// InsecureDev is set.
	BaseURL     string
	InsecureDev bool
	// Sealer seals the rotated token; required for token rotation.
	Sealer Sealer
	// Source supplies visitor credentials; nil means vehicles only.
	Source CredentialSource
	// Guards resolves X-Guard-Session; nil means the login principal acts.
	Guards GuardResolver
	Log    *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// MaxSnapshotEntries is the largest allow-list (published credentials of one
	// site) the server sends as a snapshot; a larger site cannot sync and the
	// session errors on every tick. Default DefaultMaxSnapshotEntries (20 000).
	MaxSnapshotEntries int
	// SnapshotPartEntries and SnapshotPartBytes bound one snapshot part (default
	// 2000 entries, the protocol limit, and 900 000 bytes of JSON, under the 1 MiB frame limit).
	SnapshotPartEntries int
	SnapshotPartBytes   int
}

// Timings of the protocol (design 4 and 8).
const (
	EnrolTokenTTL      = 15 * time.Minute
	RotateEvery        = 180 * 24 * time.Hour
	RotationWindow     = 10 * time.Minute
	OpenTTL            = 15 * time.Second
	ConnectedWithin    = 90 * time.Second
	HeartbeatSeconds   = 30
	ClockSuspectOffset = 120 * time.Second
	ClockBlockOffset   = 24 * time.Hour
	ChangeLogRetention = 7 * 24 * time.Hour
	ChangeLogKeep      = 50000
	// MaxEventAge is the oldest occurred_at (after clock correction) the server
	// accepts; older events are rejected `invalid`.
	MaxEventAge = 30 * 24 * time.Hour
)

// Poll rate limit of one agent: a burst that covers the agent's worst case after
// an outage (hello, a snapshot's parts' acks, and five event batches in flight, each
// its own request, plus their acks and a heartbeat) and a sustained 4 requests per second.
const (
	PollBurst    = 12
	PollInterval = 250 * time.Millisecond
)

// Service is the access module.
type Service struct {
	db  DB
	cfg Config
	hub *session.Manager

	openUser, openPoint, openSite  *ratelimit.Limiter
	enrolLimit, wsLimit, pollLimit *ratelimit.Limiter
}

// NewService returns a Service.
func NewService(db DB, cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Source == nil {
		cfg.Source = VehiclesOnly{}
	}
	if cfg.MaxSnapshotEntries <= 0 {
		cfg.MaxSnapshotEntries = DefaultMaxSnapshotEntries
	}
	s := &Service{
		db: db, cfg: cfg,
		openUser:   ratelimit.New(10, 6*time.Second),       // 10/min per user
		openPoint:  ratelimit.New(6, 10*time.Second),       // 6/min per point (a gate cycle takes 10 s or more)
		openSite:   ratelimit.New(60, time.Second),         // 60/min per site
		enrolLimit: ratelimit.New(10, 6*time.Second),       // 10/min per IP
		wsLimit:    ratelimit.New(20, 3*time.Second),       // 20/min per agent
		pollLimit:  ratelimit.New(PollBurst, PollInterval),   // 4/s per agent, burst for hello + 5 batches
	}
	s.hub = session.NewManager(&backend{s: s}, session.Options{
		Now: cfg.Now, Log: cfg.Log, SnapshotPartEntries: cfg.SnapshotPartEntries, SnapshotPartBytes: cfg.SnapshotPartBytes,
	})
	return s
}

// Hub is the live connection manager, for the transport handlers and tests.
func (s *Service) Hub() *session.Manager { return s.hub }

// Run starts the background work of the hub (idle sessions, periodic sync)
// until ctx ends.
func (s *Service) Run(ctx context.Context) { s.hub.Run(ctx) }

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

// WakeSite tells the connected agents of a site that the allow-list, the
// configuration or their commands changed (the in-process notification of
// design 4.8); other replicas notice within 2 s by polling.
func (s *Service) WakeSite(siteID uuid.UUID) { s.hub.NotifySite(siteID) }

// publicURLs derives the ws and poll URLs of the instance (https and wss only
// unless InsecureDev).
func (s *Service) publicURLs() (ws, poll string, err error) {
	u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/"))
	if err != nil || u.Host == "" {
		return "", "", problem.Internal(fmt.Errorf("the instance base URL %q is not usable for agents", s.cfg.BaseURL))
	}
	switch {
	case u.Scheme == "https":
		return "wss://" + u.Host + u.Path + "/v1/agent/ws", "https://" + u.Host + u.Path + "/v1/agent/poll", nil
	case u.Scheme == "http" && s.cfg.InsecureDev:
		return "ws://" + u.Host + u.Path + "/v1/agent/ws", "http://" + u.Host + u.Path + "/v1/agent/poll", nil
	}
	return "", "", problem.New(503, problem.CodeInternalError, "agents need an https base URL (OPENBEKCI_BASE_URL)")
}

// ensureState makes sure the site's version row exists.
func ensureState(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO access_site_state (org_id, site_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, orgID, siteID)
	return err
}

// bumpVersion takes the next allow-list version of the site. The row lock it
// takes is held to the end of the transaction, which orders the writers and
// keeps the versions gap-free: a rolled-back transaction gives its number back.
func bumpVersion(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (int64, error) {
	if err := ensureState(ctx, tx, orgID, siteID); err != nil {
		return 0, err
	}
	var v int64
	err := tx.QueryRow(ctx, `UPDATE access_site_state SET version = version + 1, updated_at = now()
		WHERE org_id = $1 AND site_id = $2 RETURNING version`, orgID, siteID).Scan(&v)
	return v, err
}

// pageSize resolves a page limit (default 50, at most 200).
func pageSize(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	}
	return limit
}

// decodeIDCursor reads a keyset cursor over ids (UUIDv7: creation order).
func decodeIDCursor(c string) (uuid.UUID, error) {
	_, after, err := tenancy.PageParams{Cursor: c}.Resolve()
	return after, err
}

// pageOf cuts rows (fetched with limit+1) to the limit and returns the next cursor.
func pageOf[T any](rows []T, limit int, idOf func(T) uuid.UUID) ([]T, string) {
	return tenancy.Page(rows, limit, idOf)
}

// errVersion is returned inside a transaction when If-Match does not match.
var errVersion = errors.New("version mismatch")

// siteScope derives the scope of a site from its row (under RLS) and checks
// perm for it. Use inside the tenant transaction, never with request-built scopes.
func siteScope(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) error {
	sc, err := authz.ScopeOfSite(ctx, tx, siteID)
	if err != nil {
		return err
	}
	return authz.Require(ctx, perm, sc)
}

// dbProblem maps an error of a write to a problem.
func dbProblem(err error, what string) error {
	if err == nil {
		return nil
	}
	var pe *problem.Error
	switch {
	case errors.As(err, &pe):
		return err
	case errors.Is(err, errVersion):
		return problem.New(409, problem.CodeVersionConflict, "the "+what+" was changed by someone else; reload and try again")
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	if isPG(err, "23505") {
		return problem.Conflict("the " + what + " conflicts with an existing one")
	}
	if isPG(err, "23503") {
		return problem.Validation(problem.Field{Field: "reference", Code: "invalid_value"})
	}
	return problem.Internal(fmt.Errorf("%s: %w", what, err))
}
