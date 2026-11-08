package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const userColumns = `id, email::text, phone, locale, status, is_platform_admin, password_hash IS NOT NULL`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Phone, &u.Locale, &u.Status, &u.IsPlatformAdmin, &u.HasPassword)
	return u, err
}

// CreateUserParams describes a new account. At least one of Email and Phone
// is required; Password is optional (SMS and magic-link users have none).
type CreateUserParams struct {
	Email           string
	Phone           string
	Password        string
	Locale          string
	IsPlatformAdmin bool
}

// newUser is a validated CreateUserParams, ready to insert.
type newUser struct {
	email, phone, hash *string
	locale             string
	platformAdmin      bool
}

// CreateUser creates an account. It is the entry point for the admin CLI and,
// later, invitations.
func (s *Service) CreateUser(ctx context.Context, p CreateUserParams) (User, error) {
	nu, err := s.prepareUser(p)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = insertUser(ctx, tx, nu)
		return err
	})
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, problem.Conflict("a user with this email or phone already exists")
		}
		return User{}, problem.Internal(fmt.Errorf("create user: %w", err))
	}
	return u, nil
}

// PrepareFindOrCreateUser validates p and returns a function that, inside a
// caller-owned transaction, returns the user with the given sign-in
// identifier or creates the one p describes. `admin create-org` hands it to
// the organization transaction so that the organization, the person, the
// membership and a new user are committed together or not at all. The
// function reports whether it created the user.
func (s *Service) PrepareFindOrCreateUser(identifier string, p CreateUserParams) (func(context.Context, pgx.Tx) (uuid.UUID, bool, error), error) {
	nu, err := s.prepareUser(p)
	if err != nil {
		return nil, err
	}
	kind, norm, ok := s.classify(identifier)
	if !ok {
		return nil, problem.Validation(problem.Field{Field: "identifier", Code: "invalid_format"})
	}
	return func(ctx context.Context, tx pgx.Tx) (uuid.UUID, bool, error) {
		u, found, err := findByContact(ctx, tx, kind, norm)
		if err != nil {
			return uuid.Nil, false, problem.Internal(fmt.Errorf("find user: %w", err))
		}
		if found {
			return u.ID, false, nil
		}
		if u, err = insertUser(ctx, tx, nu); err != nil {
			if isUniqueViolation(err) {
				return uuid.Nil, false, problem.Conflict("a user with this email or phone already exists")
			}
			return uuid.Nil, false, problem.Internal(fmt.Errorf("create user: %w", err))
		}
		return u.ID, true, nil
	}, nil
}

func insertUser(ctx context.Context, tx pgx.Tx, nu newUser) (User, error) {
	return scanUser(tx.QueryRow(ctx, `
		INSERT INTO "user" (id, email, phone, password_hash, locale, is_platform_admin)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+userColumns, id.New(), nu.email, nu.phone, nu.hash, nu.locale, nu.platformAdmin))
}

// prepareUser validates and normalizes the parameters of a new account.
func (s *Service) prepareUser(p CreateUserParams) (newUser, error) {
	var email, phone *string
	var fields []problem.Field
	if strings.TrimSpace(p.Email) != "" {
		e, ok := NormalizeEmail(p.Email)
		if !ok {
			fields = append(fields, problem.Field{Field: "email", Code: "invalid_format"})
		}
		email = &e
	}
	if strings.TrimSpace(p.Phone) != "" {
		ph, ok := NormalizePhone(p.Phone, s.cfg.DefaultCountryCode)
		if !ok {
			fields = append(fields, problem.Field{Field: "phone", Code: "invalid_format"})
		}
		phone = &ph
	}
	if email == nil && phone == nil && len(fields) == 0 {
		fields = append(fields, problem.Field{Field: "email", Code: "required"})
	}
	var hash *string
	if p.Password != "" {
		if pe := ValidatePassword(p.Password); pe != nil {
			pe.Fields[0].Field = "password"
			fields = append(fields, pe.Fields...)
		} else {
			h, err := crypto.HashPassword(p.Password)
			if err != nil {
				return newUser{}, problem.Internal(err)
			}
			hash = &h
		}
	}
	if len(fields) > 0 {
		return newUser{}, problem.Validation(fields...)
	}
	locale := p.Locale
	if locale == "" {
		locale = "tr"
	}
	return newUser{email: email, phone: phone, hash: hash, locale: locale, platformAdmin: p.IsPlatformAdmin}, nil
}

// FindUser looks an account up by email or phone ("@" selects email).
// It returns (User{}, false, nil) when there is none.
func (s *Service) FindUser(ctx context.Context, identifier string) (User, bool, error) {
	kind, norm, ok := s.classify(identifier)
	if !ok {
		return User{}, false, nil
	}
	var u User
	found := false
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, found, err = findByContact(ctx, tx, kind, norm)
		return err
	})
	if err != nil {
		return User{}, false, problem.Internal(fmt.Errorf("find user: %w", err))
	}
	return u, found, nil
}

// GetUser loads an account by id.
func (s *Service) GetUser(ctx context.Context, userID uuid.UUID) (User, error) {
	var u User
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = s.loadUser(ctx, tx, userID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, problem.NotFound("no such user")
	}
	if err != nil {
		return User{}, problem.Internal(fmt.Errorf("get user: %w", err))
	}
	return u, nil
}

// PromotePlatformAdmin gives an existing user platform administrator rights.
func (s *Service) PromotePlatformAdmin(ctx context.Context, userID uuid.UUID) error {
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var was bool
		if err := tx.QueryRow(ctx, `SELECT is_platform_admin FROM "user" WHERE id = $1 FOR UPDATE`, userID).Scan(&was); err != nil {
			return err
		}
		if was {
			return nil // already an operator: nothing changed, nothing to record
		}
		if _, err := tx.Exec(ctx, `UPDATE "user" SET is_platform_admin = true, updated_at = $2, version = version + 1 WHERE id = $1`, userID, s.now()); err != nil {
			return err
		}
		diff, err := audit.Diff("user", map[string]any{"is_platform_admin": false}, map[string]any{"is_platform_admin": true})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "user.promote_platform_admin", TargetType: "user", TargetID: userID, Diff: diff})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return problem.NotFound("no such user")
	}
	if err != nil {
		return problem.Internal(fmt.Errorf("promote platform admin: %w", err))
	}
	return nil
}

// SetPasswordAdmin sets a user's password without re-authentication; for the
// admin CLI, where the operator holds the database anyway.
func (s *Service) SetPasswordAdmin(ctx context.Context, userID uuid.UUID, password string) error {
	if pe := ValidatePassword(password); pe != nil {
		pe.Fields[0].Field = "password"
		return pe
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return problem.Internal(err)
	}
	return s.updatePassword(ctx, userID, hash, uuid.Nil, "user.password_set_admin")
}

// updatePassword stores the hash and revokes every session of the user except
// the family keep (uuid.Nil keeps none).
func (s *Service) updatePassword(ctx context.Context, userID uuid.UUID, hash string, keep uuid.UUID, action string) error {
	now := s.now()
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE "user" SET password_hash = $2, updated_at = $3, version = version + 1 WHERE id = $1`, userID, hash, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if _, err = tx.Exec(ctx, `UPDATE session SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL AND family_id <> $3`, userID, now, keep); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "user", TargetID: userID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return problem.NotFound("no such user")
	}
	if err != nil {
		return problem.Internal(fmt.Errorf("update password: %w", err))
	}
	return nil
}

type contactKind string

const (
	byEmail contactKind = "email"
	byPhone contactKind = "phone"
)

// classify normalizes a sign-in identifier: an address with "@" is an email,
// anything else a phone number.
func (s *Service) classify(identifier string) (contactKind, string, bool) {
	if strings.Contains(identifier, "@") {
		e, ok := NormalizeEmail(identifier)
		return byEmail, e, ok
	}
	p, ok := NormalizePhone(identifier, s.cfg.DefaultCountryCode)
	return byPhone, p, ok
}

func findByContact(ctx context.Context, tx pgx.Tx, kind contactKind, value string) (User, bool, error) {
	col := "email"
	if kind == byPhone {
		col = "phone"
	}
	u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM "user" WHERE `+col+` = $1`, value))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	return u, err == nil, err
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
