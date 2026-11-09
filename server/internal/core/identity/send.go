package identity

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Delivery channels and statuses stored in delivery_log.
const (
	channelSMS   = "sms"
	channelEmail = "email"

	deliverySent    = "sent"
	deliveryFailed  = "failed"
	deliveryDropped = "dropped"

	// maxSendJobs bounds the background sends in flight; sends beyond it are
	// dropped (and recorded) rather than queued without limit.
	maxSendJobs = 32
	sendTimeout = 60 * time.Second
)

// sender runs the background half of the code and link requests.
type sender struct {
	slots chan struct{}
	wg    sync.WaitGroup
}

func newSender() *sender { return &sender{slots: make(chan struct{}, maxSendJobs)} }

// dispatch runs job in the background and returns at once, so the requester
// sees the same answer at the same speed whether or not there was anything to
// send. When maxSendJobs jobs are in flight the job is dropped and recorded.
func (s *Service) dispatch(ctx context.Context, job func(context.Context)) {
	select {
	case s.send.slots <- struct{}{}:
	default:
		s.recordDelivery(ctx, "", "", nil, "", deliveryDropped, "send queue full")
		return
	}
	s.send.wg.Add(1)
	// The request's context ends with the response; the send must outlive it.
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	go func() {
		defer s.send.wg.Done()
		defer func() { <-s.send.slots }()
		defer cancel()
		job(jctx)
	}()
}

// Drain waits until every background send has finished or ctx ends. The
// server calls it on shutdown; tests call it before reading what was sent.
func (s *Service) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.send.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for background sends: %w", ctx.Err())
	}
}

// checkDailyBudget spends one of the identifier's daily sends. It does not
// depend on whether an account exists, so it reveals nothing.
func (s *Service) checkDailyBudget(identifier string) error {
	if ok, wait := s.cfg.Limits.SendDaily.Allow(identifier); !ok {
		return problem.RateLimited(wait)
	}
	return nil
}

// deliverSMS sends a text and records the outcome in the delivery log. It
// reports nothing to the caller: a gateway failure must not tell a requester
// that the account exists. A message beyond the instance's hourly SMS budget
// is dropped and recorded.
func (s *Service) deliverSMS(ctx context.Context, purpose string, userID *uuid.UUID, phone, text string) {
	if ok, _ := s.cfg.Limits.SMSHourly.Allow("instance"); !ok {
		s.recordDelivery(ctx, channelSMS, purpose, userID, phone, deliveryDropped, "hourly SMS budget exhausted")
		return
	}
	if _, err := s.cfg.SMS.Send(ctx, phone, text); err != nil {
		s.log.ErrorContext(ctx, "send sms", "purpose", purpose, "user_id", userID, "error", err)
		s.recordDelivery(ctx, channelSMS, purpose, userID, phone, deliveryFailed, err.Error())
		return
	}
	s.recordDelivery(ctx, channelSMS, purpose, userID, phone, deliverySent, "")
}

// deliverMail is deliverSMS for mail.
func (s *Service) deliverMail(ctx context.Context, purpose string, userID *uuid.UUID, msg mail.Message) {
	if ok, _ := s.cfg.Limits.MailHourly.Allow("instance"); !ok {
		s.recordDelivery(ctx, channelEmail, purpose, userID, msg.To, deliveryDropped, "hourly mail budget exhausted")
		return
	}
	if err := s.cfg.Mail.Send(ctx, msg); err != nil {
		s.log.ErrorContext(ctx, "send mail", "purpose", purpose, "user_id", userID, "error", err)
		s.recordDelivery(ctx, channelEmail, purpose, userID, msg.To, deliveryFailed, err.Error())
		return
	}
	s.recordDelivery(ctx, channelEmail, purpose, userID, msg.To, deliverySent, "")
}

func (s *Service) recordDelivery(ctx context.Context, channel, purpose string, userID *uuid.UUID, target, status, detail string) {
	if channel == "" {
		channel = channelEmail // a dropped send has no channel; keep the column valid
	}
	if status == deliveryDropped {
		// a dropped message means a budget or the send queue ran out: operators must notice
		s.log.WarnContext(ctx, "delivery dropped", "channel", channel, "purpose", purpose, "reason", detail)
	}
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO delivery_log (id, at, channel, purpose, target, user_id, status, detail)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id.New(), s.now(), channel, purpose, maskTarget(target), userID, status, nullable(clip(detail, 300)))
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "record delivery", "error", err)
	}
}

// maskTarget hides most of a phone number or address: the delivery log is for
// operators diagnosing a gateway, not a list of contacts.
func maskTarget(t string) string {
	if at := strings.IndexByte(t, '@'); at > 0 {
		return t[:1] + "***" + t[at:]
	}
	if len(t) > 5 {
		return t[:3] + strings.Repeat("*", len(t)-5) + t[len(t)-2:]
	}
	return t
}

// Delivery is one entry of the delivery log.
type Delivery struct {
	At      time.Time
	Channel string
	Purpose string
	Target  string // masked
	Status  string
	Detail  string
}

// RecentDeliveries lists the newest delivery log entries, optionally only the
// failed and dropped ones. It is how an operator finds out that the SMS or
// mail gateway is failing (the requesters are never told).
func (s *Service) RecentDeliveries(ctx context.Context, limit int, onlyProblems bool) ([]Delivery, error) {
	if limit < 1 {
		limit = 20
	}
	var out []Delivery
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT at, channel, purpose, target, status, COALESCE(detail, '') FROM delivery_log
			WHERE NOT $2 OR status <> 'sent' ORDER BY at DESC LIMIT $1`, limit, onlyProblems)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d Delivery
			if err := rows.Scan(&d.At, &d.Channel, &d.Purpose, &d.Target, &d.Status, &d.Detail); err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, problem.Internal(fmt.Errorf("list deliveries: %w", err))
	}
	return out, nil
}

// RequestOTP sends a six-digit sign-in code by SMS. It answers the same, and
// at the same speed, whether or not an account has the number, so it cannot be
// used to find out who is registered: the lookup and the send run in the
// background.
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
	if err := s.checkDailyBudget(phone); err != nil {
		return err
	}
	s.dispatch(ctx, func(ctx context.Context) { s.sendOTP(ctx, phone, nil) })
	return nil
}

// sendOTP is the background half of RequestOTP. With su set (sign-up through a
// join code) an unknown number gets a pending account first; a known one gets
// the ordinary sign-in code, so the two cases send the same message.
func (s *Service) sendOTP(ctx context.Context, phone string, su *signUp) {
	now := s.now()
	var u User
	var found bool
	var code string
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, found, err = findByContact(ctx, tx, byPhone, phone); err != nil {
			return err
		}
		if !found && su != nil {
			if u, err = su.insert(ctx, tx, byPhone, phone); err != nil {
				return err
			}
			found = true
		}
		if !found || (u.Status != "active" && u.Status != "pending") {
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
		s.log.ErrorContext(ctx, "create otp", "error", err)
		return
	}
	if !found {
		return
	}
	s.deliverSMS(ctx, purposeSignIn, &u.ID, phone, s.otpText(u.Locale, code))
}

// RequestMagicLink e-mails a single-use sign-in link (15 minutes). Like
// RequestOTP it answers the same, immediately, for unknown addresses: the
// account lookup and the send happen in the background.
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
	if err := s.checkDailyBudget(email); err != nil {
		return err
	}
	s.dispatch(ctx, func(ctx context.Context) { s.sendMagicLink(ctx, email, client, nil) })
	return nil
}

// sendMagicLink is the background half of RequestMagicLink (see sendOTP for su).
func (s *Service) sendMagicLink(ctx context.Context, email string, client Client, su *signUp) {
	var u User
	var found bool
	var token string
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, found, err = findByContact(ctx, tx, byEmail, email); err != nil {
			return err
		}
		if !found && su != nil {
			if u, err = su.insert(ctx, tx, byEmail, email); err != nil {
				return err
			}
			found = true
		}
		if !found || (u.Status != "active" && u.Status != "pending") {
			found = false
			return nil
		}
		token, err = s.insertLink(ctx, tx, u.ID, "email", email)
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "create magic link", "error", err)
		return
	}
	if !found {
		return
	}
	link := LinkURL(s.cfg.BaseURL, client, LinkMagic, token)
	s.deliverMail(ctx, purposeMagic, &u.ID, s.linkMail(u.Locale, email, link))
}
