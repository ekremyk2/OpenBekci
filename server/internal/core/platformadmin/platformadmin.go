// Package platformadmin holds instance-operator operations that cross
// organizations. It is the one core module allowed the platform (BYPASSRLS)
// transaction, apart from the cross-organization part of GET /v1/me.
package platformadmin

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TxRunner is satisfied by *db.DB.
type TxRunner interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Service is the platform administration module.
type Service struct{ db TxRunner }

// NewService returns a Service.
func NewService(db TxRunner) *Service { return &Service{db: db} }

var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// CreateOrgParams describes a new organization and its first administrator.
type CreateOrgParams struct {
	Slug          string
	Name          string
	CountryCode   string // ISO 3166-1 alpha-2, default TR
	DefaultLocale string // default tr
	Timezone      string // default Europe/Istanbul
	// AdminUserID is the user who becomes org_admin.
	AdminUserID uuid.UUID
	// AdminName is the display name of the administrator's person.
	AdminName string
}

// Org is a created organization.
type Org struct {
	ID       uuid.UUID
	Slug     string
	PersonID uuid.UUID
}

// CreateOrganization creates an organization, a person linked to the admin
// user and an org-scoped org_admin membership, in one transaction.
func (s *Service) CreateOrganization(ctx context.Context, p CreateOrgParams) (Org, error) {
	if !slugRE.MatchString(p.Slug) {
		return Org{}, problem.Validation(problem.Field{Field: "slug", Code: "invalid_format"})
	}
	if p.Name == "" {
		return Org{}, problem.Validation(problem.Field{Field: "name", Code: "required"})
	}
	if p.CountryCode == "" {
		p.CountryCode = "TR"
	}
	if p.DefaultLocale == "" {
		p.DefaultLocale = "tr"
	}
	if p.Timezone == "" {
		p.Timezone = "Europe/Istanbul"
	}
	if p.AdminName == "" {
		p.AdminName = "Administrator"
	}
	org := Org{ID: id.New(), Slug: p.Slug, PersonID: id.New()}
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO organization (id, org_id, slug, name, country_code, default_locale, timezone)
			VALUES ($1, $1, $2, $3, $4, $5, $6)`, org.ID, p.Slug, p.Name, p.CountryCode, p.DefaultLocale, p.Timezone); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO person (id, org_id, user_id, display_name) VALUES ($1, $2, $3, $4)`,
			org.PersonID, org.ID, p.AdminUserID, p.AdminName); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO membership (id, org_id, person_id, role, scope_type) VALUES ($1, $2, $3, 'org_admin', 'org')`,
			id.New(), org.ID, org.PersonID)
		return err
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return Org{}, problem.Conflict("an organization with this slug already exists")
		}
		return Org{}, problem.Internal(fmt.Errorf("create organization: %w", err))
	}
	return org, nil
}
