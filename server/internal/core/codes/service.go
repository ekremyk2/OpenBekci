package codes

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// PermRead lets a member read the labels.
const PermRead authz.Permission = "codes.read"

// Permissions declares the module's permission; the wiring registers it.
func Permissions() []authz.PermissionDef {
	return []authz.PermissionDef{{Name: PermRead, Description: "Read enum and status labels",
		Roles: authz.BuiltinRoles(), Scopes: []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock},
		Occupants: []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}}}
}

// Label is one code with its text.
type Label struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Codes is the organization-level answer: enum labels per enum, problem codes,
// and the site-defined ticket statuses.
type Codes struct {
	Locale         string             `json:"locale"`
	Enums          map[string][]Label `json:"enums"`
	Errors         []Label            `json:"errors"`
	TicketStatuses []Label            `json:"ticket_statuses"`
}

// TxRunner is the database access the service needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Service answers GET /v1/orgs/{org_id}/codes.
type Service struct{ db TxRunner }

// NewService returns a Service.
func NewService(db TxRunner) *Service { return &Service{db: db} }

// errorSections are the sections that are problem and field error codes
// rather than enums.
var errorSections = []string{"problem_codes", "field_error_codes", "import_codes"}

func sorted(m map[string]string) []Label {
	out := make([]Label, 0, len(m))
	for c, l := range m {
		out = append(out, Label{Code: c, Label: l})
	}
	slices.SortFunc(out, func(a, b Label) int {
		switch {
		case a.Code < b.Code:
			return -1
		case a.Code > b.Code:
			return 1
		}
		return 0
	})
	return out
}

// List returns the labels in the locale (the caller's default is the client's
// choice: it passes locale). With siteID it also lists the site's ticket
// statuses (code: the status id, label: the site's name for it).
func (s *Service) List(ctx context.Context, orgID uuid.UUID, locale string, siteID *uuid.UUID) (Codes, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return Codes{}, err
	}
	loc, sections := Labels(locale)
	out := Codes{Locale: loc, Enums: map[string][]Label{}, Errors: []Label{}, TicketStatuses: []Label{}}
	for name, m := range sections {
		if slices.Contains(errorSections, name) {
			out.Errors = append(out.Errors, sorted(m)...)
			continue
		}
		out.Enums[name] = sorted(m)
	}
	slices.SortFunc(out.Errors, func(a, b Label) int {
		if a.Code < b.Code {
			return -1
		}
		if a.Code > b.Code {
			return 1
		}
		return 0
	})
	if siteID == nil {
		return out, nil
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := authz.ScopeOfSite(ctx, tx, *siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermRead, sc); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id::text, name FROM ticket_status WHERE org_id = $1 AND site_id = $2 AND NOT archived ORDER BY position`, orgID, *siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l Label
			if err := rows.Scan(&l.Code, &l.Label); err != nil {
				return err
			}
			out.TicketStatuses = append(out.TicketStatuses, l)
		}
		return rows.Err()
	})
	if err != nil {
		return Codes{}, tenancy.DBProblem(err, "codes")
	}
	return out, nil
}
