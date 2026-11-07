package app

import (
	"fmt"
	"log/slog"

	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// newSMSGateway builds the configured SMS gateway; nil (SMS disabled) when
// SMS_PROVIDER is empty.
func newSMSGateway(cfg Config, log *slog.Logger) (sms.Gateway, error) {
	switch cfg.SMSProvider {
	case "":
		return nil, nil //nolint:nilnil // nil gateway means SMS is disabled
	case "log":
		log.Warn("SMS_PROVIDER=log: sign-in codes are written to the log, not sent")
		return sms.Log{Log: log}, nil
	case "netgsm":
		return &sms.Netgsm{Usercode: cfg.SMSNetgsmUsercode, Password: cfg.SMSNetgsmPassword, MsgHeader: cfg.SMSNetgsmMsgHeader}, nil
	}
	return nil, fmt.Errorf("unknown SMS provider %q", cfg.SMSProvider)
}

// newMailer builds the configured email sender; nil (email disabled) when
// MAIL_PROVIDER is empty and SMTP_HOST is unset.
func newMailer(cfg Config, log *slog.Logger) (mail.Sender, error) {
	switch cfg.MailProvider {
	case "":
		return nil, nil //nolint:nilnil // nil sender means email is disabled
	case "log":
		log.Warn("MAIL_PROVIDER=log: sign-in links are written to the log, not sent")
		return mail.Log{Log: log}, nil
	case "smtp":
		s, err := mail.NewSMTP(mail.SMTPConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom, TLS: cfg.SMTPTLS,
		})
		if err != nil {
			return nil, fmt.Errorf("configure smtp: %w", err)
		}
		return s, nil
	}
	return nil, fmt.Errorf("unknown mail provider %q", cfg.MailProvider)
}
