// Package devices registers the push devices of a signed-in account (backend
// plan 4.12, section 12; mobile plan section 9). A device is one push token of
// a user: it is stored sealed with the core field cipher, never returned, and
// deleted when the session that registered it signs out.
package devices

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/push"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Limits.
const (
	maxTokenLen   = 4096
	maxPerUser    = 20
	purposeCipher = "device.push_token"
)

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InAppTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Config configures the Service.
type Config struct {
	// EncryptionKey seals the tokens (OPENBEKCI_ENCRYPTION_KEY).
	EncryptionKey []byte
	// AllowPrivateEndpoints lets UnifiedPush endpoints be plain http or private
	// addresses (the same switch as the sender's).
	AllowPrivateEndpoints bool
	Now                   func() time.Time
}

// Service is the devices module.
type Service struct {
	db     TxRunner
	cipher *crypto.FieldCipher
	cfg    Config
}

// NewService returns a Service. A missing encryption key is an error.
func NewService(db TxRunner, cfg Config) (*Service, error) {
	c, err := crypto.NewFieldCipher(cfg.EncryptionKey, purposeCipher)
	if err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{db: db, cipher: c, cfg: cfg}, nil
}

// Device is a registered device as the API shows it: no token.
type Device struct {
	ID         uuid.UUID
	PushKind   string
	RelayID    *string
	AppVersion *string
	Locale     *string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Input is a registration.
type Input struct {
	PushKind   string
	Token      string
	RelayID    string
	AppVersion string
	Locale     string
}

var (
	relayIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	localeRe  = regexp.MustCompile(`^[A-Za-z]{2,3}([_-][A-Za-z0-9]{2,8})*$`)
)

func (s *Service) validate(in Input) error {
	var fs []problem.Field
	switch in.PushKind {
	case push.KindFCM, push.KindAPNs, push.KindUnifiedPush:
	default:
		fs = append(fs, problem.Field{Field: "push_kind", Code: "invalid_value"})
	}
	switch {
	case in.Token == "" || strings.TrimSpace(in.Token) != in.Token:
		fs = append(fs, problem.Field{Field: "token", Code: "required"})
	case len(in.Token) > maxTokenLen:
		fs = append(fs, problem.Field{Field: "token", Code: "too_long"})
	case in.PushKind == push.KindUnifiedPush:
		u, err := url.Parse(in.Token)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && (!s.cfg.AllowPrivateEndpoints || u.Scheme != "http")) {
			fs = append(fs, problem.Field{Field: "token", Code: "invalid_format"})
		}
	}
	if in.PushKind != push.KindUnifiedPush && !relayIDRe.MatchString(in.RelayID) {
		fs = append(fs, problem.Field{Field: "relay_id", Code: "required"})
	}
	if in.PushKind == push.KindUnifiedPush && in.RelayID != "" && !relayIDRe.MatchString(in.RelayID) {
		fs = append(fs, problem.Field{Field: "relay_id", Code: "invalid_format"})
	}
	if utf8.RuneCountInString(in.AppVersion) > 64 {
		fs = append(fs, problem.Field{Field: "app_version", Code: "too_long"})
	}
	if in.Locale != "" && (len(in.Locale) > 16 || !localeRe.MatchString(in.Locale)) {
		fs = append(fs, problem.Field{Field: "locale", Code: "invalid_format"})
	}
	if len(fs) > 0 {
		return problem.Validation(fs...)
	}
	return nil
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

const deviceColumns = `id, push_kind, relay_id, app_version, locale, disabled_at IS NULL, created_at, updated_at`

func scanDevice(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.PushKind, &d.RelayID, &d.AppVersion, &d.Locale, &d.Enabled, &d.CreatedAt, &d.UpdatedAt)
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	return d, err
}

// Register stores the device of the caller, or updates the existing row of the
// same (user, token) and enables it again. family is the registering session's
// family id: signing that session out deletes the device.
func (s *Service) Register(ctx context.Context, p *authz.Principal, in Input) (Device, error) {
	if p == nil {
		return Device{}, problem.Unauthenticated("authentication is required")
	}
	if err := s.validate(in); err != nil {
		return Device{}, err
	}
	var out Device
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		hash := tokenHash(in.Token)
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM device WHERE user_id = $1 AND token_hash = $2 FOR UPDATE`, p.UserID, hash).Scan(&existing)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		rowID := existing
		if !found {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM device WHERE user_id = $1`, p.UserID).Scan(&n); err != nil {
				return err
			}
			if n >= maxPerUser {
				return problem.Validation(problem.Field{Field: "token", Code: "too_many_devices"})
			}
			rowID = id.New()
		}
		sealed, err := s.cipher.Seal([]byte(in.Token), rowID[:])
		if err != nil {
			return err
		}
		fam := p.FamilyID
		if found {
			out, err = scanDevice(tx.QueryRow(ctx, `UPDATE device SET push_kind = $2, token_enc = $3, relay_id = $4, app_version = $5, locale = $6,
				session_family_id = $7, disabled_at = NULL, disabled_reason = NULL, updated_at = $8 WHERE id = $1 RETURNING `+deviceColumns,
				rowID, in.PushKind, sealed, nullable(in.RelayID), nullable(in.AppVersion), nullable(in.Locale), fam, s.cfg.Now()))
		} else {
			out, err = scanDevice(tx.QueryRow(ctx, `INSERT INTO device (id, user_id, session_family_id, push_kind, token_enc, token_hash, relay_id, app_version, locale, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10) RETURNING `+deviceColumns,
				rowID, p.UserID, fam, in.PushKind, sealed, hash, nullable(in.RelayID), nullable(in.AppVersion), nullable(in.Locale), s.cfg.Now()))
		}
		if err != nil {
			return err
		}
		action := "device.register"
		if found {
			action = "device.update"
		}
		// the token is never audited; only the kind and relay are
		return audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "device", TargetID: out.ID})
	})
	var pe *problem.Error
	if errors.As(err, &pe) {
		return Device{}, err
	}
	if err != nil {
		return Device{}, problem.Internal(fmt.Errorf("register device: %w", err))
	}
	return out, nil
}

// Delete removes the caller's device. A device of another user is "not found".
func (s *Service) Delete(ctx context.Context, p *authz.Principal, deviceID uuid.UUID) error {
	if p == nil {
		return problem.Unauthenticated("authentication is required")
	}
	var n int64
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM device WHERE id = $1 AND user_id = $2`, deviceID, p.UserID)
		if err != nil {
			return err
		}
		if n = tag.RowsAffected(); n == 0 {
			return nil
		}
		return audit.Record(ctx, tx, audit.Event{Action: "device.delete", TargetType: "device", TargetID: deviceID})
	})
	if err != nil {
		return problem.Internal(fmt.Errorf("delete device: %w", err))
	}
	if n == 0 {
		return problem.NotFound("no such device")
	}
	return nil
}

// Target is a device the dispatcher may send to, with its token opened.
type Target struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	PushKind string
	Token    string
	RelayID  string
}

// ForUsers returns the enabled devices of the users, tokens opened. A device
// whose token cannot be opened (wrong key) is skipped.
func (s *Service) ForUsers(ctx context.Context, userIDs []uuid.UUID) ([]Target, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	var out []Target
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, user_id, push_kind, token_enc, coalesce(relay_id, '') FROM device
			WHERE user_id = ANY($1) AND disabled_at IS NULL ORDER BY created_at, id`, userIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t Target
			var sealed []byte
			if err := rows.Scan(&t.ID, &t.UserID, &t.PushKind, &sealed, &t.RelayID); err != nil {
				return err
			}
			pt, err := s.cipher.Open(sealed, t.ID[:])
			if err != nil {
				continue
			}
			t.Token = string(pt)
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("load devices: %w", err)
	}
	return out, nil
}

// Disable marks devices the relay reported as invalid. It is idempotent.
func (s *Service) Disable(ctx context.Context, ids []uuid.UUID, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE device SET disabled_at = $2, disabled_reason = $3, updated_at = $2 WHERE id = ANY($1) AND disabled_at IS NULL`,
			ids, s.cfg.Now(), reason)
		return err
	})
	if err != nil {
		return fmt.Errorf("disable devices: %w", err)
	}
	return nil
}

// DeleteForUser deletes every device of a user inside tx; the privacy erase of
// an account calls it.
func DeleteForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM device WHERE user_id = $1`, userID)
	return err
}
