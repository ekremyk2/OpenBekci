package integrations

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// An API key is "obk_<prefix>_<secret>": the prefix is 8 lower-case letters and
// digits (public, unique, shown in lists and audit), the secret is 256 random
// bits in base64url. Only SHA-256 of the secret is stored, which is the right
// tool for a high-entropy token (the same as session tokens).
const (
	keyScheme    = "obk_"
	prefixLen    = 8
	prefixAlpha  = "abcdefghijklmnopqrstuvwxyz0123456789"
	minSecretLen = 32
)

// APIKey is a key as the API shows it: the prefix and metadata, never the secret.
type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	OrgID      uuid.UUID  `json:"org_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	SiteID     *uuid.UUID `json:"site_id"`
	Status     string     `json:"status"` // active, revoked or expired (open enum)
	CreatedBy  uuid.UUID  `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	RotatedAt  *time.Time `json:"rotated_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// APIKeyCreated is the answer to creating or rotating a key: the full secret
// is returned exactly once.
type APIKeyCreated struct {
	Key    APIKey `json:"key"`
	Secret string `json:"secret"`
}

// APIKeyInput is the request to create a key.
type APIKeyInput struct {
	Name      string
	Scopes    []string
	SiteID    *uuid.UUID
	ExpiresAt *time.Time
}

const apiKeyColumns = `id, org_id, name, prefix, scopes, site_id, created_by, created_at, rotated_at, last_used_at, expires_at, revoked_at` //nolint:gosec // column names, not a credential

func scanKey(row pgx.Row, now time.Time) (APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.OrgID, &k.Name, &k.Prefix, &k.Scopes, &k.SiteID, &k.CreatedBy, &k.CreatedAt, &k.RotatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt)
	if err != nil {
		return k, err
	}
	if k.Scopes == nil {
		k.Scopes = []string{}
	}
	k.CreatedAt = k.CreatedAt.UTC()
	for _, t := range []**time.Time{&k.RotatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt} {
		if *t != nil {
			u := (**t).UTC()
			*t = &u
		}
	}
	k.Status = keyStatus(k.RevokedAt, k.ExpiresAt, now)
	return k, nil
}

func keyStatus(revoked, expires *time.Time, now time.Time) string {
	switch {
	case revoked != nil:
		return "revoked"
	case expires != nil && !expires.After(now):
		return "expired"
	}
	return "active"
}

// newKeyToken builds a key's public prefix, its full token and the hash to store.
func newKeyToken() (prefix, token string, hash []byte, err error) {
	b := make([]byte, prefixLen)
	if _, err := rand.Read(b); err != nil {
		return "", "", nil, fmt.Errorf("read random prefix: %w", err)
	}
	for i := range b {
		b[i] = prefixAlpha[int(b[i])%len(prefixAlpha)]
	}
	prefix = string(b)
	secret, hash, err := crypto.NewToken()
	if err != nil {
		return "", "", nil, err
	}
	return prefix, keyScheme + prefix + "_" + secret, hash, nil
}

// splitKeyToken returns the prefix and secret of a presented token, or false
// when it does not have the shape of an API key.
func splitKeyToken(tok string) (prefix, secret string, ok bool) {
	rest, ok := strings.CutPrefix(tok, keyScheme)
	if !ok || len(rest) < prefixLen+1+minSecretLen || rest[prefixLen] != '_' {
		return "", "", false
	}
	prefix, secret = rest[:prefixLen], rest[prefixLen+1:]
	for _, c := range prefix {
		if !strings.ContainsRune(prefixAlpha, c) {
			return "", "", false
		}
	}
	return prefix, secret, true
}

// requireKeyAdmin checks the permission and that the caller is a signed-in
// person who re-authenticated recently: a key can never manage keys (it has no
// re-authentication, and its scopes cannot include apikeys.*).
func (s *Service) requireKeyAdmin(ctx context.Context, orgID uuid.UUID) (*authz.Principal, error) {
	if err := authz.Require(ctx, PermAPIKeysManage, authz.Scope{OrgID: orgID}); err != nil {
		return nil, err
	}
	p := authz.PrincipalFromContext(ctx)
	if p.APIKey != nil {
		return nil, problem.Forbidden("an API key cannot manage API keys")
	}
	if err := p.RequireReauth(s.now()); err != nil {
		return nil, err
	}
	return p, nil
}

// validateScopes checks that every scope is a registered organization-scope
// permission that the creator holds through a role, and not an apikeys.*
// permission (a key never manages keys).
func (s *Service) validateScopes(ctx context.Context, orgID uuid.UUID, scopes []string) ([]string, error) {
	if len(scopes) == 0 || len(scopes) > 200 {
		return nil, problem.Validation(problem.Field{Field: "scopes", Code: "invalid_value"})
	}
	g := authz.GrantsFromContext(ctx)
	out := make([]string, 0, len(scopes))
	for _, name := range scopes {
		perm := authz.Permission(name)
		var def authz.PermissionDef
		var ok bool
		if s.cfg.Registry != nil {
			def, ok = s.cfg.Registry.Lookup(perm)
		}
		f := func(code string) error {
			return problem.Validation(problem.Field{Field: "scopes", Code: code, Params: map[string]any{"permission": name}})
		}
		switch {
		case slices.Contains(out, name):
			return nil, f("duplicate")
		case !ok:
			return nil, f("unknown_permission")
		case strings.HasPrefix(name, "apikeys."):
			return nil, f("not_allowed")
		case !slices.Contains(def.Scopes, authz.ScopeOrg):
			return nil, f("not_allowed")
		case !g.HasViaRole(perm, authz.Scope{OrgID: orgID}):
			return nil, f("not_held")
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// CreateAPIKey creates a key and returns its secret, shown once. The caller
// must hold apikeys.manage and have re-authenticated recently; the key's
// scopes must be permissions the caller holds, and the key never exceeds them
// (see authz.APIKeyInfo).
func (s *Service) CreateAPIKey(ctx context.Context, orgID uuid.UUID, in APIKeyInput) (APIKeyCreated, error) {
	p, err := s.requireKeyAdmin(ctx, orgID)
	if err != nil {
		return APIKeyCreated{}, err
	}
	name := strings.TrimSpace(in.Name)
	if n := len([]rune(name)); n < 1 || n > 120 {
		return APIKeyCreated{}, problem.Validation(problem.Field{Field: "name", Code: "invalid_value"})
	}
	scopes, err := s.validateScopes(ctx, orgID, in.Scopes)
	if err != nil {
		return APIKeyCreated{}, err
	}
	now := s.now()
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return APIKeyCreated{}, problem.Validation(problem.Field{Field: "expires_at", Code: "must_be_future"})
	}
	var out APIKeyCreated
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if in.SiteID != nil {
			var one int
			if err := tx.QueryRow(ctx, `SELECT 1 FROM site WHERE org_id = $1 AND id = $2`, orgID, *in.SiteID).Scan(&one); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return problem.Validation(problem.Field{Field: "site_id", Code: "invalid_value"})
				}
				return err
			}
		}
		keyID := id.New()
		var token string
		for attempt := 0; ; attempt++ {
			prefix, tok, hash, err := newKeyToken()
			if err != nil {
				return err
			}
			// a savepoint keeps a prefix collision from aborting the transaction
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			_, err = sp.Exec(ctx, `INSERT INTO api_key (id, org_id, name, prefix, secret_hash, scopes, site_id, created_by, created_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, keyID, orgID, name, prefix, hash, scopes, in.SiteID, p.UserID, now, in.ExpiresAt)
			if err == nil {
				if err := sp.Commit(ctx); err != nil {
					return err
				}
				token = tok
				break
			}
			_ = sp.Rollback(ctx)
			if !tenancy.IsUnique(err, "api_key_prefix") || attempt >= 4 {
				return err
			}
		}
		k, err := scanKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE org_id = $1 AND id = $2`, orgID, keyID), now)
		if err != nil {
			return err
		}
		diff, err := audit.Diff("api_key", nil, map[string]any{"name": name, "prefix": k.Prefix, "scopes": scopes, "site_id": in.SiteID, "expires_at": in.ExpiresAt})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: in.SiteID, Action: "apikey.create", TargetType: "api_key", TargetID: keyID, Diff: diff}); err != nil {
			return err
		}
		out = APIKeyCreated{Key: k, Secret: token}
		return nil
	})
	if err != nil {
		return APIKeyCreated{}, tenancy.DBProblem(err, "API key")
	}
	return out, nil
}

// ListAPIKeys lists keys, newest first is not promised: ids ascend (creation
// order). status filters on active, revoked or expired ("" lists all).
func (s *Service) ListAPIKeys(ctx context.Context, orgID uuid.UUID, status string, limit int, cursor string) ([]APIKey, string, error) {
	if err := authz.Require(ctx, PermAPIKeysRead, authz.Scope{OrgID: orgID}); err != nil {
		return nil, "", err
	}
	if status != "" && !slices.Contains([]string{"active", "revoked", "expired"}, status) {
		return nil, "", problem.Validation(problem.Field{Field: "status", Code: "invalid_value"})
	}
	lim, after, err := tenancy.PageParams{Limit: limit, Cursor: cursor}.Resolve()
	if err != nil {
		return nil, "", err
	}
	now := s.now()
	var out []APIKey
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE org_id = $1 AND id > $2
			AND ($3 = '' OR ($3 = 'revoked' AND revoked_at IS NOT NULL)
			     OR ($3 = 'expired' AND revoked_at IS NULL AND expires_at IS NOT NULL AND expires_at <= $5)
			     OR ($3 = 'active' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $5)))
			ORDER BY id LIMIT $4`, orgID, after, status, lim+1, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			k, err := scanKey(rows, now)
			if err != nil {
				return err
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "API keys")
	}
	items, next := tenancy.Page(out, lim, func(k APIKey) uuid.UUID { return k.ID })
	return items, next, nil
}

// RotateAPIKey replaces the key's prefix and secret and returns the new secret,
// shown once. The old token stops working at once. A revoked key cannot be
// rotated (409); the key keeps its id, scopes, site and expiry.
func (s *Service) RotateAPIKey(ctx context.Context, orgID, keyID uuid.UUID) (APIKeyCreated, error) {
	if _, err := s.requireKeyAdmin(ctx, orgID); err != nil {
		return APIKeyCreated{}, err
	}
	now := s.now()
	var out APIKeyCreated
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, keyID), now)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such API key")
		}
		if err != nil {
			return err
		}
		if before.RevokedAt != nil {
			return problem.Conflict("the API key is revoked and cannot be rotated")
		}
		var token string
		for attempt := 0; ; attempt++ {
			prefix, tok, hash, err := newKeyToken()
			if err != nil {
				return err
			}
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			_, err = sp.Exec(ctx, `UPDATE api_key SET prefix = $3, secret_hash = $4, rotated_at = $5, last_used_at = NULL WHERE org_id = $1 AND id = $2`,
				orgID, keyID, prefix, hash, now)
			if err == nil {
				if err := sp.Commit(ctx); err != nil {
					return err
				}
				token = tok
				break
			}
			_ = sp.Rollback(ctx)
			if !tenancy.IsUnique(err, "api_key_prefix") || attempt >= 4 {
				return err
			}
		}
		k, err := scanKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE org_id = $1 AND id = $2`, orgID, keyID), now)
		if err != nil {
			return err
		}
		diff, err := audit.Diff("api_key", map[string]any{"prefix": before.Prefix}, map[string]any{"prefix": k.Prefix})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: k.SiteID, Action: "apikey.rotate", TargetType: "api_key", TargetID: keyID, Diff: diff}); err != nil {
			return err
		}
		out = APIKeyCreated{Key: k, Secret: token}
		return nil
	})
	if err != nil {
		return APIKeyCreated{}, tenancy.DBProblem(err, "API key")
	}
	return out, nil
}

// RevokeAPIKey revokes a key; its token answers 401 api_key_revoked from then
// on. Revoking a revoked key returns it unchanged.
func (s *Service) RevokeAPIKey(ctx context.Context, orgID, keyID uuid.UUID) (APIKey, error) {
	p, err := s.requireKeyAdmin(ctx, orgID)
	if err != nil {
		return APIKey{}, err
	}
	now := s.now()
	var out APIKey
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, keyID), now)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such API key")
		}
		if err != nil {
			return err
		}
		if before.RevokedAt != nil {
			out = before
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE api_key SET revoked_at = $3, revoked_by = $4 WHERE org_id = $1 AND id = $2`, orgID, keyID, now, p.UserID); err != nil {
			return err
		}
		out, err = scanKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE org_id = $1 AND id = $2`, orgID, keyID), now)
		if err != nil {
			return err
		}
		diff, err := audit.Diff("api_key", map[string]any{"status": before.Status}, map[string]any{"status": "revoked"})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: out.SiteID, Action: "apikey.revoke", TargetType: "api_key", TargetID: keyID, Diff: diff})
	})
	if err != nil {
		return APIKey{}, tenancy.DBProblem(err, "API key")
	}
	return out, nil
}

// AuthenticateAPIKey resolves a bearer token that has the shape of an API key.
// found is false when the token is not an API key we know (malformed, unknown
// prefix or wrong secret): the caller then treats it as an ordinary session
// token. A known key that is revoked or expired answers 401 with the stable
// codes api_key_revoked and api_key_expired; the answer is only given to a
// holder of the full secret. The principal is the key's creator carrying
// authz.APIKeyInfo; the Resolver turns that into the intersection of the key's
// scopes and the creator's grants at call time.
func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) (p *authz.Principal, found bool, err error) {
	prefix, secret, ok := splitKeyToken(token)
	if !ok {
		return nil, false, nil
	}
	now := s.now()
	var (
		keyID, orgID, createdBy uuid.UUID
		hash                    []byte
		scopes                  []string
		siteID                  *uuid.UUID
		expires, revoked        *time.Time
		lastUsed                *time.Time
		userStatus              string
	)
	// The key's organization is not known yet, so the lookup runs on the
	// platform pool (the same as the agent token lookup of the access module).
	err = s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT k.id, k.org_id, k.secret_hash, k.scopes, k.site_id, k.created_by, k.expires_at, k.revoked_at, k.last_used_at, u.status
			FROM api_key k JOIN "user" u ON u.id = k.created_by WHERE k.prefix = $1`, prefix).
			Scan(&keyID, &orgID, &hash, &scopes, &siteID, &createdBy, &expires, &revoked, &lastUsed, &userStatus)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, problem.Internal(fmt.Errorf("authenticate api key: %w", err))
	}
	if !crypto.Equal(hash, crypto.HashToken(secret)) {
		return nil, false, nil
	}
	switch {
	case revoked != nil:
		return nil, true, problem.New(401, problem.CodeAPIKeyRevoked, "the API key was revoked")
	case expires != nil && !expires.After(now):
		return nil, true, problem.New(401, problem.CodeAPIKeyExpired, "the API key has expired")
	case userStatus != "active":
		return nil, true, problem.Unauthenticated("the API key's owner is no longer active")
	}
	if s.limiter != nil {
		if allowed, wait := s.limiter.Allow(keyID.String()); !allowed {
			return nil, true, problem.RateLimited(wait)
		}
	}
	if lastUsed == nil || now.Sub(*lastUsed) >= touchInterval {
		// compare-and-set, so concurrent requests write once per interval
		if err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE api_key SET last_used_at = $3 WHERE org_id = $1 AND id = $2
				AND (last_used_at IS NULL OR last_used_at <= $4)`, orgID, keyID, now, now.Add(-touchInterval))
			return err
		}); err != nil {
			s.cfg.Log.WarnContext(ctx, "touch api key", "error", err)
		}
	}
	perms := make([]authz.Permission, len(scopes))
	for i, sc := range scopes {
		perms[i] = authz.Permission(sc)
	}
	return &authz.Principal{
		ID: createdBy, UserID: createdBy,
		APIKey: &authz.APIKeyInfo{KeyID: keyID, OrgID: orgID, Scopes: perms, SiteID: siteID},
	}, true, nil
}
