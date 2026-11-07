// Package community is the community module (backend plan 4.15, decisions
// C1 to C4): facilities and instantly confirmed bookings with rules and an
// optional fee, visitors with QR admission and resident approval, recurring
// visitors, the guard console (shared booth device, PIN switch) and parcels.
//
// Money, notifications and QR rendering do not live here: the module declares
// the small interfaces it needs (BookingCharger, Notifier, QRRenderer) with
// no-op defaults, and exposes VisitorCredentials and the Hooks for the access
// module (gate control).
package community

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Doc is the fields a client sent (see tenancy.Doc).
type Doc = tenancy.Doc

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// PlatformRunner lists organizations for the jobs, satisfied by *db.DB.
type PlatformRunner interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Money is an amount in minor units.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// BookingCharge asks the money module to post the fee of a confirmed booking
// as a booking_fee charge on the unit side. It returns the charge id, or
// uuid.Nil when no charge was created.
type BookingCharge struct {
	OrgID, SiteID, UnitID, BookingID uuid.UUID
	// Side is "owner" or "tenant".
	Side        string
	Amount      Money
	Description string
}

// BookingRefund asks the money module to cancel or credit the fee charge of a
// cancelled booking (full refund before the facility's cutoff).
type BookingRefund struct {
	OrgID, SiteID, BookingID, ChargeID uuid.UUID
	Amount                             Money
	Reason                             string
}

// BookingCharger is implemented by the money module (phase C). Both calls run
// inside the booking's transaction, so the charge exists exactly when the
// booking does. The default does nothing: the booking stores its fee and no
// charge.
type BookingCharger interface {
	ChargeBooking(ctx context.Context, tx pgx.Tx, c BookingCharge) (chargeID uuid.UUID, err error)
	RefundBooking(ctx context.Context, tx pgx.Tx, r BookingRefund) error
}

// Notifier hands a notification to the notifications worker (phase D) inside
// the transaction of the change that causes it (River is the outbox). The
// default drops it.
type Notifier interface {
	Notify(ctx context.Context, tx pgx.Tx, n jobs.Notify) error
}

// QRRenderer renders the text a QR code encodes ("obk1.<token>") as an image.
// format is "svg" or "png"; it returns the bytes and the content type. No
// default is registered: the QR image operations stay draft-only until one is.
type QRRenderer interface {
	Render(text, format string) (data []byte, contentType string, err error)
}

// VisitorDecision is what OnVisitorDecided receives.
type VisitorDecision struct {
	OrgID, SiteID, EntryID, UnitID uuid.UUID
	RequestID                      uuid.UUID
	Approved                       bool
	DecidedBy                      uuid.UUID
	At                             time.Time
}

// Hooks are the events the access module subscribes to. They run inside the
// transaction of the change, so enqueueing access.project there is atomic.
// Nil hooks are skipped.
type Hooks struct {
	// OnVisitorDecided runs after a resident approved or denied a walk-in.
	OnVisitorDecided func(ctx context.Context, tx pgx.Tx, d VisitorDecision) error
	// OnVisitorChanged runs after a visitor entry or recurring visitor was
	// created, changed, cancelled, revoked, or marked arrived or left;
	// sourceType is "visitor_entry" or "recurring_visitor".
	OnVisitorChanged func(ctx context.Context, tx pgx.Tx, siteID uuid.UUID, sourceType string, sourceID uuid.UUID) error
}

// Config configures the Service.
type Config struct {
	// Charger, Notifier and QR default to no-ops (QR to nil: no image).
	Charger  BookingCharger
	Notifier Notifier
	QR       QRRenderer
	Hooks    Hooks
	// EncryptionKey seals visitor QR tokens so the people of the unit can show
	// the QR again; without it the token is shown once, at creation.
	EncryptionKey []byte
	// BaseURL is the public URL without trailing slash (approval links).
	BaseURL string
	// Platform lists organizations for the jobs.
	Platform PlatformRunner
	Log      *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service is the community module.
type Service struct {
	db     TxRunner
	cfg    Config
	log    *slog.Logger
	now    func() time.Time
	cipher *crypto.FieldCipher
}

// NewService returns a Service.
func NewService(db TxRunner, cfg Config) (*Service, error) {
	s := &Service{db: db, cfg: cfg, log: cfg.Log, now: cfg.Now}
	if cfg.Charger == nil {
		s.cfg.Charger = noopCharger{}
	}
	if cfg.Notifier == nil {
		s.cfg.Notifier = noopNotifier{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if len(cfg.EncryptionKey) > 0 {
		c, err := crypto.NewFieldCipher(cfg.EncryptionKey, "community.qr_token")
		if err != nil {
			return nil, err
		}
		s.cipher = c
	}
	return s, nil
}

type noopCharger struct{}

func (noopCharger) ChargeBooking(context.Context, pgx.Tx, BookingCharge) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (noopCharger) RefundBooking(context.Context, pgx.Tx, BookingRefund) error { return nil }

type noopNotifier struct{}

func (noopNotifier) Notify(context.Context, pgx.Tx, jobs.Notify) error { return nil }

// QRImage renders text with the configured QRRenderer, or reports that none is.
func (s *Service) QRImage(text, format string) ([]byte, string, error) {
	if s.cfg.QR == nil {
		return nil, "", problem.New(501, problem.CodeInternalError, "QR images are not available on this instance")
	}
	return s.cfg.QR.Render(text, format)
}

// ---------------------------------------------------------------------------
// helpers shared by the files of the module

const (
	// visitorSlack is how long before and after its window a visitor QR is valid.
	visitorSlack = 30 * time.Minute
	// maxVisitorWindow caps one pre-registration; longer stays are recurring visitors.
	maxVisitorWindow = 7 * 24 * time.Hour
	// qrPrefix prefixes the token the QR encodes.
	qrPrefix = "obk1."
	// DefaultRetentionDays is how long visitor logs are kept unless the site says otherwise.
	DefaultRetentionDays = 90
	// DefaultRequestExpiry is how long a resident has to answer a walk-in request.
	DefaultRequestExpiry = 10 * time.Minute
)

func validation(field, code string) *problem.Error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

func fieldsErr(fields []problem.Field) error {
	if len(fields) == 0 {
		return nil
	}
	return problem.Validation(fields...)
}

func principalUser(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

// callerPerson returns the person the signed-in user is in the organization.
func callerPerson(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (uuid.UUID, bool, error) {
	u := principalUser(ctx)
	if u == nil {
		return uuid.Nil, false, nil
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM person WHERE org_id = $1 AND user_id = $2`, orgID, *u).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// siteInfo is what the module needs of a site.
type siteInfo struct {
	ID       uuid.UUID
	Loc      *time.Location
	Currency string
	Settings map[string]any
}

func loadSite(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (siteInfo, error) {
	var tz, cur string
	var settings []byte
	var status string
	err := tx.QueryRow(ctx, `SELECT timezone, currency, settings, status FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&tz, &cur, &settings, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return siteInfo{}, problem.NotFound("no such site")
	}
	if err != nil {
		return siteInfo{}, err
	}
	if status != "active" {
		return siteInfo{}, problem.New(409, problem.CodeSiteOffboarded, "the site was offboarded")
	}
	loc, lerr := time.LoadLocation(tz)
	if lerr != nil {
		loc = time.UTC
	}
	si := siteInfo{ID: siteID, Loc: loc, Currency: cur, Settings: map[string]any{}}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &si.Settings)
	}
	return si, nil
}

// settingInt reads an integer site setting (JSON numbers arrive as float64).
func (si siteInfo) settingInt(key string, def int) int {
	switch v := si.Settings[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	}
	return def
}

// requestExpiry is the site's time a resident has to answer (setting visitor_request_expiry_minutes).
func (si siteInfo) requestExpiry() time.Duration {
	return time.Duration(si.settingInt("visitor_request_expiry_minutes", int(DefaultRequestExpiry/time.Minute))) * time.Minute
}

// retentionDays is the site's visitor log retention (setting visitor_retention_days).
func (si siteInfo) retentionDays() int {
	return si.settingInt("visitor_retention_days", DefaultRetentionDays)
}

// unitInfo is a unit of a site.
type unitInfo struct {
	ID         uuid.UUID
	SiteID     uuid.UUID
	DoorNumber string
}

// loadUnit loads a unit of the site; a unit of another site or organization is a validation problem.
func loadUnit(ctx context.Context, tx pgx.Tx, orgID, siteID, unitID uuid.UUID, field string) (unitInfo, error) {
	u := unitInfo{ID: unitID, SiteID: siteID}
	err := tx.QueryRow(ctx, `SELECT door_number FROM unit WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, siteID, unitID).Scan(&u.DoorNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, problem.Validation(problem.Field{Field: field, Code: "invalid_value"})
	}
	return u, err
}

// siteScope loads the scope of a site from its row.
func siteScope(ctx context.Context, tx pgx.Tx, siteID uuid.UUID) (authz.Scope, error) {
	return authz.ScopeOfSite(ctx, tx, siteID)
}

// managerLevel reports whether the caller holds the module's manager rights
// (board and managers: bookings.manage) for the scope, as opposed to a
// resident who holds the portal permissions through an occupancy.
func managerLevel(ctx context.Context, sc authz.Scope) bool {
	return authz.Has(ctx, PermBookingsManage, sc)
}

// residentUnits are the units the caller currently lives in or owns at a site.
func residentUnits(ctx context.Context, siteID uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, o := range authz.GrantsFromContext(ctx).Current() {
		if o.SiteID == siteID {
			out = append(out, o.UnitID)
		}
	}
	return out
}

func isExclusion(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23P01"
}

// newQRToken returns the text the QR encodes, its token part's hash (the
// stored value) and, when a cipher is configured, the sealed token.
func (s *Service) newQRToken(orgID, rowID uuid.UUID) (text string, hash, sealed []byte, err error) {
	tok, h, err := crypto.NewToken()
	if err != nil {
		return "", nil, nil, err
	}
	if s.cipher != nil {
		sealed, err = s.cipher.Seal([]byte(tok), []byte(orgID.String()+rowID.String()))
		if err != nil {
			return "", nil, nil, err
		}
	}
	return qrPrefix + tok, h, sealed, nil
}

// openQRToken returns the QR text of a stored sealed token, or "" when none is kept.
func (s *Service) openQRToken(orgID, rowID uuid.UUID, sealed []byte) string {
	if s.cipher == nil || len(sealed) == 0 {
		return ""
	}
	tok, err := s.cipher.Open(sealed, []byte(orgID.String()+rowID.String()))
	if err != nil {
		return ""
	}
	return qrPrefix + string(tok)
}

// ParseQR extracts the token of a scanned "obk1.<token>" value and its hash.
func ParseQR(text string) (hash []byte, ok bool) {
	tok, found := strings.CutPrefix(strings.TrimSpace(text), qrPrefix)
	if !found || len(tok) < 20 || len(tok) > 200 {
		return nil, false
	}
	return crypto.HashToken(tok), true
}

// HashHex is the hex form of a stored token hash, as the access module's credential value.
func HashHex(h []byte) string { return hex.EncodeToString(h) }

// TokenHashHex is the hex SHA-256 of a token, for tests and tools.
func TokenHashHex(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // values here are ids and short strings
	}
	return b
}
