// Package privacy is the privacy module (backend plan 4.13 and 16): consent
// decisions on the organization's privacy texts, the per-person data export
// (a job that files a JSON and a PDF as private documents), the privacy erase
// and the retention job.
package privacy

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// DB is the database access of the module, satisfied by *db.DB.
type DB interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// InsertFunc inserts a River job in the caller's transaction; the wiring sets
// it once the job client exists (SetInsert).
type InsertFunc func(ctx context.Context, tx pgx.Tx, args river.JobArgs) error

// Config configures the Service.
type Config struct {
	// Files stores the export files; Notifier tells the person it is ready.
	Files    Uploader
	Notifier jobs.Notifier
	Log      *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Service is the privacy module.
type Service struct {
	db     DB
	cfg    Config
	insert atomic.Pointer[InsertFunc]
	now    func() time.Time
	log    *slog.Logger
}

// NewService returns a Service.
func NewService(db DB, cfg Config) *Service {
	s := &Service{db: db, cfg: cfg, now: cfg.Now, log: cfg.Log}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// SetInsert gives the service the job queue (set before the first export request).
func (s *Service) SetInsert(f InsertFunc) { s.insert.Store(&f) }

// Consent decisions.
const (
	Accepted = "accepted"
	Declined = "declined"
)

// State is a user's current decision on one consent text.
type State struct {
	Version   string    `json:"version"`
	Decision  string    `json:"decision"`
	DecidedAt time.Time `json:"decided_at"`
}

// Decide records the user's decision on a consent text of the organization
// (the caller has checked that the text and version exist) and audits it.
func (s *Service) Decide(ctx context.Context, orgID, userID uuid.UUID, purpose, version, decision string) error {
	return s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO consent (org_id, user_id, purpose, version, decision, decided_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (org_id, user_id, purpose) DO UPDATE SET version = EXCLUDED.version, decision = EXCLUDED.decision, decided_at = EXCLUDED.decided_at`,
			orgID, userID, purpose, version, decision, s.now()); err != nil {
			return err
		}
		diff, err := audit.Diff("consent", nil, map[string]any{"purpose": purpose, "version": version, "decision": decision})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, Action: "privacy.consent_" + decision, TargetType: "user", TargetID: userID, Diff: diff})
	})
}

// States returns the user's decisions in the organization by purpose.
func (s *Service) States(ctx context.Context, orgID, userID uuid.UUID) (map[string]State, error) {
	out := map[string]State{}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT purpose, version, decision, decided_at FROM consent WHERE org_id = $1 AND user_id = $2`, orgID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			var st State
			if err := rows.Scan(&p, &st.Version, &st.Decision, &st.DecidedAt); err != nil {
				return err
			}
			st.DecidedAt = st.DecidedAt.UTC()
			out[p] = st
		}
		return rows.Err()
	})
	return out, err
}

// Accepted lists, per organization, the consent texts (purpose to version) the
// user accepted: GET /v1/me flags the required ones that are missing.
func (s *Service) Accepted(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]map[string]string, error) {
	out := map[uuid.UUID]map[string]string{}
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT org_id, purpose, version FROM consent WHERE user_id = $1 AND decision = 'accepted'`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var org uuid.UUID
			var p, v string
			if err := rows.Scan(&org, &p, &v); err != nil {
				return err
			}
			if out[org] == nil {
				out[org] = map[string]string{}
			}
			out[org][p] = v
		}
		return rows.Err()
	})
	return out, err
}

// Required reports whether a consent text must be accepted to use the
// organization: the privacy notice. The per-purpose texts of a pack (açık
// rıza) are choices and never required.
func Required(purpose string) bool {
	return strings.HasSuffix(purpose, ".privacy_notice")
}

var errNoSite = problem.Conflict("the person has no site to file a document in")

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
