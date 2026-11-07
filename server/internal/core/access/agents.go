package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func isPG(err error, code string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == code
}

// Token prefixes.
const (
	EnrolPrefix = "obk_enr_"
	AgentPrefix = "obk_agt_"
)

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

// newAgentToken builds obk_agt_<agent id>.<256-bit secret>.
func newAgentToken(agentID uuid.UUID) (string, error) {
	sec, err := randomSecret()
	if err != nil {
		return "", err
	}
	return AgentPrefix + agentID.String() + "." + sec, nil
}

// parseAgentToken extracts the agent id from a presented token.
func parseAgentToken(tok string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(tok, AgentPrefix)
	if !ok {
		return uuid.Nil, false
	}
	idPart, secret, ok := strings.Cut(rest, ".")
	if !ok || len(secret) < 20 {
		return uuid.Nil, false
	}
	agentID, err := uuid.Parse(idPart)
	return agentID, err == nil
}

// DriverStateView is a driver of an agent as the API shows it.
type DriverStateView struct {
	Instance string `json:"instance"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
	Mock     bool   `json:"mock"`
}

// Agent is an on-site agent as the API shows it.
type Agent struct {
	ID              uuid.UUID         `json:"id"`
	OrgID           uuid.UUID         `json:"org_id"`
	SiteID          uuid.UUID         `json:"site_id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	Connected       bool              `json:"connected"`
	Version         int               `json:"version"`
	Platform        *string           `json:"platform"`
	ProtocolVersion *int64            `json:"protocol_version"`
	LastSeen        *time.Time        `json:"last_seen"`
	SnapshotVersion *int64            `json:"snapshot_version"`
	QueueDepth      *int64            `json:"queue_depth"`
	Drivers         []DriverStateView `json:"drivers"`
	ClockOffsetMS   *int64            `json:"clock_offset_ms"`
	ClockSuspect    bool              `json:"clock_suspect"`
	RotateBy        *time.Time        `json:"rotate_by"`
	EnrolExpiresAt  *time.Time        `json:"enrol_expires_at"`
	RevokedAt       *time.Time        `json:"revoked_at"`
	CreatedBy       *uuid.UUID        `json:"created_by"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	LostEvents      *int64            `json:"lost_events"`
	StoreError      *string           `json:"store_error"`
	DeadLetters     *int64            `json:"dead_letters"`
}

// AgentEnrolment is the answer to creating or re-enrolling an agent: the
// enrolment token is shown once.
type AgentEnrolment struct {
	Agent              Agent     `json:"agent"`
	EnrolmentToken     string    `json:"enrolment_token"`
	EnrolmentExpiresAt time.Time `json:"enrolment_expires_at"`
}

const agentColumns = `id, org_id, site_id, name, status, version, platform, protocol_version, last_seen, snapshot_version, queue_depth,
	drivers_status, clock_offset_ms, clock_suspect, rotate_by, enrol_expires_at, revoked_at, created_by, created_at, updated_at,
	lost_events, store_error, dead_letters`

func scanAgent(row pgx.Row, now time.Time, live func(uuid.UUID) bool) (Agent, error) {
	var a Agent
	var proto *int32
	var drivers []byte
	err := row.Scan(&a.ID, &a.OrgID, &a.SiteID, &a.Name, &a.Status, &a.Version, &a.Platform, &proto, &a.LastSeen, &a.SnapshotVersion, &a.QueueDepth,
		&drivers, &a.ClockOffsetMS, &a.ClockSuspect, &a.RotateBy, &a.EnrolExpiresAt, &a.RevokedAt, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
		&a.LostEvents, &a.StoreError, &a.DeadLetters)
	if err != nil {
		return a, err
	}
	if proto != nil {
		p := int64(*proto)
		a.ProtocolVersion = &p
	}
	a.Drivers = []DriverStateView{}
	var ds []protocol.DriverState
	if json.Unmarshal(drivers, &ds) == nil {
		for _, d := range ds {
			a.Drivers = append(a.Drivers, DriverStateView{Instance: d.Instance, State: d.State, Detail: d.Detail, Mock: d.Mock})
		}
	}
	for _, t := range []**time.Time{&a.LastSeen, &a.RotateBy, &a.EnrolExpiresAt, &a.RevokedAt} {
		if *t != nil {
			u := (**t).UTC()
			*t = &u
		}
	}
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	a.Connected = a.Status == "active" && (live(a.ID) || (a.LastSeen != nil && now.Sub(*a.LastSeen) <= ConnectedWithin))
	return a, nil
}

func (s *Service) loadAgent(ctx context.Context, tx pgx.Tx, orgID, agentID uuid.UUID, lock bool) (Agent, error) {
	q := `SELECT ` + agentColumns + ` FROM agent WHERE org_id = $1 AND id = $2`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanAgent(tx.QueryRow(ctx, q, orgID, agentID), s.now(), s.hub.Connected)
}

// ListAgents lists the agents of a site (permission access.read for the site).
func (s *Service) ListAgents(ctx context.Context, orgID, siteID uuid.UUID, status string, limit int, cursor string) ([]Agent, string, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return nil, "", err
	}
	after, err := decodeIDCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	n := pageSize(limit)
	var out []Agent
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if err := siteScope(ctx, tx, PermRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+agentColumns+` FROM agent WHERE org_id = $1 AND site_id = $2 AND id > $3
			AND ($4 = '' OR status = $4) ORDER BY id LIMIT $5`, orgID, siteID, after, status, n+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAgent(rows, s.now(), s.hub.Connected)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbProblem(err, "agents")
	}
	items, next := pageOf(out, n, func(a Agent) uuid.UUID { return a.ID })
	return items, next, nil
}

// GetAgent returns one agent.
func (s *Service) GetAgent(ctx context.Context, orgID, agentID uuid.UUID) (Agent, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return Agent{}, err
	}
	var a Agent
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if a, err = s.loadAgent(ctx, tx, orgID, agentID, false); err != nil {
			return err
		}
		return siteScope(ctx, tx, PermRead, a.SiteID)
	})
	return a, dbProblem(err, "agent")
}

func validName(name string) error {
	if n := len([]rune(strings.TrimSpace(name))); n < 1 || n > 200 {
		return problem.Validation(problem.Field{Field: "name", Code: "invalid_value"})
	}
	return nil
}

// CreateAgent creates a pending agent and its one-time enrolment token,
// shown once (permission access.manage).
func (s *Service) CreateAgent(ctx context.Context, orgID, siteID uuid.UUID, name string) (AgentEnrolment, error) {
	if err := authz.RequireInOrg(ctx, PermManage, orgID); err != nil {
		return AgentEnrolment{}, err
	}
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return AgentEnrolment{}, err
	}
	var out AgentEnrolment
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if err := siteScope(ctx, tx, PermManage, siteID); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false); err != nil {
			return err
		}
		tok, hash, exp, err := s.newEnrolToken()
		if err != nil {
			return err
		}
		agentID := id.New()
		var by *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			by = &u
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent (id, org_id, site_id, name, enrol_token_hash, enrol_expires_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, agentID, orgID, siteID, name, hash, exp, by); err != nil {
			return err
		}
		a, err := s.loadAgent(ctx, tx, orgID, agentID, false)
		if err != nil {
			return err
		}
		diff, err := audit.Diff("agent", nil, map[string]any{"name": name, "status": "pending"})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "access.agent_create", TargetType: "agent", TargetID: agentID, Diff: diff}); err != nil {
			return err
		}
		out = AgentEnrolment{Agent: a, EnrolmentToken: tok, EnrolmentExpiresAt: exp}
		return nil
	})
	if err != nil {
		return AgentEnrolment{}, dbProblem(err, "agent")
	}
	return out, nil
}

func (s *Service) newEnrolToken() (token string, hash []byte, expires time.Time, err error) {
	sec, err := randomSecret()
	if err != nil {
		return "", nil, time.Time{}, err
	}
	token = EnrolPrefix + sec
	return token, hashToken(token), s.now().Add(EnrolTokenTTL).Truncate(time.Microsecond), nil
}

// UpdateAgent renames an agent (If-Match: expectedVersion).
func (s *Service) UpdateAgent(ctx context.Context, orgID, agentID uuid.UUID, expectedVersion int, name *string) (Agent, error) {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return Agent{}, err
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if err := validName(trimmed); err != nil {
			return Agent{}, err
		}
		name = &trimmed
	}
	var out Agent
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := s.loadAgent(ctx, tx, orgID, agentID, true)
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermManage, before.SiteID); err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return errVersion
		}
		if name == nil || *name == before.Name {
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE agent SET name = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, agentID, *name); err != nil {
			return err
		}
		diff, err := audit.Diff("agent", map[string]any{"name": before.Name}, map[string]any{"name": *name})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &before.SiteID, Action: "access.agent_update", TargetType: "agent", TargetID: agentID, Diff: diff}); err != nil {
			return err
		}
		out, err = s.loadAgent(ctx, tx, orgID, agentID, false)
		return err
	})
	if err != nil {
		return Agent{}, dbProblem(err, "agent")
	}
	return out, nil
}

// ReenrolAgent issues a new enrolment token and invalidates the agent's
// current token: the agent goes back to pending and a connected one is
// revoked (permission access.manage).
func (s *Service) ReenrolAgent(ctx context.Context, orgID, agentID uuid.UUID) (AgentEnrolment, error) {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return AgentEnrolment{}, err
	}
	var out AgentEnrolment
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, err := s.loadAgent(ctx, tx, orgID, agentID, true)
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermManage, a.SiteID); err != nil {
			return err
		}
		tok, hash, exp, err := s.newEnrolToken()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent SET status = 'pending', enrol_token_hash = $3, enrol_expires_at = $4, enrol_used_at = NULL,
				token_hash = NULL, previous_token_hash = NULL, previous_valid_until = NULL, rotation_sealed = NULL,
				revoked_at = NULL, revoked_by = NULL, rotate_now = false, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, agentID, hash, exp); err != nil {
			return err
		}
		diff, err := audit.Diff("agent", map[string]any{"status": a.Status}, map[string]any{"status": "pending"})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &a.SiteID, Action: "access.agent_reenrol", TargetType: "agent", TargetID: agentID, Diff: diff}); err != nil {
			return err
		}
		a, err = s.loadAgent(ctx, tx, orgID, agentID, false)
		out = AgentEnrolment{Agent: a, EnrolmentToken: tok, EnrolmentExpiresAt: exp}
		return err
	})
	if err != nil {
		return AgentEnrolment{}, dbProblem(err, "agent")
	}
	s.hub.Revoke(agentID)
	return out, nil
}

// RevokeAgent revokes an agent: a connected agent receives `command revoked`
// and the socket closes with 4401; any later connection with its token is told
// the same. It needs access.manage and a recent re-authentication.
func (s *Service) RevokeAgent(ctx context.Context, orgID, agentID uuid.UUID) (Agent, error) {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return Agent{}, err
	}
	if p := authz.PrincipalFromContext(ctx); p != nil {
		if err := p.RequireReauth(s.now()); err != nil {
			return Agent{}, err
		}
	}
	var out Agent
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, err := s.loadAgent(ctx, tx, orgID, agentID, true)
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermManage, a.SiteID); err != nil {
			return err
		}
		if a.Status == "revoked" {
			return problem.Conflict("the agent is already revoked")
		}
		var by *uuid.UUID
		if p := authz.PrincipalFromContext(ctx); p != nil {
			u := p.UserID
			by = &u
		}
		if _, err := tx.Exec(ctx, `UPDATE agent SET status = 'revoked', revoked_at = now(), revoked_by = $3,
				enrol_token_hash = NULL, rotate_now = false, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, agentID, by); err != nil {
			return err
		}
		// its pending commands will never be delivered
		if _, err := tx.Exec(ctx, `UPDATE agent_command SET status = 'expired', completed_at = now(), updated_at = now()
			WHERE org_id = $1 AND agent_id = $2 AND status IN ('pending', 'delivered')`, orgID, agentID); err != nil {
			return err
		}
		diff, err := audit.Diff("agent", map[string]any{"status": a.Status}, map[string]any{"status": "revoked"})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &a.SiteID, Action: "access.agent_revoke", TargetType: "agent", TargetID: agentID, Diff: diff}); err != nil {
			return err
		}
		out, err = s.loadAgent(ctx, tx, orgID, agentID, false)
		return err
	})
	if err != nil {
		return Agent{}, dbProblem(err, "agent")
	}
	s.hub.Revoke(agentID)
	return out, nil
}

// ForceRotation sets rotate_now: the agent's next hello asks it to rotate its
// token (permission access.manage).
func (s *Service) ForceRotation(ctx context.Context, orgID, agentID uuid.UUID) (Agent, error) {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return Agent{}, err
	}
	var out Agent
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, err := s.loadAgent(ctx, tx, orgID, agentID, true)
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermManage, a.SiteID); err != nil {
			return err
		}
		if a.Status != "active" {
			return problem.Conflict("only an active agent can be asked to rotate its token")
		}
		if _, err := tx.Exec(ctx, `UPDATE agent SET rotate_now = true, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, agentID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &a.SiteID, Action: "access.agent_rotate_requested", TargetType: "agent", TargetID: agentID}); err != nil {
			return err
		}
		out, err = s.loadAgent(ctx, tx, orgID, agentID, false)
		return err
	})
	if err != nil {
		return Agent{}, dbProblem(err, "agent")
	}
	return out, nil
}

// ---- agent side: enrolment, authentication, rotation ------------------------

// notFoundEnrol is the one answer for an unknown, expired or used token.
func notFoundEnrol() error {
	return problem.NotFound("the enrolment token is unknown, expired or already used")
}

// Enrol exchanges the one-time enrolment token for an agent token. Every
// failure to identify a usable token answers the same 404 body.
func (s *Service) Enrol(ctx context.Context, req protocol.EnrolRequest, ip string) (protocol.EnrolResponse, error) {
	if ok, wait := s.enrolLimit.Allow(ip); !ok {
		return protocol.EnrolResponse{}, problem.RateLimited(wait)
	}
	var fields []problem.Field
	if !strings.HasPrefix(req.EnrolmentToken, EnrolPrefix) || len(req.EnrolmentToken) > 256 {
		fields = append(fields, problem.Field{Field: "enrolment_token", Code: "invalid_format"})
	}
	for name, v := range map[string]string{"hostname": req.Hostname, "platform": req.Platform, "agent_version": req.AgentVersion} {
		if len(v) > 200 {
			fields = append(fields, problem.Field{Field: name, Code: "too_long"})
		}
	}
	if len(fields) > 0 {
		return protocol.EnrolResponse{}, problem.Validation(fields...)
	}
	wsURL, pollURL, err := s.publicURLs()
	if err != nil {
		return protocol.EnrolResponse{}, err
	}
	hash := hashToken(req.EnrolmentToken)
	var orgID, agentID uuid.UUID
	found := false
	err = s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT org_id, agent_id FROM access_enrol_agent($1)`, hash).Scan(&orgID, &agentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return protocol.EnrolResponse{}, problem.Internal(fmt.Errorf("enrol lookup: %w", err))
	}
	if !found {
		return protocol.EnrolResponse{}, notFoundEnrol()
	}
	token, err := newAgentToken(agentID)
	if err != nil {
		return protocol.EnrolResponse{}, problem.Internal(err)
	}
	now := s.now()
	var resp protocol.EnrolResponse
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var siteID uuid.UUID
		var stored []byte
		var expires *time.Time
		var status string
		err := tx.QueryRow(ctx, `SELECT site_id, status, enrol_token_hash, enrol_expires_at FROM agent WHERE org_id = $1 AND id = $2 FOR UPDATE`,
			orgID, agentID).Scan(&siteID, &status, &stored, &expires)
		if err != nil {
			return errNoEnrol
		}
		if status != "pending" || len(stored) == 0 || subtle.ConstantTimeCompare(stored, hash) != 1 || expires == nil || !expires.After(now) {
			return errNoEnrol
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, false)
		if err != nil {
			return errNoEnrol
		}
		rotateBy := now.Add(RotateEvery)
		if _, err := tx.Exec(ctx, `UPDATE agent SET status = 'active', enrol_token_hash = NULL, enrol_used_at = $3, enrolled_at = $3,
				token_hash = $4, previous_token_hash = NULL, previous_valid_until = NULL, rotation_sealed = NULL, rotate_by = $5, rotate_now = false,
				hostname = NULLIF($6, ''), platform = NULLIF($7, ''), agent_version = NULLIF($8, ''), updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, agentID, now, hashToken(token), rotateBy, req.Hostname, req.Platform, req.AgentVersion); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, ActorKind: audit.ActorSystem, Action: "access.agent_enrol", TargetType: "agent", TargetID: agentID}); err != nil {
			return err
		}
		resp = protocol.EnrolResponse{
			AgentID: agentID.String(), AgentToken: token, OrgID: orgID.String(), SiteID: site.ID.String(),
			ServerTime: protocol.At(now), WSURL: wsURL, PollURL: pollURL, ProtocolVersions: []int{protocol.Version},
		}
		return nil
	})
	if errors.Is(err, errNoEnrol) {
		return protocol.EnrolResponse{}, notFoundEnrol()
	}
	if err != nil {
		return protocol.EnrolResponse{}, problem.Internal(fmt.Errorf("enrol: %w", err))
	}
	return resp, nil
}

var errNoEnrol = errors.New("enrolment token not usable")

// AgentPrincipal is an authenticated agent: the token resolves to its
// organization, site and id, and nothing in the protocol names another site.
type AgentPrincipal struct {
	OrgID, SiteID, AgentID uuid.UUID
	Status                 string
	// ViaPrevious is true when the retired token (inside its 10-minute window) was presented.
	ViaPrevious bool
}

// ErrUnknownToken is the one error of authentication: the answer is a bare 401.
var ErrUnknownToken = errors.New("unknown agent token")

// AuthenticateAgent resolves an agent bearer token. A revoked agent
// authenticates (its status says so) so that it can be told with `command
// revoked`; only an unknown, rotated-out or malformed token is an error.
func (s *Service) AuthenticateAgent(ctx context.Context, bearer string) (AgentPrincipal, error) {
	agentID, ok := parseAgentToken(bearer)
	if !ok {
		return AgentPrincipal{}, ErrUnknownToken
	}
	var org *uuid.UUID // access_agent_org answers NULL for an unknown agent
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT access_agent_org($1)`, agentID).Scan(&org)
	})
	if err != nil {
		return AgentPrincipal{}, fmt.Errorf("agent lookup: %w", err)
	}
	if org == nil {
		return AgentPrincipal{}, ErrUnknownToken
	}
	orgID := *org
	presented := hashToken(bearer)
	now := s.now()
	var p AgentPrincipal
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var cur, prev []byte
		var prevUntil *time.Time
		err := tx.QueryRow(ctx, `SELECT site_id, status, token_hash, previous_token_hash, previous_valid_until FROM agent WHERE org_id = $1 AND id = $2`,
			orgID, agentID).Scan(&p.SiteID, &p.Status, &cur, &prev, &prevUntil)
		if err != nil {
			return ErrUnknownToken
		}
		switch {
		case len(cur) > 0 && subtle.ConstantTimeCompare(cur, presented) == 1:
		case len(prev) > 0 && subtle.ConstantTimeCompare(prev, presented) == 1 && prevUntil != nil && now.Before(*prevUntil):
			p.ViaPrevious = true
		default:
			return ErrUnknownToken
		}
		if p.Status == "pending" {
			return ErrUnknownToken
		}
		p.OrgID, p.AgentID = orgID, agentID
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrUnknownToken) {
			return AgentPrincipal{}, ErrUnknownToken
		}
		return AgentPrincipal{}, fmt.Errorf("agent authentication: %w", err)
	}
	return p, nil
}

// RotateToken issues a new agent token. The retired token stays valid for 10
// minutes, and repeating the call with it inside that window returns the same
// new token (idempotent), so a crash between steps recovers.
func (s *Service) RotateToken(ctx context.Context, p AgentPrincipal) (protocol.RotateResponse, error) {
	if p.Status != "active" {
		return protocol.RotateResponse{}, ErrUnknownToken
	}
	if s.cfg.Sealer == nil {
		return protocol.RotateResponse{}, problem.Internal(errors.New("access: no sealer configured for token rotation"))
	}
	now := s.now()
	var out protocol.RotateResponse
	err := s.db.InTenantTx(ctx, p.OrgID, func(tx pgx.Tx) error {
		var sealed []byte
		var prevUntil, rotateBy *time.Time
		if err := tx.QueryRow(ctx, `SELECT rotation_sealed, previous_valid_until, rotate_by FROM agent WHERE org_id = $1 AND id = $2 FOR UPDATE`,
			p.OrgID, p.AgentID).Scan(&sealed, &prevUntil, &rotateBy); err != nil {
			return err
		}
		aad := []byte(p.AgentID.String())
		if p.ViaPrevious {
			if len(sealed) == 0 || prevUntil == nil || !now.Before(*prevUntil) || rotateBy == nil {
				return ErrUnknownToken
			}
			plain, err := s.cfg.Sealer.Open(sealed, aad)
			if err != nil {
				return err
			}
			out = protocol.RotateResponse{AgentToken: string(plain), RotateBy: protocol.At(*rotateBy), PreviousValidUntil: protocol.At(*prevUntil)}
			return nil
		}
		next, err := newAgentToken(p.AgentID)
		if err != nil {
			return err
		}
		seal, err := s.cfg.Sealer.Seal([]byte(next), aad)
		if err != nil {
			return err
		}
		until := now.Add(RotationWindow).Truncate(time.Microsecond)
		newRotateBy := now.Add(RotateEvery).Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `UPDATE agent SET previous_token_hash = token_hash, previous_valid_until = $3, token_hash = $4, rotation_sealed = $5,
				rotate_by = $6, rotate_now = false, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			p.OrgID, p.AgentID, until, hashToken(next), seal, newRotateBy); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: p.OrgID, SiteID: &p.SiteID, ActorKind: audit.ActorSystem, Action: "access.agent_rotate", TargetType: "agent", TargetID: p.AgentID}); err != nil {
			return err
		}
		out = protocol.RotateResponse{AgentToken: next, RotateBy: protocol.At(newRotateBy), PreviousValidUntil: protocol.At(until)}
		return nil
	})
	if errors.Is(err, ErrUnknownToken) {
		return protocol.RotateResponse{}, ErrUnknownToken
	}
	if err != nil {
		return protocol.RotateResponse{}, problem.Internal(fmt.Errorf("rotate token: %w", err))
	}
	return out, nil
}
