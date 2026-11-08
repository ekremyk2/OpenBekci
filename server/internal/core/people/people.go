// Package people is the people module (backend plan 4.3, decisions S3 to S8):
// persons with encrypted national ids, occupancies (owner, tenant,
// household), role memberships, invitations and the site join code with unit
// claims, the directory with consent, the move-out use case, vendors and the
// spreadsheet import of units and people.
package people

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Doc is the fields a client sent (see tenancy.Doc).
type Doc = tenancy.Doc

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// OccupancyEnded describes an occupancy that was ended or got an end date:
// by a move-out (the leaving person and, with it, their household), by editing
// valid_to, or by ending a household member.
type OccupancyEnded struct {
	OrgID, SiteID, UnitID, PersonID, OccupancyID uuid.UUID
	Kind, Side                                   string
	// EndsOn is the first day the person no longer lives there (valid_to);
	// it may lie in the future.
	EndsOn time.Time
}

// Hooks are the events other modules subscribe to. They run inside the
// transaction of the change, so what they do is atomic with it. Nil hooks are
// skipped.
type Hooks struct {
	// OnOccupancyEnded runs after an occupancy was given an end date (move-out,
	// household member ended, or valid_to set by an update); the community
	// module revokes the visitor credentials the person registered.
	OnOccupancyEnded func(ctx context.Context, tx pgx.Tx, e OccupancyEnded) error
}

// Config configures the Service.
type Config struct {
	// Hooks are the events other modules subscribe to.
	Hooks Hooks
	// EncryptionKey seals national ids; without it they cannot be stored.
	EncryptionKey []byte
	// BaseURL is the public URL without trailing slash (invitation links).
	BaseURL      string
	InstanceName string
	// SMS and Mail deliver invitations; either may be nil (that channel is then unavailable).
	SMS  sms.Gateway
	Mail mail.Sender
	// DefaultCountryCode is the calling code for national phone numbers ("90").
	DefaultCountryCode string
	Log                *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service is the people module.
type Service struct {
	db     TxRunner
	ident  *identity.Service
	cipher *crypto.FieldCipher
	lim    limits
	cfg    Config
	log    *slog.Logger
	now    func() time.Time
}

// NewService returns a Service. ident creates and finds sign-in accounts for invitations.
func NewService(db TxRunner, ident *identity.Service, cfg Config) (*Service, error) {
	s := &Service{db: db, ident: ident, cfg: cfg, log: cfg.Log, now: cfg.Now, lim: newLimits()}
	if len(cfg.EncryptionKey) > 0 {
		c, err := crypto.NewFieldCipher(cfg.EncryptionKey, "person.national_id")
		if err != nil {
			return nil, err
		}
		s.cipher = c
	}
	if s.cfg.DefaultCountryCode == "" {
		s.cfg.DefaultCountryCode = "90"
	}
	if s.cfg.InstanceName == "" {
		s.cfg.InstanceName = "OpenBekci"
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// occupancyEnded runs the OnOccupancyEnded hook for an occupancy that now ends on endsOn.
func (s *Service) occupancyEnded(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, occ Occupancy, endsOn time.Time) error {
	h := s.cfg.Hooks.OnOccupancyEnded
	if h == nil {
		return nil
	}
	return h(ctx, tx, OccupancyEnded{OrgID: orgID, SiteID: occ.SiteID, UnitID: occ.UnitID, PersonID: occ.PersonID, OccupancyID: occ.ID, Kind: occ.Kind, Side: occ.Side, EndsOn: endsOn})
}

// siteGrants says where perm is held: organization-wide, or for the listed
// sites (derived from the caller's grants, never from the request).
func siteGrants(ctx context.Context, perm authz.Permission) (orgWide bool, sites []uuid.UUID) {
	ow, sites, _ := authz.GrantsFromContext(ctx).ScopeIDs(perm)
	return ow, sites
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // values here are ids and strings
	}
	return b
}

func principalID(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

func fieldProblem(field, code string) *problem.Error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// today is the current date in UTC at midnight.
func (s *Service) today() time.Time {
	n := s.now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
