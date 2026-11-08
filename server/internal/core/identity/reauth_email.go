package identity

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// RequestReauthEmailCode e-mails a six-digit re-authentication code to the
// signed-in user's own address (same lifetime and attempt limit as an SMS
// code). It is the re-authentication route of users without a password or a
// phone number, such as the first administrator of `admin create-org`. The
// caller is authenticated, so errors are reported plainly.
func (s *Service) RequestReauthEmailCode(ctx context.Context, p *authz.Principal, m Meta) error {
	if s.cfg.Mail == nil {
		return problem.New(503, problem.CodeMailUnavailable, "email codes are not available on this instance")
	}
	u, err := s.GetUser(ctx, p.UserID)
	if err != nil {
		return err
	}
	if u.Email == nil {
		return problem.Validation(problem.Field{Field: "email", Code: "required"})
	}
	email := *u.Email
	if ok, wait := s.cfg.Limits.LinkSendIP.Allow(m.IP); !ok {
		return problem.RateLimited(wait)
	}
	if ok, wait := s.cfg.Limits.LinkSendTarget.Allow(purposeReauth + ":" + email); !ok {
		return problem.RateLimited(wait)
	}
	if err := s.checkDailyBudget(email); err != nil {
		return err
	}
	if ok, wait := s.cfg.Limits.MailHourly.Allow("instance"); !ok {
		return problem.RateLimited(wait)
	}
	code, err := crypto.RandomDigits(6)
	if err != nil {
		return problem.Internal(err)
	}
	now := s.now()
	err = s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE otp_code SET consumed_at = $2 WHERE purpose = $3 AND target = $1 AND consumed_at IS NULL`,
			email, now, purposeReauth); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO otp_code (id, user_id, target_kind, target, purpose, code_hash, created_at, expires_at)
			VALUES ($1, $2, 'email', $3, $4, $5, $6, $7)`,
			id.New(), u.ID, email, purposeReauth, s.codeMAC(purposeReauth, email, code), now, now.Add(s.cfg.OTPTTL))
		return err
	})
	if err != nil {
		return problem.Internal(fmt.Errorf("create reauth code: %w", err))
	}
	msg := s.reauthMail(u.Locale, email, code)
	if err := s.cfg.Mail.Send(ctx, msg); err != nil {
		s.log.ErrorContext(ctx, "send reauth code", "user_id", u.ID, "error", err)
		s.recordDelivery(ctx, channelEmail, purposeReauth, &u.ID, email, deliveryFailed, err.Error())
		return problem.Wrap(503, problem.CodeMailUnavailable, "the email could not be sent; try again later", err)
	}
	s.recordDelivery(ctx, channelEmail, purposeReauth, &u.ID, email, deliverySent, "")
	return nil
}

func (s *Service) reauthMail(locale, to, code string) mail.Message {
	minutes := int(s.cfg.OTPTTL.Minutes())
	if locale == "en" {
		return mail.Message{
			To: to, Subject: s.cfg.InstanceName + " confirmation code",
			Body: fmt.Sprintf("Your %s confirmation code is %s. It is valid for %d minutes. Do not share it with anyone. If you did not ask for it, ignore this message.\n", s.cfg.InstanceName, code, minutes),
		}
	}
	return mail.Message{
		To: to, Subject: s.cfg.InstanceName + " doğrulama kodu",
		Body: fmt.Sprintf("%s doğrulama kodunuz: %s. %d dakika geçerlidir. Kodu kimseyle paylaşmayın. Bunu siz istemediyseniz bu iletiyi yok sayın.\n", s.cfg.InstanceName, code, minutes),
	}
}
