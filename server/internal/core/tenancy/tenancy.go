// Package tenancy is the tenancy module (backend plan 4.1): organization settings, sites, blocks,
// units, allocation keys, unit tags, vehicles, spreadsheet import and site export/import.
package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
	_ "time/tzdata" // IANA zones for timezone validation on hosts without a zoneinfo database

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/extval"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Permissions of the module.
const (
	PermOrgRead   authz.Permission = "org.read"
	PermOrgManage authz.Permission = "org.manage"
)

// orgPermissions declares the organization settings permissions.
func orgPermissions() []authz.PermissionDef {
	org := []authz.ScopeType{authz.ScopeOrg}
	anywhere := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock} // any member reads the settings
	return []authz.PermissionDef{
		{Name: PermOrgRead, Description: "Read the organization's settings",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember, authz.RoleAuditor, authz.RoleAccountant, authz.RoleStaff}, Scopes: anywhere},
		{Name: PermOrgManage, Description: "Change the organization's settings",
			Roles: []authz.Role{authz.RoleOrgAdmin}, Scopes: org},
	}
}

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Service is the tenancy module.
type Service struct {
	db         TxRunner
	archiveKey []byte // signs site archives; see WithArchiveKey
	siteHook   SiteHook
	refHint    BankReferenceHint
	validators extval.Validators
}

// NewService returns a Service.
func NewService(db TxRunner) *Service { return &Service{db: db} }

// SiteHook is called inside the transaction that creates a site, after the
// site row exists. The packs module uses it to apply the presets of the
// organization's enabled packs and to emit site.created. An error aborts the
// site creation.
type SiteHook interface {
	SiteCreated(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) error
}

// WithValidators sets the validators of the packs enabled for an organization
// (vehicle plates, the IBAN of the bank details) and returns s.
func (s *Service) WithValidators(v extval.Validators) *Service {
	s.validators = v
	return s
}

// WithSiteHook sets the hook called when a site is created and returns s.
func (s *Service) WithSiteHook(h SiteHook) *Service {
	s.siteHook = h
	return s
}

// Org is an organization as the API shows it. The JSON tags are the audit
// field names (see audit.Diff).
type Org struct {
	ID            uuid.UUID      `json:"id"`
	Slug          string         `json:"slug"`
	Name          string         `json:"name"`
	Status        string         `json:"status"`
	CountryCode   string         `json:"country_code"`
	DefaultLocale string         `json:"default_locale"`
	Timezone      string         `json:"timezone"`
	Settings      map[string]any `json:"settings"`
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// auditView is what an org.update diff compares: the editable settings only.
type auditView struct {
	Name          string         `json:"name"`
	CountryCode   string         `json:"country_code"`
	DefaultLocale string         `json:"default_locale"`
	Timezone      string         `json:"timezone"`
	Settings      map[string]any `json:"settings"`
}

func (o Org) auditView() auditView {
	return auditView{Name: o.Name, CountryCode: o.CountryCode, DefaultLocale: o.DefaultLocale, Timezone: o.Timezone, Settings: o.Settings}
}

const orgColumns = `id, slug, name, status, country_code, default_locale, timezone, settings, version, created_at, updated_at`

func scanOrg(row pgx.Row) (Org, error) {
	var o Org
	var settings []byte
	err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.Status, &o.CountryCode, &o.DefaultLocale, &o.Timezone, &settings, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	o.Settings = map[string]any{}
	_ = json.Unmarshal(settings, &o.Settings)
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return o, err
}

// Get returns the organization. It needs org.read at any scope inside the
// organization: a member limited to one site or block reads the settings too.
func (s *Service) Get(ctx context.Context, orgID uuid.UUID) (Org, error) {
	if err := authz.RequireInOrg(ctx, PermOrgRead, orgID); err != nil {
		return Org{}, err
	}
	var o Org
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		o, err = scanOrg(tx.QueryRow(ctx, `SELECT `+orgColumns+` FROM organization WHERE id = $1`, orgID))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Org{}, problem.NotFound("no such organization")
	}
	if err != nil {
		return Org{}, problem.Internal(fmt.Errorf("get organization: %w", err))
	}
	return o, nil
}

// Patch is a partial update; nil fields stay as they are.
type Patch struct {
	Name          *string
	CountryCode   *string
	DefaultLocale *string
	Timezone      *string
	// Settings is merged key by key into the stored settings; a null value removes the key.
	Settings map[string]any
}

var (
	localeRE  = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	countryRE = regexp.MustCompile(`^[A-Z]{2}$`)
)

func (p Patch) validate() error {
	var fields []problem.Field
	if p.Name != nil {
		if n := len([]rune(*p.Name)); n < 1 || n > 200 {
			fields = append(fields, problem.Field{Field: "name", Code: "invalid_value"})
		}
	}
	if p.CountryCode != nil && !countryRE.MatchString(*p.CountryCode) {
		fields = append(fields, problem.Field{Field: "country_code", Code: "invalid_format"})
	}
	if p.DefaultLocale != nil && !localeRE.MatchString(*p.DefaultLocale) {
		fields = append(fields, problem.Field{Field: "default_locale", Code: "invalid_format"})
	}
	if p.Timezone != nil {
		if _, err := time.LoadLocation(*p.Timezone); err != nil || *p.Timezone == "" || *p.Timezone == "Local" {
			fields = append(fields, problem.Field{Field: "timezone", Code: "invalid_value"})
		}
	}
	fields = append(fields, validatePaymentSettings(p.Settings)...)
	if len(fields) > 0 {
		return problem.Validation(fields...)
	}
	return nil
}

// Update applies p if the organization is still at expectedVersion (the
// If-Match value) and records org.update with the diff in the same
// transaction. It answers 409 version_conflict otherwise. A patch that changes
// nothing returns the current organization without writing. It needs org.manage.
func (s *Service) Update(ctx context.Context, orgID uuid.UUID, expectedVersion int, p Patch) (Org, error) {
	if err := authz.Require(ctx, PermOrgManage, authz.Scope{OrgID: orgID}); err != nil {
		return Org{}, err
	}
	if err := p.validate(); err != nil {
		return Org{}, err
	}
	var out Org
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanOrg(tx.QueryRow(ctx, `SELECT `+orgColumns+` FROM organization WHERE id = $1 FOR UPDATE`, orgID))
		if err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return errVersion
		}
		next := before
		if p.Name != nil {
			next.Name = *p.Name
		}
		if p.CountryCode != nil {
			next.CountryCode = *p.CountryCode
		}
		if p.DefaultLocale != nil {
			next.DefaultLocale = *p.DefaultLocale
		}
		if p.Timezone != nil {
			next.Timezone = *p.Timezone
		}
		if p.Settings != nil {
			merged := map[string]any{}
			for k, v := range before.Settings {
				merged[k] = v
			}
			for k, v := range p.Settings {
				if v == nil {
					delete(merged, k)
				} else {
					merged[k] = v
				}
			}
			next.Settings = merged
			if err := guardOrgMerchantChange(ctx, tx, orgID, before.Settings, merged); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("organization", before.auditView(), next.auditView())
		if err != nil {
			return err
		}
		if len(diff) == 0 {
			out = before
			return nil
		}
		settingsJSON, _ := json.Marshal(next.Settings)
		out, err = scanOrg(tx.QueryRow(ctx, `
			UPDATE organization SET name = $2, country_code = $3, default_locale = $4, timezone = $5, settings = $6,
			       updated_at = now(), version = version + 1
			WHERE id = $1 RETURNING `+orgColumns, orgID, next.Name, next.CountryCode, next.DefaultLocale, next.Timezone, settingsJSON))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, Action: "org.update", TargetType: "organization", TargetID: orgID, Diff: diff})
	})
	switch {
	case errors.Is(err, errVersion):
		return Org{}, problem.New(409, problem.CodeVersionConflict, "the organization was changed by someone else; reload and try again")
	case errors.Is(err, pgx.ErrNoRows):
		return Org{}, problem.NotFound("no such organization")
	case err != nil:
		var pe *problem.Error
		if errors.As(err, &pe) {
			return Org{}, err
		}
		return Org{}, problem.Internal(fmt.Errorf("update organization: %w", err))
	}
	return out, nil
}

var errVersion = errors.New("version mismatch")
