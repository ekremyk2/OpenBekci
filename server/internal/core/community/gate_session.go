package community

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// PIN attempt limits (per person and per console, see community.createGuardSession).
const (
	personFailLimit  = 5
	personFailWindow = 15 * time.Minute
	consoleFailLimit = 20
	consoleFailWin   = time.Hour
	// GuardSessionTTL is how long a guard session lasts.
	GuardSessionTTL = 12 * time.Hour
)

var pinRE = regexp.MustCompile(`^\d{4,8}$`)

// GuardRef is a guard of a site as the booth shows it: name and id only.
type GuardRef struct {
	PersonID    uuid.UUID `json:"person_id"`
	DisplayName string    `json:"display_name"`
	HasPIN      bool      `json:"has_pin"`
}

// GuardSession is a guard at the shared booth device.
type GuardSession struct {
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expires_at"`
	PersonID    uuid.UUID `json:"person_id"`
	DisplayName string    `json:"display_name"`
}

// guardCtx says who acts at the console: the guard of the session, or nobody
// extra (the console login is then the actor).
type guardCtx struct {
	Person *uuid.UUID
	Name   string
}

// consoleID identifies the console login session a guard session is bound to.
func consoleID(ctx context.Context) uuid.UUID {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return uuid.Nil
	}
	if p.FamilyID != uuid.Nil {
		return p.FamilyID
	}
	return p.SessionID
}

// activeOperator reports whether the person holds a membership that carries
// gate.operate at the site (a guard session never grants more than the
// console login: the guard must still be a guard).
func activeOperator(ctx context.Context, tx pgx.Tx, orgID, siteID, personID uuid.UUID, roles []string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM membership
		WHERE org_id = $1 AND person_id = $2 AND role = ANY($3) AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())
		  AND (scope_type = 'org' OR (scope_type = 'site' AND scope_id = $4)))`, orgID, personID, roles, siteID).Scan(&ok)
	return ok, err
}

func operatorRoles() []string {
	out := make([]string, len(gateRoles))
	for i, r := range gateRoles {
		out[i] = string(r)
	}
	return out
}

// guardFor resolves the X-Guard-Session token of a request at a site. No token
// means the console login principal is the actor. An unknown, expired or ended
// token is 401 invalid_grant; a token of another console login or another
// site is 403 insufficient_permission.
func (s *Service) guardFor(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, token string) (guardCtx, error) {
	if token == "" {
		return guardCtx{}, nil
	}
	var (
		sessionSite, console, person uuid.UUID
		name                         string
	)
	err := tx.QueryRow(ctx, `SELECT g.site_id, g.console_id, g.person_id, p.display_name FROM guard_session g
		JOIN person p ON p.org_id = g.org_id AND p.id = g.person_id
		WHERE g.org_id = $1 AND g.token_hash = $2 AND g.ended_at IS NULL AND g.expires_at > now()`, orgID, crypto.HashToken(token)).Scan(&sessionSite, &console, &person, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return guardCtx{}, problem.New(401, problem.CodeInvalidGrant, "the guard session is not valid; pick your name and enter your PIN again")
	}
	if err != nil {
		return guardCtx{}, err
	}
	if console != consoleID(ctx) || sessionSite != siteID {
		return guardCtx{}, problem.Forbidden("the guard session belongs to another console or site")
	}
	ok, err := activeOperator(ctx, tx, orgID, siteID, person, operatorRoles())
	if err != nil {
		return guardCtx{}, err
	}
	if !ok {
		return guardCtx{}, problem.New(401, problem.CodeInvalidGrant, "the guard session is not valid; pick your name and enter your PIN again")
	}
	return guardCtx{Person: &person, Name: name}, nil
}

// gateAt checks gate.operate at the site and resolves the guard session.
func (s *Service) gateAt(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, token string) (guardCtx, error) {
	if _, err := requireAt(ctx, tx, PermGateOperate, siteID); err != nil {
		return guardCtx{}, err
	}
	return s.guardFor(ctx, tx, orgID, siteID, token)
}

// gateAudit records a gate action, attributed to the guard of the session.
func gateAudit(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, g guardCtx, action, targetType string, targetID uuid.UUID, extra audit.Changes) error {
	diff := audit.Changes{}
	for k, v := range extra {
		diff[k] = v
	}
	if g.Person != nil {
		diff["guard_person_id"] = audit.Change{After: mustJSON(g.Person.String())}
	}
	return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: action, TargetType: targetType, TargetID: targetID, Diff: diff})
}

// ListGuards lists the guards of a site by name and id (gate.operate): the
// booth shows them as buttons.
func (s *Service) ListGuards(ctx context.Context, orgID, siteID uuid.UUID, p tenancy.PageParams) ([]GuardRef, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []GuardRef
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireAt(ctx, tx, PermGateOperate, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT p.id, p.display_name, EXISTS (SELECT 1 FROM gate_pin gp WHERE gp.org_id = p.org_id AND gp.person_id = p.id)
			FROM person p WHERE p.org_id = $1 AND p.id > $2 AND p.id IN (
				SELECT m.person_id FROM membership m WHERE m.org_id = $1 AND m.role = 'guard' AND m.valid_from <= now() AND (m.valid_to IS NULL OR m.valid_to > now())
				  AND (m.scope_type = 'org' OR (m.scope_type = 'site' AND m.scope_id = $3)))
			ORDER BY p.id LIMIT $4`, orgID, after, siteID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g GuardRef
			if err := rows.Scan(&g.PersonID, &g.DisplayName, &g.HasPIN); err != nil {
				return err
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list guards")
	}
	items, next := tenancy.Page(out, limit, func(g GuardRef) uuid.UUID { return g.PersonID })
	return items, next, nil
}

// SetGuardPin sets or resets a guard's PIN (gate.manage for every guard
// membership of the person); it also clears a lock.
func (s *Service) SetGuardPin(ctx context.Context, orgID, personID uuid.UUID, pin string) error {
	if !pinRE.MatchString(pin) {
		return validation("pin", "invalid_format")
	}
	hash, err := crypto.HashPasswordContext(ctx, pin)
	if err != nil {
		return problem.Internal(err)
	}
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM person WHERE org_id = $1 AND id = $2)`, orgID, personID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return problem.NotFound("no such person")
		}
		rows, err := tx.Query(ctx, `SELECT scope_type, scope_id FROM membership WHERE org_id = $1 AND person_id = $2 AND role = 'guard' AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())`, orgID, personID)
		if err != nil {
			return err
		}
		type scopeRow struct {
			typ string
			id  *uuid.UUID
		}
		var scopes []scopeRow
		for rows.Next() {
			var r scopeRow
			if err := rows.Scan(&r.typ, &r.id); err != nil {
				rows.Close()
				return err
			}
			scopes = append(scopes, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(scopes) == 0 {
			return problem.Conflict("the person is not a guard")
		}
		var firstSite *uuid.UUID
		for _, sr := range scopes {
			switch sr.typ {
			case "org":
				if err := authz.Require(ctx, PermGateManage, authz.Scope{OrgID: orgID}); err != nil {
					return err
				}
			case "site":
				if _, err := requireAt(ctx, tx, PermGateManage, *sr.id); err != nil {
					return err
				}
				firstSite = sr.id
			default:
				return problem.Conflict("the person is not a guard")
			}
		}
		caller, _, err := callerPerson(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gate_pin (org_id, person_id, pin_hash, set_by) VALUES ($1, $2, $3, $4)
			ON CONFLICT (org_id, person_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, attempts_from = now(), set_by = EXCLUDED.set_by, updated_at = now()`,
			orgID, personID, hash, nilIfZero(caller)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: firstSite, Action: "gate.pin_set", TargetType: "person", TargetID: personID,
			Diff: audit.Changes{"pin": {After: mustJSON("set")}}})
	})
	return tenancy.DBProblem(err, "guard pin")
}

// CreateGuardSession switches the guard at the shared console (gate.operate):
// the guard picked their name and typed the PIN. Everything wrong about it
// answers the same invalid_grant (wrong PIN, unknown person, no PIN, not a
// guard of the site). 5 failures in 15 minutes lock that guard for 15 minutes
// and 20 failures from one console in an hour lock the console for an hour,
// 429 with Retry-After (also for a correct PIN during the lock); every failed
// attempt is audited.
func (s *Service) CreateGuardSession(ctx context.Context, orgID, siteID, personID uuid.UUID, pin string) (GuardSession, error) {
	if !pinRE.MatchString(pin) {
		return GuardSession{}, validation("pin", "invalid_format")
	}
	now := s.now()
	console := consoleID(ctx)
	var out GuardSession
	var failure error
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := requireAt(ctx, tx, PermGateOperate, siteID); err != nil {
			return err
		}
		if _, err := loadSite(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		// locks, derived from the recent failures
		var pinHash *string
		var from time.Time
		err := tx.QueryRow(ctx, `SELECT pin_hash, attempts_from FROM gate_pin WHERE org_id = $1 AND person_id = $2`, orgID, personID).Scan(&pinHash, &from)
		if errors.Is(err, pgx.ErrNoRows) {
			pinHash, from = nil, time.Time{}
		} else if err != nil {
			return err
		}
		var lastOK *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(at) FROM gate_pin_attempt WHERE org_id = $1 AND person_id = $2 AND ok`, orgID, personID).Scan(&lastOK); err != nil {
			return err
		}
		since := now.Add(-personFailWindow)
		if from.After(since) {
			since = from
		}
		if lastOK != nil && lastOK.After(since) {
			since = *lastOK
		}
		var pn int
		var plast *time.Time
		if err := tx.QueryRow(ctx, `SELECT count(*), max(at) FROM gate_pin_attempt WHERE org_id = $1 AND person_id = $2 AND NOT ok AND at > $3`, orgID, personID, since).Scan(&pn, &plast); err != nil {
			return err
		}
		var cn int
		var clast *time.Time
		if err := tx.QueryRow(ctx, `SELECT count(*), max(at) FROM gate_pin_attempt WHERE org_id = $1 AND console_id = $2 AND NOT ok AND at > $3`, orgID, console, now.Add(-consoleFailWin)).Scan(&cn, &clast); err != nil {
			return err
		}
		var retry time.Duration
		if pn >= personFailLimit && plast != nil {
			retry = plast.Add(personFailWindow).Sub(now)
		}
		if cn >= consoleFailLimit && clast != nil {
			if r := clast.Add(consoleFailWin).Sub(now); r > retry {
				retry = r
			}
		}
		if retry > 0 {
			failure = problem.RateLimited(retry)
			return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "gate.pin_locked", TargetType: "person", TargetID: personID})
		}
		isGuard, err := activeOperator(ctx, tx, orgID, siteID, personID, []string{string(authz.RoleGuard)})
		if err != nil {
			return err
		}
		valid := false
		if pinHash != nil && isGuard {
			valid, err = crypto.VerifyPasswordContext(ctx, pin, *pinHash)
			if err != nil {
				return err
			}
		} else if err := crypto.DummyVerifyContext(ctx, pin); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gate_pin_attempt (id, org_id, site_id, person_id, console_id, ok, at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id.New(), orgID, siteID, personID, console, valid, now); err != nil {
			return err
		}
		if !valid {
			failure = problem.New(401, problem.CodeInvalidGrant, "the guard or the PIN is not valid")
			return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "gate.pin_failed", TargetType: "person", TargetID: personID})
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT display_name FROM person WHERE org_id = $1 AND id = $2`, orgID, personID).Scan(&name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE guard_session SET ended_at = now() WHERE org_id = $1 AND console_id = $2 AND ended_at IS NULL`, orgID, console); err != nil {
			return err
		}
		token, hash, err := crypto.NewToken()
		if err != nil {
			return err
		}
		sessionID := id.New()
		out = GuardSession{Token: token, ExpiresAt: now.Add(GuardSessionTTL).UTC(), PersonID: personID, DisplayName: name}
		if _, err := tx.Exec(ctx, `INSERT INTO guard_session (id, org_id, site_id, person_id, console_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			sessionID, orgID, siteID, personID, console, hash, out.ExpiresAt); err != nil {
			return err
		}
		return gateAudit(ctx, tx, orgID, siteID, guardCtx{Person: &personID, Name: name}, "gate.session_start", "guard_session", sessionID, nil)
	})
	if err != nil {
		return GuardSession{}, tenancy.DBProblem(err, "guard session")
	}
	if failure != nil {
		return GuardSession{}, failure
	}
	return out, nil
}
