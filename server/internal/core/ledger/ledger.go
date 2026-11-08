// Package ledger is the ledger module (backend plan 4.4): funds, the chart of
// accounts, cash boxes, fiscal years, the append-only double-entry journal and
// the Poster every other money module posts through.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// TxRunner is the database access the modules need, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// DateLayout is the layout of civil dates ("2026-03-01") used throughout the money modules.
const DateLayout = "2006-01-02"

// Service is the ledger module.
type Service struct {
	db  TxRunner
	Now func() time.Time
}

// NewService returns a Service; now nil means time.Now.
func NewService(db TxRunner, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, Now: now}
}

// ParseDate parses a civil date.
func ParseDate(s string) (time.Time, error) { return time.Parse(DateLayout, s) }

// FormatDate formats a civil date.
func FormatDate(t time.Time) string { return t.Format(DateLayout) }

// Today is the current civil date in the site's time zone.
func Today(tz string, now time.Time) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return now.In(loc).Format(DateLayout)
}

// Location returns the site's time zone (UTC when unknown).
func Location(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// RequireSite checks perm for the site's own scope (derived from its row under
// RLS, never from the request) inside the tenant transaction.
func RequireSite(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) error {
	if siteID == uuid.Nil {
		return problem.Validation(problem.Field{Field: "site_id", Code: "required"})
	}
	sc, err := authz.ScopeOfSite(ctx, tx, siteID)
	if err != nil {
		return err
	}
	return authz.Require(ctx, perm, sc)
}

// HasSite is RequireSite as a boolean.
func HasSite(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) bool {
	sc, err := authz.ScopeOfSite(ctx, tx, siteID)
	return err == nil && authz.Has(ctx, perm, sc)
}

// SiteFor requires perm and loads the (active) site.
func SiteFor(ctx context.Context, tx pgx.Tx, perm authz.Permission, orgID, siteID uuid.UUID) (tenancy.Site, error) {
	if err := RequireSite(ctx, tx, perm, siteID); err != nil {
		return tenancy.Site{}, err
	}
	return tenancy.LoadSite(ctx, tx, orgID, siteID, false, false)
}

// Actor returns the signed-in user id, if any.
func Actor(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil && p.UserID != uuid.Nil {
		id := p.UserID
		return &id
	}
	return nil
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// Audit records a money audit event with a PII-safe diff of before and after
// (either may be nil). entity must be listed in pii.PlainEntities to keep values.
func Audit(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, action, entity string, targetID uuid.UUID, before, after any) error {
	diff, err := audit.Diff(entity, before, after)
	if err != nil {
		return err
	}
	ev := audit.Event{OrgID: orgID, SiteID: &siteID, Action: action, TargetType: entity, TargetID: targetID, Diff: diff}
	if p := authz.PrincipalFromContext(ctx); p != nil && p.UserID == uuid.Nil {
		ev.ActorKind = audit.ActorJob
	}
	return audit.Record(ctx, tx, ev)
}

// SystemContext returns a context for jobs: a principal without a user (audit
// events are recorded as job events) holding every money permission and the
// tenancy read permissions organization-wide. Jobs never run on behalf of a person.
func SystemContext(ctx context.Context, orgID uuid.UUID) context.Context {
	var items []authz.Grant
	for _, d := range Permissions() {
		items = append(items, authz.Grant{Permission: d.Name, ScopeType: authz.ScopeOrg})
	}
	for _, p := range []authz.Permission{tenancy.PermAllocationRead, tenancy.PermUnitRead, tenancy.PermSiteRead} {
		items = append(items, authz.Grant{Permission: p, ScopeType: authz.ScopeOrg})
	}
	ctx = authz.WithPrincipal(ctx, &authz.Principal{})
	return authz.WithGrants(ctx, authz.Grants{OrgID: orgID, Items: items})
}

// Currency returns the site's currency code.
func Currency(site tenancy.Site) money.Code { return money.Code(site.Currency) }

// Mny builds a Money of the site's currency.
func Mny(site tenancy.Site, amount int64) money.Money { return money.New(amount, Currency(site)) }

// IsLockedErr reports the database lock-date trigger firing.
func IsLockedErr(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23000" && len(pe.Message) > 10 && pe.Message[:10] == "entry date"
}

// Locked is the 409 locked_period problem.
func Locked(day, lock string) *problem.Error {
	return problem.New(409, problem.CodeLockedPeriod, fmt.Sprintf("%s is on or before the lock date %s", day, lock))
}

// DBProblem maps a database error to a problem.
func DBProblem(err error, what string) error {
	if IsLockedErr(err) {
		return problem.New(409, problem.CodeLockedPeriod, "the posting date is on or before the lock date")
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23000" {
		return problem.New(422, problem.CodeValidationFailed, pe.Message)
	}
	return tenancy.DBProblem(err, what)
}
