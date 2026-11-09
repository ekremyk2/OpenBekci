// Package chat is the chat notification channel (backend plan section 12,
// decisions O4 and O5): an organization's bot credentials (Telegram, WhatsApp,
// providers of packs), the links between a user and a chat account, the
// provider webhook that verifies a link, and the send the dispatcher calls.
package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ErrNoLink: the person has no verified chat link.
var ErrNoLink = errors.New("chat: no linked chat")

// TxRunner is the database access, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// ProviderSource lists the providers of the packs enabled for an organization
// (*packs.Host).
type ProviderSource interface {
	ChatProviders(ctx context.Context, org uuid.UUID) ([]chat.Provider, error)
}

// Config configures the Service.
type Config struct {
	// EncryptionKey seals the credentials and the chat ids (OPENBEKCI_ENCRYPTION_KEY).
	EncryptionKey []byte
	Packs         ProviderSource
	// Options point the built-in providers at their services (tests: fakes).
	Options chat.Options
	// BaseURL is the public address of the server; webhook URLs are built from it.
	BaseURL string
	Now     func() time.Time
	Log     *slog.Logger
}

// Service is the chat module.
type Service struct {
	db     TxRunner
	cipher *crypto.FieldCipher
	cfg    Config
}

// NewService returns a Service; a missing encryption key is an error.
func NewService(db TxRunner, cfg Config) (*Service, error) {
	c, err := crypto.NewFieldCipher(cfg.EncryptionKey, "chat.sealed")
	if err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{db: db, cipher: c, cfg: cfg}, nil
}

const (
	codePrefix = "BEKCI-"
	codeTTL    = 10 * time.Minute
)

var codeRe = regexp.MustCompile(`(?i)BEKCI-[A-Z2-7]{26}`) // 16 random bytes in base32: 128 bits

func newCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return codePrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

func hash(parts ...string) []byte {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return h[:]
}

// ---- settings

// ProviderSettings is one core provider's settings as the API shows them:
// the schema, the plain values, and which secrets are set (never their values).
type ProviderSettings struct {
	Provider   string
	Schema     map[string]any
	Values     map[string]any
	SecretsSet []string
	Configured bool
	WebhookURL string
}

func (s *Service) aad(org uuid.UUID, provider, key string) []byte {
	return []byte(strings.Join([]string{org.String(), provider, key}, "|"))
}

type stored struct {
	plain  map[string]string
	sealed map[string]string
}

func (s *Service) load(ctx context.Context, tx pgx.Tx, org uuid.UUID, provider string) (stored, error) {
	st := stored{plain: map[string]string{}, sealed: map[string]string{}}
	var plain, sealed []byte
	err := tx.QueryRow(ctx, `SELECT settings, secrets FROM chat_config WHERE org_id = $1 AND provider = $2`, org, provider).Scan(&plain, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(plain, &st.plain); err != nil {
		return st, err
	}
	return st, json.Unmarshal(sealed, &st.sealed)
}

func (s *Service) open(org uuid.UUID, provider string, st stored) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range st.plain {
		out[k] = v
	}
	for k, v := range st.sealed {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, err
		}
		pt, err := s.cipher.Open(raw, s.aad(org, provider, k))
		if err != nil {
			return nil, fmt.Errorf("chat: secret %q: %w", k, err)
		}
		out[k] = string(pt)
	}
	return out, nil
}

func (s *Service) webhookURL(org uuid.UUID, provider string) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/v1/orgs/" + org.String() + "/chat/webhooks/" + provider
}

func (s *Service) settingsOf(org uuid.UUID, b chat.Builtin, st stored) ProviderSettings {
	ps := ProviderSettings{Provider: b.ID, Schema: b.Schema, Values: map[string]any{}, SecretsSet: []string{}, WebhookURL: s.webhookURL(org, b.ID)}
	for k, v := range st.plain {
		ps.Values[k] = v
	}
	for k := range st.sealed {
		ps.SecretsSet = append(ps.SecretsSet, k)
	}
	slices.Sort(ps.SecretsSet)
	if cfg, err := s.open(org, b.ID, st); err == nil {
		_, ferr := b.Factory(cfg, s.cfg.Options)
		ps.Configured = ferr == nil
	}
	return ps
}

// Settings lists the core providers' settings of the organization.
func (s *Service) Settings(ctx context.Context, org uuid.UUID) ([]ProviderSettings, error) {
	var out []ProviderSettings
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		for _, b := range chat.Builtins() {
			st, err := s.load(ctx, tx, org, b.ID)
			if err != nil {
				return err
			}
			out = append(out, s.settingsOf(org, b, st))
		}
		return nil
	})
	return out, err
}

// UpdateSettings merges values into the provider's settings: a key left out
// keeps its value, null clears it. Every value is checked against the
// schema's pattern.
func (s *Service) UpdateSettings(ctx context.Context, org uuid.UUID, provider string, values map[string]any) (ProviderSettings, error) {
	b, ok := chat.BuiltinByID(provider)
	if !ok {
		return ProviderSettings{}, problem.NotFound("no such chat provider")
	}
	props, _ := b.Schema["properties"].(map[string]any)
	var bad []problem.Field
	for k, v := range values {
		p, known := props[k].(map[string]any)
		if !known {
			bad = append(bad, problem.Field{Field: "values." + k, Code: "unknown_field"})
			continue
		}
		if v == nil {
			continue
		}
		str, isStr := v.(string)
		if !isStr {
			bad = append(bad, problem.Field{Field: "values." + k, Code: "invalid_type"})
			continue
		}
		if pat, _ := p["pattern"].(string); pat != "" && !regexp.MustCompile(pat).MatchString(str) {
			bad = append(bad, problem.Field{Field: "values." + k, Code: "invalid_format"})
		}
	}
	if len(bad) > 0 {
		slices.SortFunc(bad, func(a, b problem.Field) int { return strings.Compare(a.Field, b.Field) })
		return ProviderSettings{}, problem.Validation(bad...)
	}
	var out ProviderSettings
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		st, err := s.load(ctx, tx, org, provider)
		if err != nil {
			return err
		}
		for k, v := range values {
			p := props[k].(map[string]any)
			secret, _ := p["x-secret"].(bool)
			delete(st.plain, k)
			delete(st.sealed, k)
			if v == nil {
				continue
			}
			if secret {
				sealed, err := s.cipher.Seal([]byte(v.(string)), s.aad(org, provider, k))
				if err != nil {
					return err
				}
				st.sealed[k] = base64.StdEncoding.EncodeToString(sealed)
			} else {
				st.plain[k] = v.(string)
			}
		}
		plain, _ := json.Marshal(st.plain)
		sealed, _ := json.Marshal(st.sealed)
		if _, err := tx.Exec(ctx, `INSERT INTO chat_config (org_id, provider, settings, secrets, updated_at) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (org_id, provider) DO UPDATE SET settings = EXCLUDED.settings, secrets = EXCLUDED.secrets, updated_at = EXCLUDED.updated_at`,
			org, provider, plain, sealed, s.cfg.Now()); err != nil {
			return err
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k) // names only, never values
		}
		slices.Sort(keys)
		diff, err := audit.Diff("chat_settings", nil, map[string]any{"provider": provider, "keys": keys})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "chat.settings_update", TargetType: "chat_provider", TargetID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(provider)), Diff: diff}); err != nil {
			return err
		}
		out = s.settingsOf(org, b, st)
		return nil
	})
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) {
			return ProviderSettings{}, err
		}
		return ProviderSettings{}, problem.Internal(fmt.Errorf("update chat settings: %w", err))
	}
	return out, nil
}

// provider returns the configured provider of the organization: a core
// provider with a complete settings row, or one of an enabled pack.
func (s *Service) provider(ctx context.Context, org uuid.UUID, providerID string) (chat.Provider, error) {
	if b, ok := chat.BuiltinByID(providerID); ok {
		var cfg map[string]string
		err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
			st, err := s.load(ctx, tx, org, providerID)
			if err != nil {
				return err
			}
			cfg, err = s.open(org, providerID, st)
			return err
		})
		if err != nil {
			return nil, err
		}
		p, err := b.Factory(cfg, s.cfg.Options)
		if err != nil {
			return nil, errNotConfigured
		}
		return p, nil
	}
	if s.cfg.Packs != nil {
		ps, err := s.cfg.Packs.ChatProviders(ctx, org)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if p.ID() == providerID {
				return p, nil
			}
		}
	}
	return nil, errNotConfigured
}

var errNotConfigured = problem.Conflict("the chat provider is not configured for this organization")

// ---- links

// Link is a user's link to a chat account.
type Link struct {
	ID       uuid.UUID
	OrgID    uuid.UUID
	Provider string
	// Status: "linked", "pending" (code issued, not yet verified) or
	// "unverified" (the provider reported the chat gone).
	Status    string
	LinkedAt  *time.Time
	CreatedAt time.Time
}

const linkSelect = `SELECT id, org_id, provider, verified_at, created_at, code_hash IS NOT NULL AND code_expires_at > now() FROM chat_link`

func scanLink(row pgx.Row) (Link, error) {
	var l Link
	var pending bool
	if err := row.Scan(&l.ID, &l.OrgID, &l.Provider, &l.LinkedAt, &l.CreatedAt, &pending); err != nil {
		return l, err
	}
	l.CreatedAt = l.CreatedAt.UTC()
	switch {
	case l.LinkedAt != nil:
		t := l.LinkedAt.UTC()
		l.LinkedAt = &t
		l.Status = "linked"
	case pending:
		l.Status = "pending"
	default:
		l.Status = "unverified"
	}
	return l, nil
}

// Started is the answer to Start.
type Started struct {
	Provider  string
	DeepLink  string
	ExpiresAt time.Time
}

// Start issues a linking code for the user and returns the provider's deep
// link that carries it. A user who is linked stays linked until the new code
// is verified.
func (s *Service) Start(ctx context.Context, org, user uuid.UUID, providerID string) (Started, error) {
	p, err := s.provider(ctx, org, providerID)
	if err != nil {
		return Started{}, err
	}
	code, err := newCode()
	if err != nil {
		return Started{}, problem.Internal(err)
	}
	exp := s.cfg.Now().Add(codeTTL).UTC()
	err = s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		var lid uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO chat_link (org_id, id, user_id, provider, code_hash, code_expires_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (org_id, user_id, provider) DO UPDATE SET code_hash = EXCLUDED.code_hash, code_expires_at = EXCLUDED.code_expires_at
			RETURNING id`, org, id.New(), user, providerID, hash(providerID, code), exp).Scan(&lid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "chat.link_start", TargetType: "chat_link", TargetID: lid})
	})
	if err != nil {
		return Started{}, problem.Internal(fmt.Errorf("start chat link: %w", err))
	}
	return Started{Provider: providerID, DeepLink: p.DeepLink(code), ExpiresAt: exp}, nil
}

// ListMine returns the user's links in the organization.
func (s *Service) ListMine(ctx context.Context, org, user uuid.UUID) ([]Link, error) {
	var out []Link
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, linkSelect+` WHERE org_id = $1 AND user_id = $2 ORDER BY created_at, id`, org, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanLink(rows)
			if err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

// ForUser lists the user's links across organizations (GET /v1/me).
func (s *Service) ForUser(ctx context.Context, user uuid.UUID) ([]Link, error) {
	var out []Link
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, linkSelect+` WHERE user_id = $1 ORDER BY created_at, id`, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanLink(rows)
			if err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

// Unlink deletes the user's link; someone else's is "not found".
func (s *Service) Unlink(ctx context.Context, org, user, linkID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM chat_link WHERE org_id = $1 AND id = $2 AND user_id = $3`, org, linkID, user)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return problem.NotFound("no such chat link")
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: org, Action: "chat.unlink", TargetType: "chat_link", TargetID: linkID})
	})
	var pe *problem.Error
	if errors.As(err, &pe) {
		return err
	}
	if err != nil {
		return problem.Internal(fmt.Errorf("unlink chat: %w", err))
	}
	return nil
}

// ---- inbound (webhook)

// Inbound handles the messages a verified webhook carried: a message with a
// valid, unexpired, unused code verifies that user's link; a message from an
// already linked account refreshes its session window. Everything else is
// ignored without an answer (a wrong code tells a stranger nothing).
func (s *Service) Inbound(ctx context.Context, org uuid.UUID, providerID string, msgs []chat.Inbound) error {
	for _, m := range msgs {
		if m.ExternalID == "" {
			continue
		}
		if err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error { return s.inbound(ctx, tx, org, providerID, m) }); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) inbound(ctx context.Context, tx pgx.Tx, org uuid.UUID, providerID string, m chat.Inbound) error {
	now := s.cfg.Now()
	ext := hash(providerID, m.ExternalID)
	// any message from a linked account opens its session window
	if _, err := tx.Exec(ctx, `UPDATE chat_link SET last_inbound_at = $4 WHERE org_id = $1 AND provider = $2 AND external_hash = $3 AND verified_at IS NOT NULL`,
		org, providerID, ext, now); err != nil {
		return err
	}
	code := codeRe.FindString(m.Text)
	if code == "" {
		return nil
	}
	sealed, err := s.cipher.Seal([]byte(m.ExternalID), s.aad(org, providerID, "external"))
	if err != nil {
		return err
	}
	// a savepoint keeps a unique violation (the account is linked to another user) from poisoning the transaction
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	var lid, uid uuid.UUID
	err = sp.QueryRow(ctx, `UPDATE chat_link SET external_id_sealed = $4, external_hash = $5, verified_at = $6, last_inbound_at = $6, code_hash = NULL, code_expires_at = NULL
		WHERE org_id = $1 AND provider = $2 AND code_hash = $3 AND code_expires_at > $6 RETURNING id, user_id`,
		org, providerID, hash(providerID, strings.ToUpper(code)), sealed, ext, now).Scan(&lid, &uid)
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_ = sp.Rollback(ctx)
		return nil // unknown, expired or already used code
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		_ = sp.Rollback(ctx)
		s.cfg.Log.WarnContext(ctx, "chat link: the chat account is already linked to another user", "org", org, "provider", providerID)
		return nil
	case err != nil:
		_ = sp.Rollback(ctx)
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{OrgID: org, ActorKind: audit.ActorSystem, Action: "chat.link_verified", TargetType: "chat_link", TargetID: lid})
}
