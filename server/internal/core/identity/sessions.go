package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Session kinds stored in session.kind.
const (
	KindWebCookie    = "web_cookie"
	KindAccessToken  = "access_token"
	KindRefreshToken = "refresh_token"
)

// WebSession is a freshly issued browser session.
type WebSession struct {
	Token     string // the cookie value; never stored, only its hash is
	SessionID uuid.UUID
	ExpiresAt time.Time
}

// TokenPair is a freshly issued mobile or integration session.
type TokenPair struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	SessionID        uuid.UUID
}

type sessionInsert struct {
	userID, family uuid.UUID
	rotatedFrom    *uuid.UUID
	kind           string
	expires        time.Time
	m              Meta
}

func (s *Service) insertSession(ctx context.Context, tx pgx.Tx, in sessionInsert) (token string, rowID uuid.UUID, err error) {
	token, hash, err := crypto.NewToken()
	if err != nil {
		return "", uuid.Nil, err
	}
	rowID = id.New()
	if in.kind == KindWebCookie {
		in.family = rowID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO session (id, user_id, kind, token_hash, family_id, rotated_from, device_name, ip, user_agent, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		rowID, in.userID, in.kind, hash, in.family, in.rotatedFrom, nullable(clip(in.m.DeviceName, 128)),
		nullable(clip(in.m.IP, 64)), nullable(clip(in.m.UserAgent, 512)), s.now(), in.expires)
	return token, rowID, err
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// IssueWebSession creates a browser session for the user. The caller sets the
// cookie.
func (s *Service) IssueWebSession(ctx context.Context, userID uuid.UUID, m Meta) (WebSession, error) {
	exp := s.now().Add(s.cfg.WebSessionTTL)
	var out WebSession
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tok, rowID, err := s.insertSession(ctx, tx, sessionInsert{userID: userID, kind: KindWebCookie, expires: exp, m: m})
		out = WebSession{Token: tok, SessionID: rowID, ExpiresAt: exp}
		return err
	})
	if err != nil {
		return WebSession{}, problem.Internal(fmt.Errorf("issue web session: %w", err))
	}
	return out, nil
}

// IssueTokens creates a new session family: an access token (15 minutes) and
// a rotating refresh token (60 days).
func (s *Service) IssueTokens(ctx context.Context, userID uuid.UUID, m Meta) (TokenPair, error) {
	family := id.New()
	var out TokenPair
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.insertPair(ctx, tx, userID, family, nil, m)
		return err
	})
	if err != nil {
		return TokenPair{}, problem.Internal(fmt.Errorf("issue tokens: %w", err))
	}
	return out, nil
}

// Authenticate resolves a presented token. kind must be KindWebCookie for a
// cookie and KindAccessToken for a bearer token: a refresh token never
// authenticates API calls. It touches last_used_at at most every five minutes.
func (s *Service) Authenticate(ctx context.Context, kind, token string) (*authz.Principal, error) {
	now := s.now()
	var p authz.Principal
	var reauth, lastUsed *time.Time
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT s.id, s.family_id, s.user_id, s.reauth_until, s.last_used_at, u.is_platform_admin
			FROM session s JOIN "user" u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.kind = $2 AND s.revoked_at IS NULL AND s.expires_at > $3 AND u.status = 'active'`,
			crypto.HashToken(token), kind, now).Scan(&p.SessionID, &p.FamilyID, &p.UserID, &reauth, &lastUsed, &p.IsPlatformAdmin)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, problem.Unauthenticated("the session is missing, expired or revoked")
	}
	if err != nil {
		return nil, problem.Internal(fmt.Errorf("authenticate: %w", err))
	}
	p.ID = p.UserID
	p.ViaCookie = kind == KindWebCookie
	if reauth != nil {
		p.ReauthUntil = *reauth
	}
	if lastUsed == nil || now.Sub(*lastUsed) >= touchInterval {
		if err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE session SET last_used_at = $2 WHERE id = $1`, p.SessionID, now)
			return err
		}); err != nil {
			s.log.WarnContext(ctx, "touch session", "error", err)
		}
	}
	return &p, nil
}

// RevokeSession revokes the session family familyID of the user. It reports
// whether such an open session existed.
func (s *Service) RevokeSession(ctx context.Context, userID, familyID uuid.UUID) (bool, error) {
	var n int64
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE session SET revoked_at = $3 WHERE user_id = $1 AND family_id = $2 AND revoked_at IS NULL`, userID, familyID, s.now())
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		if n == 0 {
			return nil
		}
		return audit.Record(ctx, tx, audit.Event{Action: "session.revoke", TargetType: "session", TargetID: familyID})
	})
	if err != nil {
		return false, problem.Internal(fmt.Errorf("revoke session: %w", err))
	}
	return n > 0, nil
}

// RevokeAllSessions revokes every session of the user, the current one included.
func (s *Service) RevokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE session SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, s.now())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "session.revoke_all", TargetType: "user", TargetID: userID})
	})
	if err != nil {
		return problem.Internal(fmt.Errorf("revoke all sessions: %w", err))
	}
	return nil
}

// SessionInfo is one open session as listed to its owner. ID is the family id.
type SessionInfo struct {
	ID         uuid.UUID
	Kind       string // KindWebCookie or KindRefreshToken (a mobile session)
	DeviceName *string
	IP         *string
	UserAgent  *string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  time.Time
	Current    bool
}

// ListSessions returns the user's open sessions, most recently used first.
func (s *Service) ListSessions(ctx context.Context, userID, currentFamily uuid.UUID) ([]SessionInfo, error) {
	now := s.now()
	out := []SessionInfo{}
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT s.family_id, s.kind, s.device_name, s.ip, s.user_agent, s.expires_at,
			       (SELECT min(f.created_at) FROM session f WHERE f.family_id = s.family_id),
			       (SELECT max(f.last_used_at) FROM session f WHERE f.family_id = s.family_id)
			FROM session s
			WHERE s.user_id = $1 AND s.kind IN ('web_cookie', 'refresh_token') AND s.revoked_at IS NULL AND s.expires_at > $2
			ORDER BY 8 DESC NULLS LAST, 7 DESC`, userID, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var si SessionInfo
			if err := rows.Scan(&si.ID, &si.Kind, &si.DeviceName, &si.IP, &si.UserAgent, &si.ExpiresAt, &si.CreatedAt, &si.LastUsedAt); err != nil {
				return err
			}
			si.Current = si.ID == currentFamily
			out = append(out, si)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, problem.Internal(fmt.Errorf("list sessions: %w", err))
	}
	return out, nil
}

// ReauthMethod is how a caller proves it is still the user.
type ReauthMethod string

// Re-authentication methods.
const (
	ReauthPassword ReauthMethod = "password"
	ReauthSMSOTP   ReauthMethod = "sms_otp"
	// ReauthEmailCode is a code mailed to the user's own address by
	// RequestReauthEmailCode.
	ReauthEmailCode ReauthMethod = "email_code"
)

// Reauth verifies the caller's password, a fresh SMS code (sent to the user's
// own phone through RequestOTP) or a fresh email code (RequestReauthEmailCode)
// and opens the re-authentication window on the current session. It returns
// when the window ends.
func (s *Service) Reauth(ctx context.Context, p *authz.Principal, method ReauthMethod, secret string, m Meta) (time.Time, error) {
	ident := "uid:" + p.UserID.String()
	if err := s.ipGate(m.IP); err != nil {
		return time.Time{}, err
	}
	attemptID, err := s.beginAttempt(ctx, ident, methodReauth, m.IP, &p.UserID)
	if err != nil {
		return time.Time{}, err
	}
	var u User
	var hash *string
	if err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, err = s.loadUser(ctx, tx, p.UserID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT password_hash FROM "user" WHERE id = $1`, p.UserID).Scan(&hash)
	}); err != nil {
		return time.Time{}, problem.Internal(fmt.Errorf("load user: %w", err))
	}
	good := false
	switch method {
	case ReauthPassword:
		ok, err := s.verifyPassword(ctx, attemptID, secret, hash, &p.UserID)
		if err != nil {
			return time.Time{}, err
		}
		good = ok && hash != nil
	case ReauthSMSOTP:
		if u.Phone != nil {
			uid, ok, err := s.checkOTP(ctx, *u.Phone, secret)
			if err != nil {
				return time.Time{}, problem.Internal(fmt.Errorf("check otp: %w", err))
			}
			good = ok && uid == p.UserID
		}
	case ReauthEmailCode:
		if u.Email != nil {
			uid, ok, err := s.checkCode(ctx, "email", purposeReauth, *u.Email, secret)
			if err != nil {
				return time.Time{}, problem.Internal(fmt.Errorf("check email code: %w", err))
			}
			good = ok && uid == p.UserID
		}
	}
	if !good {
		return time.Time{}, errInvalidGrant()
	}
	s.recordSuccess(ctx, ident, methodReauth, m.IP, &p.UserID)
	until := s.now().Add(s.cfg.ReauthWindow)
	if err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE session SET reauth_until = $2 WHERE id = $1`, p.SessionID, until)
		return err
	}); err != nil {
		return time.Time{}, problem.Internal(fmt.Errorf("mark reauth: %w", err))
	}
	return until, nil
}

// ChangePassword sets (or replaces) the caller's password. It needs a recent
// re-authentication, and it signs the user out everywhere else.
func (s *Service) ChangePassword(ctx context.Context, p *authz.Principal, newPassword string) error {
	if err := p.RequireReauth(s.now()); err != nil {
		return err
	}
	if pe := ValidatePassword(newPassword); pe != nil {
		return pe
	}
	hash, err := crypto.HashPasswordContext(ctx, newPassword)
	if errors.Is(err, crypto.ErrBusy) {
		return problem.RateLimited(2 * time.Second)
	}
	if err != nil {
		return problem.Internal(err)
	}
	return s.updatePassword(ctx, p.UserID, hash, p.FamilyID, "user.password_change")
}

// Prune deletes expired sessions, spent or expired codes and old login
// attempts (retention 90 days; backend plan section 16). It returns how many
// rows went.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	now := s.now()
	var total int64
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		for _, q := range []struct {
			sql string
			arg time.Time
		}{
			// a revoked or expired token is kept a week so reuse of a rotated refresh token is still recognised
			{`DELETE FROM session WHERE COALESCE(revoked_at, expires_at) < $1`, now.Add(-7 * 24 * time.Hour)},
			{`DELETE FROM otp_code WHERE expires_at < $1`, now.Add(-24 * time.Hour)},
			{`DELETE FROM login_attempt WHERE at < $1`, now.Add(-90 * 24 * time.Hour)},
			// sealed grace blobs are useless after their 30 s; erase stale ones
			{`UPDATE session SET grace_blob = NULL, grace_until = NULL WHERE grace_blob IS NOT NULL AND grace_until < $1`, now},
			{`DELETE FROM delivery_log WHERE at < $1`, now.Add(-90 * 24 * time.Hour)},
		} {
			tag, err := tx.Exec(ctx, q.sql, q.arg)
			if err != nil {
				return err
			}
			total += tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("prune identity tables: %w", err)
	}
	return total, nil
}
