package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// maxNextLen bounds the ?next= of a web-login link.
const maxNextLen = 512

// SafeNext reports whether next is an in-app path the web-login link may
// redirect to: an absolute path inside the /orgs/{org_id}/... tree of the web
// app, optionally with a query string. Anything else (another host, a scheme,
// protocol-relative or backslash forms, encoded separators, dot segments,
// control characters) is refused. The empty string is accepted (no next).
func SafeNext(next string) bool {
	if next == "" {
		return true
	}
	if len(next) > maxNextLen || !strings.HasPrefix(next, "/orgs/") || strings.HasPrefix(next, "//") {
		return false
	}
	for _, r := range next {
		if r == '\\' || r == '#' || unicode.IsControl(r) || unicode.IsSpace(r) || r > unicode.MaxASCII {
			return false
		}
	}
	lower := strings.ToLower(next)
	for _, bad := range []string{"%2f", "%5c", "%2e", "%00"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return false
	}
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) < 2 || segs[0] != "orgs" {
		return false
	}
	if _, err := uuid.Parse(segs[1]); err != nil {
		return false
	}
	for _, s := range segs[1:] {
		if s == "." || s == ".." {
			return false
		}
	}
	return true
}

// WebLoginLink is a freshly issued one-time link.
type WebLoginLink struct {
	Token     string // shown once; only its hash is stored
	URL       string
	ExpiresAt time.Time
}

// IssueWebLoginLink creates a one-time token for the signed-in caller, bound to
// the caller's current session, and returns the link
// {BASE_URL}/login/link/{token}[?next=...]. next is optional and must pass
// SafeNext.
func (s *Service) IssueWebLoginLink(ctx context.Context, p *authz.Principal, next string) (WebLoginLink, error) {
	if p == nil {
		return WebLoginLink{}, problem.Unauthenticated("authentication is required")
	}
	if !SafeNext(next) {
		return WebLoginLink{}, problem.Validation(problem.Field{Field: "next", Code: "invalid_value"})
	}
	if l := s.cfg.Limits.WebLinkUser; l != nil {
		if ok, wait := l.Allow(p.UserID.String()); !ok {
			return WebLoginLink{}, problem.RateLimited(wait)
		}
	}
	token, hash, err := crypto.NewToken()
	if err != nil {
		return WebLoginLink{}, problem.Internal(err)
	}
	now := s.now()
	exp := now.Add(s.cfg.WebLoginTTL)
	rowID := id.New()
	err = s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		// earlier open links of this session are voided: one live link per session
		if _, err := tx.Exec(ctx, `UPDATE web_login_link SET consumed_at = $3 WHERE user_id = $1 AND session_family_id = $2 AND consumed_at IS NULL`,
			p.UserID, p.FamilyID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO web_login_link (id, user_id, session_family_id, token_hash, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			rowID, p.UserID, p.FamilyID, hash, now, exp); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "auth.web_login_link_issue", TargetType: "user", TargetID: p.UserID})
	})
	if err != nil {
		return WebLoginLink{}, problem.Internal(fmt.Errorf("issue web login link: %w", err))
	}
	link := LinkURL(s.cfg.BaseURL, ClientWeb, LinkWebLogin, token)
	if next != "" {
		link += "?next=" + url.QueryEscape(next)
	}
	return WebLoginLink{Token: token, URL: link, ExpiresAt: exp}, nil
}

// ExchangeWebLoginLink consumes a web-login token (single use) and returns its
// user; the caller then issues a cookie session. The token must be unexpired,
// unused and the session that issued it must still be open. Anything else is
// invalid_grant.
func (s *Service) ExchangeWebLoginLink(ctx context.Context, token string, m Meta) (User, error) {
	if err := s.ipGate(m.IP); err != nil {
		return User{}, err
	}
	if len(token) < 20 || len(token) > 200 {
		return User{}, errInvalidGrant()
	}
	now := s.now()
	var uid *uuid.UUID
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var u uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE web_login_link l SET consumed_at = $2
			WHERE l.token_hash = $1 AND l.consumed_at IS NULL AND l.expires_at > $2
			  AND EXISTS (SELECT 1 FROM session s WHERE s.family_id = l.session_family_id AND s.user_id = l.user_id
			              AND s.revoked_at IS NULL AND s.expires_at > $2)
			RETURNING l.user_id`, crypto.HashToken(token), now).Scan(&u)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		uid = &u
		return audit.Record(ctx, tx, audit.Event{ActorKind: audit.ActorUser, ActorUserID: &u, Action: "auth.web_login_link_exchange", TargetType: "user", TargetID: u})
	})
	if err != nil {
		return User{}, problem.Internal(fmt.Errorf("exchange web login link: %w", err))
	}
	if uid == nil {
		return User{}, errInvalidGrant()
	}
	var u User
	if err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = s.loadUser(ctx, tx, *uid)
		return err
	}); err != nil {
		return User{}, problem.Internal(fmt.Errorf("load user: %w", err))
	}
	if u.Status != "active" {
		return User{}, errInvalidGrant()
	}
	return u, nil
}
