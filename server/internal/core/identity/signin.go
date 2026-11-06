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

// lockoutKey names the failure counter of an account: one per user, so email
// and phone attempts count together. An identifier without an account is
// counted under the normalised identifier itself, so unknown accounts are
// locked out like known ones.
func lockoutKey(u User, found bool, norm string) (string, *uuid.UUID) {
	if found {
		return "uid:" + u.ID.String(), &u.ID
	}
	return norm, nil
}

// beginAttempt is the lockout gate of a credential check. Under a per-key
// Postgres advisory lock it refuses the attempt while the key is locked and
// otherwise records it as a failure BEFORE the credential is verified, so that
// parallel guesses cannot all pass a check-then-act race: at most
// LockoutThreshold attempts are ever evaluated between two successes. A
// correct credential then calls recordSuccess, which resets the counter.
func (s *Service) beginAttempt(ctx context.Context, key, method, ip string, userID *uuid.UUID) error {
	var wait time.Duration
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return err
		}
		var err error
		if wait, err = s.lockedFor(ctx, tx, key); err != nil || wait > 0 {
			return err
		}
		return s.insertAttempt(ctx, tx, key, method, ip, false, userID)
	})
	if err != nil {
		return problem.Internal(fmt.Errorf("check lockout: %w", err))
	}
	if wait > 0 {
		return problem.RateLimited(wait)
	}
	return nil
}

func (s *Service) insertAttempt(ctx context.Context, tx pgx.Tx, key, method, ip string, success bool, userID *uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO login_attempt (id, identifier, method, ip, success, user_id, at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id.New(), key, method, clip(ip, 64), success, userID, s.now())
	return err
}

// recordSuccess notes a verified credential, which resets the failure count of key.
func (s *Service) recordSuccess(ctx context.Context, key, method, ip string, userID *uuid.UUID) {
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		return s.insertAttempt(ctx, tx, key, method, ip, true, userID)
	})
	if err != nil {
		s.log.ErrorContext(ctx, "record login attempt", "error", err)
	}
}

// lockedFor returns how much longer key is locked out. After
// LockoutThreshold failures since the last success (within 30 minutes) the
// lock lasts 30s, doubling with every further failure, up to 30 minutes.
func (s *Service) lockedFor(ctx context.Context, tx pgx.Tx, key string) (time.Duration, error) {
	now := s.now()
	var n int
	var last *time.Time
	err := tx.QueryRow(ctx, `
		SELECT count(*), max(at) FROM login_attempt
		WHERE identifier = $1 AND NOT success AND at > $2
		  AND at > COALESCE((SELECT max(at) FROM login_attempt WHERE identifier = $1 AND success), '-infinity')`,
		key, now.Add(-lockoutWindow)).Scan(&n, &last)
	if err != nil {
		return 0, err
	}
	if n < s.cfg.LockoutThreshold || last == nil {
		return 0, nil
	}
	d := lockoutBase << min(n-s.cfg.LockoutThreshold, maxLockoutSteps)
	d = min(d, lockoutMax)
	if remaining := last.Add(d).Sub(now); remaining > 0 {
		return remaining, nil
	}
	return 0, nil
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
		crypto.DummyVerify(password)
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
	if err := s.beginAttempt(ctx, key, methodPassword, m.IP, uid); err != nil {
		return User{}, err
	}
	if !found || hash == nil || u.Status != "active" {
		crypto.DummyVerify(password)
		return User{}, errInvalidGrant()
	}
	match, verr := crypto.VerifyPassword(password, *hash)
	if verr != nil {
		s.log.ErrorContext(ctx, "stored password hash is unreadable", "user_id", u.ID, "error", verr)
	}
	if !match {
		return User{}, errInvalidGrant()
	}
	s.recordSuccess(ctx, key, methodPassword, m.IP, uid)
	if crypto.NeedsRehash(*hash) {
		if nh, err := crypto.HashPassword(password); err == nil {
			_ = s.db.InAppTx(ctx, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE "user" SET password_hash = $2 WHERE id = $1`, u.ID, nh)
				return err
			})
		}
	}
	return u, nil
}

// RequestOTP sends a six-digit sign-in code by SMS. It answers the same
// whether or not an account has the number, so it cannot be used to find out
// who is registered.
func (s *Service) RequestOTP(ctx context.Context, phoneRaw string, m Meta) error {
	if s.cfg.SMS == nil {
		return problem.New(503, problem.CodeSMSUnavailable, "sign-in by SMS is not available on this instance")
	}
	phone, ok := NormalizePhone(phoneRaw, s.cfg.DefaultCountryCode)
	if !ok {
		return problem.Validation(problem.Field{Field: "phone", Code: "invalid_format"})
	}
	if ok, wait := s.cfg.Limits.OTPSendIP.Allow(m.IP); !ok {
		return problem.RateLimited(wait)
	}
	if ok, wait := s.cfg.Limits.OTPSendTarget.Allow(phone); !ok {
		return problem.RateLimited(wait)
	}
	now := s.now()
	var u User
	var found bool
	var code string
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, found, err = findByContact(ctx, tx, byPhone, phone); err != nil {
			return err
		}
		if !found || u.Status != "active" {
			found = false
			return nil
		}
		if code, err = crypto.RandomDigits(6); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE otp_code SET consumed_at = $2 WHERE purpose = $3 AND target = $1 AND consumed_at IS NULL`,
			phone, now, purposeSignIn); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO otp_code (id, user_id, target_kind, target, purpose, code_hash, created_at, expires_at)
			VALUES ($1, $2, 'phone', $3, $4, $5, $6, $7)`,
			id.New(), u.ID, phone, purposeSignIn, s.codeMAC(purposeSignIn, phone, code), now, now.Add(s.cfg.OTPTTL))
		return err
	})
	if err != nil {
		return problem.Internal(fmt.Errorf("create otp: %w", err))
	}
	if !found {
		return nil
	}
	if _, err := s.cfg.SMS.Send(ctx, phone, s.otpText(u.Locale, code)); err != nil {
		s.log.ErrorContext(ctx, "send sign-in code", "user_id", u.ID, "error", err)
		return problem.Wrap(503, problem.CodeSMSUnavailable, "the SMS could not be sent; try again later", err)
	}
	return nil
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
	if err := s.beginAttempt(ctx, key, methodOTP, m.IP, kid); err != nil {
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
	if u.Status != "active" {
		return User{}, errInvalidGrant()
	}
	s.recordSuccess(ctx, key, methodOTP, m.IP, kid)
	return u, nil
}

// RequestMagicLink e-mails a single-use sign-in link (15 minutes). Like
// RequestOTP it answers the same for unknown addresses.
func (s *Service) RequestMagicLink(ctx context.Context, emailRaw string, client Client, m Meta) error {
	if s.cfg.Mail == nil {
		return problem.New(503, problem.CodeMailUnavailable, "sign-in by email link is not available on this instance")
	}
	email, ok := NormalizeEmail(emailRaw)
	if !ok {
		return problem.Validation(problem.Field{Field: "email", Code: "invalid_format"})
	}
	if ok, wait := s.cfg.Limits.LinkSendIP.Allow(m.IP); !ok {
		return problem.RateLimited(wait)
	}
	if ok, wait := s.cfg.Limits.LinkSendTarget.Allow(email); !ok {
		return problem.RateLimited(wait)
	}
	var u User
	var found bool
	var token string
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, found, err = findByContact(ctx, tx, byEmail, email); err != nil {
			return err
		}
		if !found || u.Status != "active" {
			found = false
			return nil
		}
		token, err = s.insertLink(ctx, tx, u.ID, "email", email)
		return err
	})
	if err != nil {
		return problem.Internal(fmt.Errorf("create magic link: %w", err))
	}
	if !found {
		return nil
	}
	link := LinkURL(s.cfg.BaseURL, client, LinkMagic, token)
	msg := s.linkMail(u.Locale, email, link)
	if err := s.cfg.Mail.Send(ctx, msg); err != nil {
		s.log.ErrorContext(ctx, "send magic link", "user_id", u.ID, "error", err)
		return problem.Wrap(503, problem.CodeMailUnavailable, "the email could not be sent; try again later", err)
	}
	return nil
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
