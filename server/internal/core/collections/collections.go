// Package collections is the collections module (backend plan 4.8, M5):
// dunning stages per site, the hourly stage job and manual reminder runs,
// ihtarname (warning letter) generation, collection cases that freeze late fees
// and reminders, and the debt breakdown export.
package collections

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Service is the collections module.
type Service struct {
	db       ledger.TxRunner
	bill     *billing.Service
	Notifier ledger.Notifier
	Now      func() time.Time
}

// NewService returns a Service; now nil means time.Now, notifier nil drops notifications.
func NewService(db ledger.TxRunner, bill *billing.Service, now func() time.Time, n ledger.Notifier) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, bill: bill, Now: now, Notifier: n}
}

func (s *Service) run(ctx context.Context, orgID uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.db.InTenantTx(ctx, orgID, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return ledger.DBProblem(err, what)
}

func bad(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// Channels a stage or run can use.
var knownChannels = []string{"in_app", "email", "sms", "letter"}

func validChannels(ch []string, field string) error {
	if len(ch) == 0 {
		return bad(field, "required")
	}
	for _, c := range ch {
		if !slices.Contains(knownChannels, c) {
			return bad(field, "invalid_value")
		}
	}
	return nil
}

// Stage is a dunning stage as the API shows it.
type Stage struct {
	ID           uuid.UUID `json:"id"`
	OrgID        uuid.UUID `json:"org_id"`
	SiteID       uuid.UUID `json:"site_id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	DaysAfterDue int       `json:"days_after_due"`
	Channels     []string  `json:"channels"`
	TemplateKey  *string   `json:"template_key"`
	Position     int       `json:"position"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Version      int       `json:"version"`
}

const stageSel = `SELECT id, org_id, site_id, name, kind, days_after_due, channels, template_key, position, enabled, created_at, updated_at, version FROM dunning_stage`

func scanStage(r pgx.Row) (st Stage, err error) {
	err = r.Scan(&st.ID, &st.OrgID, &st.SiteID, &st.Name, &st.Kind, &st.DaysAfterDue, &st.Channels, &st.TemplateKey, &st.Position, &st.Enabled, &st.CreatedAt, &st.UpdatedAt, &st.Version)
	st.CreatedAt, st.UpdatedAt = st.CreatedAt.UTC(), st.UpdatedAt.UTC()
	return
}

// StageInput creates a stage.
type StageInput struct {
	SiteID       uuid.UUID `json:"site_id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	DaysAfterDue int       `json:"days_after_due"`
	Channels     []string  `json:"channels"`
	TemplateKey  *string   `json:"template_key"`
	Position     *int      `json:"position"`
	Enabled      *bool     `json:"enabled"`
}

func validKind(k string) bool { return k == "reminder" || k == "ihtarname" }

// CreateStage adds a stage (collections.manage).
func (s *Service) CreateStage(ctx context.Context, orgID uuid.UUID, in StageInput) (Stage, error) {
	if in.Name == "" {
		return Stage{}, bad("name", "required")
	}
	if !validKind(in.Kind) {
		return Stage{}, bad("kind", "invalid_value")
	}
	if in.DaysAfterDue < 0 {
		return Stage{}, bad("days_after_due", "too_small")
	}
	if in.Channels == nil {
		in.Channels = []string{"in_app"}
	}
	if err := validChannels(in.Channels, "channels"); err != nil {
		return Stage{}, err
	}
	var out Stage
	err := s.run(ctx, orgID, "dunning stage", func(tx pgx.Tx) error {
		if _, err := tenancy.LoadSite(ctx, tx, orgID, in.SiteID, false, false); err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermCollectionsManage, in.SiteID); err != nil {
			return err
		}
		pos := 0
		if in.Position != nil {
			pos = *in.Position
		} else if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM dunning_stage WHERE org_id=$1 AND site_id=$2`, orgID, in.SiteID).Scan(&pos); err != nil {
			return err
		}
		enabled := in.Enabled == nil || *in.Enabled
		sid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO dunning_stage (id, org_id, site_id, name, kind, days_after_due, channels, template_key, position, enabled) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			sid, orgID, in.SiteID, in.Name, in.Kind, in.DaysAfterDue, in.Channels, in.TemplateKey, pos, enabled); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "dunning_stage.create", "dunning_stage", sid, nil, map[string]any{"name": in.Name, "kind": in.Kind, "days_after_due": in.DaysAfterDue}); err != nil {
			return err
		}
		var err error
		out, err = scanStage(tx.QueryRow(ctx, stageSel+` WHERE org_id=$1 AND id=$2`, orgID, sid))
		return err
	})
	return out, err
}

// ListStages lists a site's stages in order (money.read).
func (s *Service) ListStages(ctx context.Context, orgID, siteID uuid.UUID, p ledger.PageParams) ([]Stage, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Stage
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, stageSel+` WHERE org_id=$1 AND site_id=$2 AND id > $3 ORDER BY id LIMIT $4`, orgID, siteID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			st, err := scanStage(rows)
			if err != nil {
				return err
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(st Stage) uuid.UUID { return st.ID })
	return items, next, nil
}

// GetStage returns a stage (money.read).
func (s *Service) GetStage(ctx context.Context, orgID, stageID uuid.UUID) (Stage, error) {
	var st Stage
	err := s.run(ctx, orgID, "dunning stage", func(tx pgx.Tx) (err error) {
		if st, err = scanStage(tx.QueryRow(ctx, stageSel+` WHERE org_id=$1 AND id=$2`, orgID, stageID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, st.SiteID)
	})
	return st, err
}

// StageUpdate is a partial update; TemplateKey is nil to leave it and points to a nil string to clear it.
type StageUpdate struct {
	Name         *string
	Kind         *string
	DaysAfterDue *int
	Channels     *[]string
	TemplateKey  **string
	Position     *int
	Enabled      *bool
}

// UpdateStage changes a stage if version matches (collections.manage).
func (s *Service) UpdateStage(ctx context.Context, orgID, stageID uuid.UUID, version int, u StageUpdate) (Stage, error) {
	var out Stage
	err := s.run(ctx, orgID, "dunning stage", func(tx pgx.Tx) error {
		st, err := scanStage(tx.QueryRow(ctx, stageSel+` WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, stageID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermCollectionsManage, st.SiteID); err != nil {
			return err
		}
		if st.Version != version {
			return problem.New(409, problem.CodeVersionConflict, "the stage was changed by someone else")
		}
		if u.Name != nil {
			if *u.Name == "" {
				return bad("name", "required")
			}
			st.Name = *u.Name
		}
		if u.Kind != nil {
			if !validKind(*u.Kind) {
				return bad("kind", "invalid_value")
			}
			st.Kind = *u.Kind
		}
		if u.DaysAfterDue != nil {
			if *u.DaysAfterDue < 0 {
				return bad("days_after_due", "too_small")
			}
			st.DaysAfterDue = *u.DaysAfterDue
		}
		if u.Channels != nil {
			if err := validChannels(*u.Channels, "channels"); err != nil {
				return err
			}
			st.Channels = *u.Channels
		}
		if u.TemplateKey != nil {
			st.TemplateKey = *u.TemplateKey
		}
		if u.Position != nil {
			st.Position = *u.Position
		}
		if u.Enabled != nil {
			st.Enabled = *u.Enabled
		}
		if _, err := tx.Exec(ctx, `UPDATE dunning_stage SET name=$3, kind=$4, days_after_due=$5, channels=$6, template_key=$7, position=$8, enabled=$9, updated_at=now(), version=version+1 WHERE org_id=$1 AND id=$2`,
			orgID, stageID, st.Name, st.Kind, st.DaysAfterDue, st.Channels, st.TemplateKey, st.Position, st.Enabled); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, st.SiteID, "dunning_stage.update", "dunning_stage", stageID, nil, map[string]any{"name": st.Name, "enabled": st.Enabled, "days_after_due": st.DaysAfterDue}); err != nil {
			return err
		}
		out, err = scanStage(tx.QueryRow(ctx, stageSel+` WHERE org_id=$1 AND id=$2`, orgID, stageID))
		return err
	})
	return out, err
}

// DeleteStage removes a stage that has no events (collections.manage); otherwise disable it.
func (s *Service) DeleteStage(ctx context.Context, orgID, stageID uuid.UUID, version int) error {
	return s.run(ctx, orgID, "dunning stage", func(tx pgx.Tx) error {
		st, err := scanStage(tx.QueryRow(ctx, stageSel+` WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, stageID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermCollectionsManage, st.SiteID); err != nil {
			return err
		}
		if st.Version != version {
			return problem.New(409, problem.CodeVersionConflict, "the stage was changed by someone else")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM dunning_event WHERE org_id=$1 AND stage_id=$2) + (SELECT count(*) FROM dunning_run WHERE org_id=$1 AND stage_id=$2)`, orgID, stageID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problem.Conflict("the stage has been used; disable it instead")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM dunning_stage WHERE org_id=$1 AND id=$2`, orgID, stageID); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, st.SiteID, "dunning_stage.delete", "dunning_stage", stageID, nil, nil)
	})
}
