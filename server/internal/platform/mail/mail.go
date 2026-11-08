// Package mail is the outgoing email abstraction. Drivers: smtp (go-mail) and
// log (development and tests). Without a configured driver the email channel
// is disabled and features that need it (magic links) are not offered.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// Message is a plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender sends one message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// TLS modes of the SMTP driver.
const (
	TLSStartTLS = "starttls" // opportunistic upgrade is refused: STARTTLS is mandatory
	TLSImplicit = "tls"      // TLS from the first byte (port 465)
	TLSNone     = "none"     // plaintext; local relays and tests only
)

// SMTPConfig configures the SMTP driver.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      string // TLSStartTLS (default), TLSImplicit or TLSNone
}

// SMTP sends mail through an SMTP server.
type SMTP struct{ cfg SMTPConfig }

// NewSMTP validates cfg and returns the driver.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if cfg.Host == "" || cfg.From == "" {
		return nil, fmt.Errorf("mail: SMTP_HOST and SMTP_FROM are required")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	switch cfg.TLS {
	case "":
		cfg.TLS = TLSStartTLS
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return nil, fmt.Errorf("mail: SMTP_TLS %q is not one of starttls, tls, none", cfg.TLS)
	}
	return &SMTP{cfg: cfg}, nil
}

// Send implements Sender.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	msg := gomail.NewMsg()
	if err := msg.From(s.cfg.From); err != nil {
		return fmt.Errorf("mail: from address: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("mail: to address: %w", err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.Body)

	opts := []gomail.Option{gomail.WithPort(s.cfg.Port), gomail.WithTimeout(15 * time.Second)}
	switch s.cfg.TLS {
	case TLSStartTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case TLSImplicit:
		opts = append(opts, gomail.WithSSL())
	case TLSNone:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	}
	if s.cfg.Username != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.cfg.Username), gomail.WithPassword(s.cfg.Password))
	}
	c, err := gomail.NewClient(s.cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("mail: smtp client: %w", err)
	}
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}
	return nil
}

// Log is the development driver: it writes the message to the log instead of
// sending it. Never use it in production, the log then holds sign-in links.
type Log struct{ Log *slog.Logger }

// Send implements Sender.
func (l Log) Send(ctx context.Context, m Message) error {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	log.InfoContext(ctx, "mail (log driver, not sent)", "to", m.To, "subject", m.Subject, "body", m.Body)
	return nil
}

// Recorder is a Sender that keeps what it was asked to send; for tests.
type Recorder struct {
	mu   sync.Mutex
	msgs []Message
	// Err, when set, is returned by Send instead of recording.
	Err error
}

// Send implements Sender.
func (r *Recorder) Send(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.msgs = append(r.msgs, m)
	return nil
}

// Messages returns a copy of the recorded messages.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.msgs...)
}

// Last returns the most recent message, or false if none.
func (r *Recorder) Last() (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return Message{}, false
	}
	return r.msgs[len(r.msgs)-1], true
}
