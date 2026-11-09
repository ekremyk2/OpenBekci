package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Attempt methods recorded in login_attempt.
const (
	methodPassword = "password"
	methodOTP      = "sms_otp"
	methodReauth   = "reauth"

	purposeSignIn   = "sign_in"
	purposeMagic    = "magic_link"
	purposeReauth   = "reauth"
	lockoutWindow   = 30 * time.Minute
	lockoutBase     = 30 * time.Second
	lockoutMax      = 30 * time.Minute
	maxLockoutSteps = 6
)

// ipGate applies the per-address limiter to a credential check.
func (s *Service) ipGate(ip string) error {
	if ok, wait := s.cfg.Limits.AuthIP.Allow(ip); !ok {
		return problem.RateLimited(wait)
	}
	return nil
}

func (s *Service) loadUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (User, error) {
	return scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM "user" WHERE id = $1`, userID))
}

// PasswordSignIn checks an email-or-phone and password. A wrong password, an
// unknown account, a disabled account and an account without a password are
// indistinguishable to the caller (400 invalid_grant).
func (s *Service) PasswordSignIn(ctx context.Context, identifier, password string, m Meta) (User, error) {
	kind, norm, ok := s.classify(identifier)
	if err := s.ipGate(m.IP); err != nil {
		return User{}, err
	}
	if !ok {
		if _, err := s.verifyPassword(ctx, uuid.Nil, password, nil, nil); err != nil {
			return User{}, err
		}
		return User{}, errInvalidGrant()
	}
	var u User
	var hash *string
	var found bool
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, found, err = findByContact(ctx, tx, kind, norm)
		if err != nil || !found {
			return err
		}
		return tx.QueryRow(ctx, `SELECT password_hash FROM "user" WHERE id = $1`, u.ID).Scan(&hash)
	})
	if err != nil {
		return User{}, problem.Internal(fmt.Errorf("load user: %w", err))
	}
	key, uid := lockoutKey(u, found, norm)
	attemptID, err := s.beginAttempt(ctx, key, methodPassword, m.IP, uid)
	if err != nil {
		return User{}, err
	}
	if !found || hash == nil || u.Status != "active" {
		hash = nil // burn the time of a verification all the same
	}
	match, err := s.verifyPassword(ctx, attemptID, password, hash, uid)
	if err != nil {
		return User{}, err
	}
	if !match || hash == nil {
		return User{}, errInvalidGrant()
	}
	s.recordSuccess(ctx, key, methodPassword, m.IP, uid)
	if crypto.NeedsRehash(*hash) {
		if nh, err := crypto.HashPasswordContext(ctx, password); err == nil {
			_ = s.db.InAppTx(ctx, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE "user" SET password_hash = $2 WHERE id = $1`, u.ID, nh)
				return err
			})
		}
	}
	return u, nil
}

// verifyPassword checks password against hash; a nil hash only burns the time
// of a verification, so that an unknown account cannot be told from a wrong
// password. When the computation is refused (the argon2 queue is full, or the
// request's context ended while waiting) the pre-recorded failure attemptID is
// refunded, since no credential was checked, and the caller gets a 429.
func (s *Service) verifyPassword(ctx context.Context, attemptID uuid.UUID, password string, hash *string, userID *uuid.UUID) (bool, error) {
	var match bool
	var err error
	if hash == nil {
		err = crypto.DummyVerifyContext(ctx, password)
	} else {
		match, err = crypto.VerifyPasswordContext(ctx, password, *hash)
	}
	switch {
	case err == nil:
		return match, nil
	case errors.Is(err, crypto.ErrInvalidHash):
		s.log.ErrorContext(ctx, "stored password hash is unreadable", "user_id", userID, "error", err)
		return false, nil
	}
	if attemptID != uuid.Nil {
		s.refundAttempt(ctx, attemptID)
	}
	if !errors.Is(err, crypto.ErrBusy) {
		return false, problem.Wrap(503, problem.CodeTimeout, "the request was cancelled while waiting", err)
	}
	s.log.WarnContext(ctx, "password computation refused: too many in progress")
	return false, problem.RateLimited(2 * time.Second)
}

func (s *Service) codeMAC(purpose, target, code string) []byte {
	return crypto.MAC(s.cfg.SecretKey, purpose, target, code)
}

// checkOTP verifies code against the newest open sign-in code of phone.
func (s *Service) checkOTP(ctx context.Context, phone, code string) (uuid.UUID, bool, error) {
	return s.checkCode(ctx, "phone", purposeSignIn, phone, code)
}

// checkCode verifies code against the newest open code of the purpose sent to
// target (kind "phone" or "email"), counting the attempt. It reports the
// owning user id when the code is right.
func (s *Service) checkCode(ctx context.Context, kind, purpose, target, code string) (uuid.UUID, bool, error) {
	now := s.now()
	var uid uuid.UUID
	good := false
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var otpID uuid.UUID
		var owner *uuid.UUID
		var hash []byte
		var attempts int
		err := tx.QueryRow(ctx, `
			SELECT id, user_id, code_hash, attempts FROM otp_code
			WHERE purpose = $3 AND target_kind = $4 AND target = $1 AND consumed_at IS NULL AND expires_at > $2
			ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, target, now, purpose, kind).Scan(&otpID, &owner, &hash, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if attempts >= s.cfg.OTPAttempts || owner == nil {
			return nil
		}
		if crypto.Equal(hash, s.codeMAC(purpose, target, code)) {
			good, uid = true, *owner
			_, err = tx.Exec(ctx, `UPDATE otp_code SET consumed_at = $2, attempts = attempts + 1 WHERE id = $1`, otpID, now)
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE otp_code SET attempts = attempts + 1 WHERE id = $1`, otpID)
		return err
	})
	return uid, good, err
}

// OTPSignIn verifies an SMS code and returns its user.
func (s *Service) OTPSignIn(ctx context.Context, phoneRaw, code string, m Meta) (User, error) {
	phone, ok := NormalizePhone(phoneRaw, s.cfg.DefaultCountryCode)
	if err := s.ipGate(m.IP); err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, errInvalidGrant()
	}
	var owner User
	var found bool
	if err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		owner, found, err = findByContact(ctx, tx, byPhone, phone)
		return err
	}); err != nil {
		return User{}, problem.Internal(fmt.Errorf("load user: %w", err))
	}
	key, kid := lockoutKey(owner, found, phone)
	if _, err := s.beginAttempt(ctx, key, methodOTP, m.IP, kid); err != nil {
		return User{}, err
	}
	uid, good, err := s.checkOTP(ctx, phone, code)
	if err != nil {
		return User{}, problem.Internal(fmt.Errorf("check otp: %w", err))
	}
	if !good || !found || uid != owner.ID {
		return User{}, errInvalidGrant()
	}
	var u User
	if err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		u, err = s.loadUser(ctx, tx, uid)
		return err
	}); err != nil {
		return User{}, problem.Internal(fmt.Errorf("load user: %w", err))
	}
	if u, err = s.activatePending(ctx, u); err != nil {
		return User{}, err
	}
	if u.Status != "active" {
		return User{}, errInvalidGrant()
	}
	s.recordSuccess(ctx, key, methodOTP, m.IP, kid)
	return u, nil
}

// insertLink stores a new magic-link token for the user, voiding earlier
// open links to the same target, and returns the token.
func (s *Service) insertLink(ctx context.Context, tx pgx.Tx, userID uuid.UUID, targetKind, target string) (string, error) {
	now := s.now()
	token, hash, err := crypto.NewToken()
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE otp_code SET consumed_at = $2 WHERE purpose = $3 AND target = $1 AND consumed_at IS NULL`,
		target, now, purposeMagic); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO otp_code (id, user_id, target_kind, target, purpose, code_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id.New(), userID, targetKind, target, purposeMagic, hash, now, now.Add(s.cfg.LinkTTL))
	return token, err
}

// CreateLoginLink issues a magic link for userID without sending it, for the
// admin CLI (first sign-in of a user created by an operator). It works for
// users without email too; the link then simply carries the token.
func (s *Service) CreateLoginLink(ctx context.Context, userID uuid.UUID, client Client) (string, error) {
	var token string
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		u, err := s.loadUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		kind, target := "phone", ""
		if u.Phone != nil {
			target = *u.Phone
		}
		if u.Email != nil {
			kind, target = "email", *u.Email
		}
		token, err = s.insertLink(ctx, tx, userID, kind, target)
		return err
	})
	if err != nil {
		return "", problem.Internal(fmt.Errorf("create login link: %w", err))
	}
	return LinkURL(s.cfg.BaseURL, client, LinkMagic, token), nil
}

// MagicLinkSignIn consumes a magic-link token (single use) and returns its user.
func (s *Service) MagicLinkSignIn(ctx context.Context, token string, m Meta) (User, error) {
	if err := s.ipGate(m.IP); err != nil {
		return User{}, err
	}
	if len(token) < 20 || len(token) > 200 {
		return User{}, errInvalidGrant()
	}
	now := s.now()
	var uid *uuid.UUID
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE otp_code SET consumed_at = $2
			WHERE purpose = $3 AND code_hash = $1 AND consumed_at IS NULL AND expires_at > $2
			RETURNING user_id`, crypto.HashToken(token), now, purposeMagic).Scan(&uid)
		if errors.Is(err, pgx.ErrNoRows) {
			uid = nil
			return nil
		}
		return err
	})
	if err != nil {
		return User{}, problem.Internal(fmt.Errorf("consume magic link: %w", err))
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
	if u, err = s.activatePending(ctx, u); err != nil {
		return User{}, err
	}
	if u.Status != "active" {
		return User{}, errInvalidGrant()
	}
	return u, nil
}

func (s *Service) otpText(locale, code string) string {
	minutes := int(s.cfg.OTPTTL.Minutes())
	if locale == "en" {
		return fmt.Sprintf("%s sign-in code: %s. Valid for %d minutes. Do not share it with anyone.", s.cfg.InstanceName, code, minutes)
	}
	return fmt.Sprintf("%s giriş kodunuz: %s. %d dakika geçerlidir. Kodu kimseyle paylaşmayın.", s.cfg.InstanceName, code, minutes)
}

func (s *Service) linkMail(locale, to, link string) mail.Message {
	minutes := int(s.cfg.LinkTTL.Minutes())
	if locale == "en" {
		return mail.Message{
			To: to, Subject: s.cfg.InstanceName + " sign-in link",
			Body: fmt.Sprintf("Use this link to sign in to %s:\n\n%s\n\nIt works once and expires in %d minutes. If you did not ask for it, ignore this message.\n", s.cfg.InstanceName, link, minutes),
		}
	}
	return mail.Message{
		To: to, Subject: s.cfg.InstanceName + " giriş bağlantısı",
		Body: fmt.Sprintf("%s hesabınıza giriş yapmak için bu bağlantıyı kullanın:\n\n%s\n\nBağlantı tek kullanımlıktır ve %d dakika içinde geçerliliğini yitirir. Bunu siz istemediyseniz bu iletiyi yok sayın.\n", s.cfg.InstanceName, link, minutes),
	}
}
