package tenancy

import (
	"context"

	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// Doc is a JSON object with only the fields the client sent: the input of
// every PATCH and create, so "absent" and "null" stay distinguishable.
type Doc map[string]json.RawMessage

// ToDoc converts any JSON-encodable value (the generated request body) to a Doc.
func ToDoc(v any) (Doc, error) {
	if v == nil {
		return Doc{}, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d := Doc{}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return d, nil
}

// Has reports whether the client sent the field (possibly null).
func (d Doc) Has(k string) bool { _, ok := d[k]; return ok }

// IsNull reports whether the field was sent as null.
func (d Doc) IsNull(k string) bool { return strings.TrimSpace(string(d[k])) == "null" }

// Get decodes the field into dst; a wrong type is a field problem on the
// result's Fields (returned as a *problem.Error).
func (d Doc) Get(k string, dst any) error {
	raw, ok := d[k]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return problem.Validation(problem.Field{Field: k, Code: "invalid_type"})
	}
	return nil
}

// String returns the field as a string pointer: nil when absent or null.
func (d Doc) String(k string) (*string, error) {
	if !d.Has(k) || d.IsNull(k) {
		return nil, nil //nolint:nilnil // absent and null both mean "no value"
	}
	var s string
	if err := d.Get(k, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Unknown returns the fields that are not in allowed, as validation problems.
func (d Doc) Unknown(allowed ...string) []problem.Field {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var out []problem.Field
	for k := range d {
		if !ok[k] {
			out = append(out, problem.Field{Field: k, Code: "unknown_field"})
		}
	}
	return out
}

// PageParams are the cursor-pagination inputs of a list.
type PageParams struct {
	Limit  int
	Cursor string
}

// DefaultLimit is the page size when none is given.
const DefaultLimit = 50

// Resolve returns the effective limit and the id the page starts after.
func (p PageParams) Resolve() (limit int, after uuid.UUID, err error) {
	limit = p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > 200 {
		limit = 200
	}
	if p.Cursor == "" {
		return limit, uuid.Nil, nil
	}
	b, derr := base64.RawURLEncoding.DecodeString(p.Cursor)
	if derr == nil {
		after, derr = uuid.ParseBytes(b)
	}
	if derr != nil {
		return 0, uuid.Nil, problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
	}
	return limit, after, nil
}

// Page cuts rows (fetched with limit+1) to the limit and returns the cursor
// of the next page, empty on the last page. Lists are keyset-paginated by id,
// which UUIDv7 makes creation order.
func Page[T any](rows []T, limit int, idOf func(T) uuid.UUID) ([]T, string) {
	if len(rows) <= limit {
		if rows == nil {
			rows = []T{}
		}
		return rows, ""
	}
	rows = rows[:limit]
	return rows, base64.RawURLEncoding.EncodeToString([]byte(idOf(rows[len(rows)-1]).String()))
}

// IsUnique reports a unique violation, optionally of the named constraint or index.
func IsUnique(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && (constraint == "" || strings.Contains(pe.ConstraintName, constraint))
}

// DBProblem maps an error of a write to a problem: unique and foreign-key
// violations are conflicts and validation problems, everything else is an
// internal error carrying what.
func DBProblem(err error, what string) error {
	var pe *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrVersion):
		return VersionConflict(what)
	case isProblem(err):
		return err
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("not found")
	case errors.As(err, &pe) && pe.Code == "23505":
		return problem.Conflict("the " + what + " conflicts with an existing one (" + pe.ConstraintName + ")")
	case errors.As(err, &pe) && pe.Code == "23503":
		return problem.Validation(problem.Field{Field: "reference", Code: "invalid_value", Params: map[string]any{"constraint": pe.ConstraintName}})
	case errors.As(err, &pe) && pe.Code == "23514":
		return problem.Validation(problem.Field{Field: "request", Code: "invalid_value", Params: map[string]any{"constraint": pe.ConstraintName}})
	}
	return problem.Internal(fmt.Errorf("%s: %w", what, err))
}

func isProblem(err error) bool {
	var pe *problem.Error
	return errors.As(err, &pe)
}

// ErrVersion is returned inside a transaction when If-Match does not match.
var ErrVersion = errors.New("version mismatch")

// VersionConflict is the 409 for a stale If-Match.
func VersionConflict(what string) *problem.Error {
	return problem.New(409, problem.CodeVersionConflict, "the "+what+" was changed by someone else; reload and try again")
}

// requireSite derives the scope of a site from its row (under RLS) and checks
// perm for it. Use inside the tenant transaction, never with request-built scopes.
func requireSite(ctx context.Context, tx pgx.Tx, perm authz.Permission, siteID uuid.UUID) (authz.Scope, error) {
	sc, err := authz.ScopeOfSite(ctx, tx, siteID)
	if err != nil {
		return sc, err
	}
	return sc, authz.Require(ctx, perm, sc)
}

// loadSiteFor checks perm against the site's own scope, then loads it.
func loadSiteFor(ctx context.Context, tx pgx.Tx, perm authz.Permission, orgID, siteID uuid.UUID, forUpdate, allowOffboarded bool) (Site, error) {
	if _, err := requireSite(ctx, tx, perm, siteID); err != nil {
		return Site{}, err
	}
	return LoadSite(ctx, tx, orgID, siteID, forUpdate, allowOffboarded)
}

// requireBlockOfSite is requireBlock and also insists the block belongs to siteID.
func requireBlockOfSite(ctx context.Context, tx pgx.Tx, perm authz.Permission, blockID, siteID uuid.UUID) error {
	sc, err := requireBlock(ctx, tx, perm, blockID)
	if err != nil {
		return err
	}
	if sc.SiteID == nil || *sc.SiteID != siteID {
		return problem.Validation(problem.Field{Field: "block_id", Code: "invalid_value"})
	}
	return nil
}

// requireBlock is requireSite for a block (and its site).
func requireBlock(ctx context.Context, tx pgx.Tx, perm authz.Permission, blockID uuid.UUID) (authz.Scope, error) {
	sc, err := authz.ScopeOfBlock(ctx, tx, blockID)
	if err != nil {
		return sc, err
	}
	return sc, authz.Require(ctx, perm, sc)
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }
