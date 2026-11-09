package identity

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SignUpInput is a sign-up through a site's join code (setup-people S4 path 2):
// one contact, and optionally the name the resident goes by.
type SignUpInput struct {
	// Code is the join code, used only to bound abuse per code. The caller
	// has already checked that it is valid.
	Code  string
	Phone string
	Email string
	Name  string
	// Locale is "tr" or "en"; empty means "tr".
	Locale string
	// Client selects the web or the mobile form of the e-mailed link.
	Client Client
}

// maxSignUpName is the longest display name a sign-up may carry, in characters.
const maxSignUpName = 200

// cleanName normalizes the name given at sign-up (NFC, surrounding and doubled
// white space removed) and refuses one that is too long or holds control,
// format (zero-width, bidirectional override) or private-use characters: the
// name ends up on a manager's screen next to the person it names, and in
// exports, so it must be plain text. An empty name is fine.
func cleanName(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	name := strings.Join(strings.Fields(norm.NFC.String(raw)), " ")
	if utf8.RuneCountInString(name) > maxSignUpName {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) || unicode.Is(unicode.Cs, r) || r == utf8.RuneError || r == ' ' || r == ' ' {
			return "", false
		}
	}
	return name, true
}

// signUp is the validated, normalized sign-up the background send creates an
// account from when the contact is unknown.
type signUp struct {
	name, locale string
}

func (su *signUp) insert(ctx context.Context, tx pgx.Tx, kind contactKind, value string) (User, error) {
	var email, phone, name *string
	if kind == byEmail {
		email = &value
	} else {
		phone = &value
	}
	if su.name != "" {
		name = &su.name
	}
	return scanUser(tx.QueryRow(ctx, `
		INSERT INTO "user" (id, email, phone, locale, status, display_name)
		VALUES ($1, $2, $3, $4, 'pending', $5)
		RETURNING `+userColumns, id.New(), email, phone, su.locale, name))
}

// refresh overwrites the name and locale of a still pending account with the
// latest request's, so that the person who finally proves the contact gets
// the name they typed and not one a stranger gave earlier. An empty name
// clears the earlier one.
func (su *signUp) refresh(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (User, error) {
	var name *string
	if su.name != "" {
		name = &su.name
	}
	return scanUser(tx.QueryRow(ctx, `
		UPDATE "user" SET display_name = $2, locale = $3, updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending'
		RETURNING `+userColumns, userID, name, su.locale))
}

// RequestSignUp starts a sign-up: it sends the code (SMS) or the link (e-mail)
// that completes it. An unknown contact gets a pending account; a contact that
// already has an account gets the ordinary sign-in code or link, and an
// account that is disabled gets nothing. The answer and its speed are the same
// in every case, and there is no second account for one contact: the lookup,
// the insert and the send all run in the background, as in RequestOTP.
//
// The code and the sign-in then work as everywhere else: the OTP or the link
// is verified through auth.createToken (or createSession) and turns a pending
// account active. The result is a session with no organization yet.
func (s *Service) RequestSignUp(ctx context.Context, in SignUpInput, m Meta) error {
	phoneRaw, emailRaw := strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email)
	var fields []problem.Field
	switch {
	case phoneRaw == "" && emailRaw == "":
		fields = append(fields, problem.Field{Field: "phone", Code: "required"})
	case phoneRaw != "" && emailRaw != "":
		fields = append(fields, problem.Field{Field: "email", Code: "invalid_value"})
	}
	name, nameOK := cleanName(in.Name)
	if !nameOK {
		fields = append(fields, problem.Field{Field: "name", Code: "invalid_value", Params: map[string]any{"max": maxSignUpName}})
	}
	locale := in.Locale
	switch locale {
	case "":
		locale = "tr"
	case "tr", "en":
	default:
		fields = append(fields, problem.Field{Field: "locale", Code: "invalid_value"})
	}
	var phone, email string
	if phoneRaw != "" {
		var ok bool
		if phone, ok = NormalizePhone(phoneRaw, s.cfg.DefaultCountryCode); !ok {
			fields = append(fields, problem.Field{Field: "phone", Code: "invalid_format"})
		}
	}
	if emailRaw != "" {
		var ok bool
		if email, ok = NormalizeEmail(emailRaw); !ok {
			fields = append(fields, problem.Field{Field: "email", Code: "invalid_format"})
		}
	}
	if len(fields) > 0 {
		return problem.Validation(fields...)
	}
	if phone != "" && s.cfg.SMS == nil {
		return problem.New(503, problem.CodeSMSUnavailable, "sign-up by SMS is not available on this instance")
	}
	if email != "" && s.cfg.Mail == nil {
		return problem.New(503, problem.CodeMailUnavailable, "sign-up by email link is not available on this instance")
	}

	ipL, targetL, target := s.cfg.Limits.OTPSendIP, s.cfg.Limits.OTPSendTarget, phone
	if email != "" {
		ipL, targetL, target = s.cfg.Limits.LinkSendIP, s.cfg.Limits.LinkSendTarget, email
	}
	// The address goes first: a client that is already limited must not keep
	// draining the join code's shared budget with refused requests, which
	// would lock the residents out of a code they all use.
	if ok, wait := ipL.Allow(m.IP); !ok {
		return problem.RateLimited(wait)
	}
	if ok, wait := s.cfg.Limits.SignUpCode.Allow(in.Code); !ok {
		return problem.RateLimited(wait)
	}
	if ok, wait := targetL.Allow(target); !ok {
		return problem.RateLimited(wait)
	}
	if err := s.checkDailyBudget(target); err != nil {
		return err
	}
	su := &signUp{name: name, locale: locale}
	if email != "" {
		s.dispatch(ctx, func(ctx context.Context) { s.sendMagicLink(ctx, email, in.Client, su) })
	} else {
		s.dispatch(ctx, func(ctx context.Context) { s.sendOTP(ctx, phone, su) })
	}
	return nil
}

// activatePending turns a pending account active once its contact has been
// proven (a right code or link). Other accounts are returned unchanged.
func (s *Service) activatePending(ctx context.Context, u User) (User, error) {
	if u.Status != "pending" {
		return u, nil
	}
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE "user" SET status = 'active', updated_at = $2, version = version + 1 WHERE id = $1 AND status = 'pending'`, u.ID, s.now())
		return err
	})
	if err != nil {
		return u, problem.Internal(fmt.Errorf("activate account: %w", err))
	}
	u.Status = "active"
	return u, nil
}
