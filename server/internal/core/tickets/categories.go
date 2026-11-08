package tickets

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/opsx"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Category groups tickets and sets their default visibility and assignee role.
type Category struct {
	ID                  uuid.UUID `json:"id"`
	OrgID               uuid.UUID `json:"org_id"`
	SiteID              uuid.UUID `json:"site_id"`
	Name                string    `json:"name"`
	DefaultVisibility   string    `json:"default_visibility"`
	DefaultAssigneeRole *string   `json:"default_assignee_role"`
	Position            int       `json:"position"`
	Archived            bool      `json:"archived"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Version             int       `json:"version"`
}

const categoryColumns = `id, org_id, site_id, name, default_visibility, default_assignee_role, position, archived, created_at, updated_at, version`

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.OrgID, &c.SiteID, &c.Name, &c.DefaultVisibility, &c.DefaultAssigneeRole, &c.Position, &c.Archived, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, err
}

// Archived filter values of ListCategories.
const (
	ArchivedActive = "active"
	ArchivedOnly   = "archived"
	ArchivedAll    = "all"
)

// ListCategories lists the site's categories; archived is active (default),
// archived or all.
func (s *Service) ListCategories(ctx context.Context, orgID, siteID uuid.UUID, archived string, p tenancy.PageParams) ([]Category, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	if archived == "" {
		archived = ArchivedActive
	}
	if !slices.Contains([]string{ArchivedActive, ArchivedOnly, ArchivedAll}, archived) {
		return nil, "", problem.Validation(problem.Field{Field: "archived", Code: "invalid_value"})
	}
	var out []Category
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := opsx.SiteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if _, err := opsx.RequireAny(ctx, sc, AnyView); err != nil {
			return err
		}
		if err := ensureWorkflow(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		// archived categories are for management; everybody else only sees active ones
		if archived != ArchivedActive && !authz.Has(ctx, PermManage, sc) && !authz.Has(ctx, PermRead, sc) {
			archived = ArchivedActive
		}
		rows, err := tx.Query(ctx, `SELECT `+categoryColumns+` FROM ticket_category WHERE org_id = $1 AND site_id = $2
			AND ($3 = '00000000-0000-0000-0000-000000000000'::uuid OR (position, id) > (SELECT position, id FROM ticket_category WHERE org_id = $1 AND id = $3))
			AND ($4 = 'all' OR ($4 = 'archived') = archived) ORDER BY position, id LIMIT $5`, orgID, siteID, after, archived, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCategory(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "categories")
	}
	items, next := tenancy.Page(out, limit, func(c Category) uuid.UUID { return c.ID })
	return items, next, nil
}

func (s *Service) loadCategory(ctx context.Context, tx pgx.Tx, orgID, catID uuid.UUID, forUpdate bool) (Category, error) {
	q := `SELECT ` + categoryColumns + ` FROM ticket_category WHERE org_id = $1 AND id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	c, err := scanCategory(tx.QueryRow(ctx, q, orgID, catID))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, problem.NotFound("no such category")
	}
	return c, err
}

// GetCategory returns one category.
func (s *Service) GetCategory(ctx context.Context, orgID, catID uuid.UUID) (Category, error) {
	var c Category
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		if c, err = s.loadCategory(ctx, tx, orgID, catID, false); err != nil {
			return err
		}
		sc, err := opsx.SiteScope(ctx, tx, c.SiteID)
		if err != nil {
			return err
		}
		_, err = opsx.RequireAny(ctx, sc, AnyView)
		return err
	})
	return c, tenancy.DBProblem(err, "category")
}

var categoryAssigneeRoles = []string{"site_manager", "staff", "board_member"}

func checkCategory(d tenancy.Doc, create bool) (name *string, vis *string, role *string, pos *int, archived *bool, err error) {
	var bad []problem.Field
	unknown := d.Unknown("site_id", "name", "default_visibility", "default_assignee_role", "position", "archived")
	bad = append(bad, unknown...)
	if name, err = d.String("name"); err != nil {
		return
	}
	if name != nil {
		t := strings.TrimSpace(*name)
		name = &t
		if l := len([]rune(t)); l < 1 || l > 80 {
			bad = append(bad, problem.Field{Field: "name", Code: "invalid_length"})
		}
	} else if create || d.Has("name") {
		bad = append(bad, problem.Field{Field: "name", Code: "required"})
	}
	if vis, err = d.String("default_visibility"); err != nil {
		return
	}
	if vis != nil && *vis != "private" && *vis != "common" {
		bad = append(bad, problem.Field{Field: "default_visibility", Code: "invalid_value"})
	}
	if role, err = d.String("default_assignee_role"); err != nil {
		return
	}
	if role != nil && !slices.Contains(categoryAssigneeRoles, *role) {
		bad = append(bad, problem.Field{Field: "default_assignee_role", Code: "invalid_value"})
	}
	if d.Has("position") {
		var p int
		if err = d.Get("position", &p); err != nil {
			return
		}
		pos = &p
	}
	if d.Has("archived") {
		var a bool
		if err = d.Get("archived", &a); err != nil {
			return
		}
		archived = &a
	}
	if len(bad) > 0 {
		err = problem.Validation(bad...)
	}
	return
}

// CreateCategory adds a category to a site (tickets.manage).
func (s *Service) CreateCategory(ctx context.Context, orgID uuid.UUID, d tenancy.Doc) (Category, error) {
	siteStr, err := d.String("site_id")
	if err != nil {
		return Category{}, err
	}
	siteID, perr := uuid.Parse(deref(siteStr))
	if perr != nil {
		return Category{}, problem.Validation(problem.Field{Field: "site_id", Code: "required"})
	}
	name, vis, role, pos, archived, err := checkCategory(d, true)
	if err != nil {
		return Category{}, err
	}
	var c Category
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := opsx.SiteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermManage, sc); err != nil {
			return err
		}
		if err := ensureWorkflow(ctx, tx, orgID, siteID); err != nil {
			return err
		}
		v := "private"
		if vis != nil {
			v = *vis
		}
		p := 0
		if pos != nil {
			p = *pos
		} else if err := tx.QueryRow(ctx, `SELECT coalesce(max(position), 0) + 1 FROM ticket_category WHERE org_id = $1 AND site_id = $2`, orgID, siteID).Scan(&p); err != nil {
			return err
		}
		c, err = scanCategory(tx.QueryRow(ctx, `INSERT INTO ticket_category (id, org_id, site_id, name, default_visibility, default_assignee_role, position, archived)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+categoryColumns, id.New(), orgID, siteID, *name, v, role, p, archived != nil && *archived))
		if err != nil {
			return err
		}
		diff, _ := audit.Diff("ticket_category", nil, c)
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &siteID, Action: "tickets.category_create", TargetType: "ticket_category", TargetID: c.ID, Diff: diff})
	})
	if tenancy.IsUnique(err, "ticket_category_name") {
		return Category{}, problem.Conflict("a category with this name exists")
	}
	return c, tenancy.DBProblem(err, "category")
}

// UpdateCategory changes a category (tickets.manage). Only the fields sent
// change; default_assignee_role: null clears the default.
func (s *Service) UpdateCategory(ctx context.Context, orgID, catID uuid.UUID, expected int, d tenancy.Doc) (Category, error) {
	name, vis, role, pos, archived, err := checkCategory(d, false)
	if err != nil {
		return Category{}, err
	}
	var c Category
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := s.loadCategory(ctx, tx, orgID, catID, true)
		if err != nil {
			return err
		}
		sc, err := opsx.SiteScope(ctx, tx, before.SiteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermManage, sc); err != nil {
			return err
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		next := before
		if name != nil {
			next.Name = *name
		}
		if vis != nil {
			next.DefaultVisibility = *vis
		}
		if d.Has("default_assignee_role") {
			next.DefaultAssigneeRole = role
		}
		if pos != nil {
			next.Position = *pos
		}
		if archived != nil {
			next.Archived = *archived
		}
		c, err = scanCategory(tx.QueryRow(ctx, `UPDATE ticket_category SET name = $3, default_visibility = $4, default_assignee_role = $5, position = $6, archived = $7,
			updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2 RETURNING `+categoryColumns,
			orgID, catID, next.Name, next.DefaultVisibility, next.DefaultAssigneeRole, next.Position, next.Archived))
		if err != nil {
			return err
		}
		diff, _ := audit.Diff("ticket_category", before, c)
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &c.SiteID, Action: "tickets.category_update", TargetType: "ticket_category", TargetID: c.ID, Diff: diff})
	})
	if tenancy.IsUnique(err, "ticket_category_name") {
		return Category{}, problem.Conflict("a category with this name exists")
	}
	return c, tenancy.DBProblem(err, "category")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
