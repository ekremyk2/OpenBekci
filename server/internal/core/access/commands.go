package access

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// OpenRequest is the optional body of the manual open.
type OpenRequest struct {
	VisitorEntryID *uuid.UUID
	CredentialID   *uuid.UUID
}

// Command is an agent command as the API shows it (status of an open).
type Command struct {
	ID          uuid.UUID  `json:"id"`
	PointID     *uuid.UUID `json:"point_id"`
	Command     string     `json:"command"`
	Status      string     `json:"status"`
	Reason      *string    `json:"reason"`
	IssuedAt    time.Time  `json:"issued_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// OpenPoint queues a manual open of an access point for its agent (permission
// access.open). It fails at once with 409 agent_offline when the agent is not
// connected (a live session or a frame within 90 seconds) or its clock is
// suspect, so the guard sees a visible failure instead of a spinner. The
// command lives 15 seconds, is audited with the actor, and is never retried:
// a failed or interrupted result means the outcome is unknown.
func (s *Service) OpenPoint(ctx context.Context, orgID, pointID uuid.UUID, req OpenRequest, guardToken string) (Command, error) {
	if err := authz.RequireAnywhere(ctx, PermOpen); err != nil {
		return Command{}, err
	}
	p := authz.PrincipalFromContext(ctx)
	var out Command
	var agentID, siteID uuid.UUID
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		pt, err := scanPoint(tx.QueryRow(ctx, `SELECT `+pointColumns+` FROM access_point WHERE org_id = $1 AND id = $2`, orgID, pointID))
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermOpen, pt.SiteID); err != nil {
			return err
		}
		siteID = pt.SiteID
		if !pt.Enabled {
			return problem.Conflict("the access point is disabled")
		}
		if pt.AgentID == nil {
			return problem.New(409, problem.CodeAgentOffline, "the access point has no agent assigned")
		}
		agentID = *pt.AgentID
		ag, err := s.loadAgent(ctx, tx, orgID, agentID, false)
		if err != nil {
			return err
		}
		if ag.Status != "active" || !ag.Connected {
			return problem.New(409, problem.CodeAgentOffline, "the agent of this access point is offline; open it by hand")
		}
		if ag.ClockSuspect {
			return problem.New(409, problem.CodeAgentOffline, "the agent's clock is not trustworthy; open it by hand")
		}
		if ag.ClockOffsetMS != nil {
			if off := time.Duration(*ag.ClockOffsetMS) * time.Millisecond; off > ClockBlockOffset || off < -ClockBlockOffset {
				return problem.New(409, problem.CodeAgentOffline, "the agent's clock is off by more than a day; open it by hand")
			}
		}
		actorID, display, err := s.resolveActor(ctx, tx, orgID, siteID, p, guardToken)
		if err != nil {
			return err
		}
		if req.CredentialID != nil {
			var one int
			if err := tx.QueryRow(ctx, `SELECT 1 FROM credential WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, siteID, *req.CredentialID).Scan(&one); err != nil {
				return problem.Validation(problem.Field{Field: "credential_id", Code: "invalid_value"})
			}
		}
		if ok, wait := s.openUser.Allow(actorID.String()); !ok {
			return problem.RateLimited(wait)
		}
		if ok, wait := s.openPoint.Allow(pointID.String()); !ok {
			return problem.RateLimited(wait)
		}
		if ok, wait := s.openSite.Allow(siteID.String()); !ok {
			return problem.RateLimited(wait)
		}
		now := s.now().Truncate(time.Microsecond)
		issued, expires := protocol.At(now), protocol.At(now.Add(OpenTTL))
		cmd := protocol.Command{
			Command: protocol.CmdOpenPoint, PointID: pointID.String(),
			Actor: &protocol.Actor{UserID: actorID.String(), Display: display}, IssuedAt: &issued, ExpiresAt: &expires,
		}
		if req.CredentialID != nil {
			cmd.CredentialID = req.CredentialID.String()
		}
		if req.VisitorEntryID != nil {
			cmd.VisitorEntryID = req.VisitorEntryID.String()
		}
		payload, err := json.Marshal(cmd)
		if err != nil {
			return err
		}
		cid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO agent_command (id, org_id, site_id, agent_id, point_id, command, payload, actor_user_id, actor_display, issued_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'open_point', $6, $7, NULLIF($8, ''), $9, $10)`,
			cid, orgID, siteID, agentID, pointID, payload, actorID, display, issued.Time, expires.Time); err != nil {
			return err
		}
		diff, err := audit.Diff("agent_command", nil, map[string]any{"point_id": pointID, "agent_id": agentID, "command_id": cid, "actor_user_id": actorID})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "access.open_requested", TargetType: "agent_command", TargetID: cid, Diff: diff}); err != nil {
			return err
		}
		out = Command{ID: cid, PointID: &pointID, Command: protocol.CmdOpenPoint, Status: "pending", IssuedAt: issued.Time, ExpiresAt: expires.Time}
		return nil
	})
	if err != nil {
		return Command{}, dbProblem(err, "access point")
	}
	s.hub.NotifyAgent(agentID)
	return out, nil
}

// resolveActor names who the opening is attributed to: the guard of the gate
// console's session when one is given (and a resolver is wired), else the
// signed-in user. The server stamps this on the event; the agent's claim is never used.
func (s *Service) resolveActor(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, p *authz.Principal, guardToken string) (uuid.UUID, string, error) {
	if guardToken != "" && s.cfg.Guards != nil {
		uid, display, err := s.cfg.Guards.ResolveGuard(ctx, orgID, siteID, guardToken)
		if err != nil {
			return uuid.Nil, "", problem.Unauthenticated("the guard session is not valid")
		}
		return uid, display, nil
	}
	if p == nil {
		return uuid.Nil, "", problem.Unauthenticated("authentication is required")
	}
	var display *string
	err := tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id = $1 AND user_id = $2 LIMIT 1`, orgID, p.UserID).Scan(&display)
	if err != nil && err != pgx.ErrNoRows {
		return uuid.Nil, "", err
	}
	name := ""
	if display != nil {
		name = *display
	}
	return p.UserID, name, nil
}

// GetCommand returns the status of a command (the console polls it every
// 500 ms): pending, delivered, done, failed or expired with the reason.
func (s *Service) GetCommand(ctx context.Context, orgID, commandID uuid.UUID) (Command, error) {
	if err := authz.RequireAnywhere(ctx, PermOpen); err != nil {
		return Command{}, err
	}
	var c Command
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var siteID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT site_id FROM agent_command WHERE org_id = $1 AND id = $2`, orgID, commandID).Scan(&siteID); err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermOpen, siteID); err != nil {
			return err
		}
		// A command nobody picked up in time is expired as soon as somebody asks.
		if _, err := tx.Exec(ctx, `UPDATE agent_command SET status = 'expired', completed_at = $3, updated_at = now(),
				result = COALESCE(result, '{"status":"expired","reason":"not_delivered"}')
			WHERE org_id = $1 AND id = $2 AND status = 'pending' AND expires_at <= $3`, orgID, commandID, s.now()); err != nil {
			return err
		}
		var result []byte
		if err := tx.QueryRow(ctx, `SELECT id, point_id, command, status, result, issued_at, expires_at, completed_at FROM agent_command WHERE org_id = $1 AND id = $2`,
			orgID, commandID).Scan(&c.ID, &c.PointID, &c.Command, &c.Status, &result, &c.IssuedAt, &c.ExpiresAt, &c.CompletedAt); err != nil {
			return err
		}
		if len(result) > 0 {
			var r struct {
				Reason string `json:"reason"`
				Status string `json:"status"`
			}
			if json.Unmarshal(result, &r) == nil {
				reason := r.Reason
				if reason == "" && c.Status != "done" {
					reason = r.Status
				}
				if reason != "" {
					c.Reason = &reason
				}
			}
		}
		c.IssuedAt, c.ExpiresAt = c.IssuedAt.UTC(), c.ExpiresAt.UTC()
		if c.CompletedAt != nil {
			t := c.CompletedAt.UTC()
			c.CompletedAt = &t
		}
		return nil
	})
	return c, dbProblem(err, "command")
}
